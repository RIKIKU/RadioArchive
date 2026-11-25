#Requires -Modules @{ ModuleName = 'Pester'; RequiredVersion = '5.9.1' }
# Where the sign-in is saved (ADR 0028): the DPAPI file on Windows, a
# SecretManagement vault, or this session only, each checked in new pwsh
# processes, the way a second window would find it. Authentik and the
# radio are stood in for inside the module; the real ones are the
# behaviour tests' (web/module-tests).

BeforeAll {
    $manifest = (Resolve-Path -Path (Join-Path -Path $PSScriptRoot -ChildPath '../RadioArchive/RadioArchive.psd1')).Path
    $pwsh = (Get-Process -Id $PID).Path
    $origin = 'https://radio.example.test'
    $plain = 'refresh-' + [guid]::NewGuid().ToString('N')

    # Runs a script in a new pwsh process with its own home, as another
    # window of the same user would; returns its output lines.
    function Invoke-Child([string] $Script, [string] $HomeDir = $script:userHome, [string] $ModulePath = $env:PSModulePath) {
        $file = Join-Path -Path $TestDrive -ChildPath "child-$([guid]::NewGuid().ToString('N')).ps1"
        Set-Content -Path $file -Value $Script -Encoding utf8
        $start = [System.Diagnostics.ProcessStartInfo]::new($pwsh)
        foreach ($a in @('-NoProfile', '-NonInteractive', '-File', $file)) { $start.ArgumentList.Add($a) }
        $start.RedirectStandardOutput = $true
        $start.RedirectStandardError = $true
        $start.UseShellExecute = $false
        $start.Environment['HOME'] = $HomeDir
        $start.Environment['XDG_DATA_HOME'] = Join-Path -Path $HomeDir -ChildPath '.local/share'
        $start.Environment['XDG_CONFIG_HOME'] = Join-Path -Path $HomeDir -ChildPath '.config'
        $start.Environment['RADIOARCHIVE_SIGNIN_DIR'] = Join-Path -Path $HomeDir -ChildPath 'SignIns'
        $start.Environment['PSModulePath'] = $ModulePath
        $p = [System.Diagnostics.Process]::Start($start)
        $out = $p.StandardOutput.ReadToEndAsync()
        $err = $p.StandardError.ReadToEndAsync()
        $p.WaitForExit()
        if ($err.Result.Trim()) { Write-Warning -Message "child stderr: $($err.Result)" }
        @($out.Result -split "`r?`n" | Where-Object -FilterScript { $_ -ne '' })
    }

    # Every file under a folder that holds the token as text, UTF-16, hex
    # of either, or Base64 of either.
    function Find-Token([string] $Folder, [string] $Text) {
        # Each form as hex text, so the list isn't flattened into bytes.
        $utf8 = [System.Text.Encoding]::UTF8.GetBytes($Text)
        $utf16 = [System.Text.Encoding]::Unicode.GetBytes($Text)
        $forms = @(
            [System.Convert]::ToHexString($utf8)
            [System.Convert]::ToHexString($utf16)
            [System.Convert]::ToHexString([System.Text.Encoding]::ASCII.GetBytes([System.Convert]::ToHexString($utf8)))
            [System.Convert]::ToHexString([System.Text.Encoding]::ASCII.GetBytes([System.Convert]::ToHexString($utf8).ToLowerInvariant()))
            [System.Convert]::ToHexString([System.Text.Encoding]::ASCII.GetBytes([System.Convert]::ToHexString($utf16)))
            [System.Convert]::ToHexString([System.Text.Encoding]::ASCII.GetBytes([System.Convert]::ToHexString($utf16).ToLowerInvariant()))
            [System.Convert]::ToHexString([System.Text.Encoding]::ASCII.GetBytes([System.Convert]::ToBase64String($utf8)))
            [System.Convert]::ToHexString([System.Text.Encoding]::ASCII.GetBytes([System.Convert]::ToBase64String($utf16)))
        )
        foreach ($f in Get-ChildItem -Path $Folder -Recurse -File -Force -ErrorAction SilentlyContinue) {
            $hex = [System.Convert]::ToHexString([System.IO.File]::ReadAllBytes($f.FullName))
            foreach ($form in $forms) {
                if ($hex.Contains($form)) {
                    $f.FullName
                    break
                }
            }
        }
    }

    # A sign-in with every outside party stood in for: Authentik hands out
    # $plain as the refresh token, and the radio says this is an admin.
    $fake = @"
& (Get-Module RadioArchive) {
    function script:Get-RaServerSetting { @{ Issuer = 'https://auth.example.test/'; ClientId = 'c'; Scope = 's'; Endpoints = @{ Token = 'https://auth.example.test/t'; Revocation = 'https://auth.example.test/r' } } }
    function script:Invoke-RaBrowserSignIn { `$a = [securestring]::new(); 'access'.ToCharArray() | ForEach-Object { `$a.AppendChar(`$_) }; `$r = [securestring]::new(); '$plain'.ToCharArray() | ForEach-Object { `$r.AppendChar(`$_) }; @{ Status = 200; access_token = `$a; refresh_token = `$r; expires_in = 300 } }
    function script:Invoke-RaApi { [pscustomobject]@{ id = '1'; email = 'owner@example.test'; name = 'Owner'; role = 'admin'; via = 'module' } }
    function script:Invoke-RaTokenRequest { `$script:revoked = `$true; @{ Status = 200 } }
}
"@
}

Describe 'Off Windows, a sign-in is never written with ConvertFrom-SecureString and no key' -Skip:$IsWindows {
    BeforeAll {
        $script:userHome = Join-Path -Path $TestDrive -ChildPath 'linux-home'
        $null = New-Item -ItemType Directory -Path $script:userHome
    }
    It 'because there it gives the token as reversible hex' {
        $secure = ConvertTo-SecureString -String $plain -AsPlainText -Force
        $hex = ConvertFrom-SecureString -SecureString $secure
        [System.Text.Encoding]::Unicode.GetString([System.Convert]::FromHexString($hex)) | Should -Be $plain
    }
    It 'so the DPAPI functions refuse to run' {
        Import-Module -Name $manifest -Force
        $secure = ConvertTo-SecureString -String $plain -AsPlainText -Force
        { & (Get-Module -Name RadioArchive) { param($t) Protect-RaToken -Token $t } $secure } | Should -Throw -ErrorId 'dpapi_windows_only'
    }
    It 'with no vault, the sign-in is for this session only: a warning, and no file' {
        $out = Invoke-Child -Script "Import-Module '$manifest'; $fake; `$c = Connect-RadioArchive -Uri '$origin' 3>&1; `$c | ForEach-Object { if (`$_ -is [System.Management.Automation.WarningRecord]) { 'WARNING ' + `$_.Message } else { `$_.SavedIn } }"
        ($out -join ' ') | Should -Match 'WARNING No SecretManagement vault is registered.*Register-SecretVault'
        $out | Should -Contain 'This session only'
        Join-Path -Path $script:userHome -ChildPath 'SignIns' | Should -Not -Exist
        Find-Token -Folder $script:userHome -Text $plain | Should -BeNullOrEmpty
        # The next window has no sign-in.
        $next = Invoke-Child -Script "Import-Module '$manifest'; try { Get-RadioArchiveSystem } catch { `$_.FullyQualifiedErrorId }"
        $next | Should -BeLike 'not_connected*'
    }
}

Describe 'With a SecretStore vault' -Skip:$IsWindows {
    BeforeAll {
        $script:userHome = Join-Path -Path $TestDrive -ChildPath 'vault-home'
        $null = New-Item -ItemType Directory -Path $script:userHome
        $null = Invoke-Child -Script @'
Register-SecretVault -Name TestStore -ModuleName Microsoft.PowerShell.SecretStore -DefaultVault
Reset-SecretStore -Scope CurrentUser -Authentication None -Interaction None -Force -Confirm:$false 3>$null
'@
    }
    It 'saves the sign-in in the default vault, and a new window reads it' {
        $out = Invoke-Child -Script "Import-Module '$manifest'; $fake; (Connect-RadioArchive -Uri '$origin').SavedIn"
        $out | Should -Be 'Vault TestStore'
        $read = Invoke-Child -Script "Import-Module '$manifest'; & (Get-Module RadioArchive) { (Read-RaRefreshToken -Origin '$origin').RefreshToken | ConvertFrom-SecureString -AsPlainText }"
        $read | Should -Be $plain
        Find-Token -Folder (Join-Path -Path $script:userHome -ChildPath 'SignIns') -Text $plain | Should -BeNullOrEmpty
    }
    It 'Disconnect-RadioArchive revokes it and removes it from the vault and its file' {
        $out = Invoke-Child -Script "Import-Module '$manifest'; $fake; Disconnect-RadioArchive -Uri '$origin'; & (Get-Module RadioArchive) { `$script:revoked }; @(Microsoft.PowerShell.SecretManagement\Get-SecretInfo -Vault TestStore).Count"
        $out | Should -Be @('True', '0')
        @(Get-ChildItem -Path (Join-Path -Path $script:userHome -ChildPath 'SignIns') -Filter '*.json') | Should -BeNullOrEmpty
    }
    It '-Vault without SecretManagement installed stops, saying how to install it' {
        $bare = ($env:PSModulePath -split [System.IO.Path]::PathSeparator | Where-Object -FilterScript { -not (Test-Path -LiteralPath (Join-Path -Path $_ -ChildPath 'Microsoft.PowerShell.SecretManagement')) }) -join [System.IO.Path]::PathSeparator
        $out = Invoke-Child -ModulePath $bare -Script "Import-Module '$manifest'; $fake; try { Connect-RadioArchive -Uri '$origin' -Vault TestStore } catch { `$_.FullyQualifiedErrorId; `$_.CategoryInfo.Category }"
        $out[0] | Should -BeLike 'secretmanagement_missing*'
        $out[1] | Should -Be 'NotInstalled'
    }
}

Describe 'On Windows, the DPAPI file' -Skip:(-not $IsWindows) {
    BeforeAll {
        $script:userHome = Join-Path -Path $TestDrive -ChildPath 'windows-home'
        $null = New-Item -ItemType Directory -Path $script:userHome
    }
    It 'saves the sign-in encrypted, and a new window reads it' {
        $out = Invoke-Child -Script "Import-Module '$manifest'; $fake; (Connect-RadioArchive -Uri '$origin').SavedIn"
        $out | Should -Be 'DPAPI file'
        $read = Invoke-Child -Script "Import-Module '$manifest'; & (Get-Module RadioArchive) { (Read-RaRefreshToken -Origin '$origin').RefreshToken | ConvertFrom-SecureString -AsPlainText }"
        $read | Should -Be $plain
        $files = @(Get-ChildItem -Path (Join-Path -Path $script:userHome -ChildPath 'SignIns') -Filter '*.json')
        $files.Count | Should -Be 1
        Find-Token -Folder $script:userHome -Text $plain | Should -BeNullOrEmpty
    }
    It 'Disconnect-RadioArchive revokes it and deletes the file' {
        $out = Invoke-Child -Script "Import-Module '$manifest'; $fake; Disconnect-RadioArchive -Uri '$origin'; & (Get-Module RadioArchive) { `$script:revoked }"
        $out | Should -Be 'True'
        @(Get-ChildItem -Path (Join-Path -Path $script:userHome -ChildPath 'SignIns') -Filter '*.json') | Should -BeNullOrEmpty
    }
}

Describe 'A listener''s sign-in is refused and not saved' {
    BeforeAll {
        $script:userHome = Join-Path -Path $TestDrive -ChildPath 'listener-home'
        $null = New-Item -ItemType Directory -Path $script:userHome
    }
    It 'is revoked, nothing is saved, and the error is the radio''s' {
        $refuse = $fake.Replace("function script:Invoke-RaApi { [pscustomobject]@{ id = '1'; email = 'owner@example.test'; name = 'Owner'; role = 'admin'; via = 'module' } }",
            "function script:Invoke-RaApi { throw (New-RaErrorRecord -ErrorId 'admin_required' -Status 403 -Message 'This account isn''t an admin.') }")
        $out = Invoke-Child -Script "Import-Module '$manifest'; $refuse; try { Connect-RadioArchive -Uri '$origin' -WarningAction SilentlyContinue } catch { `$_.FullyQualifiedErrorId; `$_.CategoryInfo.Category }; & (Get-Module RadioArchive) { [bool]`$script:revoked; `$null -eq `$script:Connection }"
        $out[0] | Should -BeLike 'admin_required*'
        $out[1] | Should -Be 'PermissionDenied'
        $out[2..3] | Should -Be @('True', 'True')
        @(Get-ChildItem -Path (Join-Path -Path $script:userHome -ChildPath 'SignIns') -Filter '*.json' -ErrorAction SilentlyContinue) | Should -BeNullOrEmpty
        Find-Token -Folder $script:userHome -Text $plain | Should -BeNullOrEmpty
    }
}

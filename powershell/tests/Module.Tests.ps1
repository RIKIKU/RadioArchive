#Requires -Modules @{ ModuleName = 'Pester'; RequiredVersion = '5.9.1' }
# Unit tests with the API mocked (Invoke-RaApi): the pipeline, what each
# change sends, -WhatIf and -Confirm, errors, paging, and the sign-in's
# pieces that need no server.

BeforeAll {
    Import-Module -Name (Join-Path -Path $PSScriptRoot -ChildPath '../RadioArchive/RadioArchive.psd1') -Force

    # The talkgroup table as the API answers it: 1001 and 1003 in North.
    $script:table = [pscustomobject]@{
        calls_since = '2026-10-26T00:00:00Z'
        talkgroups  = @(
            [pscustomobject]@{ system_id = 1; id = 1001; label = 'DISP N'; name = 'Dispatch North'; tag = 'dispatch'; groups = @('North'); state = 'public'; unreviewed = $false; delay_minutes = 0; alert = $null; led_colour = $null; sort_order = 0; fireground_channel = $null; last_heard_at = '2026-11-25T00:00:00Z'; calls_30_days = 60 }
            [pscustomobject]@{ system_id = 1; id = 1002; label = 'FG 41'; name = 'Fireground 41'; tag = 'fireground'; groups = @(); state = 'public'; unreviewed = $false; delay_minutes = 0; alert = $null; led_colour = $null; sort_order = 0; fireground_channel = 41; last_heard_at = $null; calls_30_days = 0 }
            [pscustomobject]@{ system_id = 1; id = 1003; label = 'ADMIN'; name = 'Admin'; tag = 'admin'; groups = @('North'); state = 'hidden'; unreviewed = $false; delay_minutes = 0; alert = $null; led_colour = $null; sort_order = 0; fireground_channel = 43; last_heard_at = $null; calls_30_days = 3 }
            [pscustomobject]@{ system_id = 1; id = 1004; label = ''; name = ''; tag = 'other'; groups = @(); state = 'ignored'; unreviewed = $true; delay_minutes = 0; alert = $null; led_colour = $null; sort_order = 0; fireground_channel = 44; last_heard_at = $null; calls_30_days = 1 }
        )
    }

    $manifest = (Resolve-Path -Path (Join-Path -Path $PSScriptRoot -ChildPath '../RadioArchive/RadioArchive.psd1')).Path
    # Runs a script in a new, non-interactive pwsh: its output and errors.
    function Invoke-Child([string] $Script) {
        $file = Join-Path -Path $TestDrive -ChildPath "child-$([guid]::NewGuid().ToString('N')).ps1"
        Set-Content -Path $file -Value $Script -Encoding utf8
        $start = [System.Diagnostics.ProcessStartInfo]::new((Get-Process -Id $PID).Path)
        foreach ($a in @('-NoProfile', '-NonInteractive', '-File', $file)) { $start.ArgumentList.Add($a) }
        $start.RedirectStandardOutput = $true
        $start.RedirectStandardError = $true
        $start.UseShellExecute = $false
        $p = [System.Diagnostics.Process]::Start($start)
        $out = $p.StandardOutput.ReadToEndAsync()
        $err = $p.StandardError.ReadToEndAsync()
        $p.WaitForExit()
        @{ Out = @($out.Result -split "`r?`n" | Where-Object -FilterScript { $_ -ne '' }); Err = $err.Result.Trim() }
    }

    # An API refusal, as Invoke-RaApi throws it.
    function New-Refusal([string] $Code, [int] $Status, [string] $Message = 'Refused.') {
        & (Get-Module -Name RadioArchive) { param($c, $s, $m) New-RaErrorRecord -ErrorId $c -Status $s -Message $m } $Code $Status $Message
    }
}

Describe 'Talkgroups through the pipeline (R51)' {
    BeforeEach {
        $script:calls = [System.Collections.Generic.List[object]]::new()
        Mock -ModuleName RadioArchive Invoke-RaApi {
            $script:calls.Add([pscustomobject]@{ Method = $Method; Path = $Path; Body = $Body })
            if ($Method -eq 'GET' -and $Path -eq '/api/v1/admin/talkgroups') {
                return $script:table
            }
            if ($Method -eq 'PATCH') {
                $id = [int]($Path -split '/')[-1]
                $row = $script:table.talkgroups | Where-Object -Property id -EQ $id
                return $row
            }
        }
    }
    It 'Get-RadioArchiveTalkgroup -Group North finds 1001 and 1003, hidden ones too' {
        $tg = @(Get-RadioArchiveTalkgroup -Group North)
        $tg.Id | Should -Be @(1001, 1003)
        $tg[0].PSTypeNames[0] | Should -Be 'RadioArchive.Talkgroup'
        $tg[1].State | Should -Be 'Hidden'
        $tg[0].LastHeardAt | Should -BeOfType [datetimeoffset]
        $tg[0].Group | Should -Be @('North')
    }
    It 'filters combine, with wildcards, case-insensitively' {
        @(Get-RadioArchiveTalkgroup -Name 'dispatch*').Id | Should -Be @(1001)
        @(Get-RadioArchiveTalkgroup -Name 'FG*').Id | Should -Be @(1002)
        @(Get-RadioArchiveTalkgroup -State Hidden, Ignored).Id | Should -Be @(1003, 1004)
        @(Get-RadioArchiveTalkgroup -Group 'n*' -State Public).Id | Should -Be @(1001)
        @(Get-RadioArchiveTalkgroup -Unreviewed).Id | Should -Be @(1004)
        @(Get-RadioArchiveTalkgroup -SystemId 2) | Should -BeNullOrEmpty
    }
    It 'reads the table once for the whole pipeline' {
        $null = 1001, 1003 | ForEach-Object -Process { [pscustomobject]@{ SystemId = 1; Id = $_ } } | Get-RadioArchiveTalkgroup
        @($script:calls | Where-Object -Property Path -EQ '/api/v1/admin/talkgroups').Count | Should -Be 1
    }
    It 'Get -Group North | Set -State Hidden sends one PATCH per talkgroup, with state hidden only' {
        Get-RadioArchiveTalkgroup -Group North | Set-RadioArchiveTalkgroup -State Hidden -Confirm:$false
        $patches = @($script:calls | Where-Object -Property Method -EQ 'PATCH')
        $patches.Path | Should -Be @('/api/v1/admin/systems/1/talkgroups/1001', '/api/v1/admin/systems/1/talkgroups/1003')
        foreach ($p in $patches) {
            @($p.Body.Keys) | Should -Be @('state')
            $p.Body.state | Should -Be 'hidden'
        }
    }
    It 'a state change asks by default, as ConfirmImpact High does; -WhatIf changes nothing' {
        $ConfirmPreference | Should -Be 'High'
        Get-RadioArchiveTalkgroup -Id 1001 | Set-RadioArchiveTalkgroup -State Hidden -WhatIf
        @($script:calls | Where-Object -Property Method -EQ 'PATCH') | Should -BeNullOrEmpty
        # In a session that can't ask, the prompt is an error, not a change;
        # a label change doesn't ask, so it's made.
        $fake = "& (Get-Module RadioArchive) { function script:Invoke-RaApi { 'CALLED' } }"
        $state = Invoke-Child -Script "Import-Module '$manifest'; $fake; Set-RadioArchiveTalkgroup -SystemId 1 -Id 1001 -State Hidden"
        $state.Out | Should -Not -Contain 'CALLED'
        $state.Err | Should -Match 'NonInteractive'
        $label = Invoke-Child -Script "Import-Module '$manifest'; $fake; Set-RadioArchiveTalkgroup -SystemId 1 -Id 1001 -Label x; 'DONE'"
        $label.Out | Should -Contain 'DONE'
        $label.Err | Should -BeNullOrEmpty
        $remove = Invoke-Child -Script "Import-Module '$manifest'; $fake; Remove-RadioArchiveUnit -SystemId 1 -Unit 5001"
        $remove.Err | Should -Match 'NonInteractive'
    }
    It 'a label change doesn''t ask (Medium)' {
        Set-RadioArchiveTalkgroup -SystemId 1 -Id 1001 -Label 'DISP North'
        @($script:calls | Where-Object -Property Method -EQ 'PATCH').Count | Should -Be 1
    }
    It '-Alert '''' and -FiregroundChannel $null clear; -Group @() empties; -PassThru outputs the talkgroup' {
        $out = Set-RadioArchiveTalkgroup -SystemId 1 -Id 1002 -Alert '' -FiregroundChannel $null -Group @() -PassThru
        $body = ($script:calls | Where-Object -Property Method -EQ 'PATCH').Body
        $body.Contains('alert') | Should -BeTrue
        $body.alert | Should -BeNullOrEmpty
        $body.Contains('fireground_channel') | Should -BeTrue
        $body.fireground_channel | Should -BeNullOrEmpty
        ($body.groups | ConvertTo-Json -Compress) | Should -BeIn @('[]', $null)
        (@{ groups = $body.groups } | ConvertTo-Json -Compress) | Should -Be '{"groups":[]}'
        $out.Id | Should -Be 1002
    }
    It 'Set- outputs nothing without -PassThru' {
        Set-RadioArchiveTalkgroup -SystemId 1 -Id 1001 -Label x | Should -BeNullOrEmpty
    }
}

Describe 'Errors (guidelines section 7)' {
    It 'a refusal for one item is a non-terminating error with the API''s code, message, category and the item' {
        Mock -ModuleName RadioArchive Invoke-RaApi {
            if ($Path -like '*/1009') { throw (New-Refusal -Code 'talkgroup_not_found' -Status 404 -Message 'System 1 has no talkgroup 1009.') }
            $script:table.talkgroups[0]
        }
        $items = @([pscustomobject]@{ SystemId = 1; Id = 1009 }, [pscustomobject]@{ SystemId = 1; Id = 1001 })
        # The error stream, not -ErrorVariable: that also collects the
        # private functions' errors the command caught.
        $all = @($items | Set-RadioArchiveTalkgroup -Label x -PassThru 2>&1)
        $out = @($all | Where-Object -FilterScript { $_ -isnot [System.Management.Automation.ErrorRecord] })
        $failures = @($all | Where-Object -FilterScript { $_ -is [System.Management.Automation.ErrorRecord] })
        $out.Count | Should -Be 1
        $failures.Count | Should -Be 1
        $failures[0].FullyQualifiedErrorId | Should -BeLike 'talkgroup_not_found*'
        $failures[0].Exception.Message | Should -Be 'System 1 has no talkgroup 1009.'
        $failures[0].CategoryInfo.Category | Should -Be 'ObjectNotFound'
        $failures[0].TargetObject | Should -Be '1/1009'
    }
    It 'maps statuses to categories: <Status> <Code> is <Category>' -ForEach @(
        @{ Status = 400; Code = 'invalid_label'; Category = 'InvalidArgument' }
        @{ Status = 401; Code = 'invalid_token'; Category = 'AuthenticationError' }
        @{ Status = 403; Code = 'admin_required'; Category = 'PermissionDenied' }
        @{ Status = 404; Code = 'system_not_found'; Category = 'ObjectNotFound' }
        @{ Status = 409; Code = 'system_exists'; Category = 'ResourceExists' }
        @{ Status = 409; Code = 'last_admin'; Category = 'InvalidOperation' }
        @{ Status = 429; Code = 'slow_down'; Category = 'LimitsExceeded' }
        @{ Status = 503; Code = 'admin_check_unavailable'; Category = 'ConnectionError' }
    ) {
        (New-Refusal -Code $Code -Status $Status).CategoryInfo.Category | Should -Be $Category
    }
    It 'not being an admin stops the whole pipeline (terminating)' {
        Mock -ModuleName RadioArchive Invoke-RaApi { throw (New-Refusal -Code 'admin_required' -Status 403 -Message "This account isn't an admin.") }
        { 1, 2 | ForEach-Object -Process { [pscustomobject]@{ SystemId = $_ } } | Set-RadioArchiveSystem -Name x -ErrorAction SilentlyContinue } |
            Should -Throw -ErrorId 'admin_required*'
    }
    It 'not signed in is terminating, and says how to sign in' {
        $env:RADIOARCHIVE_SIGNIN_DIR = Join-Path -Path $TestDrive -ChildPath 'none'
        try {
            & (Get-Module -Name RadioArchive) { $script:Connection = $null }
            { Get-RadioArchiveSystem } | Should -Throw -ErrorId 'not_connected*' -ExpectedMessage '*Connect-RadioArchive*'
        } finally {
            Remove-Item -Path Env:RADIOARCHIVE_SIGNIN_DIR
        }
    }
    It 'an unexpected error from below is wrapped, so its exception never reaches $Error' {
        $inner = [System.Management.Automation.ErrorRecord]::new([System.Net.Http.HttpRequestException]::new('boom'), 'x', 'NotSpecified', $null)
        $copy = & (Get-Module -Name RadioArchive) { param($e) Copy-RaErrorRecord -ErrorRecord $e } $inner
        $copy.FullyQualifiedErrorId | Should -Be 'unexpected'
        $copy.Exception | Should -BeOfType [System.InvalidOperationException]
    }
}

Describe 'Systems, units, groups, tags, attachments, recorders and accounts' {
    BeforeEach {
        $script:calls = [System.Collections.Generic.List[object]]::new()
        Mock -ModuleName RadioArchive Invoke-RaApi {
            $script:calls.Add([pscustomobject]@{ Method = $Method; Path = $Path; Body = $Body; Query = $Query })
            switch -Wildcard ("$Method $Path") {
                'GET /api/v1/admin/systems' { return @([pscustomobject]@{ id = 1; label = 'EX'; name = 'Example network'; auto_create = $true; alert = $null; led_colour = $null; sort_order = 0 }) }
                'POST /api/v1/admin/systems' { return [pscustomobject]@{ id = $Body.id; label = $Body.label; name = $Body.name; auto_create = $Body.auto_create; alert = $Body.alert; led_colour = $Body.led_colour; sort_order = $Body.sort_order } }
                'GET /api/v1/admin/systems/1/units' { return @([pscustomobject]@{ system_id = 1; from = 5001; to = 5001; alias = 'Engine 1' }, [pscustomobject]@{ system_id = 1; from = 5100; to = 5199; alias = 'Portables' }) }
                'PUT /api/v1/admin/systems/1/units/*' { return [pscustomobject]@{ system_id = 1; from = 5001; to = 5001; alias = $Body.alias } }
                'GET /api/v1/admin/attachments' { return @([pscustomobject]@{ recorder = 'Bench'; short_name = 'ex'; system_id = 1; system_label = 'EX'; attached_at = '2026-11-25T00:00:00Z' }) }
                'PUT /api/v1/admin/recorders/*/attachments/*' { return [pscustomobject]@{ recorder = 'Bench'; short_name = 'north'; system_id = $Body.system_id; system_label = 'EX'; attached_at = '2026-11-25T00:00:00Z' } }
                'GET /api/v1/admin/rejections' { return [pscustomobject]@{ unattached = @([pscustomobject]@{ recorder = 'Bench Two'; short_name = 'north/1'; calls = 4; last_at = '2026-11-25T00:00:00Z'; message = 'x' }); talkgroups = @() } }
                'GET /api/v1/admin/accounts' { return @([pscustomobject]@{ id = '7'; name = 'Sam'; email = 'sam@example.test'; role = 'listener'; status = 'active'; first_signed_in = '2026-11-25T00:00:00Z'; last_signed_in = '2026-11-25T00:00:00Z' }) }
                default { return $null }
            }
        }
    }
    It 'a system made from a piped object sends every field' {
        [pscustomobject]@{ SystemId = 2; Label = 'EY'; Name = 'Another'; AutoCreate = $true; SortOrder = 3 } | New-RadioArchiveSystem -Confirm:$false |
            ForEach-Object -Process { $_.SystemId | Should -Be 2 }
        $body = ($script:calls | Where-Object -Property Method -EQ 'POST').Body
        $body.id | Should -Be 2
        $body.auto_create | Should -BeTrue
        $body.sort_order | Should -Be 3
    }
    It 'a system pipes into its units, and a unit''s range reads as Set-RadioArchiveUnit takes it' {
        $units = @(Get-RadioArchiveSystem -Label 'e*' | Get-RadioArchiveUnit)
        $units.Unit | Should -Be @('5001', '5100-5199')
        @(Get-RadioArchiveUnit -SystemId 1 -RadioId 5150).Alias | Should -Be @('Portables')
        $units[0] | Set-RadioArchiveUnit -Alias 'Engine One' -Confirm:$false
        ($script:calls | Where-Object -Property Method -EQ 'PUT').Path | Should -Be '/api/v1/admin/systems/1/units/5001'
    }
    It 'Remove-RadioArchiveUnit asks first (High): -WhatIf deletes nothing' {
        Remove-RadioArchiveUnit -SystemId 1 -Unit 5100-5199 -WhatIf
        @($script:calls | Where-Object -Property Method -EQ 'DELETE') | Should -BeNullOrEmpty
        Remove-RadioArchiveUnit -SystemId 1 -Unit 5100-5199 -Confirm:$false
        ($script:calls | Where-Object -Property Method -EQ 'DELETE').Path | Should -Be '/api/v1/admin/systems/1/units/5100-5199'
    }
    It 'unattached rejections pipe into Add-RadioArchiveAttachment, with names escaped in the path' {
        Get-RadioArchiveRejection -Unattached | Add-RadioArchiveAttachment -SystemId 1 -Confirm:$false
        ($script:calls | Where-Object -Property Method -EQ 'PUT').Path | Should -Be '/api/v1/admin/recorders/Bench%20Two/attachments/north%2F1'
        ($script:calls | Where-Object -Property Method -EQ 'PUT').Body.system_id | Should -Be 1
    }
    It 'Set-RadioArchiveRecorder -QuietAfterMinutes $null turns the signal off (null)' {
        Set-RadioArchiveRecorder -Name Bench -QuietAfterMinutes $null -Confirm:$false
        $body = ($script:calls | Where-Object -Property Method -EQ 'PATCH').Body
        $body.Contains('quiet_after_minutes') | Should -BeTrue
        $body.quiet_after_minutes | Should -BeNullOrEmpty
    }
    It 'an account pipes into Set-RadioArchiveAccount, which asks first (High)' {
        Get-RadioArchiveAccount -Email 'SAM@*' | Set-RadioArchiveAccount -Role Admin -WhatIf
        @($script:calls | Where-Object -Property Method -EQ 'PATCH') | Should -BeNullOrEmpty
        Get-RadioArchiveAccount -Email 'SAM@*' | Set-RadioArchiveAccount -Role Admin -Confirm:$false
        $patch = $script:calls | Where-Object -Property Method -EQ 'PATCH'
        $patch.Path | Should -Be '/api/v1/admin/accounts/7'
        $patch.Body.role | Should -Be 'admin'
    }
}

Describe 'Retention (R88)' {
    BeforeEach {
        $script:calls = [System.Collections.Generic.List[object]]::new()
        Mock -ModuleName RadioArchive Invoke-RaApi {
            $script:calls.Add([pscustomobject]@{ Method = $Method; Body = $Body })
            if ($Method -eq 'GET') { return [pscustomobject]@{ days = 30; default_days = 30; min_days = 7; max_days = 3650; next_run = '2026-11-26T03:00:00Z'; disk = $null; last_nightly = $null; last_shortened = $null } }
            if (-not $Body.confirm -and $Body.days -lt 30) { return [pscustomobject]@{ error = 'confirm_shorter_retention'; message = 'Confirm?'; days = $Body.days; recordings = 1204 } }
            [pscustomobject]@{ days = $Body.days; default_days = 30; min_days = 7; max_days = 3650; next_run = '2026-11-26T03:00:00Z'; disk = $null; last_nightly = $null; last_shortened = $null }
        }
    }
    It 'shortening asks first, saying how many recordings go; -WhatIf never confirms' {
        Set-RadioArchiveRetention -Days 14 -WhatIf
        @($script:calls | Where-Object -FilterScript { $_.Body -and $_.Body.confirm }) | Should -BeNullOrEmpty
        # What -WhatIf says goes to the host: a child's output.
        $fake = "& (Get-Module RadioArchive) { function script:Invoke-RaApi { param(`$Method, `$Path, `$Body, `$Accept) if (`$Method -eq 'GET') { [pscustomobject]@{ days = 30 } } elseif (`$Body.confirm) { 'CONFIRMED' } else { [pscustomobject]@{ error = 'confirm_shorter_retention'; recordings = 1204 } } } }"
        $child = Invoke-Child -Script "Import-Module '$manifest'; $fake; Set-RadioArchiveRetention -Days 14 -WhatIf; Set-RadioArchiveRetention -Days 14"
        ($child.Out -join ' ') | Should -Match 'Shorten from 30 to 14 days, deleting 1,204 recordings'
        $child.Out | Should -Not -Contain 'CONFIRMED'
        $child.Err | Should -Match 'NonInteractive'
    }
    It 'shortening with -Confirm:$false confirms it; -PassThru shows it' {
        (Set-RadioArchiveRetention -Days 14 -Confirm:$false -PassThru).Days | Should -Be 14
        @($script:calls | Where-Object -FilterScript { $_.Body -and $_.Body.confirm }).Count | Should -Be 1
    }
    It 'lengthening doesn''t ask and never confirms a deletion' {
        Set-RadioArchiveRetention -Days 60
        @($script:calls | Where-Object -FilterScript { $_.Method -eq 'PUT' }).Body.confirm | Should -Be @($false)
    }
}

Describe 'Calls and recordings (R34)' {
    It 'pages only as far as -First needs, and -Skip skips the newest' {
        $script:pages = 0
        Mock -ModuleName RadioArchive Invoke-RaApi {
            $script:pages++
            $start = if ($Query.cursor) { [int]$Query.cursor } else { 0 }
            $n = [int]$Query.limit
            $rows = foreach ($i in $start..($start + $n - 1)) { [pscustomobject]@{ id = ('00000000-0000-4000-8000-{0:d12}' -f $i); talkgroup = 1003; patched_talkgroups = $null; state = 'hidden'; started_at = '2026-11-25T00:00:00Z'; ended_at = $null; in_progress = $false; recording = [pscustomobject]@{ url = 'u'; size = 10 } } }
            [pscustomobject]@{ calls = @($rows); next = "$($start + $n)" }
        }
        $calls = @(Get-RadioArchiveCall -SystemId 1 -Talkgroup 1003 -First 3 -Skip 2)
        $calls.Count | Should -Be 3
        $calls[0].CallId | Should -Be '00000000-0000-4000-8000-000000000002'
        $calls[0].PatchedTalkgroups | Should -Be @()
        $calls[0].State | Should -Be 'Hidden'
        $script:pages | Should -Be 1
    }
    It 'saves a recording byte for byte, and won''t replace a file without -Force' {
        $bytes = [byte[]](0..255)
        Mock -ModuleName RadioArchive Invoke-RaApi { [System.IO.File]::WriteAllBytes($OutFile, [byte[]](0..255)) }
        $id = '0000000a-0000-4000-8000-000000000001'
        $file = Save-RadioArchiveRecording -CallId $id -Path $TestDrive
        $file.Name | Should -Be "$id.ogg"
        [System.IO.File]::ReadAllBytes($file.FullName) | Should -Be $bytes
        Save-RadioArchiveRecording -CallId $id -Path $TestDrive -ErrorVariable failed -ErrorAction SilentlyContinue
        $failed[0].FullyQualifiedErrorId | Should -BeLike 'file_exists*'
        (Save-RadioArchiveRecording -CallId $id -Path $TestDrive -Force).Name | Should -Be "$id.ogg"
        @(Get-ChildItem -Path $TestDrive -Filter '*.part') | Should -BeNullOrEmpty
    }
}

Describe 'Sign-in pieces' {
    It 'reads a token field into a SecureString and blanks it in the reply' {
        $reply = [System.Text.Encoding]::UTF8.GetBytes('{"access_token": "abc\/defA", "expires_in": 300}')
        $secure = & (Get-Module -Name RadioArchive) { param($b) Read-RaSecretField -Buffer $b -Name 'access_token' } $reply
        $secure | Should -BeOfType [securestring]
        [System.Net.NetworkCredential]::new('', $secure).Password | Should -Be 'abc/defA'
        [System.Text.Encoding]::UTF8.GetString($reply) | Should -Not -Match 'abc'
        ([System.Text.Encoding]::UTF8.GetString($reply) | ConvertFrom-Json).expires_in | Should -Be 300
    }
    It 'the loopback listener reads the code into a SecureString, checks nothing else, and answers the browser' {
        $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
        $listener.Start()
        try {
            $client = [System.Net.Sockets.TcpClient]::new('127.0.0.1', $listener.LocalEndpoint.Port)
            $stream = $client.GetStream()
            $request = [System.Text.Encoding]::ASCII.GetBytes("GET /?code=a%2Bb&state=st8 HTTP/1.1`r`nHost: 127.0.0.1`r`n`r`n")
            $stream.Write($request, 0, $request.Length)
            $result = & (Get-Module -Name RadioArchive) { param($c) Read-RaLoopbackRequest -Client $c } $listener.AcceptTcpClient()
            $answer = [System.IO.StreamReader]::new($stream).ReadToEnd()
            $client.Dispose()
        } finally {
            $listener.Stop()
        }
        $result.Path | Should -Be '/'
        $result.State | Should -Be 'st8'
        $result.Code | Should -BeOfType [securestring]
        [System.Net.NetworkCredential]::new('', $result.Code).Password | Should -Be 'a+b'
        $answer | Should -Match '^HTTP/1.1 200 OK'
        $answer | Should -Match 'You can close this tab'
    }
    It 'PKCE: the S256 challenge of RFC 7636''s example verifier' {
        & (Get-Module -Name RadioArchive) { Get-RaCodeChallenge -Verifier 'dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk' } | Should -Be 'E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM'
    }
    It 'plain HTTP is refused without -AllowUnencryptedAuthentication' {
        { Connect-RadioArchive -Uri 'http://radio.example.test' } | Should -Throw -ErrorId 'unencrypted_refused*'
    }
    It 'the module talks only to the radio and its Authentik: an endpoint elsewhere is refused' {
        Mock -ModuleName RadioArchive Invoke-RestMethod {
            if ($Uri -like '*/auth/module') { return [pscustomobject]@{ issuer = 'https://auth.example.test/application/o/radioarchive-powershell/'; client_id = 'radioarchive-powershell'; scope = 's' } }
            [pscustomobject]@{ authorization_endpoint = 'https://auth.example.test/a'; token_endpoint = 'https://elsewhere.example.test/t'; revocation_endpoint = 'https://auth.example.test/r'; device_authorization_endpoint = 'https://auth.example.test/d' }
        }
        { & (Get-Module -Name RadioArchive) { Get-RaServerSetting -Origin 'https://radio.example.test' } } | Should -Throw -ErrorId 'authentik_misconfigured'
    }
}

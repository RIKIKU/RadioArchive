# Where a sign-in is saved (ADR 0028): on Windows a file under the user's
# local application data with the refresh token encrypted by DPAPI; on
# Linux and macOS the default SecretManagement vault; with -Vault, that
# vault; with neither, nowhere (this session only).
#
# ConvertFrom-SecureString with no key encrypts with DPAPI only on Windows.
# Elsewhere it just writes the text's UTF-16 bytes in hex, so it's never
# called off Windows: Protect-RaToken and Unprotect-RaToken check first.

function Get-RaSignInFolder {
    <#
    .SYNOPSIS
    The folder of saved sign-ins.

    .DESCRIPTION
    LocalApplicationData/RadioArchive/SignIns: %LOCALAPPDATA% on Windows,
    ~/.local/share on Linux. RADIOARCHIVE_SIGNIN_DIR replaces it (tests).
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param()
    if ($env:RADIOARCHIVE_SIGNIN_DIR) {
        return $env:RADIOARCHIVE_SIGNIN_DIR
    }
    # DoNotVerify: without it, a folder that doesn't exist yet (a new
    # user's ~/.local/share) comes back as ''.
    $local = [Environment]::GetFolderPath([Environment+SpecialFolder]::LocalApplicationData, [Environment+SpecialFolderOption]::DoNotVerify)
    Join-Path -Path $local -ChildPath 'RadioArchive' -AdditionalChildPath 'SignIns'
}

function Get-RaSignInPath {
    <#
    .SYNOPSIS
    The file for one server's saved sign-in.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        # The server's origin, e.g. https://radio.example.org.
        [Parameter(Mandatory)]
        [string] $Origin
    )
    $hash = [System.Security.Cryptography.SHA256]::HashData([System.Text.Encoding]::UTF8.GetBytes($Origin.ToLowerInvariant()))
    $name = [System.Convert]::ToHexString($hash).Substring(0, 16).ToLowerInvariant() + '.json'
    Join-Path -Path (Get-RaSignInFolder) -ChildPath $name
}

function Protect-RaToken {
    <#
    .SYNOPSIS
    Encrypts a token with DPAPI for this Windows account. Windows only.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        # The token.
        [Parameter(Mandatory)]
        [securestring] $Token
    )
    if (-not $IsWindows) {
        throw (New-RaErrorRecord -ErrorId 'dpapi_windows_only' -Message 'Saving a sign-in to a file is for Windows only, where DPAPI encrypts it.' -Category NotImplemented)
    }
    ConvertFrom-SecureString -SecureString $Token
}

function Unprotect-RaToken {
    <#
    .SYNOPSIS
    Decrypts a DPAPI-encrypted token into a SecureString. Windows only.
    #>
    [CmdletBinding()]
    [OutputType([securestring])]
    param(
        # The encrypted token, as Protect-RaToken wrote it.
        [Parameter(Mandatory)]
        [string] $Encrypted
    )
    if (-not $IsWindows) {
        throw (New-RaErrorRecord -ErrorId 'dpapi_windows_only' -Message 'Reading a sign-in from a file is for Windows only, where DPAPI encrypts it.' -Category NotImplemented)
    }
    ConvertTo-SecureString -String $Encrypted
}

function Import-RaSecretManagement {
    <#
    .SYNOPSIS
    Loads SecretManagement when it's needed. Returns whether it's installed.
    #>
    [CmdletBinding()]
    [OutputType([bool])]
    param()
    if (Get-Module -Name 'Microsoft.PowerShell.SecretManagement') {
        return $true
    }
    if (-not (Get-Module -Name 'Microsoft.PowerShell.SecretManagement' -ListAvailable)) {
        return $false
    }
    Import-Module -Name 'Microsoft.PowerShell.SecretManagement' -ErrorAction Stop -Verbose:$false
    $true
}

function Resolve-RaStore {
    <#
    .SYNOPSIS
    Decides where a new sign-in is saved.

    .DESCRIPTION
    Returns @{ Store = 'dpapi' | 'vault' | 'session'; Vault = name }. With
    -Vault, that vault (SecretManagement must be installed). Otherwise DPAPI
    on Windows, and the default vault on Linux and macOS, or this session
    only when there's none.
    #>
    [CmdletBinding()]
    [OutputType([hashtable])]
    param(
        # The vault named with Connect-RadioArchive -Vault.
        [string] $Vault
    )
    if ($Vault) {
        if (-not (Import-RaSecretManagement)) {
            throw (New-RaErrorRecord -ErrorId 'secretmanagement_missing' -Category NotInstalled `
                    -Message '-Vault needs the Microsoft.PowerShell.SecretManagement module. Install it with: Install-PSResource -Name Microsoft.PowerShell.SecretManagement')
        }
        if (-not (Microsoft.PowerShell.SecretManagement\Get-SecretVault -Name $Vault -ErrorAction SilentlyContinue)) {
            throw (New-RaErrorRecord -ErrorId 'no_such_vault' -Category ObjectNotFound -Message "No SecretManagement vault is called '$Vault'. Get-SecretVault lists them.")
        }
        return @{ Store = 'vault'; Vault = $Vault }
    }
    if ($IsWindows) {
        return @{ Store = 'dpapi'; Vault = $null }
    }
    if (Import-RaSecretManagement) {
        $default = Microsoft.PowerShell.SecretManagement\Get-SecretVault -ErrorAction SilentlyContinue | Where-Object -Property IsDefault
        if ($default) {
            return @{ Store = 'vault'; Vault = $default.Name }
        }
    }
    @{ Store = 'session'; Vault = $null }
}

function Save-RaSignIn {
    <#
    .SYNOPSIS
    Saves a connection's refresh token where its store says, and its file.
    #>
    [CmdletBinding()]
    param(
        # The connection.
        [Parameter(Mandatory)]
        [hashtable] $Connection
    )
    if ($Connection.Store -eq 'session') {
        return
    }
    $record = [ordered]@{
        uri                            = $Connection.Uri
        store                          = $Connection.Store
        vault                          = $Connection.Vault
        version                        = [guid]::NewGuid().Guid
        allowUnencryptedAuthentication = [bool]$Connection.AllowUnencrypted
    }
    if ($Connection.Store -eq 'dpapi') {
        $record.refreshToken = Protect-RaToken -Token $Connection.RefreshToken
    } else {
        try {
            Microsoft.PowerShell.SecretManagement\Set-Secret -Name ('RadioArchive:' + $Connection.Uri) -SecureStringSecret $Connection.RefreshToken -Vault $Connection.Vault -ErrorAction Stop
        } catch {
            throw (New-RaErrorRecord -ErrorId 'vault_failed' -Category WriteError -Message "The sign-in couldn't be saved in the vault '$($Connection.Vault)': $($_.Exception.Message) If it's locked, unlock it (for SecretStore: Unlock-SecretStore).")
        }
    }
    $path = Get-RaSignInPath -Origin $Connection.Uri
    $folder = Split-Path -Path $path -Parent
    if (-not (Test-Path -LiteralPath $folder)) {
        $null = New-Item -ItemType Directory -Path $folder -Force
    }
    # Written under another name, then moved over the old file, so another
    # window never reads half a file.
    $temp = "$path.$PID.tmp"
    [System.IO.File]::WriteAllText($temp, ($record | ConvertTo-Json -Compress))
    [System.IO.File]::Move($temp, $path, $true)
    $Connection.Version = $record.version
}

function Read-RaSignIn {
    <#
    .SYNOPSIS
    Reads saved sign-ins: all of them, or the one for a server.

    .DESCRIPTION
    Returns the files' records (uri, store, vault, version,
    allowUnencryptedAuthentication, path), without tokens.
    #>
    [CmdletBinding()]
    [OutputType([hashtable])]
    param(
        # The server's origin; omitted, every saved sign-in.
        [string] $Origin
    )
    $folder = Get-RaSignInFolder
    if (-not (Test-Path -LiteralPath $folder)) {
        return
    }
    $files = if ($Origin) { Get-Item -LiteralPath (Get-RaSignInPath -Origin $Origin) -ErrorAction SilentlyContinue } else { Get-ChildItem -LiteralPath $folder -Filter '*.json' }
    foreach ($file in $files) {
        try {
            $record = [System.IO.File]::ReadAllText($file.FullName) | ConvertFrom-Json -AsHashtable
        } catch {
            Write-Warning -Message "Skipped an unreadable saved sign-in: $($file.Name)."
            continue
        }
        $record.Remove('refreshToken')
        $record.path = $file.FullName
        $record
    }
}

function Read-RaRefreshToken {
    <#
    .SYNOPSIS
    Reads a saved sign-in's refresh token, and the version it was saved with.
    #>
    [CmdletBinding()]
    [OutputType([hashtable])]
    param(
        # The server's origin.
        [Parameter(Mandatory)]
        [string] $Origin
    )
    $path = Get-RaSignInPath -Origin $Origin
    if (-not (Test-Path -LiteralPath $path)) {
        return $null
    }
    $record = [System.IO.File]::ReadAllText($path) | ConvertFrom-Json -AsHashtable
    if ($record.store -eq 'dpapi') {
        $token = Unprotect-RaToken -Encrypted $record.refreshToken
    } else {
        if (-not (Import-RaSecretManagement)) {
            throw (New-RaErrorRecord -ErrorId 'secretmanagement_missing' -Category NotInstalled `
                    -Message "Your sign-in is saved in the vault '$($record.vault)', which needs the Microsoft.PowerShell.SecretManagement module. Install it with: Install-PSResource -Name Microsoft.PowerShell.SecretManagement")
        }
        try {
            $token = Microsoft.PowerShell.SecretManagement\Get-Secret -Name ('RadioArchive:' + $Origin) -Vault $record.vault -ErrorAction Stop
        } catch {
            throw (New-RaErrorRecord -ErrorId 'vault_failed' -Category ReadError -Message "The sign-in couldn't be read from the vault '$($record.vault)': $($_.Exception.Message) If it's locked, unlock it (for SecretStore: Unlock-SecretStore).")
        }
    }
    @{ RefreshToken = $token; Version = $record.version }
}

function Remove-RaSignIn {
    <#
    .SYNOPSIS
    Deletes a server's saved sign-in, wherever it is.
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'Low')]
    param(
        # The server's origin.
        [Parameter(Mandatory)]
        [string] $Origin
    )
    $path = Get-RaSignInPath -Origin $Origin
    if (-not (Test-Path -LiteralPath $path)) {
        return
    }
    if (-not $PSCmdlet.ShouldProcess($Origin, 'Delete the saved sign-in')) {
        return
    }
    $record = [System.IO.File]::ReadAllText($path) | ConvertFrom-Json -AsHashtable
    if ($record.store -eq 'vault' -and (Import-RaSecretManagement)) {
        try {
            Microsoft.PowerShell.SecretManagement\Remove-Secret -Name ('RadioArchive:' + $Origin) -Vault $record.vault -ErrorAction Stop
        } catch {
            Write-Warning -Message "The sign-in couldn't be removed from the vault '$($record.vault)': $($_.Exception.Message)"
        }
    }
    Remove-Item -LiteralPath $path -Force
}

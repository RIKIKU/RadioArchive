function Connect-RadioArchive {
    <#
    .SYNOPSIS
    Signs in to a RadioArchive radio as an admin.

    .DESCRIPTION
    Opens your browser at the radio's normal sign-in. When you've signed
    in, the browser says you can close it, and the module is connected. On
    a machine without a browser, use -UseDeviceCode: the module shows a
    code to enter on any device.

    The sign-in is saved, so new PowerShell windows use it without signing
    in again:

    - On Windows, in a file under your local application data, encrypted
      with Windows' data protection (DPAPI), so only your Windows account
      on this machine can read it. Nothing needs installing.
    - On Linux and macOS, in your default SecretManagement vault. With no
      vault registered, the sign-in lasts for this session only, and a
      warning says how to set one up.
    - Anywhere, -Vault names a SecretManagement vault to use instead.

    The module renews its access as it goes. A sign-in that isn't used for
    30 days ends. If one is already saved for the radio, it's used without
    opening the browser.

    The module is for the radio's admins: anyone else's sign-in is refused
    and not saved. Sign in to the radio with the browser once first, so the
    radio knows your account.

    .EXAMPLE
    Connect-RadioArchive -Uri https://radio.example.org

    Uri                        Name         Email             Role  SavedIn
    ---                        ----         -----             ----  -------
    https://radio.example.org/ Alex Example alex@example.org  Admin DPAPI file

    .EXAMPLE
    Connect-RadioArchive -Uri https://radio.example.org -UseDeviceCode

    WARNING: To sign in, open https://auth.example.org/device on any device and enter the code ABCD-EFGH

    .EXAMPLE
    Connect-RadioArchive -Uri http://192.168.1.20:8080 -AllowUnencryptedAuthentication

    Signs in to a test copy on your own network over plain HTTP, with a warning.

    .INPUTS
    System.Uri. Objects with a Uri property, such as a RadioArchive.Connection.

    .OUTPUTS
    RadioArchive.Connection. The radio (Uri), who you're signed in as
    (AccountId, Name, Email, Role), where the sign-in is saved (SavedIn) and
    ExpiresAt, when the current access token expires: the module renews it
    as it goes, and the sign-in itself ends only after 30 days unused.

    .LINK
    Disconnect-RadioArchive

    .LINK
    about_RadioArchive
    #>
    [Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSAvoidUsingAllowUnencryptedAuthentication', '', Justification = 'Only when the user passes -AllowUnencryptedAuthentication for a test copy on their own network, with a warning (guidelines section 9).')]
    [CmdletBinding()]
    [OutputType('RadioArchive.Connection')]
    param(
        # The radio's URL, e.g. https://radio.example.org. No default; no
        # wildcards.
        [Parameter(Mandatory, Position = 0, ValueFromPipeline, ValueFromPipelineByPropertyName)]
        [uri] $Uri,

        # Sign in with a code entered on any device, for a machine with no
        # browser. Off by default.
        [switch] $UseDeviceCode,

        # The SecretManagement vault to save the sign-in in. Default: a
        # DPAPI-encrypted file on Windows, the default vault on Linux and
        # macOS. Needs the Microsoft.PowerShell.SecretManagement module. No
        # wildcards.
        [ValidateNotNullOrEmpty()]
        [string] $Vault,

        # Allow a plain-HTTP URL (a test copy on your own network), which
        # sends the sign-in unencrypted. Off by default.
        [switch] $AllowUnencryptedAuthentication
    )
    process {
        try {
            $origin = Get-RaOrigin -Uri $Uri -AllowUnencryptedAuthentication:$AllowUnencryptedAuthentication
            if ($origin.StartsWith('http:')) {
                Write-Warning -Message "$origin uses plain HTTP: your sign-in is sent unencrypted."
            }
            $settings = Get-RaServerSetting -Origin $origin -AllowUnencryptedAuthentication:$AllowUnencryptedAuthentication
            $store = Resolve-RaStore -Vault $Vault

            # A saved sign-in for this radio: use it, without the browser.
            $saved = Read-RaSignIn -Origin $origin
            if ($saved -and (-not $Vault -or ($saved.store -eq 'vault' -and $saved.vault -eq $Vault))) {
                try {
                    $connection = Open-RaSavedSignIn -Record $saved
                    $connection.Me = Invoke-RaApi -Method GET -Path '/api/v1/admin/me'
                    return New-RaConnectionObject -Connection $connection
                } catch {
                    $script:Connection = $null
                    # Refused for who you are: signing in again won't help.
                    if ($_.Exception.Data['RadioArchive.Code'] -in @('admin_required', 'account_disabled')) {
                        throw
                    }
                    Write-Verbose -Message "The saved sign-in didn't work ($($_.Exception.Data['RadioArchive.Code'])); signing in again."
                }
            }
            if ($saved) {
                Remove-RaSignIn -Origin $origin -WhatIf:$false -Confirm:$false
            }

            $reply = if ($UseDeviceCode) { Invoke-RaDeviceSignIn -Settings $settings } else { Invoke-RaBrowserSignIn -Settings $settings }
            $connection = New-RaConnection -Origin $origin -Settings $settings -Store $store.Store -Vault $store.Vault -AllowUnencrypted:$AllowUnencryptedAuthentication
            Import-RaTokenReply -Connection $connection -Reply $reply

            # The radio decides: only its active admins.
            $script:Connection = $connection
            try {
                $connection.Me = Invoke-RaApi -Method GET -Path '/api/v1/admin/me'
            } catch {
                $script:Connection = $null
                $null = Revoke-RaToken -Settings $connection -Token $connection.RefreshToken
                throw
            }
            if ($connection.Store -eq 'session') {
                Write-Warning -Message ('No SecretManagement vault is registered, so this sign-in lasts until this PowerShell session ends. ' +
                    'To stay signed in, register a vault: Install-PSResource -Name Microsoft.PowerShell.SecretManagement, Microsoft.PowerShell.SecretStore; ' +
                    'Register-SecretVault -Name SecretStore -ModuleName Microsoft.PowerShell.SecretStore -DefaultVault')
            } else {
                Save-RaSignIn -Connection $connection
                $connection.RefreshToken = $null
            }
            New-RaConnectionObject -Connection $connection
        } catch {
            $script:Connection = $null
            $PSCmdlet.ThrowTerminatingError((Copy-RaErrorRecord -ErrorRecord $_ -TargetObject $Uri))
        }
    }
}

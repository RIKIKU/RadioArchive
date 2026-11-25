function Enable-RadioArchiveAccount {
    <#
    .SYNOPSIS
    Enables a disabled account, here and in Authentik.

    .DESCRIPTION
    Enables an account (ADR 0033): the radio lets it sign in again, with
    its saved settings as it left them, and the change is in the audit
    log; then the radio switches the user on in Authentik. If Authentik
    can't be reached, the account is enabled in the radio and the error
    says Authentik wasn't told (authentik_unreachable); running it again
    tries Authentik again. An admin never enables their own account
    (own_account). Asks first (ConfirmImpact High): -Confirm:$false doesn't
    ask.

    .EXAMPLE
    Get-RadioArchiveAccount -Status Disabled -Email 'sam@*' | Enable-RadioArchiveAccount

    .INPUTS
    System.String. Objects with an AccountId property, such as a RadioArchive.Account.

    .OUTPUTS
    RadioArchive.Account, with -PassThru: the account as enabled. Nothing
    otherwise.

    .LINK
    Disable-RadioArchiveAccount

    .LINK
    Get-RadioArchiveAccount
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'High')]
    [OutputType('RadioArchive.Account')]
    param(
        # The account's ID (Authentik's user ID). No default.
        [Parameter(Mandatory, Position = 0, ValueFromPipelineByPropertyName)]
        [string] $AccountId,

        # Output the account as enabled. Off by default.
        [switch] $PassThru
    )
    process {
        if (-not $PSCmdlet.ShouldProcess("account $AccountId", 'Enable it, here and in Authentik')) {
            return
        }
        try {
            $row = Invoke-RaApi -Method PATCH -Path ('/api/v1/admin/accounts/' + (ConvertTo-RaPathSegment -Value $AccountId)) -Body @{ status = 'active' }
            if ($PassThru) {
                ConvertTo-RaAccount -Row $row
            }
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $AccountId
        }
    }
}

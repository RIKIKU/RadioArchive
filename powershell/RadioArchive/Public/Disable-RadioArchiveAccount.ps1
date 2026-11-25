function Disable-RadioArchiveAccount {
    <#
    .SYNOPSIS
    Disables an account: signs it out of the radio and switches its Authentik user off.

    .DESCRIPTION
    Disables an account (ADR 0033). The radio changes first: the account is
    signed out of every browser at its next request, its next sign-in is
    refused, and the change is in the audit log. Then the radio switches
    the user off in Authentik. If Authentik can't be reached, the account
    stays disabled in the radio and the error says Authentik wasn't told
    (authentik_unreachable); running it again tries Authentik again. Its
    saved settings are kept. An admin never disables their own account
    (own_account), and the last active admin can't be disabled
    (last_admin). Asks first (ConfirmImpact High): -Confirm:$false doesn't
    ask.

    .EXAMPLE
    Get-RadioArchiveAccount -Email 'sam@example.org' | Disable-RadioArchiveAccount

    .INPUTS
    System.String. Objects with an AccountId property, such as a RadioArchive.Account.

    .OUTPUTS
    RadioArchive.Account, with -PassThru: the account as disabled. Nothing
    otherwise.

    .LINK
    Enable-RadioArchiveAccount

    .LINK
    Get-RadioArchiveAccount
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'High')]
    [OutputType('RadioArchive.Account')]
    param(
        # The account's ID (Authentik's user ID). No default.
        [Parameter(Mandatory, Position = 0, ValueFromPipelineByPropertyName)]
        [string] $AccountId,

        # Output the account as disabled. Off by default.
        [switch] $PassThru
    )
    process {
        if (-not $PSCmdlet.ShouldProcess("account $AccountId", 'Disable it, here and in Authentik')) {
            return
        }
        try {
            $row = Invoke-RaApi -Method PATCH -Path ('/api/v1/admin/accounts/' + (ConvertTo-RaPathSegment -Value $AccountId)) -Body @{ status = 'disabled' }
            if ($PassThru) {
                ConvertTo-RaAccount -Row $row
            }
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $AccountId
        }
    }
}

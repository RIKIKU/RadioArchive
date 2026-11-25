function Remove-RadioArchiveAccount {
    <#
    .SYNOPSIS
    Deletes an account: its saved settings, its sessions and its Authentik user.

    .DESCRIPTION
    Deletes an account (ADR 0033): its row, its saved settings (listening
    set, look and download basket) and every session, and its user in
    Authentik, so they can't sign in. It can't be undone; the audit log
    keeps who the account was. All or nothing: if Authentik can't be
    reached, nothing is deleted (authentik_unreachable), and running it
    again tries again. An admin never deletes their own account
    (own_account), and the last active admin can't be deleted
    (last_admin). Asks first (ConfirmImpact High): -Confirm:$false doesn't
    ask.

    .EXAMPLE
    Get-RadioArchiveAccount -Email 'old@example.org' | Remove-RadioArchiveAccount

    .INPUTS
    System.String. Objects with an AccountId property, such as a RadioArchive.Account.

    .OUTPUTS
    RadioArchive.Account, with -PassThru: the account as it was. Nothing
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

        # Output the account as it was. Off by default.
        [switch] $PassThru
    )
    process {
        if (-not $PSCmdlet.ShouldProcess("account $AccountId, its saved settings and its Authentik user", 'Delete')) {
            return
        }
        try {
            $row = Invoke-RaApi -Method DELETE -Path ('/api/v1/admin/accounts/' + (ConvertTo-RaPathSegment -Value $AccountId))
            if ($PassThru) {
                ConvertTo-RaAccount -Row $row
            }
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $AccountId
        }
    }
}

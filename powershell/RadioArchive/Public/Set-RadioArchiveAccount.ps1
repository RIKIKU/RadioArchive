function Set-RadioArchiveAccount {
    <#
    .SYNOPSIS
    Makes an account an admin, or a listener again.

    .DESCRIPTION
    Sets an account's role (R55, ADR 0025). An admin never changes their
    own role (own_role), and the last active admin can't be made a
    listener (last_admin). An admin made a listener is refused at their
    next request, in the browser and in this module. Asks first
    (ConfirmImpact High): -Confirm:$false doesn't ask.

    .EXAMPLE
    Get-RadioArchiveAccount -Email 'sam@example.org' | Set-RadioArchiveAccount -Role Admin

    .INPUTS
    System.String. Objects with an AccountId property, such as a RadioArchive.Account.

    .OUTPUTS
    RadioArchive.Account, with -PassThru: the account as changed. Nothing
    otherwise.

    .LINK
    Get-RadioArchiveAccount
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'High')]
    [OutputType('RadioArchive.Account')]
    param(
        # The account's ID (Authentik's user ID). No default.
        [Parameter(Mandatory, Position = 0, ValueFromPipelineByPropertyName)]
        [string] $AccountId,

        # Admin or Listener. No default.
        [Parameter(Mandatory)]
        [ValidateSet('Admin', 'Listener')]
        [string] $Role,

        # Output the account as changed. Off by default.
        [switch] $PassThru
    )
    process {
        if (-not $PSCmdlet.ShouldProcess("account $AccountId", "Make it $($Role.ToLowerInvariant())")) {
            return
        }
        try {
            $row = Invoke-RaApi -Method PATCH -Path ('/api/v1/admin/accounts/' + (ConvertTo-RaPathSegment -Value $AccountId)) -Body @{ role = $Role.ToLowerInvariant() }
            if ($PassThru) {
                ConvertTo-RaAccount -Row $row
            }
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $AccountId
        }
    }
}

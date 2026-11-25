function Get-RadioArchiveAccount {
    <#
    .SYNOPSIS
    Gets the accounts that have signed in to the radio, admins first.

    .DESCRIPTION
    Lists everyone who has signed in (R55, ADR 0025): name and email as at
    their last sign-in, role (Admin or Listener) and status. Roles are the
    radio's own: an Authentik group makes nobody an admin.

    .EXAMPLE
    Get-RadioArchiveAccount -Role Admin

    .EXAMPLE
    Get-RadioArchiveAccount -Email 'sam@example.org' | Set-RadioArchiveAccount -Role Admin

    .INPUTS
    System.String. Objects with an AccountId property, such as a RadioArchive.Account.

    .OUTPUTS
    RadioArchive.Account. AccountId (Authentik's user ID), Name, Email,
    Role, Status, FirstSignedInAt, LastSignedInAt and AuthentikPending
    (disabled or enabled here, and not yet in Authentik).

    .LINK
    Set-RadioArchiveAccount

    .LINK
    Disable-RadioArchiveAccount
    #>
    [CmdletBinding()]
    [OutputType('RadioArchive.Account')]
    param(
        # The accounts' IDs. Default: every account.
        [Parameter(ValueFromPipelineByPropertyName)]
        [string[]] $AccountId,

        # Their emails. Wildcards are allowed. Default: any.
        [SupportsWildcards()]
        [string[]] $Email,

        # Their names. Wildcards are allowed. Default: any.
        [SupportsWildcards()]
        [string[]] $Name,

        # Their role. Default: any.
        [ValidateSet('Admin', 'Listener')]
        [string] $Role,

        # Their status. Default: any.
        [ValidateSet('Active', 'Disabled')]
        [string] $Status
    )
    begin {
        $all = $null
        $seen = @{}
    }
    process {
        try {
            if ($null -eq $all) {
                $all = @(Invoke-RaApi -Method GET -Path '/api/v1/admin/accounts')
            }
            foreach ($row in $all) {
                if ($null -eq $row) { continue }
                if ($AccountId -and [string]$row.id -notin $AccountId) { continue }
                if (-not (Test-RaWildcard -Value $row.email -Pattern $Email)) { continue }
                if (-not (Test-RaWildcard -Value $row.name -Pattern $Name)) { continue }
                if ($Role -and $row.role -ne $Role) { continue }
                if ($Status -and $row.status -ne $Status) { continue }
                if ($seen.ContainsKey([string]$row.id)) { continue }
                $seen[[string]$row.id] = $true
                ConvertTo-RaAccount -Row $row
            }
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_
        }
    }
}

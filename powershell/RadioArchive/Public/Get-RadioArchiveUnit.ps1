function Get-RadioArchiveUnit {
    <#
    .SYNOPSIS
    Gets a system's units: radio IDs and ranges, and their aliases.

    .DESCRIPTION
    Lists a system's units (R26): one radio ID, such as 5001, or a range,
    such as 5100-5199, each with its alias. An alias of '' is a unit with
    none, which listeners see by its ID even within a range that has one.

    .EXAMPLE
    Get-RadioArchiveUnit -SystemId 1

    SystemId Unit      Alias
    -------- ----      -----
           1 5001      Engine 1
           1 5100-5199 Portables

    .EXAMPLE
    Get-RadioArchiveSystem | Get-RadioArchiveUnit -Alias 'Engine*'

    .INPUTS
    System.Int64. Objects with a SystemId property, such as a RadioArchive.System.

    .OUTPUTS
    RadioArchive.Unit. SystemId, Unit (an ID or a range, as
    Set-RadioArchiveUnit takes it), From, To, Alias, CreatedAt and
    UpdatedAt.

    .LINK
    Set-RadioArchiveUnit

    .LINK
    Remove-RadioArchiveUnit
    #>
    [CmdletBinding()]
    [OutputType('RadioArchive.Unit')]
    param(
        # The system. No default.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [long] $SystemId,

        # Units whose aliases match. Wildcards are allowed. Default: any.
        [SupportsWildcards()]
        [string[]] $Alias,

        # Units that include this radio ID. Default: any.
        [Nullable[long]] $RadioId
    )
    process {
        try {
            foreach ($row in @(Invoke-RaApi -Method GET -Path "/api/v1/admin/systems/$SystemId/units")) {
                if ($null -eq $row) { continue }
                if (-not (Test-RaWildcard -Value $row.alias -Pattern $Alias)) { continue }
                if ($null -ne $RadioId -and ($RadioId -lt [long]$row.from -or $RadioId -gt [long]$row.to)) { continue }
                ConvertTo-RaUnit -Row $row
            }
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $SystemId
        }
    }
}

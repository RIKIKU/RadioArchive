function Set-RadioArchiveUnit {
    <#
    .SYNOPSIS
    Sets a unit's alias, adding the unit if it's new.

    .DESCRIPTION
    Gives a radio ID, such as 5001, or a range, such as 5100-5199, its
    alias (R26). A unit that isn't there yet is added. An alias of '' is a
    unit with none, which listeners see by its ID even within a range that
    has one. Every parameter binds from the pipeline by property name, so
    Import-Csv | Set-RadioArchiveUnit sets many.

    .EXAMPLE
    Set-RadioArchiveUnit -SystemId 1 -Unit 5001 -Alias 'Engine 1'

    .EXAMPLE
    Set-RadioArchiveUnit -SystemId 1 -Unit 5100-5199 -Alias Portables -PassThru

    .INPUTS
    Objects with SystemId, Unit and Alias properties, such as a RadioArchive.Unit.

    .OUTPUTS
    RadioArchive.Unit, with -PassThru: the unit as set. Nothing otherwise.

    .LINK
    Get-RadioArchiveUnit

    .LINK
    Remove-RadioArchiveUnit
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'Medium')]
    [OutputType('RadioArchive.Unit')]
    param(
        # The system. No default.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [long] $SystemId,

        # A radio ID, such as 5001, or a range, such as 5100-5199. No
        # default.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [ValidatePattern('^[1-9][0-9]*(-[1-9][0-9]*)?$')]
        [string] $Unit,

        # Its alias, up to 100 characters, or '' for none. No default.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [AllowEmptyString()]
        [string] $Alias,

        # Output the unit as set. Off by default.
        [switch] $PassThru
    )
    process {
        if (-not $PSCmdlet.ShouldProcess("unit $Unit in system $SystemId", 'Set its alias')) {
            return
        }
        try {
            $row = Invoke-RaApi -Method PUT -Path "/api/v1/admin/systems/$SystemId/units/$Unit" -Body @{ alias = $Alias }
            if ($PassThru) {
                ConvertTo-RaUnit -Row $row
            }
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject "$SystemId/$Unit"
        }
    }
}

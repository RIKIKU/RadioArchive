function Remove-RadioArchiveUnit {
    <#
    .SYNOPSIS
    Deletes a unit: a radio ID or a range and its alias.

    .DESCRIPTION
    Deletes the unit exactly as it was set: 5100-5199 deletes that range,
    not the IDs in it. Asks first (ConfirmImpact High): -Confirm:$false
    doesn't ask.

    .EXAMPLE
    Get-RadioArchiveUnit -SystemId 1 -Alias 'Old*' | Remove-RadioArchiveUnit

    .INPUTS
    Objects with SystemId and Unit properties, such as a RadioArchive.Unit.

    .OUTPUTS
    None, or with -PassThru the RadioArchive.Unit as it was.

    .LINK
    Get-RadioArchiveUnit

    .LINK
    Set-RadioArchiveUnit
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'High')]
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

        # Output the unit as it was. Off by default.
        [switch] $PassThru
    )
    process {
        if (-not $PSCmdlet.ShouldProcess("unit $Unit in system $SystemId", 'Delete')) {
            return
        }
        try {
            $before = if ($PassThru) { ConvertTo-RaUnit -Row (Invoke-RaApi -Method GET -Path "/api/v1/admin/systems/$SystemId/units/$Unit") }
            $null = Invoke-RaApi -Method DELETE -Path "/api/v1/admin/systems/$SystemId/units/$Unit"
            if ($PassThru) {
                $before
            }
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject "$SystemId/$Unit"
        }
    }
}

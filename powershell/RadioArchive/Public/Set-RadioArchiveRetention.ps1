function Set-RadioArchiveRetention {
    <#
    .SYNOPSIS
    Sets how many days recordings are kept.

    .DESCRIPTION
    Sets the retention period (R88). Lengthening it just asks, as any
    change does with -Confirm. Shortening it deletes the recordings older
    than the new period at the next nightly run, so the radio first says
    how many, and the module asks before going on (ConfirmImpact High, as
    a deletion): -Confirm:$false doesn't ask. -WhatIf changes nothing and
    still says how many would go.

    .EXAMPLE
    Set-RadioArchiveRetention -Days 60

    .EXAMPLE
    Set-RadioArchiveRetention -Days 14 -WhatIf

    What if: Performing the operation "Shorten from 30 to 14 days, deleting 1,204 recordings at the next nightly run" on target "retention".

    .INPUTS
    System.Int32. Objects with a Days property, such as a RadioArchive.Retention.

    .OUTPUTS
    RadioArchive.Retention, with -PassThru: retention as set. Nothing
    otherwise.

    .LINK
    Get-RadioArchiveRetention
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'Medium')]
    [OutputType('RadioArchive.Retention')]
    param(
        # The days to keep recordings, within the radio's limits
        # (Get-RadioArchiveRetention shows them). No default.
        [Parameter(Mandatory, Position = 0, ValueFromPipelineByPropertyName)]
        [int] $Days,

        # Output retention as set. Off by default.
        [switch] $PassThru
    )
    process {
        try {
            $current = Invoke-RaApi -Method GET -Path '/api/v1/admin/retention'
            $from = [int]$current.days
            if ($Days -lt $from) {
                # The radio never shortens without confirm: true, and says
                # how many recordings would go.
                $answer = Invoke-RaApi -Method PUT -Path '/api/v1/admin/retention' -Body @{ days = $Days; confirm = $false } -Accept 409
                $count = if ($answer.PSObject.Properties['recordings']) { [long]$answer.recordings } else { 0 }
                $what = 'Shorten from {0} to {1} days, deleting {2:N0} recording{3} at the next nightly run' -f $from, $Days, $count, $(if ($count -eq 1) { '' } else { 's' })
                # A deletion: ConfirmImpact High, which an attribute can't
                # say for one case.
                if (-not $PSBoundParameters.ContainsKey('Confirm') -and $ConfirmPreference -eq 'High') {
                    $ConfirmPreference = 'Medium'
                }
                if (-not $PSCmdlet.ShouldProcess('retention', $what)) {
                    return
                }
                $row = Invoke-RaApi -Method PUT -Path '/api/v1/admin/retention' -Body @{ days = $Days; confirm = $true }
            } else {
                if (-not $PSCmdlet.ShouldProcess('retention', "Keep recordings for $Days days (now $from)")) {
                    return
                }
                $row = Invoke-RaApi -Method PUT -Path '/api/v1/admin/retention' -Body @{ days = $Days; confirm = $false }
            }
            if ($PassThru) {
                ConvertTo-RaRetention -Row $row
            }
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $Days
        }
    }
}

function Get-RadioArchiveRetention {
    <#
    .SYNOPSIS
    Gets how long recordings are kept, and the recordings volume's state.

    .DESCRIPTION
    Shows the retention period (R88) with its limits and default, when the
    nightly run is next, how full the recordings volume is, and the last
    nightly and shortened runs, as the Settings page does.

    .EXAMPLE
    Get-RadioArchiveRetention

    Days NextRunAt              FreePercent DiskState
    ---- ---------              ----------- ---------
      30 26/11/2026 3:00:00 AM         71.4 Ok

    .INPUTS
    None. It's about the radio as a whole, and takes nothing from the pipeline.

    .OUTPUTS
    RadioArchive.Retention. Days, DefaultDays, MinDays, MaxDays, NextRunAt,
    FreePercent, FreeBytes, TotalBytes, DiskState, DiskMessage, LastNightly
    and LastShortened.

    .LINK
    Set-RadioArchiveRetention
    #>
    [CmdletBinding()]
    [OutputType('RadioArchive.Retention')]
    param()
    try {
        ConvertTo-RaRetention -Row (Invoke-RaApi -Method GET -Path '/api/v1/admin/retention')
    } catch {
        Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_
    }
}

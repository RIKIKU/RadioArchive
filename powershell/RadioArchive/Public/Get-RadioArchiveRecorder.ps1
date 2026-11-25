function Get-RadioArchiveRecorder {
    <#
    .SYNOPSIS
    Gets the radio's recorders: connected or not, their systems and their signals.

    .DESCRIPTION
    Shows each active recorder's state, as the Recorders page does (R14):
    connected, last call, its client's version, the systems it reports and
    whether they're attached, decode rates, the quiet threshold, and any
    warning. Recorder keys are never shown or set here (R10): they're made
    on the server.

    .EXAMPLE
    Get-RadioArchiveRecorder

    Name  Connected LastCallAt              QuietAfterMinutes Warning
    ----  --------- ----------              ----------------- -------
    Bench      True 25/11/2026 10:02:13 AM                 30   False

    .INPUTS
    System.String. Objects with a Name property.

    .OUTPUTS
    RadioArchive.Recorder. Name, Connected, ConnectedSince,
    DisconnectedSince, LastConnectedAt, LastCallAt, ClientVersion,
    ClientOutOfDate, QuietAfterMinutes (null: the quiet signal is off),
    Systems (ShortName, SystemId, SystemLabel, SystemType, DecodeRate,
    Deaf), DeafReason, Warning and Warnings.

    .LINK
    Set-RadioArchiveRecorder

    .LINK
    Get-RadioArchiveAttachment
    #>
    [CmdletBinding()]
    [OutputType('RadioArchive.Recorder')]
    param(
        # The recorders' names. Wildcards are allowed. Default: every
        # active recorder.
        [Parameter(ValueFromPipelineByPropertyName)]
        [SupportsWildcards()]
        [string[]] $Name
    )
    begin {
        $all = $null
        $seen = @{}
    }
    process {
        try {
            if ($null -eq $all) {
                $all = @(Invoke-RaApi -Method GET -Path '/api/v1/admin/recorders')
            }
            foreach ($row in $all) {
                if ($null -eq $row) { continue }
                if (-not (Test-RaWildcard -Value $row.name -Pattern $Name)) { continue }
                if ($seen.ContainsKey($row.name)) { continue }
                $seen[$row.name] = $true
                ConvertTo-RaRecorder -Row $row
            }
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_
        }
    }
}

function Set-RadioArchiveRecorder {
    <#
    .SYNOPSIS
    Sets a recorder's quiet threshold.

    .DESCRIPTION
    Sets how many minutes without a call make the recorder's quiet signal
    trip (R14), or turns it off with -QuietAfterMinutes $null. That's the
    one setting a recorder has here: its key is made and changed only on
    the server (R10).

    .EXAMPLE
    Set-RadioArchiveRecorder -Name Bench -QuietAfterMinutes 30

    .EXAMPLE
    Get-RadioArchiveRecorder | Set-RadioArchiveRecorder -QuietAfterMinutes $null

    Turns every recorder's quiet signal off.

    .INPUTS
    System.String. Objects with a Name property, such as a RadioArchive.Recorder.

    .OUTPUTS
    RadioArchive.Recorder, with -PassThru: the recorder as changed. Nothing
    otherwise.

    .LINK
    Get-RadioArchiveRecorder
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'Medium')]
    [OutputType('RadioArchive.Recorder')]
    param(
        # The recorder's name. No default; no wildcards.
        [Parameter(Mandatory, Position = 0, ValueFromPipelineByPropertyName)]
        [string] $Name,

        # Minutes without a call before the quiet signal trips, 1 or more,
        # or $null for off. No default.
        [Parameter(Mandatory)]
        [AllowNull()]
        [Nullable[int]] $QuietAfterMinutes,

        # Output the recorder as changed. Off by default.
        [switch] $PassThru
    )
    process {
        $what = if ($null -eq $QuietAfterMinutes) { 'Turn its quiet signal off' } else { "Set its quiet threshold to $QuietAfterMinutes minutes" }
        if (-not $PSCmdlet.ShouldProcess("recorder $Name", $what)) {
            return
        }
        try {
            $row = Invoke-RaApi -Method PATCH -Path ('/api/v1/admin/recorders/' + (ConvertTo-RaPathSegment -Value $Name)) -Body @{ quiet_after_minutes = $QuietAfterMinutes }
            if ($PassThru) {
                ConvertTo-RaRecorder -Row $row
            }
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $Name
        }
    }
}

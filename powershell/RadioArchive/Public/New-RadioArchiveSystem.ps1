function New-RadioArchiveSystem {
    <#
    .SYNOPSIS
    Adds a system to the radio.

    .DESCRIPTION
    Adds a P25 system by its ID (the recorder's sysNum is mapped to it by
    an attachment, R12), with a label shown to listeners and a name.
    Every parameter binds from the pipeline by property name, so systems
    exported with Get-RadioArchiveSystem | Export-Csv add again with
    Import-Csv | New-RadioArchiveSystem.

    .EXAMPLE
    New-RadioArchiveSystem -SystemId 1 -Label EX -Name 'Example network'

    .EXAMPLE
    Import-Csv -Path systems.csv | New-RadioArchiveSystem

    .INPUTS
    Objects with SystemId, Label, Name, AutoCreate, Alert, LedColour and
    SortOrder properties, such as a RadioArchive.System.

    .OUTPUTS
    RadioArchive.System. The system as the radio made it.

    .LINK
    Get-RadioArchiveSystem

    .LINK
    Set-RadioArchiveSystem
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'Medium')]
    [OutputType('RadioArchive.System')]
    param(
        # The system's ID, a whole number from 1. No default.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [ValidateRange(1, [long]::MaxValue)]
        [long] $SystemId,

        # Its label, shown to listeners, such as EX: 1-100 characters. No
        # default.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [string] $Label,

        # Its name, such as Example network. Default: none.
        [Parameter(ValueFromPipelineByPropertyName)]
        [AllowEmptyString()]
        [string] $Name = '',

        # Make talkgroups heard on it that aren't in the list, as
        # unreviewed. Off by default.
        [Parameter(ValueFromPipelineByPropertyName)]
        [switch] $AutoCreate,

        # Its own alert: none, beep, chime, pip or tone. Default: none of
        # its own.
        [Parameter(ValueFromPipelineByPropertyName)]
        [ValidateSet('', 'none', 'beep', 'chime', 'pip', 'tone')]
        [string] $Alert,

        # Its own LED colour: none, red, orange, yellow, green, cyan, blue,
        # purple or pink. Default: none of its own.
        [Parameter(ValueFromPipelineByPropertyName)]
        [ValidateSet('', 'none', 'red', 'orange', 'yellow', 'green', 'cyan', 'blue', 'purple', 'pink')]
        [string] $LedColour,

        # Where it sorts among systems. Default: 0.
        [Parameter(ValueFromPipelineByPropertyName)]
        [int] $SortOrder = 0
    )
    process {
        $body = [ordered]@{ id = $SystemId; label = $Label; name = $Name; auto_create = [bool]$AutoCreate; sort_order = $SortOrder; alert = $null; led_colour = $null }
        if ($Alert) { $body.alert = $Alert }
        if ($LedColour) { $body.led_colour = $LedColour }
        if (-not $PSCmdlet.ShouldProcess("system $SystemId ($Label)", 'Add')) {
            return
        }
        try {
            ConvertTo-RaSystem -Row (Invoke-RaApi -Method POST -Path '/api/v1/admin/systems' -Body $body)
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $SystemId
        }
    }
}

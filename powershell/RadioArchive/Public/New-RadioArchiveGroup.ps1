function New-RadioArchiveGroup {
    <#
    .SYNOPSIS
    Adds a talkgroup group.

    .DESCRIPTION
    Adds a group, such as North, with its own alert, LED colour and sort
    order if given. Groups are also made when a talkgroup is put in one
    that doesn't exist yet (Set-RadioArchiveTalkgroup -Group).

    .EXAMPLE
    New-RadioArchiveGroup -Name South -LedColour green

    .INPUTS
    Objects with Name, Alert, LedColour and SortOrder properties, such as a RadioArchive.Group.

    .OUTPUTS
    RadioArchive.Group. The group as the radio made it.

    .LINK
    Get-RadioArchiveGroup

    .LINK
    Set-RadioArchiveGroup
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'Medium')]
    [OutputType('RadioArchive.Group')]
    param(
        # Its name, 1-100 characters. No default.
        [Parameter(Mandatory, Position = 0, ValueFromPipelineByPropertyName)]
        [string] $Name,

        # Its own alert. Default: none of its own.
        [Parameter(ValueFromPipelineByPropertyName)]
        [ValidateSet('', 'none', 'beep', 'chime', 'pip', 'tone')]
        [string] $Alert,

        # Its own LED colour. Default: none of its own.
        [Parameter(ValueFromPipelineByPropertyName)]
        [ValidateSet('', 'none', 'red', 'orange', 'yellow', 'green', 'cyan', 'blue', 'purple', 'pink')]
        [string] $LedColour,

        # Where it sorts. Default: 0.
        [Parameter(ValueFromPipelineByPropertyName)]
        [int] $SortOrder = 0
    )
    process {
        $body = [ordered]@{ name = $Name; sort_order = $SortOrder }
        if ($Alert) { $body.alert = $Alert }
        if ($LedColour) { $body.led_colour = $LedColour }
        if (-not $PSCmdlet.ShouldProcess("group $Name", 'Add')) {
            return
        }
        try {
            ConvertTo-RaLabel -Kind Group -Row (Invoke-RaApi -Method POST -Path '/api/v1/admin/groups' -Body $body)
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $Name
        }
    }
}

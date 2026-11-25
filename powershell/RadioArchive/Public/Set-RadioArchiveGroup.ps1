function Set-RadioArchiveGroup {
    <#
    .SYNOPSIS
    Changes a group's alert, LED colour or sort order.

    .DESCRIPTION
    Changes only what's given. -Alert '' and -LedColour '' clear the
    group's own setting. Rename a group with Rename-RadioArchiveGroup.

    .EXAMPLE
    Get-RadioArchiveGroup -Name North | Set-RadioArchiveGroup -LedColour blue

    .INPUTS
    System.Int64. Objects with a GroupId property, such as a RadioArchive.Group.

    .OUTPUTS
    RadioArchive.Group, with -PassThru: the group as changed. Nothing
    otherwise.

    .LINK
    Get-RadioArchiveGroup

    .LINK
    Rename-RadioArchiveGroup
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'Medium')]
    [OutputType('RadioArchive.Group')]
    param(
        # The group's ID. No default.
        [Parameter(Mandatory, Position = 0, ValueFromPipelineByPropertyName)]
        [long] $GroupId,

        # Its own alert, or '' for none of its own. Default: unchanged.
        [ValidateSet('', 'none', 'beep', 'chime', 'pip', 'tone')]
        [string] $Alert,

        # Its own LED colour, or '' for none of its own. Default: unchanged.
        [ValidateSet('', 'none', 'red', 'orange', 'yellow', 'green', 'cyan', 'blue', 'purple', 'pink')]
        [string] $LedColour,

        # Where it sorts. Default: unchanged.
        [Nullable[int]] $SortOrder,

        # Output the group as changed. Off by default.
        [switch] $PassThru
    )
    process {
        $body = [ordered]@{}
        Set-RaOptionalField -Body $body -Bound $PSBoundParameters -Clearable 'alert', 'led_colour' -Map ([ordered]@{ Alert = 'alert'; LedColour = 'led_colour'; SortOrder = 'sort_order' })
        if (-not $PSCmdlet.ShouldProcess("group $GroupId", "Change $(@($body.Keys) -join ', ')")) {
            return
        }
        try {
            $group = Update-RaLabel -Kind Group -Id $GroupId -Body $body
            if ($PassThru) {
                $group
            }
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $GroupId
        }
    }
}

function Set-RadioArchiveTag {
    <#
    .SYNOPSIS
    Changes a tag's alert, LED colour or sort order.

    .DESCRIPTION
    Changes only what's given. -Alert '' and -LedColour '' clear the tag's
    own setting. Rename a tag with Rename-RadioArchiveTag.

    .EXAMPLE
    Get-RadioArchiveTag -Name fireground | Set-RadioArchiveTag -Alert chime

    .INPUTS
    System.Int64. Objects with a TagId property, such as a RadioArchive.Tag.

    .OUTPUTS
    RadioArchive.Tag, with -PassThru: the tag as changed. Nothing otherwise.

    .LINK
    Get-RadioArchiveTag

    .LINK
    Rename-RadioArchiveTag
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'Medium')]
    [OutputType('RadioArchive.Tag')]
    param(
        # The tag's ID. No default.
        [Parameter(Mandatory, Position = 0, ValueFromPipelineByPropertyName)]
        [long] $TagId,

        # Its own alert, or '' for none of its own. Default: unchanged.
        [ValidateSet('', 'none', 'beep', 'chime', 'pip', 'tone')]
        [string] $Alert,

        # Its own LED colour, or '' for none of its own. Default: unchanged.
        [ValidateSet('', 'none', 'red', 'orange', 'yellow', 'green', 'cyan', 'blue', 'purple', 'pink')]
        [string] $LedColour,

        # Where it sorts. Default: unchanged.
        [Nullable[int]] $SortOrder,

        # Output the tag as changed. Off by default.
        [switch] $PassThru
    )
    process {
        $body = [ordered]@{}
        Set-RaOptionalField -Body $body -Bound $PSBoundParameters -Clearable 'alert', 'led_colour' -Map ([ordered]@{ Alert = 'alert'; LedColour = 'led_colour'; SortOrder = 'sort_order' })
        if (-not $PSCmdlet.ShouldProcess("tag $TagId", "Change $(@($body.Keys) -join ', ')")) {
            return
        }
        try {
            $tag = Update-RaLabel -Kind Tag -Id $TagId -Body $body
            if ($PassThru) {
                $tag
            }
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $TagId
        }
    }
}

function Set-RadioArchiveTalkgroup {
    <#
    .SYNOPSIS
    Changes a talkgroup: its state, label, name, tag, groups, delay, alert, LED colour, sort order, fireground channel number or always show.

    .DESCRIPTION
    Changes only what's given, and marks the talkgroup reviewed (R22).
    Hiding a talkgroup takes it out of the catalogue and the live feed at
    once (R32). Changing the state asks first (ConfirmImpact High), as a
    deletion does: -Confirm:$false doesn't ask. Other changes don't ask
    unless -Confirm is given.

    -Alert '' and -LedColour '' clear the talkgroup's own setting, so it
    takes one from its tag, groups or system; -FiregroundChannel $null
    clears its number; -Group @() takes it out of every group.

    .EXAMPLE
    Get-RadioArchiveTalkgroup -Group North | Set-RadioArchiveTalkgroup -State Hidden

    Hides every talkgroup in group North.

    .EXAMPLE
    Set-RadioArchiveTalkgroup -SystemId 1 -Id 1001 -Label 'DISP North' -PassThru

    .INPUTS
    Objects with SystemId and Id properties, such as a RadioArchive.Talkgroup.

    .OUTPUTS
    RadioArchive.Talkgroup, with -PassThru: the talkgroup as changed.
    Nothing otherwise.

    .LINK
    Get-RadioArchiveTalkgroup

    .LINK
    New-RadioArchiveTalkgroup
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'Medium')]
    [OutputType('RadioArchive.Talkgroup')]
    param(
        # The talkgroup's system. No default.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [long] $SystemId,

        # The talkgroup's ID, such as 1001. No default.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [Alias('Talkgroup')]
        [int] $Id,

        # Public, Hidden or Ignored. Default: unchanged.
        [ValidateSet('Public', 'Hidden', 'Ignored')]
        [string] $State,

        # A new label. Default: unchanged.
        [AllowEmptyString()]
        [string] $Label,

        # A new name. Default: unchanged.
        [AllowEmptyString()]
        [string] $Name,

        # A new tag; one that doesn't exist yet is made. Default: unchanged.
        [string] $Tag,

        # Exactly these groups; ones that don't exist yet are made, and @()
        # is none. Default: unchanged.
        [AllowEmptyCollection()]
        [string[]] $Group,

        # Minutes listeners wait before hearing it, 0 to 1440. Default:
        # unchanged.
        [Nullable[int]] $DelayMinutes,

        # Its own alert, or '' to take one from its tag, groups or system.
        # Default: unchanged.
        [ValidateSet('', 'none', 'beep', 'chime', 'pip', 'tone')]
        [string] $Alert,

        # Its own LED colour, or '' to take one from its tag, groups or
        # system. Default: unchanged.
        [ValidateSet('', 'none', 'red', 'orange', 'yellow', 'green', 'cyan', 'blue', 'purple', 'pink')]
        [string] $LedColour,

        # Where it sorts. Default: unchanged.
        [Nullable[int]] $SortOrder,

        # Its fireground channel number, or $null for none. Default:
        # unchanged.
        [Nullable[int]] $FiregroundChannel,

        # $true: never quiet, so listeners always see it (R115). $false:
        # quiet when it isn't heard, unless its tag shows it always.
        # Default: unchanged.
        [Nullable[bool]] $AlwaysShow,

        # Output the talkgroup as changed. Off by default.
        [switch] $PassThru
    )
    begin {
        # A state change counts as ConfirmImpact High (guidelines section
        # 4), which an attribute can't say for one parameter: it asks by
        # default, as High does, unless -Confirm or $ConfirmPreference says
        # otherwise.
        if ($PSBoundParameters.ContainsKey('State') -and -not $PSBoundParameters.ContainsKey('Confirm') -and $ConfirmPreference -eq 'High') {
            $ConfirmPreference = 'Medium'
        }
    }
    process {
        $body = [ordered]@{}
        Set-RaOptionalField -Body $body -Bound $PSBoundParameters -Clearable 'alert', 'led_colour', 'fireground_channel' -Map ([ordered]@{
                State = 'state'; Label = 'label'; Name = 'name'; Tag = 'tag'; Group = 'groups'; DelayMinutes = 'delay_minutes'
                Alert = 'alert'; LedColour = 'led_colour'; SortOrder = 'sort_order'; FiregroundChannel = 'fireground_channel'
                AlwaysShow = 'always_show'
            })
        if ($body.Contains('groups')) {
            $body.groups = @($Group)
        }
        $what = if ($body.Contains('state')) { "Make it $($State.ToLowerInvariant())" } else { "Change $(@($body.Keys) -join ', ')" }
        if (-not $PSCmdlet.ShouldProcess("talkgroup $Id in system $SystemId", $what)) {
            return
        }
        try {
            $row = Invoke-RaApi -Method PATCH -Path "/api/v1/admin/systems/$SystemId/talkgroups/$Id" -Body $body
            if ($PassThru) {
                ConvertTo-RaTalkgroup -Row $row
            }
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject "$SystemId/$Id"
        }
    }
}

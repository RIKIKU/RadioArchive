function New-RadioArchiveTalkgroup {
    <#
    .SYNOPSIS
    Adds a talkgroup to a system.

    .DESCRIPTION
    Adds a talkgroup, reviewed, with a tag (R22). A talkgroup that already
    exists is refused (talkgroup_exists): change it with
    Set-RadioArchiveTalkgroup. Every parameter binds from the pipeline by
    property name, so Get-RadioArchiveTalkgroup | Export-Csv, then
    Import-Csv | New-RadioArchiveTalkgroup, adds them again elsewhere;
    the CSV's Group column holds one group name.

    .EXAMPLE
    New-RadioArchiveTalkgroup -SystemId 1 -Id 1005 -Label 'TAC 5' -Name 'Tactical 5' -Tag tactical

    .INPUTS
    Objects with SystemId, Id, Label, Name, Tag, Group, State, DelayMinutes,
    Alert, LedColour, SortOrder and FiregroundChannel properties, such as a
    RadioArchive.Talkgroup.

    .OUTPUTS
    RadioArchive.Talkgroup. The talkgroup as the radio made it.

    .LINK
    Get-RadioArchiveTalkgroup

    .LINK
    Set-RadioArchiveTalkgroup
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'Medium')]
    [OutputType('RadioArchive.Talkgroup')]
    param(
        # The system it's on. No default.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [long] $SystemId,

        # Its ID, such as 1005. No default.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [Alias('Talkgroup')]
        [int] $Id,

        # Its tag, such as dispatch; one that doesn't exist yet is made. No
        # default.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [string] $Tag,

        # Its label. Default: none.
        [Parameter(ValueFromPipelineByPropertyName)]
        [AllowEmptyString()]
        [string] $Label = '',

        # Its name. Default: none.
        [Parameter(ValueFromPipelineByPropertyName)]
        [AllowEmptyString()]
        [string] $Name = '',

        # Public, Hidden or Ignored. Default: Public.
        [Parameter(ValueFromPipelineByPropertyName)]
        [ValidateSet('Public', 'Hidden', 'Ignored')]
        [string] $State = 'Public',

        # The groups it's in. Default: none.
        [Parameter(ValueFromPipelineByPropertyName)]
        [AllowEmptyCollection()]
        [string[]] $Group = @(),

        # Minutes listeners wait before hearing it, 0 to 1440. Default: 0.
        [Parameter(ValueFromPipelineByPropertyName)]
        [int] $DelayMinutes = 0,

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
        [int] $SortOrder = 0,

        # Its fireground channel number. Default: none.
        [Parameter(ValueFromPipelineByPropertyName)]
        [Nullable[int]] $FiregroundChannel
    )
    process {
        $body = [ordered]@{
            label = $Label; name = $Name; tag = $Tag; state = $State.ToLowerInvariant(); groups = @($Group | Where-Object -FilterScript { $_ })
            delay_minutes = $DelayMinutes; sort_order = $SortOrder
        }
        if ($Alert) { $body.alert = $Alert }
        if ($LedColour) { $body.led_colour = $LedColour }
        if ($null -ne $FiregroundChannel) { $body.fireground_channel = $FiregroundChannel }
        if (-not $PSCmdlet.ShouldProcess("talkgroup $Id in system $SystemId", 'Add')) {
            return
        }
        try {
            $exists = $true
            try {
                $null = Invoke-RaApi -Method GET -Path "/api/v1/admin/systems/$SystemId/talkgroups/$Id"
            } catch {
                if ($_.Exception.Data['RadioArchive.Code'] -ne 'talkgroup_not_found') {
                    throw
                }
                $exists = $false
            }
            if ($exists) {
                throw (New-RaErrorRecord -ErrorId 'talkgroup_exists' -Status 409 -Message "System $SystemId already has talkgroup $Id. Change it with Set-RadioArchiveTalkgroup.")
            }
            ConvertTo-RaTalkgroup -Row (Invoke-RaApi -Method PUT -Path "/api/v1/admin/systems/$SystemId/talkgroups/$Id" -Body $body)
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject "$SystemId/$Id"
        }
    }
}

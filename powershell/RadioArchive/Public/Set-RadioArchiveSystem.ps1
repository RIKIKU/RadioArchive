function Set-RadioArchiveSystem {
    <#
    .SYNOPSIS
    Changes a system's label, name, automatic talkgroups, alert, LED colour or sort order.

    .DESCRIPTION
    Changes only what's given. -Alert '' and -LedColour '' clear the
    system's own setting. The system is named by -SystemId, which binds
    from a piped system or talkgroup.

    .EXAMPLE
    Set-RadioArchiveSystem -SystemId 1 -Name 'Example network (north)'

    .EXAMPLE
    Get-RadioArchiveSystem -Label EX | Set-RadioArchiveSystem -AutoCreate $false

    .INPUTS
    System.Int64. Objects with a SystemId property, such as a RadioArchive.System.

    .OUTPUTS
    RadioArchive.System, with -PassThru: the system as changed. Nothing
    otherwise.

    .LINK
    Get-RadioArchiveSystem

    .LINK
    New-RadioArchiveSystem
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'Medium')]
    [OutputType('RadioArchive.System')]
    param(
        # The system's ID. No default.
        [Parameter(Mandatory, Position = 0, ValueFromPipelineByPropertyName)]
        [long] $SystemId,

        # A new label: 1-100 characters. Default: unchanged.
        [string] $Label,

        # A new name. Default: unchanged.
        [AllowEmptyString()]
        [string] $Name,

        # Whether talkgroups heard on it that aren't in the list are made,
        # as unreviewed. Default: unchanged.
        [Nullable[bool]] $AutoCreate,

        # Its own alert: none, beep, chime, pip or tone, or '' for none of
        # its own. Default: unchanged.
        [ValidateSet('', 'none', 'beep', 'chime', 'pip', 'tone')]
        [string] $Alert,

        # Its own LED colour, or '' for none of its own. Default: unchanged.
        [ValidateSet('', 'none', 'red', 'orange', 'yellow', 'green', 'cyan', 'blue', 'purple', 'pink')]
        [string] $LedColour,

        # Where it sorts among systems. Default: unchanged.
        [Nullable[int]] $SortOrder,

        # Output the system as changed. Off by default.
        [switch] $PassThru
    )
    process {
        $body = [ordered]@{}
        Set-RaOptionalField -Body $body -Bound $PSBoundParameters -Clearable 'alert', 'led_colour' -Map ([ordered]@{
                Label = 'label'; Name = 'name'; AutoCreate = 'auto_create'; Alert = 'alert'; LedColour = 'led_colour'; SortOrder = 'sort_order'
            })
        if (-not $PSCmdlet.ShouldProcess("system $SystemId", "Change $(@($body.Keys) -join ', ')")) {
            return
        }
        try {
            $row = Invoke-RaApi -Method PATCH -Path "/api/v1/admin/systems/$SystemId" -Body $body
            if ($PassThru) {
                ConvertTo-RaSystem -Row $row
            }
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $SystemId
        }
    }
}

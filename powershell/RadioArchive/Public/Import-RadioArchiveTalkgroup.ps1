function Import-RadioArchiveTalkgroup {
    <#
    .SYNOPSIS
    Imports talkgroups from the pipeline, such as a CSV, as one batch with a preview.

    .DESCRIPTION
    Takes talkgroup rows by property name: a CSV that
    Export-RadioArchiveTalkgroup wrote, trunk-recorder's talkgroup CSV
    (Decimal, Alpha Tag, Mode, Description, Tag, Group or Category, for the
    system -SystemId names), or RadioArchive.Talkgroup objects. The whole
    pipeline is one import (R52, ADR 0030): the radio previews every row
    first, -WhatIf shows that preview, and the rows are applied together,
    once, after one confirmation, or not at all.

    A talkgroup that's new is added with every field its row gives. One
    that exists changes only its label, name, tag, groups, fireground
    channel number and sort order; with -Update it also takes its delay,
    alert, LED colour, type and frequency where the row says otherwise.
    Its state never changes through an import, -Update or not: hide or
    show talkgroups with Set-RadioArchiveTalkgroup -State, so an old file
    can't make hidden talkgroups public. Nothing missing from the rows is
    deleted. Rows whose label is just their ID (placeholders), and analog
    (Mode A) or conventional rows, are skipped and reported.

    A field a row doesn't have is left as it is. In a CSV an empty cell
    leaves Tag, State, Unreviewed, DelayMinutes and SortOrder as they are,
    and means none for Alert, LedColour, Frequency and FiregroundChannel.
    Group holds names separated by semicolons.

    .EXAMPLE
    Export-RadioArchiveTalkgroup -Path talkgroups.csv
    # edit talkgroups.csv, then:
    Import-Csv -Path talkgroups.csv | Import-RadioArchiveTalkgroup -WhatIf

    Previews the edited file: every row's result, nothing changed.

    .EXAMPLE
    Import-Csv -Path talkgroups.csv | Import-RadioArchiveTalkgroup -Update

    Applies every field the file changes, after asking once. State stays
    as it is.

    .EXAMPLE
    Import-Csv -Path trunk-recorder-talkgroups.csv | Import-RadioArchiveTalkgroup -SystemId 1

    Adds and renames system 1's talkgroups from trunk-recorder's file.

    .INPUTS
    Objects with SystemId, Id (or Decimal), Label (or Alpha Tag), Name (or
    Description), Tag, Group (or Category), State, Unreviewed, Type (or
    Mode), Frequency, DelayMinutes, Alert, LedColour, SortOrder and
    FiregroundChannel properties, such as Import-Csv's rows or a
    RadioArchive.Talkgroup.

    .OUTPUTS
    RadioArchive.ImportResult. One per row: Kind, SystemId, Id, Label,
    Result (New, Changed, Unchanged or Skipped), Reason (why skipped),
    Fields (the fields changed), Changes and NotChanged (each Field, Before
    and After: NotChanged are what the row says otherwise and the import
    doesn't change, such as State), and Applied.

    .LINK
    Export-RadioArchiveTalkgroup

    .LINK
    Import-RadioArchiveSetup

    .LINK
    Set-RadioArchiveTalkgroup
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'Medium')]
    [OutputType('RadioArchive.ImportResult')]
    param(
        # The talkgroup's system: give it once for a file that has no
        # SystemId column. No default.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [long] $SystemId,

        # The talkgroup's ID, such as 1001. No default.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [Alias('Talkgroup', 'Decimal')]
        [long] $Id,

        # Its label. Default: as it is (new: none).
        [Parameter(ValueFromPipelineByPropertyName)]
        [Alias('Alpha Tag', 'AlphaTag')]
        [AllowEmptyString()]
        [string] $Label,

        # Its name. Default: as it is (new: none).
        [Parameter(ValueFromPipelineByPropertyName)]
        [Alias('Description')]
        [AllowEmptyString()]
        [string] $Name,

        # Its tag; one that doesn't exist yet is made. Default: as it is. A
        # new talkgroup needs one, unless it's unreviewed.
        [Parameter(ValueFromPipelineByPropertyName)]
        [AllowEmptyString()]
        [AllowNull()]
        [string] $Tag,

        # Exactly these groups, or names separated by semicolons; ones that
        # don't exist yet are made. Default: as it is (new: none).
        [Parameter(ValueFromPipelineByPropertyName)]
        [Alias('Category')]
        [AllowEmptyCollection()]
        [AllowEmptyString()]
        [AllowNull()]
        [string[]] $Group,

        # Public, Hidden or Ignored, for a new talkgroup only: one that
        # exists keeps its state. Default: Public.
        [Parameter(ValueFromPipelineByPropertyName)]
        [ValidateSet('', 'Public', 'Hidden', 'Ignored')]
        [AllowNull()]
        [string] $State,

        # Whether a new talkgroup is unreviewed (True or False); one that
        # exists keeps it. Default: False.
        [Parameter(ValueFromPipelineByPropertyName)]
        [AllowNull()]
        [object] $Unreviewed,

        # What the setup says it is, such as trunk-recorder's mode (D, T,
        # E); A or analog, and conventional, are skipped. Default: as it is
        # (new: none).
        [Parameter(ValueFromPipelineByPropertyName)]
        [Alias('Mode')]
        [AllowEmptyString()]
        [AllowNull()]
        [string] $Type,

        # Its frequency in hertz, a whole number, or empty for none.
        # Default: as it is (new: none).
        [Parameter(ValueFromPipelineByPropertyName)]
        [AllowNull()]
        [object] $Frequency,

        # Minutes listeners wait before hearing it, 0 to 1440. Default: as
        # it is (new: 0).
        [Parameter(ValueFromPipelineByPropertyName)]
        [AllowNull()]
        [object] $DelayMinutes,

        # Its own alert, or empty to take one from its tag, groups or
        # system. Default: as it is (new: none of its own).
        [Parameter(ValueFromPipelineByPropertyName)]
        [ValidateSet('', 'none', 'beep', 'chime', 'pip', 'tone')]
        [AllowNull()]
        [string] $Alert,

        # Its own LED colour, or empty to take one from its tag, groups or
        # system. Default: as it is (new: none of its own).
        [Parameter(ValueFromPipelineByPropertyName)]
        [ValidateSet('', 'none', 'red', 'orange', 'yellow', 'green', 'cyan', 'blue', 'purple', 'pink')]
        [AllowNull()]
        [string] $LedColour,

        # Where it sorts, a whole number. Default: as it is (new: 0).
        [Parameter(ValueFromPipelineByPropertyName)]
        [AllowNull()]
        [object] $SortOrder,

        # Its fireground channel number, or empty for none. Default: as it
        # is (new: none).
        [Parameter(ValueFromPipelineByPropertyName)]
        [AllowNull()]
        [object] $FiregroundChannel,

        # Talkgroups that exist also take the delay, alert, LED colour, type
        # and frequency the rows give. Never their state. Asks first, as a
        # High impact change does. Off by default.
        [switch] $Update
    )
    begin {
        # -Update counts as ConfirmImpact High (guidelines section 4).
        if ($Update -and -not $PSBoundParameters.ContainsKey('Confirm') -and $ConfirmPreference -eq 'High') {
            $ConfirmPreference = 'Medium'
        }
        $fixed = @($PSBoundParameters.Keys)
        $setup = New-RaSetup
        $rows = 0
        $failed = $false
    }
    process {
        try {
            $said = Get-RaSaid -Cmdlet $PSCmdlet -InputObject $_ -Fixed $fixed -Bound $PSBoundParameters
            $where = "System $SystemId, talkgroup $Id"
            $row = [ordered]@{ id = $Id }
            $fields = @(
                @('Label', 'label', 'Text', 'Value'), @('Name', 'name', 'Text', 'Value'), @('Tag', 'tag', 'Text', 'Skip'),
                @('Group', 'groups', 'Names', 'Value'), @('State', 'state', 'Lower', 'Skip'), @('Unreviewed', 'unreviewed', 'Bool', 'Skip'),
                @('Type', 'type', 'Text', 'Value'), @('Frequency', 'frequency', 'Number', 'Null'),
                @('DelayMinutes', 'delay_minutes', 'Number', 'Skip'), @('Alert', 'alert', 'Lower', 'Null'),
                @('LedColour', 'led_colour', 'Lower', 'Null'), @('SortOrder', 'sort_order', 'Number', 'Skip'),
                @('FiregroundChannel', 'fireground_channel', 'Number', 'Null')
            )
            foreach ($f in $fields) {
                if ($said.Contains($f[0])) {
                    Add-RaImportField -Row $row -Field $f[1] -Value (Get-Variable -Name $f[0] -ValueOnly) -As $f[2] -Empty $f[3] -Where $where
                }
            }
            $system = Get-RaSetupSystem -Setup $setup -SystemId $SystemId
            if (-not $system.Contains('talkgroups')) {
                $system.talkgroups = [System.Collections.Generic.List[object]]::new()
            }
            $system.talkgroups.Add($row)
            $rows++
        } catch {
            $failed = $true
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject "$SystemId/$Id"
        }
    }
    end {
        if ($failed -or $rows -eq 0) {
            # A bad row stops the batch, as the radio's own check would:
            # nothing is imported until every row is right.
            return
        }
        try {
            $preview = Get-RaImportPreview -Setup $setup -Kind 'talkgroup' -Update $Update
            $apply = $preview.Changes -gt 0 -and $PSCmdlet.ShouldProcess("$rows talkgroup row$(if ($rows -ne 1) { 's' })", $preview.Action)
            Complete-RaImport -Preview $preview -Apply $apply -Kind 'talkgroup'
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_
        }
    }
}

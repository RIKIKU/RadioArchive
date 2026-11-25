function Import-RadioArchiveGroup {
    <#
    .SYNOPSIS
    Imports groups from the pipeline, such as a CSV, as one batch with a preview.

    .DESCRIPTION
    Takes group rows by property name: a CSV that Export-RadioArchiveGroup
    wrote, or RadioArchive.Group objects. Groups go by name. The whole
    pipeline is one import (ADR 0030): the radio previews every row first,
    -WhatIf shows that preview, and the rows are applied together after
    one confirmation. A new group is added with the row's alert, LED
    colour and sort order. A group that exists changes only with -Update,
    which makes it take those where they differ; its talkgroups take their
    settings from it (ADR 0006). Nothing is deleted.

    .EXAMPLE
    Export-RadioArchiveGroup -Path groups.csv
    Import-Csv -Path groups.csv | Import-RadioArchiveGroup -Update

    .INPUTS
    Objects with Name, Alert, LedColour and SortOrder properties, such as
    Import-Csv's rows or a RadioArchive.Group.

    .OUTPUTS
    RadioArchive.ImportResult. One per row: Kind, Id (the name), Label,
    Result (New, Changed or Unchanged), Fields, Changes, NotChanged and
    Applied.

    .LINK
    Export-RadioArchiveGroup

    .LINK
    Set-RadioArchiveGroup
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'Medium')]
    [OutputType('RadioArchive.ImportResult')]
    param(
        # The group's name, such as North. No default.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [string] $Name,

        # Its alert, or empty for none. Default: as it is (new: none).
        [Parameter(ValueFromPipelineByPropertyName)]
        [ValidateSet('', 'none', 'beep', 'chime', 'pip', 'tone')]
        [AllowNull()]
        [string] $Alert,

        # Its LED colour, or empty for none. Default: as it is (new: none).
        [Parameter(ValueFromPipelineByPropertyName)]
        [ValidateSet('', 'none', 'red', 'orange', 'yellow', 'green', 'cyan', 'blue', 'purple', 'pink')]
        [AllowNull()]
        [string] $LedColour,

        # Where it sorts, a whole number. Default: as it is (new: 0).
        [Parameter(ValueFromPipelineByPropertyName)]
        [AllowNull()]
        [object] $SortOrder,

        # Groups that exist take the settings the rows give. Asks first, as a
        # High impact change does. Off by default.
        [switch] $Update
    )
    begin {
        if ($Update -and -not $PSBoundParameters.ContainsKey('Confirm') -and $ConfirmPreference -eq 'High') {
            $ConfirmPreference = 'Medium'
        }
        $fixed = @($PSBoundParameters.Keys)
        $setup = New-RaSetup
        $failed = $false
    }
    process {
        try {
            $said = Get-RaSaid -Cmdlet $PSCmdlet -InputObject $_ -Fixed $fixed -Bound $PSBoundParameters
            $setup.groups.Add((ConvertTo-RaLabelRow -Name $Name -Said $said -Alert $Alert -LedColour $LedColour -SortOrder $SortOrder -Kind 'Group'))
        } catch {
            $failed = $true
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $Name
        }
    }
    end {
        if ($failed -or $setup.groups.Count -eq 0) {
            return
        }
        try {
            $n = $setup.groups.Count
            $preview = Get-RaImportPreview -Setup $setup -Kind 'group' -Update $Update
            $apply = $preview.Changes -gt 0 -and $PSCmdlet.ShouldProcess("$n group row$(if ($n -ne 1) { 's' })", $preview.Action)
            Complete-RaImport -Preview $preview -Apply $apply -Kind 'group'
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_
        }
    }
}

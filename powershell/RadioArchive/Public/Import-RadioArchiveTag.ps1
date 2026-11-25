function Import-RadioArchiveTag {
    <#
    .SYNOPSIS
    Imports tags from the pipeline, such as a CSV, as one batch with a preview.

    .DESCRIPTION
    Takes tag rows by property name: a CSV that Export-RadioArchiveTag
    wrote, or RadioArchive.Tag objects. Tags go by name. The whole pipeline
    is one import (ADR 0030): the radio previews every row first, -WhatIf
    shows that preview, and the rows are applied together after one
    confirmation. A new tag is added with the row's alert, LED colour and
    sort order. A tag that exists changes only with -Update, which makes it
    take those where they differ; its talkgroups take their settings from
    it (ADR 0006). Nothing is deleted.

    .EXAMPLE
    Export-RadioArchiveTag -Path tags.csv
    Import-Csv -Path tags.csv | Import-RadioArchiveTag -Update -WhatIf

    .INPUTS
    Objects with Name, Alert, LedColour and SortOrder properties, such as
    Import-Csv's rows or a RadioArchive.Tag.

    .OUTPUTS
    RadioArchive.ImportResult. One per row: Kind, Id (the name), Label,
    Result (New, Changed or Unchanged), Fields, Changes, NotChanged and
    Applied.

    .LINK
    Export-RadioArchiveTag

    .LINK
    Set-RadioArchiveTag
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'Medium')]
    [OutputType('RadioArchive.ImportResult')]
    param(
        # The tag's name, such as dispatch. No default.
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

        # Tags that exist take the settings the rows give. Asks first, as a
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
            $setup.tags.Add((ConvertTo-RaLabelRow -Name $Name -Said $said -Alert $Alert -LedColour $LedColour -SortOrder $SortOrder -Kind 'Tag'))
        } catch {
            $failed = $true
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $Name
        }
    }
    end {
        if ($failed -or $setup.tags.Count -eq 0) {
            return
        }
        try {
            $n = $setup.tags.Count
            $preview = Get-RaImportPreview -Setup $setup -Kind 'tag' -Update $Update
            $apply = $preview.Changes -gt 0 -and $PSCmdlet.ShouldProcess("$n tag row$(if ($n -ne 1) { 's' })", $preview.Action)
            Complete-RaImport -Preview $preview -Apply $apply -Kind 'tag'
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_
        }
    }
}

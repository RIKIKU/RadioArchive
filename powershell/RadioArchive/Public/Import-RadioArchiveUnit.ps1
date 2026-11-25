function Import-RadioArchiveUnit {
    <#
    .SYNOPSIS
    Imports units' aliases from the pipeline, such as a CSV, as one batch with a preview.

    .DESCRIPTION
    Takes unit rows by property name: a CSV that Export-RadioArchiveUnit
    wrote, a two-column unit CSV (ID and Alias, for the system -SystemId
    names), or RadioArchive.Unit objects. A unit is a radio ID, such as
    5001, or a range, such as 5100-5199 (R26). The whole pipeline is one
    import (R52, ADR 0030): the radio previews every row first, -WhatIf
    shows that preview, and the rows are applied together after one
    confirmation. New units are added; units that exist take the row's
    alias, their only field, so there's no -Update. Nothing missing from
    the rows is deleted.

    .EXAMPLE
    Import-Csv -Path units.csv | Import-RadioArchiveUnit -SystemId 1 -WhatIf

    .EXAMPLE
    Export-RadioArchiveUnit -Path units.csv
    Import-Csv -Path units.csv | Import-RadioArchiveUnit

    The round trip: every unit Unchanged.

    .INPUTS
    Objects with SystemId, Unit (or ID) and Alias properties, such as
    Import-Csv's rows or a RadioArchive.Unit.

    .OUTPUTS
    RadioArchive.ImportResult. One per row: Kind, SystemId, Id (the unit),
    Label, Result (New, Changed or Unchanged), Fields, Changes, NotChanged
    and Applied.

    .LINK
    Export-RadioArchiveUnit

    .LINK
    Set-RadioArchiveUnit
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'Medium')]
    [OutputType('RadioArchive.ImportResult')]
    param(
        # The system: give it once for a file that has no SystemId column.
        # No default.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [long] $SystemId,

        # A radio ID, such as 5001, or a range, such as 5100-5199. No
        # default.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [Alias('ID')]
        [ValidatePattern('^\s*[1-9][0-9]*(\s*-\s*[1-9][0-9]*)?\s*$')]
        [string] $Unit,

        # Its alias, up to 100 characters, or '' for none. No default.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [AllowEmptyString()]
        [string] $Alias
    )
    begin {
        $setup = New-RaSetup
        $rows = 0
    }
    process {
        $from, $to = @($Unit -split '-' | ForEach-Object -Process { [long]$_.Trim() })
        $row = [ordered]@{ from = $from; to = $(if ($null -ne $to) { $to } else { $from }); alias = $Alias }
        $system = Get-RaSetupSystem -Setup $setup -SystemId $SystemId
        if (-not $system.Contains('units')) {
            $system.units = [System.Collections.Generic.List[object]]::new()
        }
        $system.units.Add($row)
        $rows++
    }
    end {
        if ($rows -eq 0) {
            return
        }
        try {
            $preview = Get-RaImportPreview -Setup $setup -Kind 'unit'
            $apply = $preview.Changes -gt 0 -and $PSCmdlet.ShouldProcess("$rows unit row$(if ($rows -ne 1) { 's' })", $preview.Action)
            Complete-RaImport -Preview $preview -Apply $apply -Kind 'unit'
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_
        }
    }
}

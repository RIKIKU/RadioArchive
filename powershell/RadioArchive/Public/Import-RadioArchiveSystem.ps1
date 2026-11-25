function Import-RadioArchiveSystem {
    <#
    .SYNOPSIS
    Imports systems from the pipeline, such as a CSV, as one batch with a preview.

    .DESCRIPTION
    Takes system rows by property name: a CSV that
    Export-RadioArchiveSystem wrote, or RadioArchive.System objects. The
    whole pipeline is one import (ADR 0030): the radio previews every row
    first, -WhatIf shows that preview, and the rows are applied together
    after one confirmation. A new system is added with every field its row
    gives, and needs a label. A system that exists changes only with
    -Update, which makes it take the row's label, name, auto-create, alert,
    LED colour and sort order where they differ. Talkgroups, units and
    attachments aren't changed, and nothing is deleted.

    .EXAMPLE
    Export-RadioArchiveSystem -Path systems.csv
    Import-Csv -Path systems.csv | Import-RadioArchiveSystem -Update -WhatIf

    .INPUTS
    Objects with SystemId, Label, Name, AutoCreate, Alert, LedColour and
    SortOrder properties, such as Import-Csv's rows or a
    RadioArchive.System.

    .OUTPUTS
    RadioArchive.ImportResult. One per row: Kind, Id, Label, Result (New,
    Changed or Unchanged), Fields, Changes, NotChanged (what the row says
    otherwise and an import without -Update doesn't change) and Applied.

    .LINK
    Export-RadioArchiveSystem

    .LINK
    Set-RadioArchiveSystem
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'Medium')]
    [OutputType('RadioArchive.ImportResult')]
    param(
        # The system's ID. No default.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [long] $SystemId,

        # Its label, such as EX. Default: as it is; a new system needs one.
        [Parameter(ValueFromPipelineByPropertyName)]
        [AllowEmptyString()]
        [AllowNull()]
        [string] $Label,

        # Its name. Default: as it is (new: none).
        [Parameter(ValueFromPipelineByPropertyName)]
        [AllowEmptyString()]
        [AllowNull()]
        [string] $Name,

        # Whether calls on talkgroups it doesn't know add them, unreviewed
        # (True or False). Default: as it is (new: False).
        [Parameter(ValueFromPipelineByPropertyName)]
        [AllowNull()]
        [object] $AutoCreate,

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

        # Systems that exist take the fields the rows give. Asks first, as a
        # High impact change does. Off by default.
        [switch] $Update
    )
    begin {
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
            if (@($setup.systems | Where-Object -FilterScript { $_.id -eq $SystemId }).Count -gt 0) {
                throw (New-RaErrorRecord -ErrorId 'duplicate_row' -Category InvalidArgument -Message "System $SystemId is in the rows more than once.")
            }
            $system = Get-RaSetupSystem -Setup $setup -SystemId $SystemId
            $fields = @(
                @('Label', 'label', 'Text', 'Skip'), @('Name', 'name', 'Text', 'Value'), @('AutoCreate', 'auto_create', 'Bool', 'Skip'),
                @('Alert', 'alert', 'Lower', 'Null'), @('LedColour', 'led_colour', 'Lower', 'Null'), @('SortOrder', 'sort_order', 'Number', 'Skip')
            )
            foreach ($f in $fields) {
                if ($said.Contains($f[0])) {
                    Add-RaImportField -Row $system -Field $f[1] -Value (Get-Variable -Name $f[0] -ValueOnly) -As $f[2] -Empty $f[3] -Where "System $SystemId"
                }
            }
            $rows++
        } catch {
            $failed = $true
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $SystemId
        }
    }
    end {
        if ($failed -or $rows -eq 0) {
            return
        }
        try {
            $preview = Get-RaImportPreview -Setup $setup -Kind 'system' -Update $Update
            $apply = $preview.Changes -gt 0 -and $PSCmdlet.ShouldProcess("$rows system row$(if ($rows -ne 1) { 's' })", $preview.Action)
            Complete-RaImport -Preview $preview -Apply $apply -Kind 'system'
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_
        }
    }
}

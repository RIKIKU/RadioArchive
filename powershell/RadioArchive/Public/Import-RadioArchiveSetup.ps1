function Import-RadioArchiveSetup {
    <#
    .SYNOPSIS
    Imports a whole setup from a JSON file, such as an export, with a preview.

    .DESCRIPTION
    Imports a whole-setup JSON file (R52): systems, each with its
    talkgroups and units, and the groups and tags, in the shape
    Export-RadioArchiveSetup saves. Each file is one import (ADR 0030): the
    radio previews every row first, -WhatIf shows that preview, and the
    file is applied once, after one confirmation, or not at all.

    New systems, talkgroups, units, groups and tags are added with every
    field the file gives. A talkgroup that exists changes only its label,
    name, tag, groups, fireground channel number and sort order; a unit
    its alias. With -Update, what exists also takes every other field the
    file gives otherwise: a talkgroup's delay, alert, LED colour, type and
    frequency, and a system's, group's or tag's own fields. A talkgroup's
    state and unreviewed never change through an import, -Update or not,
    so an old file can't make hidden talkgroups public. Nothing missing
    from the file is deleted; it's reported as Kept.

    .EXAMPLE
    Import-RadioArchiveSetup -Path setup.json -WhatIf

    Every row's result, and what the file leaves out (Kept); nothing is
    changed.

    .EXAMPLE
    Export-RadioArchiveSetup -Path setup.json
    Import-RadioArchiveSetup -Path setup.json

    The round trip: nothing to change.

    .INPUTS
    System.String. Objects with a Path or FullName property, such as
    Get-ChildItem's files.

    .OUTPUTS
    RadioArchive.ImportResult. One per row and per thing the file leaves
    out: Kind, SystemId, Id, Label, Result (New, Changed, Unchanged, Kept
    or Skipped), Reason, Fields, Changes, NotChanged and Applied.

    .LINK
    Export-RadioArchiveSetup

    .LINK
    Import-RadioArchiveTalkgroup
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'Medium')]
    [OutputType('RadioArchive.ImportResult')]
    param(
        # The JSON file. No default. No wildcards.
        [Parameter(Mandatory, ValueFromPipeline, ValueFromPipelineByPropertyName)]
        [Alias('FullName')]
        [string] $Path,

        # What exists takes every field the file gives, but a talkgroup's
        # state and unreviewed. Asks first, as a High impact change does.
        # Off by default.
        [switch] $Update
    )
    begin {
        if ($Update -and -not $PSBoundParameters.ContainsKey('Confirm') -and $ConfirmPreference -eq 'High') {
            $ConfirmPreference = 'Medium'
        }
    }
    process {
        try {
            $file = $PSCmdlet.GetUnresolvedProviderPathFromPSPath($Path)
            if (-not (Test-Path -LiteralPath $file -PathType Leaf)) {
                throw (New-RaErrorRecord -ErrorId 'no_such_file' -Category ObjectNotFound -Message "There's no file $file.")
            }
            try {
                $setup = [System.IO.File]::ReadAllText($file) | ConvertFrom-Json -Depth 32 -DateKind String -ErrorAction Stop
            } catch {
                throw (New-RaErrorRecord -ErrorId 'invalid_json' -Category InvalidData -Message "$file isn't JSON: $($_.Exception.Message)")
            }
            $kinds = 'system', 'talkgroup', 'unit', 'group', 'tag'
            $preview = Get-RaImportPreview -Setup $setup -Kind $kinds -Update $Update
            $apply = $preview.Changes -gt 0 -and $PSCmdlet.ShouldProcess($file, $preview.Action)
            Complete-RaImport -Preview $preview -Apply $apply -Kind $kinds -IncludeKept $true
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $Path
        }
    }
}

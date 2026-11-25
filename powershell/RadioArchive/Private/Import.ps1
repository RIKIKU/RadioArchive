# The import and export commands (issue #117, ADR 0030, ADR 0035): rows
# from the pipeline become one setup, sent as one batch to
# POST /api/v1/admin/import, first as a dry run, then with its plan.

function Get-RaSaid {
    <#
    .SYNOPSIS
    The parameters an import row says: its own properties, and the command line's.

    .DESCRIPTION
    A field a row leaves out is "not said", and changes nothing (ADR 0030).
    $PSBoundParameters can't tell: it keeps a parameter bound by an earlier
    pipeline object. So a piped row says the parameters whose name or alias
    is one of its properties, plus those the command line gave (Fixed, read
    in begin); with no pipeline, the bound parameters are the row.
    #>
    [CmdletBinding()]
    [OutputType([System.Collections.Generic.HashSet[string]])]
    param(
        # The public command's $PSCmdlet.
        [Parameter(Mandatory)]
        [System.Management.Automation.PSCmdlet] $Cmdlet,

        # The pipeline object, $_.
        [AllowNull()]
        [object] $InputObject,

        # The parameters the command line gave.
        [Parameter(Mandatory)]
        [AllowEmptyCollection()]
        [string[]] $Fixed,

        # The command's $PSBoundParameters.
        [Parameter(Mandatory)]
        [System.Collections.IDictionary] $Bound
    )
    $said = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::OrdinalIgnoreCase)
    foreach ($name in $Fixed) {
        $null = $said.Add($name)
    }
    if (-not $Cmdlet.MyInvocation.ExpectingInput) {
        foreach ($name in $Bound.Keys) {
            $null = $said.Add($name)
        }
    } elseif ($null -ne $InputObject) {
        foreach ($p in $Cmdlet.MyInvocation.MyCommand.Parameters.Values) {
            foreach ($name in @($p.Name) + @($p.Aliases)) {
                if ($InputObject.PSObject.Properties[$name]) {
                    $null = $said.Add($p.Name)
                    break
                }
            }
        }
    }
    Write-Output -InputObject $said -NoEnumerate
}

function Add-RaImportField {
    <#
    .SYNOPSIS
    Puts one field of an import row into its JSON, as text, a number, a yes or no, or a list of names.

    .DESCRIPTION
    Rows come from objects or from a CSV, where every value is text, so
    each kind of value is read from either. Empty says what an empty value
    (or $null) means for this field: Skip leaves it unsaid, Null sends
    null ("none"), Value sends '' for text (and leaves numbers unsaid).
    #>
    [Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSUseShouldProcessForStateChangingFunctions', '', Justification = 'Fills a hashtable in memory; changes nothing.')]
    [CmdletBinding()]
    param(
        # The row's JSON.
        [Parameter(Mandatory)]
        [System.Collections.IDictionary] $Row,

        # The JSON field.
        [Parameter(Mandatory)]
        [string] $Field,

        # The value.
        [AllowNull()]
        [object] $Value,

        # Text, Lower (text in lowercase), Number, Bool or Names.
        [ValidateSet('Text', 'Lower', 'Number', 'Bool', 'Names')]
        [string] $As = 'Text',

        # Skip, Null or Value.
        [ValidateSet('Skip', 'Null', 'Value')]
        [string] $Empty = 'Value',

        # The row, as an error names it.
        [Parameter(Mandatory)]
        [string] $Where
    )
    if ($As -eq 'Names') {
        $names = foreach ($v in @($Value)) {
            foreach ($n in ([string]$v -split ';')) {
                if ($n.Trim()) {
                    $n.Trim()
                }
            }
        }
        $Row[$Field] = [string[]]@($names)
        return
    }
    if ($null -eq $Value -or ($Value -is [string] -and $Value.Trim() -eq '')) {
        switch ($Empty) {
            'Null' { $Row[$Field] = $null }
            'Value' {
                if ($As -in 'Text', 'Lower') {
                    $Row[$Field] = ''
                }
            }
        }
        return
    }
    switch ($As) {
        'Text' { $Row[$Field] = [string]$Value }
        'Lower' { $Row[$Field] = ([string]$Value).ToLowerInvariant() }
        'Number' {
            if ($Value -is [int] -or $Value -is [long] -or $Value -is [short] -or $Value -is [byte]) {
                $Row[$Field] = [long]$Value
                return
            }
            $n = [long]0
            if (-not [long]::TryParse(([string]$Value).Trim(), [System.Globalization.NumberStyles]::Integer, [System.Globalization.CultureInfo]::InvariantCulture, [ref]$n)) {
                throw (New-RaErrorRecord -ErrorId 'invalid_value' -Category InvalidArgument -Message "${Where}: $Field must be a whole number, not '$Value'.")
            }
            $Row[$Field] = $n
        }
        'Bool' {
            if ($Value -is [bool]) {
                $Row[$Field] = $Value
                return
            }
            $Row[$Field] = switch -Regex (([string]$Value).Trim()) {
                '^(true|yes|1)$' { $true; break }
                '^(false|no|0)$' { $false; break }
                default { throw (New-RaErrorRecord -ErrorId 'invalid_value' -Category InvalidArgument -Message "${Where}: $Field must be True or False, not '$Value'.") }
            }
        }
    }
}

function New-RaSetup {
    <#
    .SYNOPSIS
    An empty setup document in the import's format.
    #>
    [Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSUseShouldProcessForStateChangingFunctions', '', Justification = 'Makes an object in memory; changes nothing.')]
    [CmdletBinding()]
    [OutputType([System.Collections.Specialized.OrderedDictionary])]
    param()
    [ordered]@{
        format  = 'radioarchive-setup'
        version = 1
        systems = [System.Collections.Generic.List[object]]::new()
        groups  = [System.Collections.Generic.List[object]]::new()
        tags    = [System.Collections.Generic.List[object]]::new()
    }
}

function Get-RaSetupSystem {
    <#
    .SYNOPSIS
    The setup's entry for a system, added with only its ID when it's new to the setup.
    #>
    [CmdletBinding()]
    [OutputType([System.Collections.Specialized.OrderedDictionary])]
    param(
        # The setup.
        [Parameter(Mandatory)]
        [System.Collections.IDictionary] $Setup,

        # The system's ID.
        [Parameter(Mandatory)]
        [long] $SystemId
    )
    foreach ($s in $Setup.systems) {
        if ($s.id -eq $SystemId) {
            return $s
        }
    }
    $s = [ordered]@{ id = $SystemId }
    $Setup.systems.Add($s)
    $s
}

function ConvertTo-RaLabelRow {
    <#
    .SYNOPSIS
    A group's or tag's import row: its name and the settings the row says.
    #>
    [CmdletBinding()]
    [OutputType([System.Collections.Specialized.OrderedDictionary])]
    param(
        # Its name.
        [Parameter(Mandatory)]
        [string] $Name,

        # The parameters the row says (Get-RaSaid).
        [Parameter(Mandatory)]
        [System.Collections.Generic.HashSet[string]] $Said,

        # Its alert.
        [AllowNull()]
        [string] $Alert,

        # Its LED colour.
        [AllowNull()]
        [string] $LedColour,

        # Its sort order.
        [AllowNull()]
        [object] $SortOrder,

        # Group or Tag, as an error names it.
        [Parameter(Mandatory)]
        [string] $Kind
    )
    $row = [ordered]@{ name = $Name }
    $where = "$Kind '$Name'"
    if ($Said.Contains('Alert')) {
        Add-RaImportField -Row $row -Field 'alert' -Value $Alert -As Lower -Empty Null -Where $where
    }
    if ($Said.Contains('LedColour')) {
        Add-RaImportField -Row $row -Field 'led_colour' -Value $LedColour -As Lower -Empty Null -Where $where
    }
    if ($Said.Contains('SortOrder')) {
        Add-RaImportField -Row $row -Field 'sort_order' -Value $SortOrder -As Number -Empty Skip -Where $where
    }
    $row
}

function ConvertTo-RaImportResult {
    <#
    .SYNOPSIS
    One of an import's results as a RadioArchive.ImportResult.
    #>
    [CmdletBinding()]
    [OutputType('RadioArchive.ImportResult')]
    param(
        # The API's result.
        [Parameter(Mandatory)]
        [object] $Row,

        # Whether the import was applied.
        [Parameter(Mandatory)]
        [bool] $Applied
    )
    $field = {
        param($c)
        [pscustomobject]@{ Field = [string]$c.field; Before = $c.before; After = $c.after }
    }
    $changes = @(foreach ($c in @(Get-RaProperty -Row $Row -Name 'changes')) { if ($null -ne $c) { & $field $c } })
    $notChanged = @(foreach ($c in @(Get-RaProperty -Row $Row -Name 'not_changed')) { if ($null -ne $c) { & $field $c } })
    $system = Get-RaProperty -Row $Row -Name 'system'
    [pscustomobject]@{
        PSTypeName = 'RadioArchive.ImportResult'
        Kind       = ConvertTo-RaTitle -Value $Row.kind
        SystemId   = if ($null -ne $system) { [long]$system } else { $null }
        Id         = [string]$Row.id
        Label      = [string]$Row.label
        Result     = ConvertTo-RaTitle -Value $Row.result
        Reason     = ConvertTo-RaTitle -Value (Get-RaProperty -Row $Row -Name 'reason')
        Fields     = [string[]]@($changes | ForEach-Object -Process { $_.Field })
        Changes    = $changes
        NotChanged = $notChanged
        Applied    = $Applied
    }
}

function Get-RaImportPreview {
    <#
    .SYNOPSIS
    An import's dry run: the radio's report, how many things it would change, and what ShouldProcess says.

    .DESCRIPTION
    The public command asks ShouldProcess once with Action, when Changes
    isn't 0, then hands the preview to Complete-RaImport (issue #117).
    #>
    [CmdletBinding()]
    [OutputType([hashtable])]
    param(
        # The setup.
        [Parameter(Mandatory)]
        [object] $Setup,

        # The kinds the summary counts: talkgroup, unit, system, group, tag.
        [Parameter(Mandatory)]
        [string[]] $Kind,

        # Update what exists (ADR 0035).
        [bool] $Update
    )
    $dry = Invoke-RaApi -Method POST -Path '/api/v1/admin/import' -Body ([ordered]@{ setup = $Setup; dry_run = $true; update = $Update })
    $counts = @{ new = 0; changed = 0; unchanged = 0; kept = 0; skipped = 0 }
    $plural = @{ talkgroup = 'talkgroups'; unit = 'units'; system = 'systems'; group = 'groups'; tag = 'tags' }
    foreach ($k in $Kind) {
        $c = $dry.summary.($plural[$k])
        foreach ($r in @($counts.Keys)) {
            $counts[$r] += [int]$c.$r
        }
    }
    $summary = "$($counts.new) new, $($counts.changed) changed, $($counts.unchanged) unchanged, $($counts.skipped) skipped"
    $changes = 0
    foreach ($k in $dry.summary.PSObject.Properties) {
        $changes += [int]$k.Value.new + [int]$k.Value.changed
    }
    if ($changes -eq 0) {
        Write-Verbose -Message "Nothing to change: $summary."
    }
    @{
        Setup   = $Setup
        Update  = $Update
        Report  = $dry
        Changes = $changes
        Action  = if ($Update) { "Import, updating what exists ($summary)" } else { "Import ($summary)" }
    }
}

function Complete-RaImport {
    <#
    .SYNOPSIS
    Applies a previewed import with its plan, when asked to, and outputs its results.

    .DESCRIPTION
    Outputs the results of the kinds asked for, as the radio answered them:
    with Applied $true when applied, and $false after -WhatIf, a declined
    confirmation, or when nothing would change (then nothing is sent but
    the dry run). Things the file leaves out (kept) are output only with
    IncludeKept. A refusal, import_changed included, is the command's
    error; nothing is applied then.
    #>
    [CmdletBinding()]
    [OutputType('RadioArchive.ImportResult')]
    param(
        # What Get-RaImportPreview answered.
        [Parameter(Mandatory)]
        [hashtable] $Preview,

        # Whether to apply it.
        [bool] $Apply,

        # The kinds of result to output: talkgroup, unit, system, group, tag.
        [Parameter(Mandatory)]
        [string[]] $Kind,

        # Output kept results too.
        [bool] $IncludeKept
    )
    $reply = $Preview.Report
    $applied = $false
    if ($Apply) {
        $reply = Invoke-RaApi -Method POST -Path '/api/v1/admin/import' -Body ([ordered]@{ setup = $Preview.Setup; plan = $Preview.Report.plan; update = $Preview.Update })
        $applied = [bool]$reply.applied
    }
    foreach ($r in @($reply.results)) {
        if ($null -eq $r -or $r.kind -notin $Kind -or ($r.result -eq 'kept' -and -not $IncludeKept)) {
            continue
        }
        ConvertTo-RaImportResult -Row $r -Applied $applied
    }
}

function Export-RaCsv {
    <#
    .SYNOPSIS
    Writes rows to a CSV file as Export-Csv does, header and all even with no rows, and outputs the file.
    #>
    [CmdletBinding()]
    [OutputType([System.IO.FileInfo])]
    param(
        # The public command's $PSCmdlet.
        [Parameter(Mandatory)]
        [System.Management.Automation.PSCmdlet] $Cmdlet,

        # The file.
        [Parameter(Mandatory)]
        [string] $Path,

        # The columns, in order.
        [Parameter(Mandatory)]
        [string[]] $Column,

        # The rows.
        [Parameter(Mandatory)]
        [AllowEmptyCollection()]
        [object[]] $Rows,

        # Replace a file that's there.
        [bool] $Force
    )
    $file = $Cmdlet.GetUnresolvedProviderPathFromPSPath($Path)
    if ((Test-Path -LiteralPath $file) -and -not $Force) {
        throw (New-RaErrorRecord -ErrorId 'file_exists' -Category ResourceExists -Message "$file is already there. Add -Force to replace it.")
    }
    $lines = if ($Rows.Count -gt 0) {
        @($Rows | Select-Object -Property $Column | ConvertTo-Csv -NoTypeInformation)
    } else {
        @(($Column | ForEach-Object -Process { '"' + $_ + '"' }) -join ',')
    }
    [System.IO.File]::WriteAllLines($file, [string[]]$lines, [System.Text.UTF8Encoding]::new($false))
    Get-Item -LiteralPath $file
}

function Get-RaSetup {
    <#
    .SYNOPSIS
    The radio's whole setup, GET /api/v1/admin/setup, as objects.

    .DESCRIPTION
    Read from its text with -DateKind String, so a label that looks like a
    date stays the text it is.
    #>
    [CmdletBinding()]
    [OutputType([object])]
    param()
    $temp = [System.IO.Path]::GetTempFileName()
    try {
        $null = Invoke-RaApi -Method GET -Path '/api/v1/admin/setup' -OutFile $temp
        [System.IO.File]::ReadAllText($temp) | ConvertFrom-Json -Depth 32 -DateKind String
    } finally {
        Remove-Item -LiteralPath $temp -Force -ErrorAction Ignore
    }
}

function ConvertTo-RaLabelCsvRow {
    <#
    .SYNOPSIS
    The setup's groups or tags as CSV rows: Name, Alert, LedColour, SortOrder.
    #>
    [CmdletBinding()]
    [OutputType([pscustomobject])]
    param(
        # The setup's groups or tags.
        [Parameter(Mandatory)]
        [AllowEmptyCollection()]
        [object[]] $Rows,

        # The names (wildcards) wanted.
        [Parameter(Mandatory)]
        [AllowEmptyCollection()]
        [string[]] $Wanted,

        # Whether only the wanted ones are.
        [bool] $Filter
    )
    foreach ($l in $Rows) {
        if ($null -eq $l -or ($Filter -and -not ($Wanted.Count -gt 0 -and (Test-RaWildcard -Value $l.name -Pattern $Wanted)))) {
            continue
        }
        [pscustomobject]@{
            Name      = [string]$l.name
            Alert     = Format-RaCell -Value $l.alert
            LedColour = Format-RaCell -Value $l.led_colour
            SortOrder = Format-RaCell -Value $l.sort_order
        }
    }
}

function Format-RaCell {
    <#
    .SYNOPSIS
    A value as a CSV cell: '' for null, True or False, numbers in invariant digits.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        # The value.
        [AllowNull()]
        [object] $Value
    )
    if ($null -eq $Value) {
        return ''
    }
    if ($Value -is [bool]) {
        return $(if ($Value) { 'True' } else { 'False' })
    }
    [string]::Format([System.Globalization.CultureInfo]::InvariantCulture, '{0}', $Value)
}

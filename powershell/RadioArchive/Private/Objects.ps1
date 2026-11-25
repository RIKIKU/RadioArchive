# The module's objects (guidelines section 5): each has a type name, and
# its properties are named as the parameters that take them, so a Get-
# pipes into its Set- (section 3).

function ConvertTo-RaTime {
    <#
    .SYNOPSIS
    A time from the API as a [datetimeoffset] in UTC, or $null.
    #>
    [CmdletBinding()]
    [OutputType([datetimeoffset])]
    param(
        # The value as Invoke-RestMethod read it: a string or a [datetime].
        [AllowNull()]
        [object] $Value
    )
    if ($null -eq $Value -or "$Value" -eq '') {
        return $null
    }
    if ($Value -is [datetime]) {
        return [datetimeoffset]::new($Value.ToUniversalTime())
    }
    if ($Value -is [datetimeoffset]) {
        return $Value.ToUniversalTime()
    }
    [datetimeoffset]::Parse([string]$Value, [System.Globalization.CultureInfo]::InvariantCulture).ToUniversalTime()
}

function Get-RaProperty {
    <#
    .SYNOPSIS
    A property of an API row, or $null when it's absent.
    #>
    [CmdletBinding()]
    [OutputType([object])]
    param(
        # The row.
        [Parameter(Mandatory)]
        [AllowNull()]
        [object] $Row,

        # The property's name in the API.
        [Parameter(Mandatory)]
        [string] $Name
    )
    if ($null -eq $Row) {
        return $null
    }
    $p = $Row.PSObject.Properties[$Name]
    if ($p) {
        return $p.Value
    }
    $null
}

function ConvertTo-RaTitle {
    <#
    .SYNOPSIS
    public -> Public: the API's lowercase words as the module shows them.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        # The word.
        [AllowNull()]
        [string] $Value
    )
    if (-not $Value) {
        return $Value
    }
    $Value.Substring(0, 1).ToUpperInvariant() + $Value.Substring(1)
}

function ConvertTo-RaSystem {
    <#
    .SYNOPSIS
    An API system as a RadioArchive.System.
    #>
    [CmdletBinding()]
    [OutputType('RadioArchive.System')]
    param(
        # The API's row.
        [Parameter(Mandatory)]
        [object] $Row
    )
    [pscustomobject]@{
        PSTypeName = 'RadioArchive.System'
        SystemId   = [long]$Row.id
        Label      = [string]$Row.label
        Name       = [string]$Row.name
        AutoCreate = [bool]$Row.auto_create
        Alert      = $Row.alert
        LedColour  = $Row.led_colour
        SortOrder  = [int]$Row.sort_order
        CreatedAt  = ConvertTo-RaTime -Value (Get-RaProperty -Row $Row -Name 'created_at')
        UpdatedAt  = ConvertTo-RaTime -Value (Get-RaProperty -Row $Row -Name 'updated_at')
    }
}

function ConvertTo-RaTalkgroup {
    <#
    .SYNOPSIS
    An API talkgroup (or talkgroup table row) as a RadioArchive.Talkgroup.
    #>
    [CmdletBinding()]
    [OutputType('RadioArchive.Talkgroup')]
    param(
        # The API's row.
        [Parameter(Mandatory)]
        [object] $Row
    )
    $calls = Get-RaProperty -Row $Row -Name 'calls_30_days'
    [pscustomobject]@{
        PSTypeName        = 'RadioArchive.Talkgroup'
        SystemId          = [long]$Row.system_id
        Id                = [int]$Row.id
        Label             = [string]$Row.label
        Name              = [string]$Row.name
        Tag               = $Row.tag
        Group             = [string[]]@($Row.groups | Where-Object -FilterScript { $null -ne $_ })
        State             = ConvertTo-RaTitle -Value $Row.state
        Unreviewed        = [bool]$Row.unreviewed
        DelayMinutes      = [int]$Row.delay_minutes
        Alert             = $Row.alert
        LedColour         = $Row.led_colour
        SortOrder         = [int]$Row.sort_order
        FiregroundChannel = $Row.fireground_channel
        LastHeardAt       = ConvertTo-RaTime -Value (Get-RaProperty -Row $Row -Name 'last_heard_at')
        Calls30Days       = if ($null -ne $calls) { [long]$calls } else { $null }
        CreatedAt         = ConvertTo-RaTime -Value (Get-RaProperty -Row $Row -Name 'created_at')
        UpdatedAt         = ConvertTo-RaTime -Value (Get-RaProperty -Row $Row -Name 'updated_at')
    }
}

function ConvertTo-RaUnit {
    <#
    .SYNOPSIS
    An API unit as a RadioArchive.Unit.
    #>
    [CmdletBinding()]
    [OutputType('RadioArchive.Unit')]
    param(
        # The API's row.
        [Parameter(Mandatory)]
        [object] $Row
    )
    $unit = if ([long]$Row.from -eq [long]$Row.to) { "$($Row.from)" } else { "$($Row.from)-$($Row.to)" }
    [pscustomobject]@{
        PSTypeName = 'RadioArchive.Unit'
        SystemId   = [long]$Row.system_id
        Unit       = $unit
        From       = [long]$Row.from
        To         = [long]$Row.to
        Alias      = [string]$Row.alias
        CreatedAt  = ConvertTo-RaTime -Value (Get-RaProperty -Row $Row -Name 'created_at')
        UpdatedAt  = ConvertTo-RaTime -Value (Get-RaProperty -Row $Row -Name 'updated_at')
    }
}

function ConvertTo-RaLabel {
    <#
    .SYNOPSIS
    An API group or tag as a RadioArchive.Group or RadioArchive.Tag.
    #>
    [CmdletBinding()]
    [OutputType('RadioArchive.Group', 'RadioArchive.Tag')]
    param(
        # The API's row.
        [Parameter(Mandatory)]
        [object] $Row,

        # Group or Tag.
        [Parameter(Mandatory)]
        [ValidateSet('Group', 'Tag')]
        [string] $Kind
    )
    $o = [ordered]@{ PSTypeName = "RadioArchive.$Kind" }
    $o["${Kind}Id"] = [long]$Row.id
    $o.Name = [string]$Row.name
    $o.Alert = $Row.alert
    $o.LedColour = $Row.led_colour
    $o.SortOrder = [int]$Row.sort_order
    [pscustomobject]$o
}

function ConvertTo-RaAttachment {
    <#
    .SYNOPSIS
    An API attachment as a RadioArchive.Attachment.
    #>
    [CmdletBinding()]
    [OutputType('RadioArchive.Attachment')]
    param(
        # The API's row.
        [Parameter(Mandatory)]
        [object] $Row
    )
    [pscustomobject]@{
        PSTypeName  = 'RadioArchive.Attachment'
        Recorder    = [string]$Row.recorder
        ShortName   = [string]$Row.short_name
        SystemId    = [long]$Row.system_id
        SystemLabel = [string]$Row.system_label
        AttachedAt  = ConvertTo-RaTime -Value (Get-RaProperty -Row $Row -Name 'attached_at')
    }
}

function ConvertTo-RaRecorder {
    <#
    .SYNOPSIS
    An API recorder's state as a RadioArchive.Recorder. It never holds a key (R10).
    #>
    [CmdletBinding()]
    [OutputType('RadioArchive.Recorder')]
    param(
        # The API's row.
        [Parameter(Mandatory)]
        [object] $Row
    )
    $systems = foreach ($s in @($Row.systems)) {
        if ($null -eq $s) { continue }
        [pscustomobject]@{
            ShortName   = [string]$s.short_name
            SystemId    = $s.system_id
            SystemLabel = $s.system_label
            SystemType  = $s.system_type
            DecodeRate  = $s.decode_rate
            Deaf        = [bool]$s.deaf
        }
    }
    [pscustomobject]@{
        PSTypeName        = 'RadioArchive.Recorder'
        Name              = [string]$Row.name
        Connected         = [bool]$Row.connected
        ConnectedSince    = ConvertTo-RaTime -Value $Row.connected_since
        DisconnectedSince = ConvertTo-RaTime -Value $Row.disconnected_since
        LastConnectedAt   = ConvertTo-RaTime -Value $Row.last_connected_at
        LastCallAt        = ConvertTo-RaTime -Value $Row.last_call_at
        ClientVersion     = $Row.client_version
        ClientOutOfDate   = [bool]$Row.client_out_of_date
        QuietAfterMinutes = $Row.quiet_after_minutes
        Systems           = @($systems)
        DeafReason        = $Row.deaf_reason
        Warning           = [bool]$Row.warning
        Warnings          = [string[]]@($Row.warnings | Where-Object -FilterScript { $null -ne $_ })
    }
}

function ConvertTo-RaRetention {
    <#
    .SYNOPSIS
    The API's retention setting and volume state as a RadioArchive.Retention.
    #>
    [CmdletBinding()]
    [OutputType('RadioArchive.Retention')]
    param(
        # The API's answer.
        [Parameter(Mandatory)]
        [object] $Row
    )
    $disk = Get-RaProperty -Row $Row -Name 'disk'
    [pscustomobject]@{
        PSTypeName    = 'RadioArchive.Retention'
        Days          = [int]$Row.days
        DefaultDays   = [int]$Row.default_days
        MinDays       = [int]$Row.min_days
        MaxDays       = [int]$Row.max_days
        NextRunAt     = ConvertTo-RaTime -Value $Row.next_run
        FreePercent   = Get-RaProperty -Row $disk -Name 'free_percent'
        FreeBytes     = Get-RaProperty -Row $disk -Name 'free_bytes'
        TotalBytes    = Get-RaProperty -Row $disk -Name 'total_bytes'
        DiskState     = ConvertTo-RaTitle -Value (Get-RaProperty -Row $disk -Name 'state')
        DiskMessage   = Get-RaProperty -Row $disk -Name 'message'
        LastNightly   = Get-RaProperty -Row $Row -Name 'last_nightly'
        LastShortened = Get-RaProperty -Row $Row -Name 'last_shortened'
    }
}

function ConvertTo-RaCall {
    <#
    .SYNOPSIS
    An API call of a talkgroup as a RadioArchive.Call.
    #>
    [CmdletBinding()]
    [OutputType('RadioArchive.Call')]
    param(
        # The API's row.
        [Parameter(Mandatory)]
        [object] $Row,

        # The system it was asked of.
        [Parameter(Mandatory)]
        [long] $SystemId
    )
    $recording = Get-RaProperty -Row $Row -Name 'recording'
    [pscustomobject]@{
        PSTypeName        = 'RadioArchive.Call'
        CallId            = [string]$Row.id
        SystemId          = $SystemId
        Talkgroup         = [int]$Row.talkgroup
        PatchedTalkgroups = [int[]]@($Row.patched_talkgroups | Where-Object -FilterScript { $null -ne $_ })
        State             = ConvertTo-RaTitle -Value $Row.state
        StartedAt         = ConvertTo-RaTime -Value $Row.started_at
        EndedAt           = ConvertTo-RaTime -Value $Row.ended_at
        InProgress        = [bool]$Row.in_progress
        RecordingSize     = if ($recording) { [long]$recording.size } else { $null }
    }
}

function ConvertTo-RaAccount {
    <#
    .SYNOPSIS
    An API account as a RadioArchive.Account.
    #>
    [CmdletBinding()]
    [OutputType('RadioArchive.Account')]
    param(
        # The API's row.
        [Parameter(Mandatory)]
        [object] $Row
    )
    [pscustomobject]@{
        PSTypeName      = 'RadioArchive.Account'
        AccountId       = [string]$Row.id
        Name            = [string]$Row.name
        Email           = [string]$Row.email
        Role            = ConvertTo-RaTitle -Value $Row.role
        Status          = ConvertTo-RaTitle -Value $Row.status
        FirstSignedInAt = ConvertTo-RaTime -Value $Row.first_signed_in
        LastSignedInAt  = ConvertTo-RaTime -Value $Row.last_signed_in
    }
}

function New-RaConnectionObject {
    <#
    .SYNOPSIS
    What Connect-RadioArchive outputs: a RadioArchive.Connection, no token in it.
    #>
    [Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSUseShouldProcessForStateChangingFunctions', '', Justification = 'Makes an object in memory; changes nothing.')]
    [CmdletBinding()]
    [OutputType('RadioArchive.Connection')]
    param(
        # The connection.
        [Parameter(Mandatory)]
        [hashtable] $Connection
    )
    $saved = switch ($Connection.Store) {
        'dpapi' { 'DPAPI file' }
        'vault' { "Vault $($Connection.Vault)" }
        default { 'This session only' }
    }
    [pscustomobject]@{
        PSTypeName = 'RadioArchive.Connection'
        Uri        = [uri]$Connection.Uri
        AccountId  = [string]$Connection.Me.id
        Name       = [string]$Connection.Me.name
        Email      = [string]$Connection.Me.email
        Role       = ConvertTo-RaTitle -Value $Connection.Me.role
        SavedIn    = $saved
        ExpiresAt  = $Connection.ExpiresAt
    }
}

function Test-RaWildcard {
    <#
    .SYNOPSIS
    Whether a value matches any of the patterns, case-insensitively. No patterns: yes.
    #>
    [CmdletBinding()]
    [OutputType([bool])]
    param(
        # The value.
        [AllowNull()]
        [AllowEmptyString()]
        [string] $Value,

        # The patterns (PowerShell wildcards).
        [AllowNull()]
        [string[]] $Pattern
    )
    if (-not $Pattern) {
        return $true
    }
    foreach ($p in $Pattern) {
        if ([WildcardPattern]::new($p, [System.Management.Automation.WildcardOptions]::IgnoreCase).IsMatch([string]$Value)) {
            return $true
        }
    }
    $false
}

function Set-RaOptionalField {
    <#
    .SYNOPSIS
    Puts a bound parameter into a request body: '' clears a setting (null).
    #>
    [Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSUseShouldProcessForStateChangingFunctions', '', Justification = 'Fills a hashtable in memory; changes nothing.')]
    [CmdletBinding()]
    param(
        # The body.
        [Parameter(Mandatory)]
        [System.Collections.IDictionary] $Body,

        # The caller's bound parameters.
        [Parameter(Mandatory)]
        [System.Collections.IDictionary] $Bound,

        # Parameter name = API field, for each field.
        [Parameter(Mandatory)]
        [System.Collections.IDictionary] $Map,

        # API fields where '' or $null means null (clear the setting).
        [string[]] $Clearable = @()
    )
    foreach ($name in $Map.Keys) {
        if (-not $Bound.ContainsKey($name)) {
            continue
        }
        $field = $Map[$name]
        $value = $Bound[$name]
        if ($field -in $Clearable -and ($null -eq $value -or "$value" -eq '')) {
            $Body[$field] = $null
        } elseif ($value -is [switch]) {
            $Body[$field] = [bool]$value
        } elseif ($value -is [string] -and $field -in @('state', 'role')) {
            $Body[$field] = $value.ToLowerInvariant()
        } else {
            $Body[$field] = $value
        }
    }
}

# The session's connection and every call to RadioArchive's admin API.
#
# $script:Connection holds: Uri (the radio's origin), Issuer, ClientId,
# Scope, the Authentik endpoints, AccessToken and ExpiresAt, RefreshToken
# (kept in memory only for a session-only sign-in, and between saves),
# Store, Vault, Version (of the saved copy), AllowUnencrypted and Me (who
# the API says signed in).

function Get-RaOrigin {
    <#
    .SYNOPSIS
    The origin of a URL (scheme, host and port), checking its scheme.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        # The URL.
        [Parameter(Mandatory)]
        [uri] $Uri,

        # Allow http://, with a warning.
        [switch] $AllowUnencryptedAuthentication,

        # What the URL is, for messages.
        [string] $What = 'RadioArchive'
    )
    if (-not $Uri.IsAbsoluteUri -or $Uri.Scheme -notin @('http', 'https')) {
        throw (New-RaErrorRecord -ErrorId 'bad_uri' -Category InvalidArgument -Message "Give $What's URL in full, like https://radio.example.org.")
    }
    if ($Uri.Scheme -eq 'http' -and -not $AllowUnencryptedAuthentication) {
        throw (New-RaErrorRecord -ErrorId 'unencrypted_refused' -Category SecurityError `
                -Message "$What's URL ($($Uri.GetLeftPart([System.UriPartial]::Authority))) uses plain HTTP, which would send your sign-in unencrypted. Use its https:// URL, or for a test copy on your own network add -AllowUnencryptedAuthentication.")
    }
    $Uri.GetLeftPart([System.UriPartial]::Authority)
}

function Get-RaServerSetting {
    <#
    .SYNOPSIS
    Reads what the module needs from the radio's URL alone, and the issuer's endpoints.
    #>
    [Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSAvoidUsingAllowUnencryptedAuthentication', '', Justification = 'Only when the user passes -AllowUnencryptedAuthentication for a test copy on their own network, with a warning (guidelines section 9).')]
    [CmdletBinding()]
    [OutputType([hashtable])]
    param(
        # The radio's origin.
        [Parameter(Mandatory)]
        [string] $Origin,

        # Allow http://.
        [switch] $AllowUnencryptedAuthentication
    )
    try {
        Write-Verbose -Message 'GET /auth/module'
        $settings = Invoke-RestMethod -Uri "$Origin/auth/module" -Method Get -TimeoutSec 30 -Verbose:$false -Debug:$false -ErrorAction Stop
    } catch {
        throw (New-RaErrorRecord -ErrorId 'server_unreachable' -Category ConnectionError -Message "RadioArchive can't be reached at ${Origin}, or it doesn't offer sign-in for this module: $($_.Exception.GetBaseException().Message)")
    }
    $issuerOrigin = Get-RaOrigin -Uri $settings.issuer -AllowUnencryptedAuthentication:$AllowUnencryptedAuthentication -What 'Authentik'
    try {
        Write-Verbose -Message 'GET /.well-known/openid-configuration (Authentik)'
        $discovery = Invoke-RestMethod -Uri ($settings.issuer.TrimEnd('/') + '/.well-known/openid-configuration') -Method Get -TimeoutSec 30 -Verbose:$false -Debug:$false -ErrorAction Stop
    } catch {
        throw (New-RaErrorRecord -ErrorId 'authentik_unreachable' -Category ConnectionError -Message "Authentik can't be reached at ${issuerOrigin}: $($_.Exception.GetBaseException().Message)")
    }
    $endpoints = @{
        Authorize  = $discovery.authorization_endpoint
        Token      = $discovery.token_endpoint
        Revocation = $discovery.revocation_endpoint
        Device     = $discovery.device_authorization_endpoint
    }
    # The module only ever talks to RadioArchive and its Authentik.
    foreach ($name in @($endpoints.Keys)) {
        if (-not $endpoints[$name] -or ([uri]$endpoints[$name]).GetLeftPart([System.UriPartial]::Authority) -ne $issuerOrigin) {
            throw (New-RaErrorRecord -ErrorId 'authentik_misconfigured' -Category InvalidData -Message "Authentik's $name endpoint isn't at $issuerOrigin, so the module won't use it.")
        }
    }
    @{
        Issuer    = $settings.issuer
        ClientId  = $settings.client_id
        Scope     = $settings.scope
        Endpoints = $endpoints
    }
}

function Get-RaConnection {
    <#
    .SYNOPSIS
    The session's connection, or the saved sign-in when this session has none.
    #>
    [CmdletBinding()]
    [OutputType([hashtable])]
    param()
    if ($script:Connection) {
        return $script:Connection
    }
    $saved = @(Read-RaSignIn)
    if ($saved.Count -eq 0) {
        throw (New-RaErrorRecord -ErrorId 'not_connected' -Category AuthenticationError -Message 'Not signed in to RadioArchive. Run Connect-RadioArchive -Uri <the radio''s URL>.')
    }
    if ($saved.Count -gt 1) {
        $list = ($saved | ForEach-Object -Process { $_.uri }) -join ', '
        throw (New-RaErrorRecord -ErrorId 'several_servers' -Category InvalidOperation -Message "You're signed in to several RadioArchive servers ($list). Choose one with Connect-RadioArchive -Uri <URL>.")
    }
    Open-RaSavedSignIn -Record $saved[0]
}

function Open-RaSavedSignIn {
    <#
    .SYNOPSIS
    Makes a saved sign-in this session's connection, renewing its access token.
    #>
    [Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSAvoidUsingAllowUnencryptedAuthentication', '', Justification = 'Only for a sign-in saved with -AllowUnencryptedAuthentication, with a warning each time (guidelines section 9).')]
    [CmdletBinding()]
    [OutputType([hashtable])]
    param(
        # The saved sign-in's record (Read-RaSignIn).
        [Parameter(Mandatory)]
        [hashtable] $Record
    )
    $allow = [bool]$Record.allowUnencryptedAuthentication
    $origin = Get-RaOrigin -Uri $Record.uri -AllowUnencryptedAuthentication:$allow
    if ($allow -and $origin.StartsWith('http:')) {
        Write-Warning -Message "Using your saved sign-in to $origin over plain HTTP, unencrypted."
    }
    $settings = Get-RaServerSetting -Origin $origin -AllowUnencryptedAuthentication:$allow
    $connection = New-RaConnection -Origin $origin -Settings $settings -Store $Record.store -Vault $Record.vault -AllowUnencrypted:$allow
    Request-RaToken -Connection $connection
    $script:Connection = $connection
    $connection
}

function New-RaConnection {
    <#
    .SYNOPSIS
    A new connection, not yet signed in.
    #>
    [Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSUseShouldProcessForStateChangingFunctions', '', Justification = 'Makes an object in memory; changes nothing.')]
    [CmdletBinding()]
    [OutputType([hashtable])]
    param(
        # The radio's origin.
        [Parameter(Mandatory)]
        [string] $Origin,

        # The server's settings (Get-RaServerSetting).
        [Parameter(Mandatory)]
        [hashtable] $Settings,

        # dpapi, vault or session.
        [Parameter(Mandatory)]
        [string] $Store,

        # The vault, for vault.
        [AllowNull()]
        [string] $Vault,

        # Plain HTTP allowed.
        [switch] $AllowUnencrypted
    )
    @{
        Uri              = $Origin
        Issuer           = $Settings.Issuer
        ClientId         = $Settings.ClientId
        Scope            = $Settings.Scope
        Endpoints        = $Settings.Endpoints
        AccessToken      = $null
        ExpiresAt        = [datetimeoffset]::MinValue
        RefreshToken     = $null
        Store            = $Store
        Vault            = $Vault
        Version          = $null
        AllowUnencrypted = [bool]$AllowUnencrypted
        Me               = $null
    }
}

function Request-RaToken {
    <#
    .SYNOPSIS
    Renews a connection's access token with its refresh token.

    .DESCRIPTION
    Authentik issues a new refresh token each time and revokes the old one
    (ADR 0028), so the new one is saved at once. Several sessions share one
    saved sign-in: the saved token is read first, and if Authentik refuses
    it because another session renewed meanwhile, the newer one is read and
    tried once more. A sign-in Authentik refuses has ended: the saved copy
    is deleted.
    #>
    [CmdletBinding()]
    param(
        # The connection to renew.
        [Parameter(Mandatory)]
        [hashtable] $Connection
    )
    for ($attempt = 1; $attempt -le 3; $attempt++) {
        if ($Connection.Store -ne 'session') {
            $saved = Read-RaRefreshToken -Origin $Connection.Uri
            if (-not $saved) {
                throw (New-RaErrorRecord -ErrorId 'sign_in_ended' -Category AuthenticationError -Message 'Your RadioArchive sign-in has ended. Run Connect-RadioArchive.')
            }
            $Connection.RefreshToken = $saved.RefreshToken
            $Connection.Version = $saved.Version
        }
        $form = [ordered]@{
            grant_type    = 'refresh_token'
            refresh_token = $Connection.RefreshToken
            client_id     = $Connection.ClientId
        }
        $reply = Invoke-RaTokenRequest -Uri $Connection.Endpoints.Token -Form $form
        if ($reply.Status -eq 200 -and $reply['access_token']) {
            Import-RaTokenReply -Connection $Connection -Reply $reply
            Save-RaSignIn -Connection $Connection
            if ($Connection.Store -ne 'session') {
                $Connection.RefreshToken = $null
            }
            return
        }
        if ($reply['error'] -ne 'invalid_grant') {
            throw (New-RaErrorRecord -ErrorId 'renewal_failed' -Status $reply.Status -Message "Authentik didn't renew your sign-in ($($reply['error']))." -Category AuthenticationError)
        }
        # Another session may have renewed it just now: try the newer one.
        if ($Connection.Store -ne 'session') {
            Start-Sleep -Milliseconds 300
            $now = Read-RaSignIn -Origin $Connection.Uri
            if ($now -and $now.version -ne $Connection.Version) {
                continue
            }
            Remove-RaSignIn -Origin $Connection.Uri -WhatIf:$false -Confirm:$false
        }
        break
    }
    if ($script:Connection -and $script:Connection.Uri -eq $Connection.Uri) {
        $script:Connection = $null
    }
    throw (New-RaErrorRecord -ErrorId 'sign_in_ended' -Category AuthenticationError -Message 'Your RadioArchive sign-in has ended. Run Connect-RadioArchive.')
}

function Import-RaTokenReply {
    <#
    .SYNOPSIS
    Takes the tokens from a token reply into a connection.
    #>
    [CmdletBinding()]
    param(
        # The connection.
        [Parameter(Mandatory)]
        [hashtable] $Connection,

        # The token endpoint's reply.
        [Parameter(Mandatory)]
        [hashtable] $Reply
    )
    $Connection.AccessToken = $Reply['access_token']
    $Connection.ExpiresAt = [datetimeoffset]::UtcNow.AddSeconds([double]$Reply['expires_in'])
    if ($Reply['refresh_token']) {
        $Connection.RefreshToken = $Reply['refresh_token']
    }
    if ($Reply['id_token']) {
        $Reply['id_token'].Dispose()
    }
}

function ConvertTo-RaQueryString {
    <#
    .SYNOPSIS
    Builds a query string from names and values; arrays repeat the name.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        # Names and values.
        [System.Collections.IDictionary] $Query
    )
    if (-not $Query -or $Query.Count -eq 0) {
        return ''
    }
    $parts = foreach ($key in $Query.Keys) {
        foreach ($value in @($Query[$key])) {
            if ($null -ne $value -and "$value" -ne '') {
                [uri]::EscapeDataString($key) + '=' + [uri]::EscapeDataString([string]$value)
            }
        }
    }
    if (-not $parts) {
        return ''
    }
    '?' + ($parts -join '&')
}

function ConvertTo-RaPathSegment {
    <#
    .SYNOPSIS
    Escapes one path segment (a recorder's name, a shortName).
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        # The value.
        [Parameter(Mandatory)]
        [string] $Value
    )
    [uri]::EscapeDataString($Value)
}

function Invoke-RaApi {
    <#
    .SYNOPSIS
    Calls RadioArchive's admin API as the signed-in admin.

    .DESCRIPTION
    Renews the access token when it has under 60 seconds left. A refusal
    becomes an ErrorRecord (guidelines section 7), thrown for the caller to
    catch. HTTP errors don't throw inside Invoke-RestMethod
    (-SkipHttpErrorCheck), so no exception holding the request, and its
    Authorization header, ever reaches $Error. -Verbose shows the method and
    the path. With -OutFile, the answer's body is written to that file
    (a recording), and nothing is returned.
    #>
    [Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSAvoidUsingAllowUnencryptedAuthentication', '', Justification = 'Only when the user passes -AllowUnencryptedAuthentication for a test copy on their own network, with a warning (guidelines section 9).')]
    [CmdletBinding()]
    param(
        # GET, POST, PUT, PATCH or DELETE.
        [Parameter(Mandatory)]
        [string] $Method,

        # The path, e.g. /api/v1/admin/systems.
        [Parameter(Mandatory)]
        [string] $Path,

        # Query parameters; arrays repeat the name.
        [System.Collections.IDictionary] $Query,

        # A body, sent as JSON.
        [object] $Body,

        # Statuses that aren't errors, besides 2xx (e.g. 409 for retention's
        # confirmation).
        [int[]] $Accept = @(),

        # Write the answer's body to this file.
        [string] $OutFile
    )
    $connection = Get-RaConnection
    for ($try = 1; $try -le 2; $try++) {
        if ($connection.ExpiresAt -lt [datetimeoffset]::UtcNow.AddSeconds(60)) {
            Request-RaToken -Connection $connection
        }
        Write-Verbose -Message "$Method $Path"
        $status = 0
        $request = @{
            Uri                            = $connection.Uri + $Path + (ConvertTo-RaQueryString -Query $Query)
            Method                         = $Method
            Authentication                 = 'Bearer'
            Token                          = $connection.AccessToken
            AllowUnencryptedAuthentication = [bool]$connection.AllowUnencrypted
            SkipHttpErrorCheck             = $true
            StatusCodeVariable             = 'status'
            TimeoutSec                     = 120
            Verbose                        = $false
            Debug                          = $false
            ErrorAction                    = 'Stop'
        }
        if ($PSBoundParameters.ContainsKey('Body')) {
            $request.Body = [System.Text.Encoding]::UTF8.GetBytes(($Body | ConvertTo-Json -Depth 8 -Compress))
            $request.ContentType = 'application/json; charset=utf-8'
        }
        if ($OutFile) {
            $request.OutFile = $OutFile
        }
        try {
            $reply = Invoke-RestMethod @request
        } catch {
            throw (New-RaErrorRecord -ErrorId 'server_unreachable' -Category ConnectionError -Message "RadioArchive can't be reached at $($connection.Uri): $($_.Exception.GetBaseException().Message)")
        }
        Write-Debug -Message "$Method $Path answered $status"
        if ($status -lt 300 -or $status -in $Accept) {
            return $reply
        }
        $code = $null
        $message = $null
        if ($OutFile -and (Test-Path -LiteralPath $OutFile)) {
            try {
                $reply = [System.IO.File]::ReadAllText($OutFile) | ConvertFrom-Json
            } catch {
                $reply = $null
            }
            Remove-Item -LiteralPath $OutFile -Force
        }
        if ($reply -is [psobject] -and $reply.PSObject.Properties['error']) {
            $code = [string]$reply.error
            if ($reply.PSObject.Properties['message']) {
                $message = [string]$reply.message
            }
        }
        # An access token Authentik has since refused, or one that expired
        # early: renew once and try again.
        if ($status -eq 401 -and $code -eq 'invalid_token' -and $try -eq 1) {
            $connection.ExpiresAt = [datetimeoffset]::MinValue
            continue
        }
        if (-not $code) {
            $code = "http_$status"
        }
        if (-not $message) {
            $message = "RadioArchive answered $status."
        }
        throw (New-RaErrorRecord -ErrorId $code -Status $status -Message $message)
    }
}

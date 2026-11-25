# Groups and tags (R22, ADR 0006): the same fields, so their commands share
# these.

function Get-RaLabel {
    <#
    .SYNOPSIS
    Lists the radio's groups or tags, filtered.
    #>
    [CmdletBinding()]
    [OutputType('RadioArchive.Group', 'RadioArchive.Tag')]
    param(
        # Group or Tag.
        [Parameter(Mandatory)]
        [ValidateSet('Group', 'Tag')]
        [string] $Kind,

        # Their IDs.
        [long[]] $Id,

        # Their names (wildcards).
        [string[]] $Name
    )
    $path = if ($Kind -eq 'Group') { '/api/v1/admin/groups' } else { '/api/v1/admin/tags' }
    foreach ($row in @(Invoke-RaApi -Method GET -Path $path)) {
        if ($null -eq $row) { continue }
        if ($Id -and [long]$row.id -notin $Id) { continue }
        if (-not (Test-RaWildcard -Value $row.name -Pattern $Name)) { continue }
        ConvertTo-RaLabel -Row $row -Kind $Kind
    }
}

function Update-RaLabel {
    <#
    .SYNOPSIS
    Changes a group or tag, and returns it as changed.
    #>
    [Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSUseShouldProcessForStateChangingFunctions', '', Justification = 'The public command that calls it asks ShouldProcess.')]
    [CmdletBinding()]
    [OutputType('RadioArchive.Group', 'RadioArchive.Tag')]
    param(
        # Group or Tag.
        [Parameter(Mandatory)]
        [ValidateSet('Group', 'Tag')]
        [string] $Kind,

        # Its ID.
        [Parameter(Mandatory)]
        [long] $Id,

        # The changes, as the API names them.
        [Parameter(Mandatory)]
        [System.Collections.IDictionary] $Body
    )
    $path = if ($Kind -eq 'Group') { "/api/v1/admin/groups/$Id" } else { "/api/v1/admin/tags/$Id" }
    ConvertTo-RaLabel -Row (Invoke-RaApi -Method PATCH -Path $path -Body $Body) -Kind $Kind
}

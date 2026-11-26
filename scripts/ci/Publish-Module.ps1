<#
.SYNOPSIS
Stamps a copy of the RadioArchive module with a release's version, checks
it, and publishes it to the PowerShell Gallery (ADR 0043).

.DESCRIPTION
Copies powershell/RadioArchive into Destination, sets the copy's
ModuleVersion to Version, and runs Test-ModuleManifest, which must report
that version. The repository's own manifest is left as it is. With
-Publish, then runs Publish-PSResource -Repository PSGallery with the API
key in the PSGALLERY_API_KEY environment variable, unless that version is
on the Gallery already (a re-run after a failure). Without -Publish,
nothing is published.

Needs PowerShell 7.6 or later, as the manifest does: release.yml's gallery
job runs it with 7.6.0, installed by Install-PowerShell.ps1.

.EXAMPLE
pwsh -File scripts/ci/Publish-Module.ps1 -Version 0.2.0 -Destination "$env:RUNNER_TEMP/gallery"
#>
[CmdletBinding()]
param(
    # The release's version, such as 1.2.3 (its tag is v1.2.3).
    [Parameter(Mandatory)]
    [ValidatePattern('^\d+\.\d+\.\d+$')]
    [string] $Version,

    # An empty folder for the stamped copy.
    [Parameter(Mandatory)]
    [string] $Destination,

    # Publish the copy to the PowerShell Gallery.
    [switch] $Publish
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$source = Join-Path -Path $PSScriptRoot -ChildPath '../../powershell/RadioArchive'
New-Item -ItemType Directory -Path $Destination -Force | Out-Null
if (Test-Path -Path (Join-Path -Path $Destination -ChildPath 'RadioArchive')) {
    throw "$Destination already holds a RadioArchive folder"
}
Copy-Item -Path $source -Destination $Destination -Recurse
$module = Join-Path -Path $Destination -ChildPath 'RadioArchive'
$psd1 = Join-Path -Path $module -ChildPath 'RadioArchive.psd1'

$text = Get-Content -Path $psd1 -Raw
$pattern = "(?m)^(\s*ModuleVersion\s*=\s*)'[^']*'"
if (-not ($text -match $pattern)) {
    throw "The manifest has no ModuleVersion line to stamp"
}
Set-Content -Path $psd1 -Value ($text -replace $pattern, "`${1}'$Version'") -NoNewline

$manifest = Test-ModuleManifest -Path $psd1 -ErrorAction Stop
$manifest | Format-List -Property Name, Version, PowerShellVersion, CompatiblePSEditions | Out-Host
if ("$($manifest.Version)" -ne $Version) {
    throw "The stamped manifest says $($manifest.Version), the release $Version"
}

if (-not $Publish) {
    Write-Output -InputObject "RadioArchive $Version's manifest checks out; nothing is published"
    return
}
if (-not $env:PSGALLERY_API_KEY) {
    throw 'PSGALLERY_API_KEY is empty'
}
$published = Find-PSResource -Name RadioArchive -Version $Version -Repository PSGallery -ErrorAction SilentlyContinue
if ($published) {
    Write-Output -InputObject "RadioArchive $Version is on the Gallery already (an earlier attempt published it)"
} else {
    Publish-PSResource -Path $module -Repository PSGallery -ApiKey $env:PSGALLERY_API_KEY
    Write-Output -InputObject "Published RadioArchive $Version"
}

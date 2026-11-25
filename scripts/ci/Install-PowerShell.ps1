<#
.SYNOPSIS
Installs one PowerShell release, and the modules the module's tests use, for CI.

.DESCRIPTION
Downloads Microsoft's release archive for this OS (the Windows x64 zip or
the Linux x64 tar.gz) from the PowerShell repository's GitHub releases,
checks it against the release's published SHA-256 hashes, and unpacks it
into Destination. "newest" is the newest stable release, as Microsoft's
build information names it (https://aka.ms/pwsh-buildinfo-stable). With
-ModulePath, also saves Pester 5.9.1, PSScriptAnalyzer 1.25.0,
Microsoft.PowerShell.SecretManagement 1.1.2 and
Microsoft.PowerShell.SecretStore 1.0.6 from the PowerShell Gallery there.
Writes the installed pwsh's path.

.EXAMPLE
pwsh -File scripts/ci/Install-PowerShell.ps1 -Version 7.6.0 -Destination "$env:RUNNER_TEMP/pwsh" -ModulePath "$env:RUNNER_TEMP/psmodules"
#>
[CmdletBinding()]
param(
    # A version such as 7.6.0, or newest.
    [Parameter(Mandatory)]
    [string] $Version,

    # The folder to unpack into.
    [Parameter(Mandatory)]
    [string] $Destination,

    # A folder to save the test modules in. Default: none saved.
    [string] $ModulePath
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

function Invoke-Retried([scriptblock] $ScriptBlock, [string] $What) {
    for ($try = 1; ; $try++) {
        try {
            return & $ScriptBlock
        } catch {
            if ($try -ge 3) { throw }
            Write-Warning -Message "$What failed ($($_.Exception.Message)); trying again."
            Start-Sleep -Seconds (5 * $try)
        }
    }
}

if ($Version -eq 'newest') {
    $Version = (Invoke-Retried -What 'Reading the newest release' -ScriptBlock { Invoke-RestMethod -Uri 'https://aka.ms/pwsh-buildinfo-stable' }).ReleaseTag.TrimStart('v')
}
Write-Output -InputObject "PowerShell $Version" | Out-Host
$file = if ($IsWindows) { "PowerShell-$Version-win-x64.zip" } else { "powershell-$Version-linux-x64.tar.gz" }
$base = "https://github.com/PowerShell/PowerShell/releases/download/v$Version"
$archive = Join-Path -Path ([System.IO.Path]::GetTempPath()) -ChildPath $file
Invoke-Retried -What "Downloading $file" -ScriptBlock { Invoke-WebRequest -Uri "$base/$file" -OutFile $archive }
# hashes.sha256 is UTF-16 with a byte-order mark: "<hash> *<file>" per line.
$hashes = (Invoke-Retried -What 'Downloading hashes.sha256' -ScriptBlock { Invoke-WebRequest -Uri "$base/hashes.sha256" }).Content
if ($hashes -is [byte[]]) {
    $encoding = if ($hashes.Length -ge 2 -and $hashes[0] -eq 0xFF -and $hashes[1] -eq 0xFE) { [System.Text.Encoding]::Unicode } else { [System.Text.Encoding]::UTF8 }
    $hashes = $encoding.GetString($hashes).TrimStart([char]0xFEFF)
}
$expected = ($hashes -split "`n" | Where-Object -FilterScript { $_ -match [regex]::Escape($file) } | Select-Object -First 1) -split '\s+' | Select-Object -First 1
$actual = (Get-FileHash -Path $archive -Algorithm SHA256).Hash
if (-not $expected -or $actual -ne $expected.ToUpperInvariant()) {
    throw "SHA-256 of $file is $actual; the release says $expected."
}
$null = New-Item -ItemType Directory -Path $Destination -Force
if ($IsWindows) {
    Expand-Archive -Path $archive -DestinationPath $Destination -Force
    $pwsh = Join-Path -Path $Destination -ChildPath 'pwsh.exe'
} else {
    tar -xzf $archive -C $Destination
    $pwsh = Join-Path -Path $Destination -ChildPath 'pwsh'
    chmod +x $pwsh
}

if ($ModulePath) {
    $null = New-Item -ItemType Directory -Path $ModulePath -Force
    foreach ($m in @(@{ Name = 'Pester'; Version = '5.9.1' }, @{ Name = 'PSScriptAnalyzer'; Version = '1.25.0' },
            @{ Name = 'Microsoft.PowerShell.SecretManagement'; Version = '1.1.2' }, @{ Name = 'Microsoft.PowerShell.SecretStore'; Version = '1.0.6' })) {
        Invoke-Retried -What "Saving $($m.Name) $($m.Version)" -ScriptBlock {
            Save-PSResource -Name $m.Name -Version $m.Version -Path $ModulePath -Repository PSGallery -TrustRepository
        }
    }
}
Write-Output -InputObject $pwsh

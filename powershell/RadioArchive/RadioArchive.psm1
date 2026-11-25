Set-StrictMode -Version Latest

# The connection for this session (Connect-RadioArchive), or $null.
$script:Connection = $null

foreach ($folder in @('Private', 'Public')) {
    foreach ($file in Get-ChildItem -Path (Join-Path -Path $PSScriptRoot -ChildPath $folder) -Filter '*.ps1') {
        . $file.FullName
    }
}

# Each command also has a short alias with Ra in place of RadioArchive
# (docs/powershell-guidelines.md section 2).
foreach ($command in (Get-ChildItem -Path (Join-Path -Path $PSScriptRoot -ChildPath 'Public') -Filter '*.ps1').BaseName) {
    Set-Alias -Name ($command -replace 'RadioArchive', 'Ra') -Value $command
}

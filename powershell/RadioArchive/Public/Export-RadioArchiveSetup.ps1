function Export-RadioArchiveSetup {
    <#
    .SYNOPSIS
    Saves the radio's whole setup to a JSON file, hidden talkgroups included.

    .DESCRIPTION
    Saves the whole setup (R53, ADR 0030) as the radio exports it, byte for
    byte: every system with its talkgroups and units, and the groups and
    tags, with every field, state included. The same setup always saves the
    same bytes, so two exports compare with any diff tool. Recorders,
    attachments, accounts, calls and the audit log aren't in it.
    Import-RadioArchiveSetup imports it, here or into another radio.

    The file holds hidden and ignored talkgroups: keep it as you would the
    admin pages. An existing file isn't replaced unless -Force is given.

    .EXAMPLE
    Export-RadioArchiveSetup -Path setup.json

    .INPUTS
    None. It saves the radio's whole setup.

    .OUTPUTS
    System.IO.FileInfo. The file it saved.

    .LINK
    Import-RadioArchiveSetup

    .LINK
    Export-RadioArchiveTalkgroup
    #>
    [CmdletBinding()]
    [OutputType([System.IO.FileInfo])]
    param(
        # The file to save. No default. No wildcards.
        [Parameter(Mandatory)]
        [string] $Path,

        # Replace a file that's already there. Off by default.
        [switch] $Force
    )
    try {
        $file = $PSCmdlet.GetUnresolvedProviderPathFromPSPath($Path)
        if ((Test-Path -LiteralPath $file) -and -not $Force) {
            throw (New-RaErrorRecord -ErrorId 'file_exists' -Category ResourceExists -Message "$file is already there. Add -Force to replace it.")
        }
        $temp = "$file.$PID.part"
        try {
            $null = Invoke-RaApi -Method GET -Path '/api/v1/admin/setup' -OutFile $temp
            [System.IO.File]::Move($temp, $file, $true)
        } finally {
            if (Test-Path -LiteralPath $temp) {
                Remove-Item -LiteralPath $temp -Force -WhatIf:$false -Confirm:$false
            }
        }
        Write-Warning -Message "$file holds hidden and ignored talkgroups too: keep it as you would the admin pages."
        Get-Item -LiteralPath $file
    } catch {
        Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $Path
    }
}

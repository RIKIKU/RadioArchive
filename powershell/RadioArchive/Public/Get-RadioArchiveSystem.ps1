function Get-RadioArchiveSystem {
    <#
    .SYNOPSIS
    Gets the radio's systems.

    .DESCRIPTION
    Lists the systems the radio knows (SPEC.md R11): each one's ID, label,
    name, whether new talkgroups are made automatically, and its own alert,
    LED colour and sort order. With no parameters, every system.

    .EXAMPLE
    Get-RadioArchiveSystem

    SystemId Label Name            AutoCreate
    -------- ----- ----            ----------
           1 EX    Example network       True

    .EXAMPLE
    Get-RadioArchiveSystem -Label EX | Get-RadioArchiveTalkgroup

    Every talkgroup of system EX.

    .INPUTS
    System.Int64. Objects with a SystemId property, such as a RadioArchive.Talkgroup.

    .OUTPUTS
    RadioArchive.System. SystemId, Label, Name, AutoCreate, Alert,
    LedColour, SortOrder, CreatedAt and UpdatedAt. Alert and LedColour are
    the system's own; null leaves talkgroups to their tags and groups.

    .LINK
    New-RadioArchiveSystem

    .LINK
    Set-RadioArchiveSystem

    .LINK
    Remove-RadioArchiveSystem
    #>
    [CmdletBinding()]
    [OutputType('RadioArchive.System')]
    param(
        # The systems' IDs. Default: every system.
        [Parameter(ValueFromPipelineByPropertyName)]
        [long[]] $SystemId,

        # The systems' labels, such as EX. Wildcards are allowed. Default:
        # every system.
        [SupportsWildcards()]
        [string[]] $Label
    )
    begin {
        $all = $null
        # Each system once, however many piped objects name it.
        $seen = @{}
    }
    process {
        try {
            if ($null -eq $all) {
                $all = @(Invoke-RaApi -Method GET -Path '/api/v1/admin/systems' | ForEach-Object -Process { $_ })
            }
            foreach ($row in $all) {
                if ($SystemId -and [long]$row.id -notin $SystemId) { continue }
                if (-not (Test-RaWildcard -Value $row.label -Pattern $Label)) { continue }
                if ($seen.ContainsKey([long]$row.id)) { continue }
                $seen[[long]$row.id] = $true
                ConvertTo-RaSystem -Row $row
            }
        } catch {
            $PSCmdlet.ThrowTerminatingError((Copy-RaErrorRecord -ErrorRecord $_))
        }
    }
}

function Get-RadioArchiveTalkgroup {
    <#
    .SYNOPSIS
    Gets talkgroups, hidden and ignored ones too.

    .DESCRIPTION
    Lists every talkgroup of every system, as the admin's talkgroup table
    shows them (R22, R50): label, name, tag, groups, state, delay, alert,
    LED colour, sort order, fireground channel number, when it was last
    heard and how many calls it had in the last 30 days. Hidden and ignored
    talkgroups are listed too: this is the admin API (R34). The filters
    combine: a talkgroup must match each one given, and any of its values.

    .EXAMPLE
    Get-RadioArchiveTalkgroup -Group North

    SystemId   Id Label  Name           Tag      State  LastHeardAt
    --------   -- -----  ----           ---      -----  -----------
           1 1001 DISP N Dispatch North dispatch Public 25/11/2026 10:02:13 AM
           1 1003 ADMIN  Admin          admin    Hidden 25/11/2026 9:31:13 AM

    .EXAMPLE
    Get-RadioArchiveTalkgroup -Group North | Set-RadioArchiveTalkgroup -State Hidden

    Hides every talkgroup in group North.

    .EXAMPLE
    Get-RadioArchiveTalkgroup -Unreviewed

    Talkgroups made automatically that no admin has looked at yet.

    .INPUTS
    System.Int64. Objects with SystemId and Id properties, such as a RadioArchive.Talkgroup or a RadioArchive.System.

    .OUTPUTS
    RadioArchive.Talkgroup. SystemId, Id, Label, Name, Tag, Group (the
    group names), State (Public, Hidden or Ignored), Unreviewed,
    DelayMinutes, Alert, LedColour, SortOrder, FiregroundChannel,
    LastHeardAt, Calls30Days, CreatedAt and UpdatedAt. Alert and LedColour
    are the talkgroup's own: null takes them from its tag, groups or system.

    .LINK
    Set-RadioArchiveTalkgroup

    .LINK
    New-RadioArchiveTalkgroup

    .LINK
    Get-RadioArchiveCall
    #>
    [CmdletBinding()]
    [OutputType('RadioArchive.Talkgroup')]
    param(
        # The systems' IDs. Default: every system.
        [Parameter(ValueFromPipelineByPropertyName)]
        [long[]] $SystemId,

        # The talkgroups' IDs, such as 1001. Default: every talkgroup.
        [Parameter(ValueFromPipelineByPropertyName)]
        [Alias('Talkgroup')]
        [int[]] $Id,

        # Their names or labels. Wildcards are allowed. Default: any.
        [SupportsWildcards()]
        [string[]] $Name,

        # Groups they're in, such as North. Wildcards are allowed. Default:
        # any.
        [SupportsWildcards()]
        [string[]] $Group,

        # Their tags. Wildcards are allowed. Default: any.
        [SupportsWildcards()]
        [string[]] $Tag,

        # Their states. Default: any.
        [ValidateSet('Public', 'Hidden', 'Ignored')]
        [string[]] $State,

        # Only talkgroups no admin has reviewed yet. Off by default.
        [switch] $Unreviewed
    )
    begin {
        $all = $null
        $seen = @{}
    }
    process {
        try {
            if ($null -eq $all) {
                $all = @((Invoke-RaApi -Method GET -Path '/api/v1/admin/talkgroups').talkgroups)
            }
            foreach ($row in $all) {
                if ($SystemId -and [long]$row.system_id -notin $SystemId) { continue }
                if ($Id -and [int]$row.id -notin $Id) { continue }
                if ($Name -and -not ((Test-RaWildcard -Value $row.name -Pattern $Name) -or (Test-RaWildcard -Value $row.label -Pattern $Name))) { continue }
                if ($Tag -and -not (Test-RaWildcard -Value $row.tag -Pattern $Tag)) { continue }
                if ($Group -and -not @($row.groups | Where-Object -FilterScript { Test-RaWildcard -Value $_ -Pattern $Group })) { continue }
                if ($State -and $row.state -notin $State) { continue }
                if ($Unreviewed -and -not $row.unreviewed) { continue }
                $key = "$($row.system_id):$($row.id)"
                if ($seen.ContainsKey($key)) { continue }
                $seen[$key] = $true
                ConvertTo-RaTalkgroup -Row $row
            }
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_
        }
    }
}

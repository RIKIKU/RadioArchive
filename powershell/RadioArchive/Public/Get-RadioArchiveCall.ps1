function Get-RadioArchiveCall {
    <#
    .SYNOPSIS
    Gets a talkgroup's stored calls, hidden talkgroups' too.

    .DESCRIPTION
    Lists a talkgroup's calls, newest first: those on it and those patched
    with it, in any state (R34). Pages are fetched only as the pipeline
    asks for more, so -First 10 or Select-Object -First 10 stops further
    requests. Save a call's recording with Save-RadioArchiveRecording.

    .EXAMPLE
    Get-RadioArchiveCall -SystemId 1 -Talkgroup 1003 -First 3

    .EXAMPLE
    Get-RadioArchiveTalkgroup -Id 1003 | Get-RadioArchiveCall -First 1 | Save-RadioArchiveRecording -Path .

    .INPUTS
    Objects with SystemId and Id (or Talkgroup) properties, such as a RadioArchive.Talkgroup.

    .OUTPUTS
    RadioArchive.Call. CallId, SystemId, Talkgroup, PatchedTalkgroups,
    State (the strictest in its patch), StartedAt, EndedAt, InProgress and
    RecordingSize (bytes, or null while it has no recording).

    .LINK
    Save-RadioArchiveRecording

    .LINK
    Get-RadioArchiveTalkgroup
    #>
    [CmdletBinding()]
    [OutputType('RadioArchive.Call')]
    param(
        # The talkgroup's system. No default.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [long] $SystemId,

        # The talkgroup, such as 1003. No default.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [Alias('Id')]
        [int] $Talkgroup,

        # How many calls at most.
        [PSDefaultValue(Help = 'every call')]
        [uint64] $First = [uint64]::MaxValue,

        # How many of the newest to skip.
        [PSDefaultValue(Help = 'none')]
        [uint64] $Skip = 0
    )
    process {
        try {
            $cursor = $null
            $skipped = [uint64]0
            $written = [uint64]0
            do {
                $want = [math]::Min(500, [double]$First - [double]$written + [double]$Skip - [double]$skipped)
                if ($want -lt 1) { break }
                $query = [ordered]@{ limit = [int][math]::Max(1, $want); cursor = $cursor }
                $page = Invoke-RaApi -Method GET -Path "/api/v1/admin/systems/$SystemId/talkgroups/$Talkgroup/calls" -Query $query
                foreach ($row in @($page.calls)) {
                    if ($null -eq $row) { continue }
                    if ($skipped -lt $Skip) {
                        $skipped++
                        continue
                    }
                    if ($written -ge $First) { break }
                    ConvertTo-RaCall -Row $row -SystemId $SystemId
                    $written++
                }
                $cursor = $page.next
            } while ($cursor -and $written -lt $First)
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject "$SystemId/$Talkgroup"
        }
    }
}

function Get-RadioArchiveRejection {
    <#
    .SYNOPSIS
    Gets the calls the radio rejected, counted by why.

    .DESCRIPTION
    Counts the calls refused (R12, R17, R18): a recorder's shortName not
    attached to any system, a talkgroup not in the list, or an ignored
    talkgroup. Each has the message the Recorders and Talkgroups pages
    show. Rejected calls aren't stored.

    .EXAMPLE
    Get-RadioArchiveRejection

    Kind       Recorder ShortName Talkgroup Calls Message
    ----       -------- --------- --------- ----- -------
    Unattached Bench    north                   4 Bench / north: 4 calls rejected, not attached to a system
    Talkgroup                          1004     1 TG 1004: 1 call rejected, ignored

    .EXAMPLE
    Get-RadioArchiveRejection -Unattached | Add-RadioArchiveAttachment -SystemId 1

    .INPUTS
    System.Int64. Objects with a SystemId property, such as a RadioArchive.System.

    .OUTPUTS
    RadioArchive.Rejection. Kind (Unattached or Talkgroup), Recorder and
    ShortName (unattached), SystemId and Talkgroup (talkgroups), Reason
    (unknown or ignored, for talkgroups), Calls, LastAt and Message.

    .LINK
    Add-RadioArchiveAttachment

    .LINK
    Get-RadioArchiveTalkgroup
    #>
    [CmdletBinding()]
    [OutputType('RadioArchive.Rejection')]
    param(
        # A system, for its talkgroups' rejections only. Default: every
        # system, and the unattached pairs.
        [Parameter(ValueFromPipelineByPropertyName)]
        [Nullable[long]] $SystemId,

        # Only the unattached pairs. Off by default.
        [switch] $Unattached
    )
    process {
        try {
            $query = [ordered]@{ system = $SystemId }
            $answer = Invoke-RaApi -Method GET -Path '/api/v1/admin/rejections' -Query $query
            foreach ($u in @($answer.unattached)) {
                if ($null -eq $u) { continue }
                [pscustomobject]@{
                    PSTypeName = 'RadioArchive.Rejection'
                    Kind       = 'Unattached'
                    Recorder   = [string]$u.recorder
                    ShortName  = [string]$u.short_name
                    SystemId   = $null
                    Talkgroup  = $null
                    Reason     = 'Unattached'
                    Calls      = [long]$u.calls
                    LastAt     = ConvertTo-RaTime -Value $u.last_at
                    Message    = [string]$u.message
                }
            }
            if ($Unattached) {
                return
            }
            foreach ($t in @($answer.talkgroups)) {
                if ($null -eq $t) { continue }
                [pscustomobject]@{
                    PSTypeName = 'RadioArchive.Rejection'
                    Kind       = 'Talkgroup'
                    Recorder   = $null
                    ShortName  = $null
                    SystemId   = [long]$t.system_id
                    Talkgroup  = [int]$t.talkgroup
                    Reason     = ConvertTo-RaTitle -Value $t.reason
                    Calls      = [long]$t.calls
                    LastAt     = ConvertTo-RaTime -Value $t.last_at
                    Message    = [string]$t.message
                }
            }
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_
        }
    }
}

function Get-RadioArchiveAttachment {
    <#
    .SYNOPSIS
    Gets which recorders' systems are attached to which of the radio's systems.

    .DESCRIPTION
    Lists the attachments (R12): each recorder and the shortName its
    trunk-recorder gives a system, and the radio's system that its calls are
    stored under. A recorder's calls for a shortName that isn't attached are
    rejected (Get-RadioArchiveRejection).

    .EXAMPLE
    Get-RadioArchiveAttachment

    Recorder ShortName SystemId SystemLabel
    -------- --------- -------- -----------
    Bench    ex               1 EX

    .INPUTS
    System.String. Objects with Recorder and ShortName properties, such as a RadioArchive.Recorder's systems.

    .OUTPUTS
    RadioArchive.Attachment. Recorder, ShortName, SystemId, SystemLabel
    and AttachedAt.

    .LINK
    Add-RadioArchiveAttachment

    .LINK
    Remove-RadioArchiveAttachment
    #>
    [CmdletBinding()]
    [OutputType('RadioArchive.Attachment')]
    param(
        # The recorders. Wildcards are allowed. Default: every recorder.
        [Parameter(ValueFromPipelineByPropertyName)]
        [SupportsWildcards()]
        [string[]] $Recorder,

        # The radio's systems. Default: every system.
        [Parameter(ValueFromPipelineByPropertyName)]
        [long[]] $SystemId
    )
    begin {
        $all = $null
        $seen = @{}
    }
    process {
        try {
            if ($null -eq $all) {
                $all = @(Invoke-RaApi -Method GET -Path '/api/v1/admin/attachments')
            }
            foreach ($row in $all) {
                if ($null -eq $row) { continue }
                if (-not (Test-RaWildcard -Value $row.recorder -Pattern $Recorder)) { continue }
                if ($SystemId -and [long]$row.system_id -notin $SystemId) { continue }
                $key = "$($row.recorder)/$($row.short_name)"
                if ($seen.ContainsKey($key)) { continue }
                $seen[$key] = $true
                ConvertTo-RaAttachment -Row $row
            }
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_
        }
    }
}

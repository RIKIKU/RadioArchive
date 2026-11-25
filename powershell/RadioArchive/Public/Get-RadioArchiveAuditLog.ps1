function Get-RadioArchiveAuditLog {
    <#
    .SYNOPSIS
    Gets the audit log: every admin change, newest first.

    .DESCRIPTION
    Lists the audit log's entries (R54, ADR 0029): when, who (the admin's
    account, name and email as they were), how (Browser, Module or
    Import), what kind of thing, which one, what happened to it, and each
    changed field before and after. Pages are fetched only as the pipeline
    asks for more, so -First 20 stops further requests. Reading the log
    changes nothing.

    .EXAMPLE
    Get-RadioArchiveAuditLog -Kind Talkgroup -First 5

    At                     Via    Who        Kind      Label          Action
    --                     ---    ---        ----      -----          ------
    25/11/2026 10:02:13 AM Module Alex Admin Talkgroup EX 1001 DISP N Changed

    .EXAMPLE
    Get-RadioArchiveTalkgroup -Id 1001 | Get-RadioArchiveAuditLog

    Every change to talkgroup 1001.

    .INPUTS
    Objects with SystemId and Id properties (a RadioArchive.Talkgroup), or an AccountId (a RadioArchive.Account, for that admin's changes).

    .OUTPUTS
    RadioArchive.AuditEntry. EntryId, At, AccountId, Who (the admin's name
    as it was), Email, Via (Browser, Module or Import), Kind, Thing (such as
    1/1001 for talkgroup 1001 of system 1), Label, Action and Changes (each
    Field with its Before and After, as JSON).

    .LINK
    Get-RadioArchiveTalkgroup

    .LINK
    Get-RadioArchiveAccount
    #>
    [CmdletBinding()]
    [OutputType('RadioArchive.AuditEntry')]
    param(
        # Only this admin's changes, by account ID. Default: anyone's.
        [Parameter(ValueFromPipelineByPropertyName)]
        [string] $AccountId,

        # Only changes of this kind. Default: any.
        [ValidateSet('System', 'Talkgroup', 'Unit', 'Group', 'Tag', 'Attachment', 'Recorder', 'Retention', 'Account')]
        [string] $Kind,

        # Only changes of this thing, such as 1/1001 or 1. Default: any.
        [string] $Thing,

        # A talkgroup's system, with -Id: that talkgroup's changes. Default:
        # none.
        [Parameter(ValueFromPipelineByPropertyName)]
        [Nullable[long]] $SystemId,

        # A talkgroup, with -SystemId. Default: none.
        [Parameter(ValueFromPipelineByPropertyName)]
        [Nullable[int]] $Id,

        # Only changes from this time on. Default: since the start.
        [Nullable[datetimeoffset]] $After,

        # Only changes before this time. Default: up to now.
        [Nullable[datetimeoffset]] $Before,

        # How many entries at most.
        [PSDefaultValue(Help = 'every entry')]
        [uint64] $First = [uint64]::MaxValue
    )
    process {
        try {
            $kindName = if ($Kind) { $Kind.ToLowerInvariant() } else { $null }
            $thingName = $Thing
            if ($null -ne $SystemId -and $null -ne $Id -and -not $Thing) {
                $kindName = 'talkgroup'
                $thingName = "$SystemId/$Id"
            }
            $cursor = $null
            $written = [uint64]0
            do {
                $query = [ordered]@{
                    who    = $AccountId
                    kind   = $kindName
                    thing  = $thingName
                    since  = if ($null -ne $After) { $After.Value.UtcDateTime.ToString('o') } else { $null }
                    until  = if ($null -ne $Before) { $Before.Value.UtcDateTime.ToString('o') } else { $null }
                    limit  = [int][math]::Min(200, [double]$First - [double]$written)
                    cursor = $cursor
                }
                $page = Invoke-RaApi -Method GET -Path '/api/v1/admin/audit' -Query $query
                foreach ($e in @($page.entries)) {
                    if ($null -eq $e) { continue }
                    if ($written -ge $First) { break }
                    $changes = foreach ($c in @($e.changes)) {
                        if ($null -eq $c) { continue }
                        [pscustomobject]@{
                            Field  = [string]$c.field
                            Before = if ($null -eq $c.before) { $null } else { ConvertTo-Json -InputObject $c.before -Compress -Depth 8 }
                            After  = if ($null -eq $c.after) { $null } else { ConvertTo-Json -InputObject $c.after -Compress -Depth 8 }
                        }
                    }
                    [pscustomobject]@{
                        PSTypeName = 'RadioArchive.AuditEntry'
                        EntryId    = [long]$e.id
                        At         = ConvertTo-RaTime -Value $e.at
                        AccountId  = [string]$e.who.id
                        Who        = [string]$e.who.name
                        Email      = [string]$e.who.email
                        Via        = ConvertTo-RaTitle -Value $e.via
                        Kind       = ConvertTo-RaTitle -Value $e.kind
                        Thing      = [string]$e.thing
                        Label      = [string]$e.label
                        Action     = ConvertTo-RaTitle -Value $e.action
                        Changes    = @($changes)
                    }
                    $written++
                }
                $cursor = $page.next
            } while ($cursor -and $written -lt $First)
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_
        }
    }
}

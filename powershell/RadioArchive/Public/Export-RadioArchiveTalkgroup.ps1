function Export-RadioArchiveTalkgroup {
    <#
    .SYNOPSIS
    Saves talkgroups to a CSV file that Import-RadioArchiveTalkgroup reads back.

    .DESCRIPTION
    Writes one row per talkgroup with every field the import takes:
    SystemId, Id, Label, Name, Tag, Group (names separated by semicolons),
    State, Unreviewed, Type, Frequency, DelayMinutes, Alert, LedColour,
    SortOrder and FiregroundChannel, from the radio's setup (ADR 0030).
    Import-Csv | Import-RadioArchiveTalkgroup reads the file back, and the
    file as saved imports as all Unchanged, so editing it and importing it
    changes what was edited. Every talkgroup is saved, or the ones piped
    in, or a system's.

    The file holds hidden and ignored talkgroups when there are any: keep
    it as you would the admin pages. An existing file isn't replaced unless
    -Force is given.

    .EXAMPLE
    Export-RadioArchiveTalkgroup -Path talkgroups.csv

    .EXAMPLE
    Get-RadioArchiveTalkgroup -Group North | Export-RadioArchiveTalkgroup -Path north.csv

    .INPUTS
    Objects with SystemId and Id properties, such as a
    RadioArchive.Talkgroup, or with SystemId alone, such as a
    RadioArchive.System.

    .OUTPUTS
    System.IO.FileInfo. The file it saved.

    .LINK
    Import-RadioArchiveTalkgroup

    .LINK
    Export-RadioArchiveSetup
    #>
    [CmdletBinding()]
    [OutputType([System.IO.FileInfo])]
    param(
        # Only this system's talkgroups. Default: every system's.
        [Parameter(ValueFromPipelineByPropertyName)]
        [long] $SystemId,

        # Only this talkgroup of the system. Default: all of them.
        [Parameter(ValueFromPipelineByPropertyName)]
        [Alias('Talkgroup')]
        [long] $Id,

        # The CSV file to save. No default. No wildcards.
        [Parameter(Mandatory)]
        [string] $Path,

        # Replace a file that's already there. Off by default.
        [switch] $Force
    )
    begin {
        $wanted = [System.Collections.Generic.List[object]]::new()
    }
    process {
        if ($SystemId -gt 0) {
            $wanted.Add(@($SystemId, $Id))
        }
    }
    end {
        try {
            $setup = Get-RaSetup
            $hidden = $false
            $rows = foreach ($s in @($setup.systems)) {
                foreach ($t in @($s.talkgroups)) {
                    if ($null -eq $t) { continue }
                    if (($MyInvocation.ExpectingInput -or $wanted.Count -gt 0) -and
                        -not @($wanted | Where-Object -FilterScript { $_[0] -eq $s.id -and ($_[1] -eq 0 -or $_[1] -eq $t.id) }).Count) {
                        continue
                    }
                    if ($t.state -ne 'public') {
                        $hidden = $true
                    }
                    [pscustomobject]@{
                        SystemId          = Format-RaCell -Value $s.id
                        Id                = Format-RaCell -Value $t.id
                        Label             = [string]$t.label
                        Name              = [string]$t.name
                        Tag               = Format-RaCell -Value $t.tag
                        Group             = @($t.groups | Where-Object -FilterScript { $null -ne $_ }) -join ';'
                        State             = ConvertTo-RaTitle -Value $t.state
                        Unreviewed        = Format-RaCell -Value ([bool]$t.unreviewed)
                        Type              = [string]$t.type
                        Frequency         = Format-RaCell -Value $t.frequency
                        DelayMinutes      = Format-RaCell -Value $t.delay_minutes
                        Alert             = Format-RaCell -Value $t.alert
                        LedColour         = Format-RaCell -Value $t.led_colour
                        SortOrder         = Format-RaCell -Value $t.sort_order
                        FiregroundChannel = Format-RaCell -Value $t.fireground_channel
                    }
                }
            }
            $columns = 'SystemId', 'Id', 'Label', 'Name', 'Tag', 'Group', 'State', 'Unreviewed', 'Type', 'Frequency', 'DelayMinutes',
            'Alert', 'LedColour', 'SortOrder', 'FiregroundChannel'
            $out = Export-RaCsv -Cmdlet $PSCmdlet -Path $Path -Column $columns -Rows @($rows) -Force $Force
            if ($hidden) {
                Write-Warning -Message "$($out.FullName) holds hidden or ignored talkgroups: keep it as you would the admin pages."
            }
            $out
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $Path
        }
    }
}

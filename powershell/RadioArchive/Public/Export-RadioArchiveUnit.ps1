function Export-RadioArchiveUnit {
    <#
    .SYNOPSIS
    Saves units to a CSV file that Import-RadioArchiveUnit reads back.

    .DESCRIPTION
    Writes one row per unit, a radio ID or a range, with SystemId, Unit
    (5001, or 5100-5199) and Alias, from the radio's setup (ADR 0030).
    Import-Csv | Import-RadioArchiveUnit reads the file back, and the file
    as saved imports as all Unchanged. Every unit is saved, or the ones
    piped in, or a system's. An existing file isn't replaced unless -Force
    is given.

    .EXAMPLE
    Export-RadioArchiveUnit -Path units.csv

    .EXAMPLE
    Get-RadioArchiveSystem -Label EX | Export-RadioArchiveUnit -Path ex-units.csv

    .INPUTS
    Objects with SystemId and Unit properties, such as a RadioArchive.Unit,
    or with SystemId alone, such as a RadioArchive.System.

    .OUTPUTS
    System.IO.FileInfo. The file it saved.

    .LINK
    Import-RadioArchiveUnit

    .LINK
    Export-RadioArchiveSetup
    #>
    [CmdletBinding()]
    [OutputType([System.IO.FileInfo])]
    param(
        # Only this system's units. Default: every system's.
        [Parameter(ValueFromPipelineByPropertyName)]
        [long] $SystemId,

        # Only this unit of the system, such as 5001 or 5100-5199. Default:
        # all of them.
        [Parameter(ValueFromPipelineByPropertyName)]
        [string] $Unit,

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
            $wanted.Add(@($SystemId, $Unit))
        }
    }
    end {
        try {
            $setup = Get-RaSetup
            $rows = foreach ($s in @($setup.systems)) {
                foreach ($u in @($s.units)) {
                    if ($null -eq $u) { continue }
                    $name = if ([long]$u.from -eq [long]$u.to) { "$($u.from)" } else { "$($u.from)-$($u.to)" }
                    if (($MyInvocation.ExpectingInput -or $wanted.Count -gt 0) -and
                        -not @($wanted | Where-Object -FilterScript { $_[0] -eq $s.id -and (-not $_[1] -or $_[1] -eq $name) }).Count) {
                        continue
                    }
                    [pscustomobject]@{ SystemId = Format-RaCell -Value $s.id; Unit = $name; Alias = [string]$u.alias }
                }
            }
            Export-RaCsv -Cmdlet $PSCmdlet -Path $Path -Column 'SystemId', 'Unit', 'Alias' -Rows @($rows) -Force $Force
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $Path
        }
    }
}

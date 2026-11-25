function Export-RadioArchiveSystem {
    <#
    .SYNOPSIS
    Saves systems to a CSV file that Import-RadioArchiveSystem reads back.

    .DESCRIPTION
    Writes one row per system with SystemId, Label, Name, AutoCreate,
    Alert, LedColour and SortOrder, from the radio's setup (ADR 0030).
    Import-Csv | Import-RadioArchiveSystem reads the file back, and the
    file as saved imports as all Unchanged. Every system is saved, or the
    ones piped in. An existing file isn't replaced unless -Force is given.

    .EXAMPLE
    Export-RadioArchiveSystem -Path systems.csv

    .INPUTS
    Objects with a SystemId property, such as a RadioArchive.System.

    .OUTPUTS
    System.IO.FileInfo. The file it saved.

    .LINK
    Import-RadioArchiveSystem

    .LINK
    Export-RadioArchiveSetup
    #>
    [CmdletBinding()]
    [OutputType([System.IO.FileInfo])]
    param(
        # Only this system. Default: every system.
        [Parameter(ValueFromPipelineByPropertyName)]
        [long] $SystemId,

        # The CSV file to save. No default. No wildcards.
        [Parameter(Mandatory)]
        [string] $Path,

        # Replace a file that's already there. Off by default.
        [switch] $Force
    )
    begin {
        $wanted = [System.Collections.Generic.List[long]]::new()
    }
    process {
        if ($SystemId -gt 0) {
            $wanted.Add($SystemId)
        }
    }
    end {
        try {
            $setup = Get-RaSetup
            $rows = foreach ($s in @($setup.systems)) {
                if ($null -eq $s -or (($MyInvocation.ExpectingInput -or $wanted.Count -gt 0) -and [long]$s.id -notin $wanted)) {
                    continue
                }
                [pscustomobject]@{
                    SystemId   = Format-RaCell -Value $s.id
                    Label      = [string]$s.label
                    Name       = [string]$s.name
                    AutoCreate = Format-RaCell -Value ([bool]$s.auto_create)
                    Alert      = Format-RaCell -Value $s.alert
                    LedColour  = Format-RaCell -Value $s.led_colour
                    SortOrder  = Format-RaCell -Value $s.sort_order
                }
            }
            Export-RaCsv -Cmdlet $PSCmdlet -Path $Path -Column 'SystemId', 'Label', 'Name', 'AutoCreate', 'Alert', 'LedColour', 'SortOrder' -Rows @($rows) -Force $Force
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $Path
        }
    }
}

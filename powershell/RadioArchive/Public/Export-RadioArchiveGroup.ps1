function Export-RadioArchiveGroup {
    <#
    .SYNOPSIS
    Saves groups to a CSV file that Import-RadioArchiveGroup reads back.

    .DESCRIPTION
    Writes one row per group with Name, Alert, LedColour and SortOrder,
    from the radio's setup (ADR 0030). Import-Csv | Import-RadioArchiveGroup
    reads the file back, and the file as saved imports as all Unchanged.
    Every group is saved, or the ones piped in or named. An existing file
    isn't replaced unless -Force is given.

    .EXAMPLE
    Export-RadioArchiveGroup -Path groups.csv

    .INPUTS
    Objects with a Name property, such as a RadioArchive.Group.

    .OUTPUTS
    System.IO.FileInfo. The file it saved.

    .LINK
    Import-RadioArchiveGroup

    .LINK
    Export-RadioArchiveSetup
    #>
    [CmdletBinding()]
    [OutputType([System.IO.FileInfo])]
    param(
        # Only groups with these names. Wildcards are allowed. Default: every
        # group.
        [Parameter(ValueFromPipelineByPropertyName)]
        [SupportsWildcards()]
        [string[]] $Name,

        # The CSV file to save. No default. No wildcards.
        [Parameter(Mandatory)]
        [string] $Path,

        # Replace a file that's already there. Off by default.
        [switch] $Force
    )
    begin {
        $wanted = [System.Collections.Generic.List[string]]::new()
    }
    process {
        foreach ($n in @($Name)) {
            if ($n) {
                $wanted.Add($n)
            }
        }
    }
    end {
        try {
            $rows = ConvertTo-RaLabelCsvRow -Rows @((Get-RaSetup).groups) -Wanted $wanted -Filter ($MyInvocation.ExpectingInput -or $wanted.Count -gt 0)
            Export-RaCsv -Cmdlet $PSCmdlet -Path $Path -Column 'Name', 'Alert', 'LedColour', 'SortOrder' -Rows @($rows) -Force $Force
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $Path
        }
    }
}

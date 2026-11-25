function Export-RadioArchiveTag {
    <#
    .SYNOPSIS
    Saves tags to a CSV file that Import-RadioArchiveTag reads back.

    .DESCRIPTION
    Writes one row per tag with Name, Alert, LedColour and SortOrder, from
    the radio's setup (ADR 0030). Import-Csv | Import-RadioArchiveTag reads
    the file back, and the file as saved imports as all Unchanged. Every
    tag is saved, or the ones piped in or named. An existing file isn't
    replaced unless -Force is given.

    .EXAMPLE
    Export-RadioArchiveTag -Path tags.csv

    .INPUTS
    Objects with a Name property, such as a RadioArchive.Tag.

    .OUTPUTS
    System.IO.FileInfo. The file it saved.

    .LINK
    Import-RadioArchiveTag

    .LINK
    Export-RadioArchiveSetup
    #>
    [CmdletBinding()]
    [OutputType([System.IO.FileInfo])]
    param(
        # Only tags with these names. Wildcards are allowed. Default: every
        # tag.
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
            $rows = ConvertTo-RaLabelCsvRow -Rows @((Get-RaSetup).tags) -Wanted $wanted -Filter ($MyInvocation.ExpectingInput -or $wanted.Count -gt 0)
            Export-RaCsv -Cmdlet $PSCmdlet -Path $Path -Column 'Name', 'Alert', 'LedColour', 'SortOrder' -Rows @($rows) -Force $Force
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $Path
        }
    }
}

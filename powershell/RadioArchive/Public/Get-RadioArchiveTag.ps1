function Get-RadioArchiveTag {
    <#
    .SYNOPSIS
    Gets the radio's talkgroup tags.

    .DESCRIPTION
    Lists the tags talkgroups have (R22), such as dispatch, with each tag's
    own alert, LED colour and sort order. A tag is made when a talkgroup is
    given one that doesn't exist yet.

    .EXAMPLE
    Get-RadioArchiveTag

    .EXAMPLE
    Get-RadioArchiveTag -Name fireground | Set-RadioArchiveTag -Alert chime

    .INPUTS
    System.Int64. Objects with a TagId property, such as a RadioArchive.Tag.

    .OUTPUTS
    RadioArchive.Tag. TagId, Name, Alert, LedColour and SortOrder.

    .LINK
    Set-RadioArchiveTag

    .LINK
    Rename-RadioArchiveTag
    #>
    [CmdletBinding()]
    [OutputType('RadioArchive.Tag')]
    param(
        # The tags' IDs. Default: every tag.
        [Parameter(ValueFromPipelineByPropertyName)]
        [long[]] $TagId,

        # Their names. Wildcards are allowed. Default: every tag.
        [SupportsWildcards()]
        [string[]] $Name
    )
    process {
        try {
            Get-RaLabel -Kind Tag -Id $TagId -Name $Name
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_
        }
    }
}

function Get-RadioArchiveGroup {
    <#
    .SYNOPSIS
    Gets the radio's talkgroup groups.

    .DESCRIPTION
    Lists the groups talkgroups are in (R22), such as North, with each
    group's own alert, LED colour and sort order. A talkgroup without its
    own alert or LED colour takes its tag's, then its groups', then its
    system's (ADR 0006).

    .EXAMPLE
    Get-RadioArchiveGroup -Name 'N*'

    .EXAMPLE
    Get-RadioArchiveGroup -Name North | Set-RadioArchiveGroup -LedColour blue

    .INPUTS
    System.Int64. Objects with a GroupId property, such as a RadioArchive.Group.

    .OUTPUTS
    RadioArchive.Group. GroupId, Name, Alert, LedColour and SortOrder.

    .LINK
    New-RadioArchiveGroup

    .LINK
    Set-RadioArchiveGroup

    .LINK
    Rename-RadioArchiveGroup
    #>
    [CmdletBinding()]
    [OutputType('RadioArchive.Group')]
    param(
        # The groups' IDs. Default: every group.
        [Parameter(ValueFromPipelineByPropertyName)]
        [long[]] $GroupId,

        # Their names. Wildcards are allowed. Default: every group.
        [SupportsWildcards()]
        [string[]] $Name
    )
    process {
        try {
            Get-RaLabel -Kind Group -Id $GroupId -Name $Name
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_
        }
    }
}

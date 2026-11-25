function Rename-RadioArchiveGroup {
    <#
    .SYNOPSIS
    Renames a group.

    .DESCRIPTION
    Gives the group a new name; its talkgroups stay in it. A name another
    group has is refused (group_exists).

    .EXAMPLE
    Get-RadioArchiveGroup -Name North | Rename-RadioArchiveGroup -NewName 'North district'

    .INPUTS
    System.Int64. Objects with a GroupId property, such as a RadioArchive.Group.

    .OUTPUTS
    RadioArchive.Group, with -PassThru: the group as renamed. Nothing
    otherwise.

    .LINK
    Get-RadioArchiveGroup

    .LINK
    Set-RadioArchiveGroup
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'Medium')]
    [OutputType('RadioArchive.Group')]
    param(
        # The group's ID. No default.
        [Parameter(Mandatory, Position = 0, ValueFromPipelineByPropertyName)]
        [long] $GroupId,

        # Its new name, 1-100 characters. No default.
        [Parameter(Mandatory, Position = 1)]
        [string] $NewName,

        # Output the group as renamed. Off by default.
        [switch] $PassThru
    )
    process {
        if (-not $PSCmdlet.ShouldProcess("group $GroupId", "Rename to $NewName")) {
            return
        }
        try {
            $group = Update-RaLabel -Kind Group -Id $GroupId -Body @{ name = $NewName }
            if ($PassThru) {
                $group
            }
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $GroupId
        }
    }
}

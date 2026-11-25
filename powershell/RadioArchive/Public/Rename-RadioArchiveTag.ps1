function Rename-RadioArchiveTag {
    <#
    .SYNOPSIS
    Renames a tag.

    .DESCRIPTION
    Gives the tag a new name; its talkgroups keep it. A name another tag
    has is refused (tag_exists).

    .EXAMPLE
    Get-RadioArchiveTag -Name dispatch | Rename-RadioArchiveTag -NewName Dispatch

    .INPUTS
    System.Int64. Objects with a TagId property, such as a RadioArchive.Tag.

    .OUTPUTS
    RadioArchive.Tag, with -PassThru: the tag as renamed. Nothing otherwise.

    .LINK
    Get-RadioArchiveTag

    .LINK
    Set-RadioArchiveTag
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'Medium')]
    [OutputType('RadioArchive.Tag')]
    param(
        # The tag's ID. No default.
        [Parameter(Mandatory, Position = 0, ValueFromPipelineByPropertyName)]
        [long] $TagId,

        # Its new name, 1-100 characters. No default.
        [Parameter(Mandatory, Position = 1)]
        [string] $NewName,

        # Output the tag as renamed. Off by default.
        [switch] $PassThru
    )
    process {
        if (-not $PSCmdlet.ShouldProcess("tag $TagId", "Rename to $NewName")) {
            return
        }
        try {
            $tag = Update-RaLabel -Kind Tag -Id $TagId -Body @{ name = $NewName }
            if ($PassThru) {
                $tag
            }
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $TagId
        }
    }
}

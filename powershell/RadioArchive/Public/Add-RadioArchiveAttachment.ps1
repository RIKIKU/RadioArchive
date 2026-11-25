function Add-RadioArchiveAttachment {
    <#
    .SYNOPSIS
    Attaches a recorder's system to one of the radio's systems.

    .DESCRIPTION
    Stores a recorder's calls for a shortName (the name its trunk-recorder
    gives a system) under the radio's system -SystemId from now on (R12).
    Attaching a pair that's attached already moves it to that system.
    Rejected calls (Get-RadioArchiveRejection) name the pairs to attach.
    Recorder keys never pass through the module (R10).

    .EXAMPLE
    Add-RadioArchiveAttachment -Recorder Bench -ShortName ex -SystemId 1

    .EXAMPLE
    Get-RadioArchiveRejection -Unattached | Add-RadioArchiveAttachment -SystemId 1

    Attaches every pair whose calls were rejected for not being attached.

    .INPUTS
    Objects with Recorder, ShortName and SystemId properties, such as a RadioArchive.Rejection or a RadioArchive.Attachment.

    .OUTPUTS
    RadioArchive.Attachment. The attachment as made.

    .LINK
    Get-RadioArchiveAttachment

    .LINK
    Remove-RadioArchiveAttachment

    .LINK
    Get-RadioArchiveRejection
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'Medium')]
    [OutputType('RadioArchive.Attachment')]
    param(
        # The recorder's name. No default; no wildcards.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [string] $Recorder,

        # The shortName its trunk-recorder gives the system, 1-64 bytes. No
        # default.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [string] $ShortName,

        # The radio's system to store its calls under. No default.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [long] $SystemId
    )
    process {
        if (-not $PSCmdlet.ShouldProcess("$Recorder / $ShortName", "Attach to system $SystemId")) {
            return
        }
        try {
            $path = '/api/v1/admin/recorders/{0}/attachments/{1}' -f (ConvertTo-RaPathSegment -Value $Recorder), (ConvertTo-RaPathSegment -Value $ShortName)
            ConvertTo-RaAttachment -Row (Invoke-RaApi -Method PUT -Path $path -Body @{ system_id = $SystemId })
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject "$Recorder/$ShortName"
        }
    }
}

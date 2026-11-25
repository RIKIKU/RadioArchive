function Remove-RadioArchiveAttachment {
    <#
    .SYNOPSIS
    Detaches a recorder's system.

    .DESCRIPTION
    From now on the recorder's calls for that shortName are rejected until
    it's attached again (R12). Calls already stored stay. Asks first
    (ConfirmImpact High): -Confirm:$false doesn't ask.

    .EXAMPLE
    Remove-RadioArchiveAttachment -Recorder Bench -ShortName ex

    .EXAMPLE
    Get-RadioArchiveAttachment -Recorder Old* | Remove-RadioArchiveAttachment -Confirm:$false

    .INPUTS
    Objects with Recorder and ShortName properties, such as a RadioArchive.Attachment.

    .OUTPUTS
    None, or with -PassThru the RadioArchive.Attachment as it was.

    .LINK
    Get-RadioArchiveAttachment

    .LINK
    Add-RadioArchiveAttachment
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'High')]
    [OutputType('RadioArchive.Attachment')]
    param(
        # The recorder's name. No default; no wildcards.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [string] $Recorder,

        # The shortName its trunk-recorder gives the system. No default.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [string] $ShortName,

        # Output the attachment as it was. Off by default.
        [switch] $PassThru
    )
    process {
        if (-not $PSCmdlet.ShouldProcess("$Recorder / $ShortName", 'Detach')) {
            return
        }
        try {
            $before = $null
            if ($PassThru) {
                $before = @(Invoke-RaApi -Method GET -Path '/api/v1/admin/attachments') |
                    Where-Object -FilterScript { $null -ne $_ -and $_.recorder -eq $Recorder -and $_.short_name -eq $ShortName } |
                    Select-Object -First 1
            }
            $path = '/api/v1/admin/recorders/{0}/attachments/{1}' -f (ConvertTo-RaPathSegment -Value $Recorder), (ConvertTo-RaPathSegment -Value $ShortName)
            $null = Invoke-RaApi -Method DELETE -Path $path
            if ($PassThru -and $before) {
                ConvertTo-RaAttachment -Row $before
            }
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject "$Recorder/$ShortName"
        }
    }
}

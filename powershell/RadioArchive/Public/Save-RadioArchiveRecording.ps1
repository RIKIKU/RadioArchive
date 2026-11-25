function Save-RadioArchiveRecording {
    <#
    .SYNOPSIS
    Saves a call's recording to a file, byte for byte, hidden talkgroups' too.

    .DESCRIPTION
    Downloads a stored call's recording as the radio keeps it (Ogg Opus),
    for any call, hidden and ignored ones too (R34), into the folder
    -Path names, as <CallId>.ogg. An existing file isn't replaced unless
    -Force is given. Admin playback isn't audited.

    .EXAMPLE
    Get-RadioArchiveCall -SystemId 1 -Talkgroup 1003 -First 1 | Save-RadioArchiveRecording -Path ~/Recordings

    .INPUTS
    System.String. Objects with a CallId property, such as a RadioArchive.Call.

    .OUTPUTS
    System.IO.FileInfo. The file it saved.

    .LINK
    Get-RadioArchiveCall
    #>
    [CmdletBinding()]
    [OutputType([System.IO.FileInfo])]
    param(
        # The call's ID. No default.
        [Parameter(Mandatory, ValueFromPipeline, ValueFromPipelineByPropertyName)]
        [ValidatePattern('^[0-9a-fA-F-]{36}$')]
        [string] $CallId,

        # The folder to save into. Default: the current location. No
        # wildcards.
        [string] $Path = '.',

        # Replace a file that's already there. Off by default.
        [switch] $Force
    )
    process {
        try {
            $folder = $PSCmdlet.GetUnresolvedProviderPathFromPSPath($Path)
            if (-not (Test-Path -LiteralPath $folder -PathType Container)) {
                throw (New-RaErrorRecord -ErrorId 'no_such_folder' -Category ObjectNotFound -Message "There's no folder $folder.")
            }
            $file = Join-Path -Path $folder -ChildPath "$($CallId.ToLowerInvariant()).ogg"
            if ((Test-Path -LiteralPath $file) -and -not $Force) {
                throw (New-RaErrorRecord -ErrorId 'file_exists' -Category ResourceExists -Message "$file is already there. Add -Force to replace it.")
            }
            $temp = "$file.$PID.part"
            try {
                $null = Invoke-RaApi -Method GET -Path "/api/v1/admin/calls/$CallId/recording" -OutFile $temp
                [System.IO.File]::Move($temp, $file, $true)
            } finally {
                if (Test-Path -LiteralPath $temp) {
                    Remove-Item -LiteralPath $temp -Force
                }
            }
            Get-Item -LiteralPath $file
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $CallId
        }
    }
}

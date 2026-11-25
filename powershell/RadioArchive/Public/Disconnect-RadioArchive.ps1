function Disconnect-RadioArchive {
    <#
    .SYNOPSIS
    Signs out of a RadioArchive radio.

    .DESCRIPTION
    Revokes the sign-in in Authentik, so it can't be renewed, and deletes
    the saved copy, wherever it's saved (the DPAPI file on Windows, or the
    vault). The next command asks you to run Connect-RadioArchive.

    If Authentik can't be reached, the saved copy is still deleted, and a
    warning says the sign-in couldn't be revoked: it ends by itself after
    30 days unused.

    .EXAMPLE
    Disconnect-RadioArchive

    Signs out of the radio this session is connected to.

    .EXAMPLE
    Disconnect-RadioArchive -Uri https://radio.example.org

    Signs out of that radio's saved sign-in, from any window.

    .INPUTS
    System.Uri. Objects with a Uri property, such as a RadioArchive.Connection.

    .OUTPUTS
    None.

    .LINK
    Connect-RadioArchive

    .LINK
    about_RadioArchive
    #>
    [Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSAvoidUsingAllowUnencryptedAuthentication', '', Justification = 'Only for a sign-in saved with -AllowUnencryptedAuthentication (guidelines section 9).')]
    [CmdletBinding()]
    [OutputType([void])]
    param(
        # The radio to sign out of. Default: the one this session is
        # connected to, or the only saved sign-in. No wildcards.
        [Parameter(Position = 0, ValueFromPipeline, ValueFromPipelineByPropertyName)]
        [uri] $Uri
    )
    process {
        try {
            $connection = $null
            if ($Uri) {
                $origin = $Uri.GetLeftPart([System.UriPartial]::Authority)
                if ($script:Connection -and $script:Connection.Uri -eq $origin) {
                    $connection = $script:Connection
                }
            } else {
                $connection = $script:Connection
                if (-not $connection) {
                    $all = @(Read-RaSignIn)
                    if ($all.Count -eq 0) {
                        Write-Warning -Message 'Not signed in to RadioArchive.'
                        return
                    }
                    if ($all.Count -gt 1) {
                        throw (New-RaErrorRecord -ErrorId 'several_servers' -Category InvalidArgument -Message "You're signed in to several RadioArchive servers ($(($all | ForEach-Object -Process { $_.uri }) -join ', ')). Say which with -Uri.")
                    }
                    $origin = $all[0].uri
                } else {
                    $origin = $connection.Uri
                }
            }
            $saved = Read-RaSignIn -Origin $origin

            # The refresh token: this session's, or the saved one.
            $refresh = $null
            if ($connection -and $connection.Store -eq 'session') {
                $refresh = $connection.RefreshToken
            } elseif ($saved) {
                try {
                    $refresh = (Read-RaRefreshToken -Origin $origin).RefreshToken
                } catch {
                    Write-Warning -Message "The saved sign-in couldn't be read, so it can't be revoked: $($_.Exception.Message)"
                }
            }
            if (-not $refresh -and -not $connection) {
                Write-Warning -Message "Not signed in to $origin."
                return
            }
            $revoked = $false
            if ($refresh) {
                try {
                    $settings = if ($connection) { $connection } else { Get-RaServerSetting -Origin $origin -AllowUnencryptedAuthentication:([bool]$saved.allowUnencryptedAuthentication) }
                    $revoked = Revoke-RaToken -Settings $settings -Token $refresh
                    if ($connection -and $connection.AccessToken) {
                        $null = Revoke-RaToken -Settings $settings -Token $connection.AccessToken -Hint 'access_token'
                    }
                } catch {
                    $revoked = $false
                }
            }
            Remove-RaSignIn -Origin $origin -WhatIf:$false -Confirm:$false
            if ($script:Connection -and $script:Connection.Uri -eq $origin) {
                $script:Connection = $null
            }
            if (-not $revoked) {
                Write-Warning -Message "Signed out here, but the sign-in couldn't be revoked in Authentik. It ends by itself after 30 days unused."
            }
        } catch {
            $PSCmdlet.ThrowTerminatingError((Copy-RaErrorRecord -ErrorRecord $_ -TargetObject $Uri))
        }
    }
}

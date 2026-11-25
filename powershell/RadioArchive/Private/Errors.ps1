# Errors (docs/powershell-guidelines.md section 7): the API's error code is
# the ErrorId, its message the message, the HTTP status gives the category.

function New-RaErrorRecord {
    <#
    .SYNOPSIS
    Makes an ErrorRecord for a refusal or a failure.

    .DESCRIPTION
    The ErrorId is the API's error code, the message the API's message, and
    the category comes from the HTTP status. Messages never hold a token.
    #>
    [Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSUseShouldProcessForStateChangingFunctions', '', Justification = 'Makes an object in memory; changes nothing.')]
    [CmdletBinding()]
    [OutputType([System.Management.Automation.ErrorRecord])]
    param(
        # The API's error code, or another stable identifier.
        [Parameter(Mandatory)]
        [string] $ErrorId,

        # The message to show.
        [Parameter(Mandatory)]
        [string] $Message,

        # The HTTP status, or 0 when there was none.
        [int] $Status = 0,

        # The category, when there's no HTTP status to take it from.
        [System.Management.Automation.ErrorCategory] $Category = [System.Management.Automation.ErrorCategory]::NotSpecified,

        # The input item the error is about.
        [object] $TargetObject
    )
    if ($Status -ne 0) {
        $Category = switch ($Status) {
            { $_ -in 400, 413, 422 } { 'InvalidArgument'; break }
            401 { 'AuthenticationError'; break }
            403 { 'PermissionDenied'; break }
            404 { 'ObjectNotFound'; break }
            409 {
                if ($ErrorId -like '*_exists' -or $ErrorId -eq 'label_taken') { 'ResourceExists' } else { 'InvalidOperation' }
                break
            }
            429 { 'LimitsExceeded'; break }
            { $_ -ge 500 } { 'ConnectionError'; break }
            default { 'NotSpecified' }
        }
    }
    $exception = [System.InvalidOperationException]::new($Message)
    $exception.Data['RadioArchive.Status'] = $Status
    $exception.Data['RadioArchive.Code'] = $ErrorId
    [System.Management.Automation.ErrorRecord]::new($exception, $ErrorId, $Category, $TargetObject)
}

# Failures that stop every item, not just this one: they're terminating
# (guidelines section 7).
$script:StopCodes = @('not_connected', 'several_servers', 'server_unreachable', 'authentik_unreachable', 'sign_in_ended',
    'renewal_failed', 'invalid_token', 'admin_required', 'admin_sign_in_required', 'account_disabled', 'two_credentials',
    'admin_check_unavailable', 'vault_failed', 'secretmanagement_missing', 'unencrypted_refused')

function Write-RaError {
    <#
    .SYNOPSIS
    Reports a public command's failure: terminating when it stops every item, otherwise for this item alone.
    #>
    [CmdletBinding()]
    param(
        # The public command's $PSCmdlet.
        [Parameter(Mandatory)]
        [System.Management.Automation.PSCmdlet] $Cmdlet,

        # What failed.
        [Parameter(Mandatory)]
        [System.Management.Automation.ErrorRecord] $ErrorRecord,

        # The input item the error is about.
        [object] $TargetObject
    )
    $record = Copy-RaErrorRecord -ErrorRecord $ErrorRecord -TargetObject $TargetObject
    $code = $record.Exception.Data['RadioArchive.Code']
    if ($code -in $script:StopCodes -or "$code" -like 'http_5*') {
        $Cmdlet.ThrowTerminatingError($record)
    }
    $Cmdlet.WriteError($record)
}

function Copy-RaErrorRecord {
    <#
    .SYNOPSIS
    Re-targets an error from a private function for a public command.

    .DESCRIPTION
    Keeps the error's code (as the ErrorId), message and category, and sets
    the input item the public command was working on. An error that isn't
    the module's own (no code) is wrapped with the code 'unexpected', so no
    exception from deeper down, which could hold a request, reaches $Error.
    #>
    [CmdletBinding()]
    [OutputType([System.Management.Automation.ErrorRecord])]
    param(
        # The error to copy.
        [Parameter(Mandatory)]
        [System.Management.Automation.ErrorRecord] $ErrorRecord,

        # The input item the error is about.
        [object] $TargetObject
    )
    $code = $ErrorRecord.Exception.Data['RadioArchive.Code']
    if (-not $code) {
        return New-RaErrorRecord -ErrorId 'unexpected' -Message $ErrorRecord.Exception.Message -Category $ErrorRecord.CategoryInfo.Category -TargetObject $TargetObject
    }
    [System.Management.Automation.ErrorRecord]::new($ErrorRecord.Exception, $code, $ErrorRecord.CategoryInfo.Category, $TargetObject)
}

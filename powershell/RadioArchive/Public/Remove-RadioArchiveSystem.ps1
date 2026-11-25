function Remove-RadioArchiveSystem {
    <#
    .SYNOPSIS
    Deletes a system that has no talkgroups and no calls.

    .DESCRIPTION
    Deletes the system. The radio refuses while it has talkgroups or calls
    (system_in_use). Asks first, because deleting can't be undone
    (ConfirmImpact High): -Confirm:$false doesn't ask.

    .EXAMPLE
    Remove-RadioArchiveSystem -SystemId 9

    .INPUTS
    System.Int64. Objects with a SystemId property, such as a RadioArchive.System.

    .OUTPUTS
    None, or with -PassThru the RadioArchive.System as it was before it was
    deleted.

    .LINK
    Get-RadioArchiveSystem

    .LINK
    New-RadioArchiveSystem
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'High')]
    [OutputType('RadioArchive.System')]
    param(
        # The system's ID. No default.
        [Parameter(Mandatory, Position = 0, ValueFromPipelineByPropertyName)]
        [long] $SystemId,

        # Output the system as it was. Off by default.
        [switch] $PassThru
    )
    process {
        if (-not $PSCmdlet.ShouldProcess("system $SystemId", 'Delete')) {
            return
        }
        try {
            $before = $null
            if ($PassThru) {
                $before = ConvertTo-RaSystem -Row (Invoke-RaApi -Method GET -Path "/api/v1/admin/systems/$SystemId")
            }
            $null = Invoke-RaApi -Method DELETE -Path "/api/v1/admin/systems/$SystemId"
            if ($PassThru) {
                $before
            }
        } catch {
            Write-RaError -Cmdlet $PSCmdlet -ErrorRecord $_ -TargetObject $SystemId
        }
    }
}

#Requires -Modules @{ ModuleName = 'Pester'; RequiredVersion = '5.9.1' }
# The [pester] rules of docs/powershell-guidelines.md, checked for every
# exported command, and PSScriptAnalyzer on every file. Run with
# powershell/tests/Invoke-Tests.ps1.

BeforeDiscovery {
    $root = Join-Path -Path $PSScriptRoot -ChildPath '../RadioArchive'
    Import-Module -Name (Join-Path -Path $root -ChildPath 'RadioArchive.psd1') -Force
    $commands = @(Get-Command -Module RadioArchive -CommandType Function | ForEach-Object -Process { @{ Name = $_.Name } })
    $files = @(Get-ChildItem -Path $root -Recurse -Include '*.ps1', '*.psm1', '*.psd1' |
            ForEach-Object -Process { @{ Path = $_.FullName; Name = $_.Name } })
    $allFiles = @(Get-ChildItem -Path (Join-Path -Path $PSScriptRoot -ChildPath '..') -Recurse -File -Include '*.ps1', '*.psm1', '*.psd1', '*.ps1xml', '*.txt' |
            ForEach-Object -Process { @{ Path = $_.FullName; Name = $_.Name } })
}

BeforeAll {
    $root = Join-Path -Path $PSScriptRoot -ChildPath '../RadioArchive'
    $manifestPath = Join-Path -Path $root -ChildPath 'RadioArchive.psd1'
    Import-Module -Name $manifestPath -Force
    $manifest = Import-PowerShellDataFile -Path $manifestPath
    [xml]$formats = Get-Content -Path (Join-Path -Path $root -ChildPath 'RadioArchive.Format.ps1xml') -Raw
    [xml]$types = Get-Content -Path (Join-Path -Path $root -ChildPath 'RadioArchive.Types.ps1xml') -Raw
    $common = [System.Management.Automation.PSCmdlet]::CommonParameters + [System.Management.Automation.PSCmdlet]::OptionalCommonParameters

    function Get-Binding([string] $Name) {
        (Get-Command -Name $Name).ScriptBlock.Attributes | Where-Object -FilterScript { $_ -is [System.Management.Automation.CmdletBindingAttribute] }
    }

    # Commands whose ConfirmImpact is High: deletions, and role changes
    # (guidelines section 4). Set-RadioArchiveTalkgroup -State and
    # Set-RadioArchiveRetention's shortening count as High at run time
    # (Module.Tests.ps1).
    $high = @('Remove-RadioArchiveSystem', 'Remove-RadioArchiveUnit', 'Remove-RadioArchiveAttachment', 'Set-RadioArchiveAccount',
        'Disable-RadioArchiveAccount', 'Enable-RadioArchiveAccount', 'Remove-RadioArchiveAccount')
    # Commands about the radio as a whole, which take nothing from the
    # pipeline (guidelines section 3).
    $noItems = @('Get-RadioArchiveRetention')
}

Describe 'The manifest (guidelines sections 1 and 10)' {
    It 'is valid' {
        { Test-ModuleManifest -Path $manifestPath -ErrorAction Stop } | Should -Not -Throw
    }
    It 'is for PowerShell 7.6 or later, Core only' {
        $manifest.CompatiblePSEditions | Should -Be @('Core')
        $manifest.PowerShellVersion | Should -Be '7.6'
    }
    It 'requires no other module (SecretManagement is loaded only when needed)' {
        $manifest.ContainsKey('RequiredModules') | Should -BeFalse
    }
    It 'lists its functions exactly, one per file in Public' {
        $public = (Get-ChildItem -Path (Join-Path -Path $root -ChildPath 'Public') -Filter '*.ps1').BaseName | Sort-Object
        $manifest.FunctionsToExport | Should -Not -Contain '*'
        @($manifest.FunctionsToExport | Sort-Object) | Should -Be @($public)
        @((Get-Command -Module RadioArchive -CommandType Function).Name | Sort-Object) | Should -Be @($public)
    }
    It 'lists its aliases exactly' {
        $manifest.AliasesToExport | Should -Not -Contain '*'
        @($manifest.AliasesToExport | Sort-Object) | Should -Be @($manifest.FunctionsToExport | ForEach-Object -Process { $_ -replace 'RadioArchive', 'Ra' } | Sort-Object)
    }
    It 'has about_RadioArchive' {
        Join-Path -Path $root -ChildPath 'en-US/about_RadioArchive.help.txt' | Should -Exist
        (Get-Help -Name about_RadioArchive | Out-String) | Should -Match 'Connect-RadioArchive'
    }
    It 'has no private function named as an exported alias' {
        $private = & (Get-Module -Name RadioArchive) { Get-Command -CommandType Function | Where-Object -Property Module -EQ $ExecutionContext.SessionState.Module | Select-Object -ExpandProperty Name }
        foreach ($alias in $manifest.AliasesToExport) {
            $private | Should -Not -Contain $alias
        }
    }
    It 'has a command for each thing the admin API changes, and none for recorder keys (R10)' {
        $names = (Get-Command -Module RadioArchive -CommandType Function).Name
        foreach ($n in @('Get-RadioArchiveSystem', 'Set-RadioArchiveTalkgroup', 'Set-RadioArchiveUnit', 'Set-RadioArchiveGroup', 'Set-RadioArchiveTag',
                'Add-RadioArchiveAttachment', 'Get-RadioArchiveRejection', 'Set-RadioArchiveRecorder', 'Set-RadioArchiveRetention',
                'Get-RadioArchiveCall', 'Save-RadioArchiveRecording', 'Set-RadioArchiveAccount', 'Disable-RadioArchiveAccount',
                'Enable-RadioArchiveAccount', 'Remove-RadioArchiveAccount')) {
            $names | Should -Contain $n
        }
        foreach ($command in Get-Command -Module RadioArchive -CommandType Function) {
            $command.Parameters.Keys | Where-Object -FilterScript { $_ -match 'Key' } | Should -BeNullOrEmpty -Because "$($command.Name) takes no recorder key"
        }
    }
}

Describe '<Name> (guidelines sections 2 to 8)' -ForEach $commands {
    BeforeAll {
        $command = Get-Command -Name $Name
        $binding = Get-Binding -Name $Name
        $verb, $noun = $Name -split '-', 2
        $help = Get-Help -Name $Name -Full
        $source = $command.ScriptBlock.ToString()
        $changes = $verb -in @('New', 'Set', 'Remove', 'Rename', 'Add', 'Disable', 'Enable')
    }

    It 'uses an approved verb' {
        (Get-Verb).Verb | Should -Contain $verb
    }
    It 'has a noun starting with RadioArchive' {
        $noun | Should -Match '^RadioArchive'
    }
    It 'has an Ra alias' {
        $alias = Get-Alias -Name ($Name -replace 'RadioArchive', 'Ra') -ErrorAction SilentlyContinue
        $alias.ResolvedCommandName | Should -Be $Name
    }
    It 'is an advanced function' {
        $command.CmdletBinding | Should -BeTrue
    }
    It 'declares its output type' {
        $output = @($command.OutputType.Name)
        if ($Name -eq 'Disconnect-RadioArchive') {
            $output | Should -Be @('System.Void')
        } elseif ($Name -eq 'Save-RadioArchiveRecording') {
            $output | Should -Be @('System.IO.FileInfo')
        } else {
            $output | Should -Not -BeNullOrEmpty
            $output | ForEach-Object -Process { $_ | Should -Match '^RadioArchive\.' }
        }
    }
    It 'takes pipeline input in every parameter set, with at most one ValueFromPipeline' {
        foreach ($set in $command.ParameterSets) {
            $pipeline = @($set.Parameters | Where-Object -FilterScript { $_.ValueFromPipeline -or $_.ValueFromPipelineByPropertyName })
            $byValue = @($set.Parameters | Where-Object -FilterScript { $_.ValueFromPipeline })
            if ($Name -in $noItems) {
                $pipeline | Should -BeNullOrEmpty -Because 'it acts on no item'
                continue
            }
            $pipeline.Count | Should -BeGreaterThan 0 -Because "parameter set $($set.Name)"
            $byValue.Count | Should -BeLessOrEqual 1 -Because "parameter set $($set.Name)"
        }
    }
    It 'supports ShouldProcess when it changes data, with the right ConfirmImpact' {
        if ($changes) {
            $binding.SupportsShouldProcess | Should -BeTrue
            $expected = if ($Name -in $high) { 'High' } else { 'Medium' }
            $binding.ConfirmImpact | Should -Be $expected
        } else {
            $binding.SupportsShouldProcess | Should -BeFalse
        }
    }
    It 'has -PassThru when it changes, renames, removes, disables or enables' {
        if ($verb -in @('Set', 'Rename', 'Remove', 'Disable', 'Enable')) {
            $command.Parameters.Keys | Should -Contain 'PassThru'
        }
    }
    It 'accepts wildcards on the names it filters by' {
        if ($verb -eq 'Get') {
            foreach ($p in @('Name', 'Label', 'Email', 'Group', 'Tag', 'Alias', 'Recorder')) {
                if ($command.Parameters.ContainsKey($p) -and $command.Parameters[$p].ParameterType -eq [string[]]) {
                    $command.Parameters[$p].Attributes.TypeId.Name | Should -Contain 'SupportsWildcardsAttribute' -Because "-$p"
                }
            }
        }
    }
    It 'has its own -First and -Skip ([uint64]) when it can return many items, not SupportsPaging' {
        $binding.SupportsPaging | Should -BeFalse
        $command.Parameters.ContainsKey('IncludeTotalCount') | Should -BeFalse
        if ($Name -eq 'Get-RadioArchiveCall') {
            $command.Parameters['First'].ParameterType | Should -Be ([uint64])
            $command.Parameters['Skip'].ParameterType | Should -Be ([uint64])
        }
    }
    It 'has full comment-based help' {
        $help.Synopsis | Should -Not -BeNullOrEmpty
        $help.Synopsis | Should -Not -Match "^\s*$Name\s"
        ($help.description | Out-String).Trim() | Should -Not -BeNullOrEmpty
        @($help.examples.example).Count | Should -BeGreaterThan 0
        ($help.inputTypes | Out-String).Trim() | Should -Not -BeNullOrEmpty
        ($help.returnValues | Out-String).Trim() | Should -Not -BeNullOrEmpty
        @($help.relatedLinks.navigationLink).Count | Should -BeGreaterThan 0
        foreach ($type in $command.OutputType.Name | Where-Object -FilterScript { $_ -like 'RadioArchive.*' }) {
            ($help.returnValues | Out-String) | Should -Match ([regex]::Escape($type))
        }
    }
    It 'never uses .PARAMETER, and documents every parameter' {
        $source | Should -Not -Match '\.PARAMETER'
        foreach ($parameter in $command.Parameters.Keys | Where-Object -FilterScript { $_ -notin $common }) {
            $text = ($help.parameters.parameter | Where-Object -Property name -EQ $parameter).description | Out-String
            $text.Trim() | Should -Not -BeNullOrEmpty -Because "-$parameter needs help"
        }
    }
    It 'has a table view and a default property set for each output type' {
        foreach ($type in $command.OutputType.Name | Where-Object -FilterScript { $_ -like 'RadioArchive.*' }) {
            @($formats.Configuration.ViewDefinitions.View | Where-Object -FilterScript { $_.ViewSelectedBy.TypeName -eq $type -and $_.TableControl }).Count | Should -Be 1 -Because $type
            $set = ($types.Types.Type | Where-Object -Property Name -EQ $type).Members.MemberSet.Members.PropertySet
            $set.Name | Should -Be 'DefaultDisplayPropertySet' -Because $type
        }
    }
}

Describe 'Table views (guidelines section 5)' {
    It 'labels every column computed by a script, and shows times as local time under their own names' {
        foreach ($view in $formats.Configuration.ViewDefinitions.View) {
            $headers = @($view.TableControl.TableHeaders.TableColumnHeader)
            $items = @($view.TableControl.TableRowEntries.TableRowEntry.TableColumnItems.TableColumnItem)
            $headers.Count | Should -Be $items.Count -Because $view.Name
            for ($i = 0; $i -lt $items.Count; $i++) {
                if ($items[$i].PSObject.Properties['ScriptBlock'] -and $items[$i].ScriptBlock) {
                    $label = "$($headers[$i].Label)"
                    $label | Should -Not -BeNullOrEmpty -Because "$($view.Name) column $($i + 1) is computed"
                    $items[$i].ScriptBlock | Should -BeExactly "`$_.$label.LocalDateTime" -Because "$($view.Name) column $($i + 1)"
                } else {
                    "$($items[$i].PropertyName)" | Should -Not -MatchExactly 'At$' -Because "$($view.Name) shows a time as it is"
                }
            }
        }
    }
}

Describe 'Objects and parameters line up (guidelines section 3)' {
    It 'a <Type>''s properties bind <Command>''s identity parameters' -ForEach @(
        @{ Type = 'RadioArchive.Talkgroup'; Command = 'Set-RadioArchiveTalkgroup'; Properties = @('SystemId', 'Id') }
        @{ Type = 'RadioArchive.Talkgroup'; Command = 'Get-RadioArchiveCall'; Properties = @('SystemId', 'Id') }
        @{ Type = 'RadioArchive.System'; Command = 'Set-RadioArchiveSystem'; Properties = @('SystemId') }
        @{ Type = 'RadioArchive.Unit'; Command = 'Set-RadioArchiveUnit'; Properties = @('SystemId', 'Unit') }
        @{ Type = 'RadioArchive.Group'; Command = 'Set-RadioArchiveGroup'; Properties = @('GroupId') }
        @{ Type = 'RadioArchive.Tag'; Command = 'Set-RadioArchiveTag'; Properties = @('TagId') }
        @{ Type = 'RadioArchive.Attachment'; Command = 'Remove-RadioArchiveAttachment'; Properties = @('Recorder', 'ShortName') }
        @{ Type = 'RadioArchive.Rejection'; Command = 'Add-RadioArchiveAttachment'; Properties = @('Recorder', 'ShortName') }
        @{ Type = 'RadioArchive.Recorder'; Command = 'Set-RadioArchiveRecorder'; Properties = @('Name') }
        @{ Type = 'RadioArchive.Account'; Command = 'Set-RadioArchiveAccount'; Properties = @('AccountId') }
        @{ Type = 'RadioArchive.Account'; Command = 'Disable-RadioArchiveAccount'; Properties = @('AccountId') }
        @{ Type = 'RadioArchive.Account'; Command = 'Enable-RadioArchiveAccount'; Properties = @('AccountId') }
        @{ Type = 'RadioArchive.Account'; Command = 'Remove-RadioArchiveAccount'; Properties = @('AccountId') }
        @{ Type = 'RadioArchive.Call'; Command = 'Save-RadioArchiveRecording'; Properties = @('CallId') }
    ) {
        $c = Get-Command -Name $Command
        foreach ($p in $Properties) {
            $param = $c.Parameters.Values | Where-Object -FilterScript { $_.Name -eq $p -or $_.Aliases -contains $p } | Select-Object -First 1
            $param | Should -Not -BeNullOrEmpty -Because "$Command takes $p"
            $param.ParameterSets.Values.ValueFromPipelineByPropertyName | Should -Contain $true -Because "$Command binds $p from a $Type"
        }
    }
    It 'Set- commands bind only identity from the pipeline, so a piped object changes nothing by itself' {
        foreach ($c in Get-Command -Module RadioArchive -CommandType Function | Where-Object -FilterScript { $_.Verb -in @('Set', 'Rename', 'Remove', 'Disable', 'Enable') }) {
            foreach ($p in $c.Parameters.Values | Where-Object -FilterScript { $_.ParameterSets.Values.ValueFromPipelineByPropertyName -contains $true }) {
                $p.Name | Should -BeIn @('SystemId', 'Id', 'Unit', 'GroupId', 'TagId', 'Recorder', 'ShortName', 'Name', 'AccountId', 'Days', 'Alias') -Because "$($c.Name) -$($p.Name)"
            }
        }
    }
}

Describe 'PSScriptAnalyzer: <Name> (guidelines section 11)' -ForEach $files {
    It 'has no findings' {
        $settings = Join-Path -Path $PSScriptRoot -ChildPath '../PSScriptAnalyzerSettings.psd1'
        $findings = @(Invoke-ScriptAnalyzer -Path $Path -Settings $settings -ErrorAction Stop)
        $findings | ForEach-Object -Process { Write-Warning -Message ('{0}:{1} {2} {3}' -f $_.ScriptName, $_.Line, $_.RuleName, $_.Message) }
        $findings.Count | Should -Be 0
    }

    # PSUseCorrectCasing's command check, done here on the test's own
    # thread, in the module's scope so private functions are found too.
    It 'writes commands and parameters with their declared casing' {
        $tokens = $errors = $null
        $ast = [System.Management.Automation.Language.Parser]::ParseFile($Path, [ref]$tokens, [ref]$errors)
        $module = Get-Module -Name RadioArchive
        $wrong = foreach ($c in $ast.FindAll({ param($n) $n -is [System.Management.Automation.Language.CommandAst] }, $true)) {
            $written = $c.GetCommandName()
            if (-not $written) {
                continue
            }
            $info = & $module { param($n) Get-Command -Name $n -ErrorAction Ignore | Select-Object -First 1 } $written
            if (-not $info -or $info.CommandType -in 'ExternalScript', 'Application') {
                continue
            }
            if ($written -cne $info.Name -and $written -cne "$($info.ModuleName)\$($info.Name)") {
                "line $($c.Extent.StartLineNumber): $written is $($info.Name)"
            }
            foreach ($p in $c.CommandElements | Where-Object -FilterScript { $_ -is [System.Management.Automation.Language.CommandParameterAst] }) {
                $declared = $info.Parameters.Keys | Where-Object -FilterScript { $_ -eq $p.ParameterName } | Select-Object -First 1
                if ($declared -and $p.ParameterName -cne $declared) {
                    "line $($p.Extent.StartLineNumber): $written -$($p.ParameterName) is -$declared"
                }
            }
        }
        @($wrong) | Should -BeNullOrEmpty
    }
}

Describe 'ASCII only: <Name>' -ForEach $allFiles {
    # Escapes written into PowerShell files have become literal bytes
    # before; the module's files stay plain ASCII.
    It 'holds only ASCII' {
        $bytes = [System.IO.File]::ReadAllBytes($Path)
        @($bytes | Where-Object -FilterScript { $_ -gt 127 }).Count | Should -Be 0
    }
}

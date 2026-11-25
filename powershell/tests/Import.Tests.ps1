#Requires -Modules @{ ModuleName = 'Pester'; RequiredVersion = '5.9.1' }
# The import and export commands with the API mocked (issue #117, ADR 0030,
# ADR 0035): what each row sends, one batch per pipeline, -WhatIf, -Update
# and its confirmation, and the CSV round trip.

BeforeAll {
    Import-Module -Name (Join-Path -Path $PSScriptRoot -ChildPath '../RadioArchive/RadioArchive.psd1') -Force
    $manifest = (Resolve-Path -Path (Join-Path -Path $PSScriptRoot -ChildPath '../RadioArchive/RadioArchive.psd1')).Path

    # The setup as GET /api/v1/admin/setup answers it.
    $script:setupJson = @'
{
  "format": "radioarchive-setup",
  "version": 1,
  "systems": [
    {
      "id": 1, "label": "EX", "name": "Example network", "auto_create": false, "alert": null, "led_colour": null, "sort_order": 0,
      "talkgroups": [
        { "id": 1001, "label": "DISP N", "name": "Dispatch North", "tag": "dispatch", "groups": ["North", "East"], "state": "public", "unreviewed": false, "type": "D", "frequency": 851012500, "delay_minutes": 5, "alert": "beep", "led_colour": "red", "sort_order": 2, "fireground_channel": null },
        { "id": 1003, "label": "2026-11-25", "name": "Admin", "tag": "admin", "groups": [], "state": "hidden", "unreviewed": false, "type": "", "frequency": null, "delay_minutes": 0, "alert": null, "led_colour": null, "sort_order": 0, "fireground_channel": 43 },
        { "id": 4711, "label": "", "name": "", "tag": null, "groups": [], "state": "public", "unreviewed": true, "type": "", "frequency": null, "delay_minutes": 0, "alert": null, "led_colour": null, "sort_order": 0, "fireground_channel": null }
      ],
      "units": [ { "from": 5001, "to": 5001, "alias": "Engine 1" }, { "from": 5100, "to": 5199, "alias": "Portables, north" } ]
    }
  ],
  "groups": [ { "name": "East", "alert": null, "led_colour": "cyan", "sort_order": -1 }, { "name": "North", "alert": null, "led_colour": null, "sort_order": 0 } ],
  "tags": [ { "name": "admin", "alert": null, "led_colour": null, "sort_order": 0 }, { "name": "dispatch", "alert": "chime", "led_colour": null, "sort_order": 1 } ]
}
'@

    # A stand-in for POST /api/v1/admin/import: each row against the setup,
    # unchanged when every field it says is as the setup has it.
    function script:Get-FakeReport($Body) {
        $have = $script:setupJson | ConvertFrom-Json -Depth 32 -DateKind String
        $in = $Body | ConvertTo-Json -Depth 16 | ConvertFrom-Json -AsHashtable -Depth 32 -DateKind String
        $results = [System.Collections.Generic.List[object]]::new()
        $summary = [ordered]@{}
        foreach ($k in 'systems', 'talkgroups', 'units', 'groups', 'tags') {
            $summary[$k] = [ordered]@{ new = 0; changed = 0; unchanged = 0; kept = 0; skipped = 0 }
        }
        $identity = @{ system = @('id', 'talkgroups', 'units'); talkgroup = @('id'); unit = @('from', 'to'); group = @('name'); tag = @('name') }
        $compare = {
            param($row, $was, $kind, $system, $id)
            $changes = @(foreach ($key in @($row.Keys)) {
                    if ($key -in $identity[$kind]) { continue }
                    $a = ConvertTo-Json -InputObject $was.$key -Compress -Depth 4
                    $b = ConvertTo-Json -InputObject $row[$key] -Compress -Depth 4
                    if ($key -eq 'groups') { $a = (@($was.groups) | Sort-Object) -join ';'; $b = (@($row[$key]) | Sort-Object) -join ';' }
                    if ($a -ne $b) { [ordered]@{ field = $key; before = $was.$key; after = $row[$key] } }
                })
            $result = if ($null -eq $was) { 'new' } elseif ($changes.Count) { 'changed' } else { 'unchanged' }
            $summary["${kind}s"][$result]++
            $results.Add([pscustomobject]@{ kind = $kind; system = $system; id = "$id"; label = "$id"; result = $result; changes = $changes })
        }
        foreach ($s in @($in.setup.systems)) {
            $was = $have.systems | Where-Object -Property id -EQ $s.id
            & $compare $s $was 'system' $null $s.id
            foreach ($t in @($s.talkgroups | Where-Object -FilterScript { $_ })) {
                & $compare $t ($was.talkgroups | Where-Object -Property id -EQ $t.id) 'talkgroup' $s.id $t.id
            }
            foreach ($u in @($s.units | Where-Object -FilterScript { $_ })) {
                & $compare $u ($was.units | Where-Object -FilterScript { $_.from -eq $u.from -and $_.to -eq $u.to }) 'unit' $s.id "$($u.from)-$($u.to)"
            }
        }
        foreach ($g in @($in.setup.groups)) { & $compare $g ($have.groups | Where-Object -Property name -EQ $g.name) 'group' $null $g.name }
        foreach ($g in @($in.setup.tags)) { & $compare $g ($have.tags | Where-Object -Property name -EQ $g.name) 'tag' $null $g.name }
        $results.Add([pscustomobject]@{ kind = 'talkgroup'; system = 1; id = '1002'; label = 'EX 1002'; result = 'kept' })
        $report = [pscustomobject]@{ dry_run = [bool]$in.Contains('dry_run'); applied = $in.Contains('plan'); update = [bool]$in.update; plan = 'p1'; summary = [pscustomobject]$summary; results = $results.ToArray() }
        # As Invoke-RestMethod would read it.
        $report | ConvertTo-Json -Depth 16 | ConvertFrom-Json -Depth 32 -DateKind String
    }

    function script:Use-FakeRadio {
        $script:calls = [System.Collections.Generic.List[object]]::new()
        Mock -ModuleName RadioArchive Invoke-RaApi {
            # The body as the radio receives it: JSON.
            $sent = if ($null -ne $Body) { $Body | ConvertTo-Json -Depth 16 -Compress } else { $null }
            $script:calls.Add([pscustomobject]@{ Method = $Method; Path = $Path; Body = $Body; Json = $sent; OutFile = $OutFile })
            switch ("$Method $Path") {
                'GET /api/v1/admin/setup' {
                    if ($OutFile) {
                        [System.IO.File]::WriteAllText($OutFile, $script:setupJson)
                        return
                    }
                    return $script:setupJson | ConvertFrom-Json -Depth 32 -DateKind String
                }
                'POST /api/v1/admin/import' { return Get-FakeReport -Body $Body }
            }
        }
    }

    function script:Get-Imports { @($script:calls | Where-Object -Property Path -EQ '/api/v1/admin/import') }
}

Describe 'Importing talkgroups (R52, issue #117)' {
    BeforeEach { Use-FakeRadio }

    It 'sends the whole pipeline as one dry run, then one apply with its plan' {
        $rows = @(
            [pscustomobject]@{ SystemId = 1; Id = 1001; Label = 'DISP North' }
            [pscustomobject]@{ SystemId = 1; Id = 1005; Label = 'DISP S'; Tag = 'dispatch' }
            [pscustomobject]@{ SystemId = 2; Id = 1001; Label = 'OPS'; Tag = 'ops' }
        )
        $out = @($rows | Import-RadioArchiveTalkgroup -Confirm:$false)
        $imports = Get-Imports
        $imports.Count | Should -Be 2
        $imports[0].Body.dry_run | Should -BeTrue
        $imports[0].Body.update | Should -BeFalse
        $imports[1].Body.plan | Should -Be 'p1'
        $imports[1].Body.Contains('dry_run') | Should -BeFalse
        $imports[1].Json | Should -Be ($imports[0].Json -replace '"dry_run":true', '"plan":"p1"')
        @($imports[0].Body.setup.systems.id) | Should -Be @(1, 2)
        $out.Count | Should -Be 3
        $out[0].PSTypeNames[0] | Should -Be 'RadioArchive.ImportResult'
        $out.Applied | Should -Be @($true, $true, $true)
        $out.Result | Should -Be @('Changed', 'New', 'New')
        $out[0].Fields | Should -Be @('label')
        $out.Id | Should -Not -Contain '1002' -Because 'kept talkgroups are left out of a talkgroup import''s output'
    }

    It 'a row sends only the fields it has, whatever the row before had' {
        @(
            [pscustomobject]@{ SystemId = 1; Id = 1001; Label = 'A'; FiregroundChannel = '41'; Alert = 'Beep' }
            [pscustomobject]@{ SystemId = 1; Id = 1003; Name = 'B' }
        ) | Import-RadioArchiveTalkgroup -WhatIf
        $tgs = (Get-Imports)[0].Body.setup.systems[0].talkgroups
        @($tgs[0].Keys) | Should -Be @('id', 'label', 'alert', 'fireground_channel')
        $tgs[0].fireground_channel | Should -Be 41
        $tgs[0].alert | Should -Be 'beep'
        @($tgs[1].Keys) | Should -Be @('id', 'name')
    }

    It 'reads trunk-recorder''s talkgroup CSV for the system -SystemId names' {
        $csv = @'
Decimal,Hex,Alpha Tag,Mode,Description,Tag,Category
1001,3e9,DISP N,D,Dispatch North,dispatch,North
2001,7d1,ANLG,A,Analog ops,ops,Ops
'@ | ConvertFrom-Csv
        $csv | Import-RadioArchiveTalkgroup -SystemId 1 -WhatIf
        $setup = (Get-Imports)[0].Body.setup
        $setup.systems[0].id | Should -Be 1
        $tg = $setup.systems[0].talkgroups
        $tg[0].id | Should -Be 1001
        @($tg[0].Keys) | Should -Be @('id', 'label', 'name', 'tag', 'groups', 'type')
        $tg[0].label | Should -Be 'DISP N'
        $tg[0].groups | Should -Be @('North')
        $tg[1].type | Should -Be 'A'
    }

    It 'in a CSV, empty cells mean none for settings and leave the rest unsaid; Group splits on semicolons' {
        [pscustomobject]@{ SystemId = '1'; Id = '1001'; Label = ''; Tag = ''; Group = 'North; East;'; State = ''; Unreviewed = 'False'; Type = ''
            Frequency = ''; DelayMinutes = ''; Alert = ''; LedColour = ''; SortOrder = ''; FiregroundChannel = ''
        } | Import-RadioArchiveTalkgroup -WhatIf
        $tg = (Get-Imports)[0].Body.setup.systems[0].talkgroups[0]
        @($tg.Keys) | Should -Be @('id', 'label', 'groups', 'unreviewed', 'type', 'frequency', 'alert', 'led_colour', 'fireground_channel')
        $tg.label | Should -Be ''
        $tg.groups | Should -Be @('North', 'East')
        $tg.unreviewed | Should -BeFalse
        $tg.frequency | Should -BeNullOrEmpty
        $tg.alert | Should -BeNullOrEmpty
        (Get-Imports)[0].Json | Should -Match '"frequency":null,"alert":null,"led_colour":null,"fireground_channel":null'
    }

    It 'a bad number stops the batch: nothing is sent' {
        [pscustomobject]@{ SystemId = 1; Id = 1001; SortOrder = 'first' } | Import-RadioArchiveTalkgroup -Confirm:$false -ErrorVariable failed -ErrorAction SilentlyContinue
        $failed[0].FullyQualifiedErrorId | Should -Match '^invalid_value'
        $failed[0].Exception.Message | Should -Match 'System 1, talkgroup 1001: sort_order must be a whole number'
        Get-Imports | Should -BeNullOrEmpty
    }

    It '-WhatIf previews every row with Applied $false and applies nothing' {
        $out = @([pscustomobject]@{ SystemId = 1; Id = 1001; Label = 'X' } | Import-RadioArchiveTalkgroup -WhatIf)
        (Get-Imports).Count | Should -Be 1
        $out.Result | Should -Be 'Changed'
        $out.Applied | Should -BeFalse
        $out.Changes[0].Before | Should -Be 'DISP N'
        $out.Changes[0].After | Should -Be 'X'
    }

    It 'nothing to change sends only the dry run, and asks nothing' {
        $out = @([pscustomobject]@{ SystemId = 1; Id = 1001; Label = 'DISP N' } | Import-RadioArchiveTalkgroup)
        (Get-Imports).Count | Should -Be 1
        $out.Result | Should -Be 'Unchanged'
    }

    It 'sends -Update as update; -Update asks by default, as ConfirmImpact High does' {
        [pscustomobject]@{ SystemId = 1; Id = 1001; DelayMinutes = 0 } | Import-RadioArchiveTalkgroup -Update -Confirm:$false
        (Get-Imports).Body.update | Should -Be @($true, $true)
        $ConfirmPreference | Should -Be 'High'
        $fake = "& (Get-Module RadioArchive) { function script:Invoke-RaApi { param(`$Method, `$Path, `$Body) if (`$Body.Contains('plan')) { [Console]::WriteLine('APPLIED') }; [pscustomobject]@{ applied = `$true; plan = 'p'; summary = [pscustomobject]@{ talkgroups = [pscustomobject]@{ new = 0; changed = 1; unchanged = 0; kept = 0; skipped = 0 } }; results = @() } } }"
        $child = {
            param($script)
            $file = Join-Path -Path $TestDrive -ChildPath "child-$([guid]::NewGuid().ToString('N')).ps1"
            Set-Content -Path $file -Value $script -Encoding utf8
            $o = & (Get-Process -Id $PID).Path -NoProfile -NonInteractive -File $file 2>&1
            @($o | ForEach-Object -Process { "$_" }) -join "`n"
        }
        $update = & $child "Import-Module '$manifest'; $fake; Import-RadioArchiveTalkgroup -SystemId 1 -Id 1001 -DelayMinutes 3 -Update"
        $update | Should -Not -Match 'APPLIED'
        $update | Should -Match 'NonInteractive'
        $plain = & $child "Import-Module '$manifest'; $fake; Import-RadioArchiveTalkgroup -SystemId 1 -Id 1001 -Label x"
        $plain | Should -Match 'APPLIED'
    }

    It 'import_changed is the command''s error, and nothing is output as applied' {
        Mock -ModuleName RadioArchive Invoke-RaApi {
            if ($Body.Contains('plan')) {
                throw (& (Get-Module -Name RadioArchive) { New-RaErrorRecord -ErrorId 'import_changed' -Status 409 -Message 'The setup has changed since the preview.' })
            }
            Get-FakeReport -Body $Body
        }
        $out = @([pscustomobject]@{ SystemId = 1; Id = 1001; Label = 'X' } | Import-RadioArchiveTalkgroup -Confirm:$false -ErrorVariable failed -ErrorAction SilentlyContinue)
        $out | Should -BeNullOrEmpty
        $failed[0].FullyQualifiedErrorId | Should -Match '^import_changed'
        $failed[0].CategoryInfo.Category | Should -Be 'InvalidOperation'
    }

    It 'Get-RadioArchiveTalkgroup''s objects import as they are' {
        Mock -ModuleName RadioArchive Invoke-RaApi -ParameterFilter { $Path -eq '/api/v1/admin/talkgroups' } {
            $s = $script:setupJson | ConvertFrom-Json -Depth 32 -DateKind String
            [pscustomobject]@{ talkgroups = @($s.systems[0].talkgroups | ForEach-Object -Process { $_ | Add-Member -NotePropertyName system_id -NotePropertyValue 1 -PassThru }) }
        }
        $out = @(Get-RadioArchiveTalkgroup | Import-RadioArchiveTalkgroup -Update -Confirm:$false)
        $out.Result | Should -Be @('Unchanged', 'Unchanged', 'Unchanged')
    }
}

Describe 'The CSV round trip (R53)' {
    BeforeEach { Use-FakeRadio }

    It 'Export-RadioArchive<Thing> writes a file that imports as all unchanged' -ForEach @(
        @{ Thing = 'Talkgroup'; Count = 3 }
        @{ Thing = 'Unit'; Count = 2 }
        @{ Thing = 'System'; Count = 1 }
        @{ Thing = 'Group'; Count = 2 }
        @{ Thing = 'Tag'; Count = 2 }
    ) {
        $file = Join-Path -Path $TestDrive -ChildPath "$Thing.csv"
        $saved = & "Export-RadioArchive$Thing" -Path $file -WarningAction SilentlyContinue
        $saved | Should -BeOfType [System.IO.FileInfo]
        $options = @{ Confirm = $false }
        if ((Get-Command -Name "Import-RadioArchive$Thing").Parameters.ContainsKey('Update')) {
            $options.Update = $true
        }
        $out = @(Import-Csv -Path $file | & "Import-RadioArchive$Thing" @options)
        $out.Count | Should -Be $Count
        $out.Result | Sort-Object -Unique | Should -Be 'Unchanged'
        (Get-Imports).Count | Should -Be 1 -Because 'nothing changes, so nothing is applied'
    }

    It 'the talkgroup CSV holds every field the import takes, and warns of hidden talkgroups' {
        $file = Join-Path -Path $TestDrive -ChildPath 'tg.csv'
        $null = Export-RadioArchiveTalkgroup -Path $file -WarningVariable warned -WarningAction SilentlyContinue
        $warned | Should -Match 'hidden'
        $lines = Get-Content -Path $file
        $lines[0] | Should -Be '"SystemId","Id","Label","Name","Tag","Group","State","Unreviewed","Type","Frequency","DelayMinutes","Alert","LedColour","SortOrder","FiregroundChannel"'
        $lines[1] | Should -Be '"1","1001","DISP N","Dispatch North","dispatch","North;East","Public","False","D","851012500","5","beep","red","2",""'
        $lines[2] | Should -Match '^"1","1003","2026-11-25",'
    }

    It 'exports only what is piped in, and refuses to replace a file without -Force' {
        $file = Join-Path -Path $TestDrive -ChildPath 'one.csv'
        $null = [pscustomobject]@{ SystemId = 1; Id = 1003 } | Export-RadioArchiveTalkgroup -Path $file -WarningAction SilentlyContinue
        @(Import-Csv -Path $file).Id | Should -Be @('1003')
        [pscustomobject]@{ SystemId = 1 } | Export-RadioArchiveTalkgroup -Path $file -ErrorVariable failed -ErrorAction SilentlyContinue -WarningAction SilentlyContinue
        $failed[0].FullyQualifiedErrorId | Should -Match '^file_exists'
        $null = [pscustomobject]@{ SystemId = 1 } | Export-RadioArchiveTalkgroup -Path $file -Force -WarningAction SilentlyContinue
        @(Import-Csv -Path $file).Count | Should -Be 3
        $null = [pscustomobject]@{ SystemId = 9 } | Export-RadioArchiveTalkgroup -Path $file -Force -WarningAction SilentlyContinue
        @(Get-Content -Path $file).Count | Should -Be 1 -Because 'nothing matched: the header alone'
    }
}

Describe 'The whole setup (R52, R53)' {
    BeforeEach { Use-FakeRadio }

    It 'Export-RadioArchiveSetup saves the radio''s bytes as they are; Import-RadioArchiveSetup sends them back' {
        $file = Join-Path -Path $TestDrive -ChildPath 'setup.json'
        $saved = Export-RadioArchiveSetup -Path $file -WarningVariable warned -WarningAction SilentlyContinue
        [System.IO.File]::ReadAllText($saved.FullName) | Should -BeExactly $script:setupJson
        $warned | Should -Match 'hidden'
        $out = @(Import-RadioArchiveSetup -Path $file -WhatIf)
        $sent = (Get-Imports)[0].Body.setup
        $sent.systems[0].talkgroups[1].label | Should -BeExactly '2026-11-25' -Because 'a label that looks like a date stays text'
        $out.Kind | Sort-Object -Unique | Should -Be @('Group', 'System', 'Tag', 'Talkgroup', 'Unit')
        $out | Where-Object -Property Result -EQ 'Kept' | Should -Not -BeNullOrEmpty -Because 'a setup import shows what the file leaves out'
        Export-RadioArchiveSetup -Path $file -ErrorVariable failed -ErrorAction SilentlyContinue -WarningAction SilentlyContinue
        $failed[0].FullyQualifiedErrorId | Should -Match '^file_exists'
    }

    It 'a file that isn''t JSON is refused before anything is sent' {
        $file = Join-Path -Path $TestDrive -ChildPath 'bad.json'
        Set-Content -Path $file -Value 'not json'
        Import-RadioArchiveSetup -Path $file -ErrorVariable failed -ErrorAction SilentlyContinue
        $failed[-1].FullyQualifiedErrorId | Should -Be 'invalid_json,Import-RadioArchiveSetup'
        Get-Imports | Should -BeNullOrEmpty
    }
}

Describe 'Importing systems, units, groups and tags' {
    BeforeEach { Use-FakeRadio }

    It 'units: a range and a single ID, aliases as they are' {
        @([pscustomobject]@{ ID = '5001'; Alias = 'Engine One' }, [pscustomobject]@{ ID = '5100-5199'; Alias = '' }) |
            Import-RadioArchiveUnit -SystemId 1 -WhatIf
        $units = (Get-Imports)[0].Body.setup.systems[0].units
        $units[0].from | Should -Be 5001
        $units[0].to | Should -Be 5001
        $units[1].from | Should -Be 5100
        $units[1].to | Should -Be 5199
        $units[1].alias | Should -Be ''
    }
    It 'systems: what a row says, and a system twice is refused' {
        [pscustomobject]@{ SystemId = 1; Label = 'EXU'; AutoCreate = 'True'; LedColour = '' } | Import-RadioArchiveSystem -Update -Confirm:$false
        $s = (Get-Imports)[0].Body.setup.systems[0]
        @($s.Keys) | Should -Be @('id', 'label', 'auto_create', 'led_colour')
        $s.auto_create | Should -BeTrue
        @([pscustomobject]@{ SystemId = 1 }, [pscustomobject]@{ SystemId = 1 }) | Import-RadioArchiveSystem -ErrorVariable failed -ErrorAction SilentlyContinue
        $failed[0].FullyQualifiedErrorId | Should -Match '^duplicate_row'
    }
    It 'groups and tags by name, with their settings' {
        [pscustomobject]@{ Name = 'North'; LedColour = 'Blue'; SortOrder = '3' } | Import-RadioArchiveGroup -Update -Confirm:$false
        $g = (Get-Imports)[0].Body.setup.groups[0]
        @($g.Keys) | Should -Be @('name', 'led_colour', 'sort_order')
        $g.led_colour | Should -Be 'blue'
        $g.sort_order | Should -Be 3
        Import-RadioArchiveTag -Name dispatch -Alert '' -WhatIf
        $t = (Get-Imports)[-1].Body.setup.tags[0]
        @($t.Keys) | Should -Be @('name', 'alert')
        $t.alert | Should -BeNullOrEmpty
    }
}

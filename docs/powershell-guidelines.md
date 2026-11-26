# PowerShell module guidelines

These rules are for the **RadioArchive** PowerShell module (`powershell/RadioArchive/`, issue #116, ADR 0028). They follow Microsoft's [Cmdlet Development Guidelines](https://learn.microsoft.com/powershell/scripting/developer/cmdlet/cmdlet-development-guidelines), adapted for a script module. Where this file and Microsoft's pages disagree, this file wins.

**The goal:** an experienced PowerShell user finds the module predictable without reading its docs: commands named as they'd guess, objects rather than text, the pipeline working between commands, and `-WhatIf`, `-Confirm` and `Get-Help` behaving as they do for built-in cmdlets.

Each rule says how it's checked: **[analyzer]** is PSScriptAnalyzer in CI, **[pester]** a Pester test in CI (`powershell/tests`), **[stack]** the behaviour tests against the compose stack (`web/module-tests`), and **[review]** reading the code. If a tool can check a rule, it does.

## 1. Platform and form

- **PowerShell 7.6 or later, Core only.** The manifest says `CompatiblePSEditions = 'Core'` and `PowerShellVersion = '7.6'`. The minimum is raised in an ordinary change when a release loses support, never by a test that fails on a date. CI tests on the minimum and the newest stable release, on Windows and Linux. **[pester]**
- **A script module of advanced functions**, not compiled code: `[CmdletBinding()]` and `[OutputType()]` on every exported function, pipeline input handled in `process`, errors through `$PSCmdlet` (section 7), help in comments (section 8), display in `Types.ps1xml` and `Format.ps1xml` (section 5). **[pester]**
- **Files are ASCII.** No typographic quotes or dashes, and never an escape that could be written out as a literal byte. **[pester]**

## 2. Names

- **Approved verbs only** (`Get-Verb`), each for what it means: `New` creates, `Set` changes (and, for a unit, adds one that isn't there, as its API's `PUT` does), `Add` puts into a container (an attachment), `Remove` deletes, `Rename` renames, `Disable` and `Enable` switch an account off and on, `Save` writes something to a file, `Import` brings rows or a file into the radio as one batch, and `Export` writes the radio's setup to a file that its `Import` reads back (ADR 0035). **[analyzer]** `PSUseApprovedVerbs`, **[review]**
- **Nouns are singular and start with `RadioArchive`**: `Get-RadioArchiveTalkgroup`. Signing in uses the product as the noun: `Connect-RadioArchive`, `Disconnect-RadioArchive`. **[analyzer]** `PSUseSingularNouns`, **[pester]**
- **Every command has a short alias**, `RadioArchive` replaced by `Ra`: `Get-RaTalkgroup`, `Connect-Ra`. No private function has an alias's name. **[pester]**
- **Parameter names** are PowerShell's standard ones where one exists (`Name`, `Path`, `Uri`, `PassThru`, `Force`), singular, PascalCase, never a common parameter's name. Other spellings go in `[Alias()]`. **[analyzer]**, **[review]**

## 3. Parameters and the pipeline

- **Every parameter set takes pipeline input**, at most one parameter by value. The exception is a command about the radio as a whole, which takes nothing: `Get-RadioArchiveRetention`. **[pester]**
- **Objects and parameters line up.** A property that identifies an item has the parameter's name, so a `Get-` pipes into its `Set-`: `Get-RadioArchiveTalkgroup -Group North | Set-RadioArchiveTalkgroup -State Hidden`. Talkgroups are `SystemId` and `Id`, systems `SystemId`, units `SystemId` and `Unit`, groups `GroupId`, tags `TagId`, recorders `Name`, attachments `Recorder` and `ShortName`, accounts `AccountId`, calls `CallId`. **[pester]**
- **`Set-`, `Rename-` and `Remove-` bind only identity from the pipeline**, so a piped object never changes a field by itself. `New-` binds every field, so `Get- | Export-Csv`, then `Import-Csv | New-`, adds the same items elsewhere. `Import-` binds every field too, and sends a row's fields only when the row has them as properties (or the command line gives them), so a CSV with three columns changes three fields. **[pester]**
- **An import is one batch**: the whole pipeline is one dry run, one `ShouldProcess` on its summary, and one apply with its plan, or nothing. `-WhatIf` outputs every row's result with `Applied` `$false`. `-Update`, which changes what exists beyond R52's fields, asks by default as `ConfirmImpact` High does; state never changes through an import (ADR 0035). **[pester]**
- **Strong types:** `[uri]` for URLs, `[ValidateSet()]` for a fixed list, `[switch]` for on and off, `[Nullable[bool]]` or `[Nullable[int]]` where "unchanged" is a third choice, `[datetimeoffset]` for times. **[review]**
- **"Clear" is said plainly:** `''` clears an alert or LED colour, `$null` a number, `@()` a list. **[pester]**
- **Wildcards** on the names a `Get-` filters by, case-insensitively, with `[SupportsWildcards()]`. **[pester]**
- **Commands that can return many items** have their own `-First` and `-Skip` (`[uint64]`), not `SupportsPaging`, and fetch pages only as the pipeline asks. **[pester]**

## 4. Changes and confirmation

- **Every command that changes something** declares `SupportsShouldProcess` and asks `ShouldProcess` once per item before changing it. `-WhatIf` is the dry run. **[analyzer]** `PSUseShouldProcessForStateChangingFunctions`, **[pester]**
- **`ConfirmImpact`** is `High` for deletions, state changes and role changes, and `Medium` otherwise. Where one parameter makes it a state change (`Set-RadioArchiveTalkgroup -State`) or one case a deletion (shortening retention), the command lowers its own `$ConfirmPreference` to ask by default as `High` does, unless `-Confirm` or the caller's preference says otherwise. **[pester]**
- **`-Force`** only where a command asks for more than `ShouldProcess` (replacing a saved file). It never gets around a rule of the radio. **[review]**
- **Output after a change:** `New-` and `Add-` output what they made; `Set-`, `Rename-` and `Remove-` output nothing unless `-PassThru`. **[pester]**

## 5. Output

- **Objects, never text**, each with a type name (`RadioArchive.Talkgroup`, ...) that `[OutputType()]` names. No `Write-Host` or `Format-*` for data. **[analyzer]**, **[pester]**
- **Write each object as soon as it's ready.** **[review]**
- **A table view and a `DefaultDisplayPropertySet`** for each type, with the few columns people scan. **[pester]**
- **Times** are `[datetimeoffset]` values in UTC, and views show them in local time under the property's own name. **[pester]**
- **States, roles and kinds** are shown capitalised (`Hidden`, `Admin`); parameters take them in any case. **[review]**

## 6. Feedback

- `Write-Verbose` for each request (method and path), `Write-Debug` for its answer's status, `Write-Warning` when something may surprise (plain HTTP, a sign-in for this session only, a device code to enter). Never `[Console]`. **[review]**
- **No token in any of them** (section 9). **[stack]**

## 7. Errors

- **A refusal from the radio is an `ErrorRecord`** whose `ErrorId` is the radio's error code (`last_admin`), whose message is the radio's, whose `TargetObject` is the input item, and whose category comes from the status: 400 and 422 `InvalidArgument`, 401 `AuthenticationError`, 403 `PermissionDenied`, 404 `ObjectNotFound`, 409 `ResourceExists` for an `*_exists` code and `InvalidOperation` otherwise, 429 `LimitsExceeded`, 5xx and unreachable `ConnectionError`. **[pester]**
- **One item's failure is non-terminating**, so the pipeline carries on. A failure that stops every item (not signed in, not an admin, the radio unreachable) is terminating. Never `throw` a string to the user. **[pester]**
- **HTTP errors never throw inside `Invoke-RestMethod`** (`-SkipHttpErrorCheck`): its exception would keep the request, `Authorization` header and all, in `$Error`. An error from below that isn't the module's own is wrapped. **[pester]**

## 8. Help

- **Comment-based help** at the top of each function: `.SYNOPSIS`, `.DESCRIPTION`, at least one `.EXAMPLE`, `.INPUTS`, `.OUTPUTS` naming the type and what its properties are for, and `.LINK`. **[analyzer]** `PSProvideCommentHelp`, **[pester]**
- **Each parameter is documented by a comment directly above it**, saying its default and whether it takes wildcards. Never `.PARAMETER`. **[pester]**
- **`about_RadioArchive`** explains signing in, the objects and common pipelines. **[pester]**

## 9. Sign-in and tokens

- **Admins only, signed in as themselves** through the radio's Authentik: the browser with PKCE, or a device code. No API keys. **[stack]**
- **Tokens are `SecureString`s from end to end.** They're sent with `Invoke-RestMethod -Authentication Bearer -Token`. Authentik's token, device and revocation endpoints are the only requests whose bodies hold a token: one private file builds those bodies as bytes straight from the `SecureString` and reads replies' tokens straight into new ones, zeroing the buffers. **[review]**, **[pester]**
- **Never in output, `-Verbose`, `-Debug`, a warning or error, `$Error`, a transcript, or a file in plain text, UTF-16, hex or Base64.** **[stack]**, **[pester]**
- **Saved** only as ADR 0028 says: on Windows with `ConvertFrom-SecureString` and no key (DPAPI), only ever on Windows, where it encrypts; elsewhere in a SecretManagement vault; with neither, for the session only, with a warning. **[pester]**
- **HTTPS only**, unless `-AllowUnencryptedAuthentication` (the name `Invoke-RestMethod` uses) is given, with a warning. **[pester]**
- **The module talks only to the radio and the Authentik the radio names.** **[pester]**
- **Recorder keys never pass through the module** (R10). **[pester]**

## 10. Layout and style

- `RadioArchive.psd1`, `RadioArchive.psm1` (`Set-StrictMode -Version Latest`), `Public/` (one exported function per file, named after it), `Private/`, the format and types files, `en-US/about_RadioArchive.help.txt`. The manifest lists `FunctionsToExport` and `AliasesToExport` exactly, never `*`, and needs no other module: SecretManagement is loaded only when it's used. **[pester]**
- **Style:** PSScriptAnalyzer's formatting rules, braces on the same line, 4-space indents (`powershell/PSScriptAnalyzerSettings.psd1`). Full command and parameter names, in their declared case, no aliases, no positional arguments in the module's own code. **[analyzer]**, **[pester]**

## 11. Tests

- **PSScriptAnalyzer with zero findings**, the meta-tests, the unit tests with the API mocked and the sign-in tests run in CI's `powershell` job on Windows and Linux, on 7.6.0 and the newest stable release (`powershell/tests/Invoke-Tests.ps1`). **[pester]**
- **The behaviour tests** run the module against the compose stack with Authentik in CI's image job (`scripts/ci/e2e-module.sh`), on Linux, where the stack runs. **[stack]**
- Each release publishes the module to the PowerShell Gallery, with the release's version stamped into a copy of the manifest and checked by `Test-ModuleManifest` (`release.yml`'s `gallery` job, ADR 0043). A dry run checks it and publishes nothing.

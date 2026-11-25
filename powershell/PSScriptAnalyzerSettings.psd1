@{
    # docs/powershell-guidelines.md section 11: every default rule, plus
    # these, with zero findings allowed.
    Severity            = @('Error', 'Warning', 'Information')
    IncludeDefaultRules = $true
    ExcludeRules        = @()
    Rules               = @{
        PSProvideCommentHelp             = @{
            Enable                  = $true
            ExportedOnly            = $true
            BlockComment            = $true
            VSCodeSnippetCorrection = $false
            Placement               = 'begin'
        }
        PSAvoidUsingPositionalParameters = @{
            Enable           = $true
            CommandAllowList = @()
        }
        PSAvoidUsingCmdletAliases        = @{
            AllowList = @()
        }
        PSPlaceOpenBrace                 = @{
            Enable             = $true
            OnSameLine         = $true
            NewLineAfter       = $true
            IgnoreOneLineBlock = $true
        }
        PSPlaceCloseBrace                = @{
            Enable             = $true
            NewLineAfter       = $false
            IgnoreOneLineBlock = $true
            NoEmptyLineBefore  = $false
        }
        PSUseConsistentIndentation       = @{
            Enable              = $true
            IndentationSize     = 4
            PipelineIndentation = 'IncreaseIndentationForFirstPipeline'
            Kind                = 'space'
        }
        PSUseConsistentWhitespace        = @{
            Enable                                  = $true
            CheckInnerBrace                         = $true
            CheckOpenBrace                          = $true
            CheckOpenParen                          = $true
            CheckOperator                           = $false
            CheckPipe                               = $true
            CheckPipeForRedundantWhitespace         = $false
            CheckSeparator                          = $true
            CheckParameter                          = $false
            IgnoreAssignmentOperatorInsideHashTable = $true
        }
        PSAlignAssignmentStatement       = @{
            Enable         = $true
            CheckHashtable = $true
        }
        # Keywords and operators only: Meta.Tests.ps1 checks command and
        # parameter casing itself, on the test's own thread.
        PSUseCorrectCasing               = @{
            Enable        = $true
            CheckCommands = $false
            CheckKeyword  = $true
            CheckOperator = $true
        }
    }
}

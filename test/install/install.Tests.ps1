BeforeAll {
    # Load only the replacement helper, without downloading a release or
    # changing PATH. Its filesystem behavior can also be tested off Windows.
    $installer = Join-Path $PSScriptRoot '../../scripts/install.ps1'
    $tokens = $null
    $parseErrors = $null
    $ast = [System.Management.Automation.Language.Parser]::ParseFile(
        $installer, [ref]$tokens, [ref]$parseErrors)
    if ($parseErrors.Count) {
        throw "Could not parse $installer"
    }
    $helper = $ast.Find({
        param($node)
        $node -is [System.Management.Automation.Language.FunctionDefinitionAst] -and
            $node.Name -eq 'Install-Binaries'
    }, $true)
    if (-not $helper) {
        throw 'Install-Binaries helper not found'
    }
    . ([scriptblock]::Create($helper.Extent.Text))
    $script:copyItemCommand = Get-Command Copy-Item -CommandType Cmdlet
    $script:moveItemCommand = Get-Command Move-Item -CommandType Cmdlet
}

Describe 'Installer binary replacement' {
    BeforeEach {
        $ErrorActionPreference = 'Stop'
        $sourceDir = Join-Path $TestDrive ([Guid]::NewGuid().ToString('N'))
        $destinationDir = Join-Path $TestDrive ([Guid]::NewGuid().ToString('N'))
        $names = @('sailwright', 'sailwright-gateway')
        New-Item -ItemType Directory -Path $sourceDir, $destinationDir | Out-Null
        foreach ($name in $names) {
            $source = Join-Path $sourceDir $name
            New-Item -ItemType Directory -Path $source | Out-Null
            Set-Content -LiteralPath (Join-Path $source "$name.exe") -Value "new-$name"
            Set-Content -LiteralPath (Join-Path $destinationDir "$name.exe") -Value "old-$name"
        }
        # Retain real filesystem operations for calls outside each injected
        # failure. Invoke the saved cmdlets directly to avoid mocking recursion.
        Mock Copy-Item {
            & $script:copyItemCommand -LiteralPath $LiteralPath -Destination $Destination
        }
        Mock Move-Item {
            & $script:moveItemCommand -LiteralPath $LiteralPath -Destination $Destination
        }
    }

    It 'replaces both binaries and removes obsolete backups and staging files' {
        Set-Content -LiteralPath (Join-Path $destinationDir 'sailwright.exe.previous.old') -Value 'previous'

        Install-Binaries -Names $names -SourceDir $sourceDir -DestinationDir $destinationDir

        foreach ($name in $names) {
            Get-Content -LiteralPath (Join-Path $destinationDir "$name.exe") | Should -Be "new-$name"
        }
        @(Get-ChildItem -LiteralPath $destinationDir).Count | Should -Be 2
    }

    It 'preserves both originals when staging the second binary fails' {
        Mock Copy-Item { throw 'simulated full disk' } -ParameterFilter {
            [IO.Path]::GetFileName($LiteralPath) -eq 'sailwright-gateway.exe'
        }

        { Install-Binaries -Names $names -SourceDir $sourceDir -DestinationDir $destinationDir } |
            Should -Throw '*simulated full disk*'

        foreach ($name in $names) {
            Get-Content -LiteralPath (Join-Path $destinationDir "$name.exe") | Should -Be "old-$name"
        }
        @(Get-ChildItem -LiteralPath $destinationDir).Count | Should -Be 2
    }

    It 'preserves both originals if an archive lacks the expected executable' {
        Remove-Item -LiteralPath (Join-Path $sourceDir 'sailwright-gateway/sailwright-gateway.exe')

        { Install-Binaries -Names $names -SourceDir $sourceDir -DestinationDir $destinationDir } |
            Should -Throw

        foreach ($name in $names) {
            Get-Content -LiteralPath (Join-Path $destinationDir "$name.exe") | Should -Be "old-$name"
        }
        @(Get-ChildItem -LiteralPath $destinationDir).Count | Should -Be 2
    }

    It 'restores the original if the first promotion fails' {
        Mock Move-Item { throw 'simulated promotion failure' } -ParameterFilter {
            $LiteralPath -like '*.tmp'
        }

        { Install-Binaries -Names $names -SourceDir $sourceDir -DestinationDir $destinationDir } |
            Should -Throw '*simulated promotion failure*'

        foreach ($name in $names) {
            Get-Content -LiteralPath (Join-Path $destinationDir "$name.exe") | Should -Be "old-$name"
        }
        @(Get-ChildItem -LiteralPath $destinationDir).Count | Should -Be 2
    }

    It 'rolls back both binaries if the second promotion fails' {
        Mock Move-Item { throw 'simulated gateway promotion failure' } -ParameterFilter {
            $LiteralPath -like '*.tmp' -and [IO.Path]::GetFileName($Destination) -eq 'sailwright-gateway.exe'
        }

        { Install-Binaries -Names $names -SourceDir $sourceDir -DestinationDir $destinationDir } |
            Should -Throw '*simulated gateway promotion failure*'

        foreach ($name in $names) {
            Get-Content -LiteralPath (Join-Path $destinationDir "$name.exe") | Should -Be "old-$name"
        }
        @(Get-ChildItem -LiteralPath $destinationDir).Count | Should -Be 2
    }

    It 'rolls back the first binary if moving the second original fails' {
        Mock Move-Item { throw 'simulated backup failure' } -ParameterFilter {
            [IO.Path]::GetFileName($LiteralPath) -eq 'sailwright-gateway.exe'
        }

        { Install-Binaries -Names $names -SourceDir $sourceDir -DestinationDir $destinationDir } |
            Should -Throw '*simulated backup failure*'

        foreach ($name in $names) {
            Get-Content -LiteralPath (Join-Path $destinationDir "$name.exe") | Should -Be "old-$name"
        }
        @(Get-ChildItem -LiteralPath $destinationDir).Count | Should -Be 2
    }

    It 'preserves a recovery backup across failed retries' {
        $backup = Join-Path $destinationDir 'sailwright.exe.previous.old'
        Move-Item -LiteralPath (Join-Path $destinationDir 'sailwright.exe') -Destination $backup
        Mock Copy-Item { throw 'simulated full disk' }

        foreach ($attempt in 1..2) {
            { Install-Binaries -Names $names -SourceDir $sourceDir -DestinationDir $destinationDir } |
                Should -Throw '*simulated full disk*'
            Get-Content -LiteralPath $backup | Should -Be 'old-sailwright'
            Get-Content -LiteralPath (Join-Path $destinationDir 'sailwright-gateway.exe') |
                Should -Be 'old-sailwright-gateway'
        }
        @(Get-ChildItem -LiteralPath $destinationDir).Count | Should -Be 2
    }

    It 'removes a newly installed binary when a later first-install promotion fails' {
        Get-ChildItem -LiteralPath $destinationDir | Remove-Item
        Mock Move-Item { throw 'simulated gateway promotion failure' } -ParameterFilter {
            [IO.Path]::GetFileName($Destination) -eq 'sailwright-gateway.exe'
        }

        { Install-Binaries -Names $names -SourceDir $sourceDir -DestinationDir $destinationDir } |
            Should -Throw '*simulated gateway promotion failure*'

        @(Get-ChildItem -LiteralPath $destinationDir).Count | Should -Be 0
    }
}

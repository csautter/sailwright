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

Describe 'Installer entry point' {
    BeforeEach {
        # Fail at the first download instead of reaching GitHub. Off Windows
        # the script stops even earlier, after binding its parameters.
        $env:SAILWRIGHT_DOWNLOAD_URL = 'http://127.0.0.1:9'
        $env:SAILWRIGHT_INSTALL_DIR = Join-Path $TestDrive 'bin'
    }

    AfterEach {
        Remove-Item -Path Env:SAILWRIGHT_DOWNLOAD_URL, Env:SAILWRIGHT_INSTALL_DIR, Env:SAILWRIGHT_VERSION -ErrorAction SilentlyContinue
    }

    It 'leaves the caller''s variables, preferences and functions alone under iex' {
        $env:SAILWRIGHT_VERSION = 'v0.0.0'
        $Version = 'caller'
        $InstallDir = 'caller'
        $NoGateway = 'caller'
        $ErrorActionPreference = 'Continue'
        $ProgressPreference = 'Continue'
        $functions = @(Get-ChildItem -Path Function:).Name

        # Run iex inline: Should -Throw would invoke it in a child scope and
        # hide anything leaked into this one.
        $failed = $false
        try {
            Get-Content -Raw -LiteralPath $installer | Invoke-Expression
        } catch {
            $failed = $true
        }

        $failed | Should -BeTrue
        $Version | Should -Be 'caller'
        $InstallDir | Should -Be 'caller'
        $NoGateway | Should -Be 'caller'
        $ErrorActionPreference | Should -Be 'Continue'
        $ProgressPreference | Should -Be 'Continue'
        @(Get-ChildItem -Path Function: | Where-Object { $_.Name -notin $functions }) | Should -BeNullOrEmpty
    }

    It 'binds named parameters when the file is run directly' {
        $message = $null
        try {
            & $installer -Version v0.0.0 -InstallDir (Join-Path $TestDrive 'bin') -NoGateway
        } catch {
            $message = "$_"
        }

        # Off Windows the platform check fails; on Windows the download of the
        # requested release does.
        $message | Should -Match 'install\.ps1 is for Windows|release v0\.0\.0'
    }

    It 'rejects unknown parameters' {
        { & $installer -Bogus } | Should -Throw '*cannot be found that matches parameter name*'
    }

    It 'rejects a switch value that @args splits off' {
        { & $installer -NoGateway:$false } | Should -Throw '*positional parameter cannot be found*'
    }
}

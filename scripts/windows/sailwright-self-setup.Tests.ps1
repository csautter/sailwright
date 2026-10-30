BeforeAll {
    $setupScript = Join-Path $PSScriptRoot "sailwright-self-setup.ps1"
    $previousImportOnly = $env:DEV_ALCHEMY_SELF_SETUP_IMPORT_ONLY
    try {
        $env:DEV_ALCHEMY_SELF_SETUP_IMPORT_ONLY = "1"
        . $setupScript
    } finally {
        if ($null -eq $previousImportOnly) {
            Remove-Item Env:\DEV_ALCHEMY_SELF_SETUP_IMPORT_ONLY -ErrorAction SilentlyContinue
        } else {
            $env:DEV_ALCHEMY_SELF_SETUP_IMPORT_ONLY = $previousImportOnly
        }
    }

    function Set-TestPathMock {
        param(
            [string[]]$ExistingPaths = @()
        )

        $script:ExistingPaths = $ExistingPaths
        Mock Test-Path {
            $target = if ($null -ne $LiteralPath) {
                $LiteralPath
            } else {
                $Path
            }

            foreach ($pathToTest in @($target)) {
                if ($script:ExistingPaths -contains $pathToTest) {
                    return $true
                }
            }

            return $false
        }
    }

    function Initialize-SelfSetupMainMocks {
        $script:EnsuredPackages = @()
        $script:EnsuredPaths = @()
        $script:CygwinEnsureCalls = @()
        $script:CygwinPackageCalls = @()
        $script:CygwinPipPackageCalls = @()
        $script:ShownToolVersionsRoot = $null

        Mock Test-IsAdministrator { return $true }
        Mock Invoke-SelfElevated { throw "Self-elevation should not be requested by unit tests." }
        Mock Ensure-ChocolateyInstalled {}
        Mock Ensure-GoInstallRootClean {}
        Mock Assert-GoToolchainLayout {}
        Mock Get-ChocolateyInstalledVersion { return $null }
        Mock Get-GitForWindowsVersion { return $null }
        Mock Get-GitForWindowsRoot { return "C:\Program Files\Git" }
        Mock Ensure-ChocolateyPackage {
            $script:EnsuredPackages += [pscustomobject]@{
                PackageName = $PackageName
                Version = $Version
                ExtraArgs = @($ExtraArgs)
            }
        }
        Mock Assert-NativePythonAvailable {}
        Mock Export-CommandDirectoryToGitHubPath {}
        Mock Ensure-CygwinChocolateyPackage {
            $script:CygwinEnsureCalls += [pscustomobject]@{
                Version = $Version
                InstallRoot = $InstallRoot
            }
            return $InstallRoot
        }
        Mock Ensure-PathContains { $script:EnsuredPaths += $PathEntry }
        Mock Ensure-CygwinPackage {
            $script:CygwinPackageCalls += [pscustomobject]@{
                PackageName = $PackageName
                CygwinRoot = $CygwinRoot
            }
        }
        Mock Ensure-CygwinPipPackage {
            $script:CygwinPipPackageCalls += [pscustomobject]@{
                PackageName = $PackageName
                Version = $Version
                CygwinRoot = $CygwinRoot
            }
        }
        Mock Show-InstalledToolVersions { $script:ShownToolVersionsRoot = $CygwinRoot }
    }
}

Describe "sailwright self setup Cygwin helpers" -Skip:(-not $IsWindows) {
    BeforeEach {
        $script:PreferredRoot = "C:\tools\cygwin"
        $script:CygwinVersion = "3.6.9"
    }

    Context "Get-CygwinRootDirIfAvailable" {
        It "returns the registry root before filesystem fallbacks" {
            Set-TestPathMock -ExistingPaths @(
                "HKLM:\SOFTWARE\Cygwin\setup",
                (Join-Path $script:PreferredRoot "bin\bash.exe")
            )
            Mock Get-ItemProperty { return [pscustomobject]@{ rootdir = "D:\cygwin" } }

            Get-CygwinRootDirIfAvailable | Should -Be "D:\cygwin"
        }

        It "falls back to an existing Cygwin bash path when registry roots are absent" {
            Set-TestPathMock -ExistingPaths @("C:\cygwin64\bin\bash.exe")
            Mock Get-ItemProperty { throw "Registry should not be queried when keys are absent." }

            Get-CygwinRootDirIfAvailable | Should -Be "C:\cygwin64"
        }

        It "returns null when no registry or fallback Cygwin root exists" {
            Set-TestPathMock
            Mock Get-ItemProperty { throw "Registry should not be queried when keys are absent." }

            Get-CygwinRootDirIfAvailable | Should -BeNullOrEmpty
        }
    }

    Context "Test-SameWindowsPath" {
        It "matches paths case-insensitively and ignores trailing separators" {
            Test-SameWindowsPath -Left "C:\TOOLS\cygwin\" -Right "c:/tools/cygwin" | Should -BeTrue
        }

        It "rejects empty and distinct paths" {
            Test-SameWindowsPath -Left "" -Right "C:\tools\cygwin" | Should -BeFalse
            Test-SameWindowsPath -Left "C:\tools\cygwin" -Right "D:\cygwin" | Should -BeFalse
        }
    }

    Context "Assert-CygwinInstallRoot" {
        It "returns the resolved root when it matches the selected root and required files exist" {
            Mock Get-CygwinRootDir { return $script:PreferredRoot }
            Set-TestPathMock -ExistingPaths @(
                (Join-Path $script:PreferredRoot "bin\bash.exe"),
                (Join-Path $script:PreferredRoot "etc\setup\installed.db")
            )

            Assert-CygwinInstallRoot -ExpectedRoot "C:\TOOLS\cygwin\" | Should -Be $script:PreferredRoot
        }

        It "throws when the resolved root does not match the selected root" {
            Mock Get-CygwinRootDir { return "C:\cygwin64" }
            Set-TestPathMock

            { Assert-CygwinInstallRoot -ExpectedRoot $script:PreferredRoot } |
                Should -Throw "*resolved to 'C:\cygwin64'*selected install root was 'C:\tools\cygwin'*"
        }

        It "throws when the resolved root is missing installed.db" {
            Mock Get-CygwinRootDir { return $script:PreferredRoot }
            Set-TestPathMock -ExistingPaths @(
                (Join-Path $script:PreferredRoot "bin\bash.exe")
            )

            { Assert-CygwinInstallRoot -ExpectedRoot $script:PreferredRoot } |
                Should -Throw "*missing required file 'C:\tools\cygwin\etc\setup\installed.db'*"
        }
    }

    Context "Get-CygwinAlternateInstallRoot" {
        It "returns the versioned preferred sibling when it is available" {
            Set-TestPathMock

            Get-CygwinAlternateInstallRoot -PreferredRoot $script:PreferredRoot -Version $script:CygwinVersion |
                Should -Be "C:\tools\cygwin-3.6.9"
        }

        It "increments the versioned sibling when earlier candidates already exist" {
            Set-TestPathMock -ExistingPaths @(
                "C:\tools\cygwin-3.6.9",
                "C:\tools\cygwin-3.6.9-1"
            )

            Get-CygwinAlternateInstallRoot -PreferredRoot $script:PreferredRoot -Version $script:CygwinVersion |
                Should -Be "C:\tools\cygwin-3.6.9-2"
        }
    }

    Context "Get-CleanCygwinInstallRoot" {
        It "uses the preferred root when Cygwin is missing" {
            Set-TestPathMock
            Mock Stop-CygwinProcesses { throw "No Cygwin process should be stopped." }
            Mock Remove-CygwinInstallRoot { throw "No Cygwin root should be removed." }

            Get-CleanCygwinInstallRoot -PreferredRoot $script:PreferredRoot -ExistingRoot $null -Version $script:CygwinVersion |
                Should -Be $script:PreferredRoot
        }

        It "cleans the mismatched existing root before the preferred root" {
            $script:CleanCalls = @()
            Set-TestPathMock -ExistingPaths @("D:\cygwin", $script:PreferredRoot)
            Mock Stop-CygwinProcesses { $script:CleanCalls += ("stop:{0}" -f $RootDir) }
            Mock Remove-CygwinInstallRoot {
                $script:CleanCalls += ("remove:{0}" -f $RootDir)
                return $true
            }

            Get-CleanCygwinInstallRoot -PreferredRoot $script:PreferredRoot -ExistingRoot "D:\cygwin" -Version $script:CygwinVersion |
                Should -Be $script:PreferredRoot
            ($script:CleanCalls -join "|") |
                Should -Be "stop:D:\cygwin|remove:D:\cygwin|stop:C:\tools\cygwin|remove:C:\tools\cygwin"
        }

        It "uses an alternate root when the preferred root is locked" {
            Set-TestPathMock -ExistingPaths @($script:PreferredRoot)
            Mock Stop-CygwinProcesses {}
            Mock Remove-CygwinInstallRoot { return $false }
            Mock Get-CygwinAlternateInstallRoot { return "C:\tools\cygwin-3.6.9" }

            Get-CleanCygwinInstallRoot -PreferredRoot $script:PreferredRoot -ExistingRoot $null -Version $script:CygwinVersion |
                Should -Be "C:\tools\cygwin-3.6.9"
        }
    }

    Context "Stop-CygwinProcesses" {
        It "stops only processes whose executables are under the Cygwin root" {
            $script:StoppedProcessIds = @()
            Set-TestPathMock -ExistingPaths @($script:PreferredRoot)
            Mock Assert-CygwinRootSafeForAutomation {}
            Mock Get-CimInstance {
                return @(
                    [pscustomobject]@{ ProcessId = 101; Name = "bash"; ExecutablePath = "C:\tools\cygwin\bin\bash.exe" },
                    [pscustomobject]@{ ProcessId = 102; Name = "git"; ExecutablePath = "C:\Program Files\Git\bin\git.exe" },
                    [pscustomobject]@{ ProcessId = $PID; Name = "pwsh"; ExecutablePath = "C:\tools\cygwin\bin\pwsh.exe" }
                )
            }
            Mock Stop-Process { $script:StoppedProcessIds += $Id }

            Stop-CygwinProcesses -RootDir $script:PreferredRoot

            ($script:StoppedProcessIds -join ",") | Should -Be "101"
        }
    }

    Context "Ensure-CygwinChocolateyPackage" {
        It "does nothing when the pinned Cygwin version is already installed" {
            Mock Get-ChocolateyInstalledVersion { return $script:CygwinVersion }
            Mock Get-CygwinRootDir { return $script:PreferredRoot }
            Mock Assert-CygwinInstallRoot { return $ExpectedRoot }
            Mock Get-CleanCygwinInstallRoot { throw "Clean install root should not be resolved for an already pinned Cygwin package." }
            Mock Ensure-ChocolateyPackage { throw "Cygwin should not be installed when the pinned version is already present." }

            Ensure-CygwinChocolateyPackage -Version $script:CygwinVersion -InstallRoot $script:PreferredRoot |
                Should -Be $script:PreferredRoot
        }

        It "installs missing Cygwin at the preferred root" {
            $script:InstalledCygwinArgs = @()
            Mock Get-ChocolateyInstalledVersion { return $null }
            Mock Get-CygwinRootDirIfAvailable { return $null }
            Mock Get-CleanCygwinInstallRoot { return $PreferredRoot }
            Mock Clear-CygwinRegistryRootDirs {}
            Mock Ensure-ChocolateyPackage { $script:InstalledCygwinArgs = @($ExtraArgs) }
            Mock Assert-CygwinInstallRoot { return $ExpectedRoot }

            Ensure-CygwinChocolateyPackage -Version $script:CygwinVersion -InstallRoot $script:PreferredRoot |
                Should -Be $script:PreferredRoot

            $script:InstalledCygwinArgs[0] | Should -Be "--params"
            $script:InstalledCygwinArgs[1] | Should -Be '"/InstallDir:C:\tools\cygwin /NoStartMenu"'
        }

        It "uninstalls cyg-get before a mismatched Cygwin package reinstall" {
            $script:Order = @()
            Mock Get-ChocolateyInstalledVersion {
                switch ($PackageName) {
                    "cygwin" { return "3.6.8" }
                    "cyg-get" { return "1.2.2" }
                    default { return $null }
                }
            }
            Mock Get-CygwinRootDirIfAvailable { return "C:\cygwin64" }
            Mock Stop-CygwinProcesses { $script:Order += ("stop:{0}" -f $RootDir) }
            Mock Invoke-ChocolateyPackageCommand {
                $script:Order += ("choco:{0}:{1}:{2}" -f $Command, $PackageName, $Version)
            }
            Mock Get-CleanCygwinInstallRoot {
                $script:Order += ("clean:{0}" -f $ExistingRoot)
                return $PreferredRoot
            }
            Mock Clear-CygwinRegistryRootDirs { $script:Order += "clear" }
            Mock Ensure-ChocolateyPackage {
                $script:Order += ("ensure:{0}:{1}" -f $PackageName, $Version)
            }
            Mock Assert-CygwinInstallRoot {
                $script:Order += ("assert:{0}" -f $ExpectedRoot)
                return $ExpectedRoot
            }

            $null = Ensure-CygwinChocolateyPackage -Version $script:CygwinVersion -InstallRoot $script:PreferredRoot

            ($script:Order -join "|") |
                Should -Be "stop:C:\cygwin64|choco:uninstall:cyg-get:1.2.2|choco:uninstall:cygwin:3.6.8|clean:C:\cygwin64|clear|ensure:cygwin:3.6.9|assert:C:\tools\cygwin"
        }

        It "passes an alternate root to Chocolatey when cleanup cannot remove the preferred root" {
            $script:InstalledCygwinArgs = @()
            Mock Get-ChocolateyInstalledVersion { return $null }
            Mock Get-CygwinRootDirIfAvailable { return $null }
            Mock Get-CleanCygwinInstallRoot { return "C:\tools\cygwin-3.6.9" }
            Mock Clear-CygwinRegistryRootDirs {}
            Mock Ensure-ChocolateyPackage { $script:InstalledCygwinArgs = @($ExtraArgs) }
            Mock Assert-CygwinInstallRoot { return $ExpectedRoot }

            Ensure-CygwinChocolateyPackage -Version $script:CygwinVersion -InstallRoot $script:PreferredRoot |
                Should -Be "C:\tools\cygwin-3.6.9"

            $script:InstalledCygwinArgs[1] | Should -Be '"/InstallDir:C:\tools\cygwin-3.6.9 /NoStartMenu"'
        }

        It "stops before uninstalling when the registry root is corrupt and unsafe" {
            $script:Order = @()
            Mock Get-ChocolateyInstalledVersion {
                if ($PackageName -eq "cygwin") {
                    return "3.6.8"
                }
                return $null
            }
            Mock Get-CygwinRootDirIfAvailable { return "C:\Users" }
            Mock Stop-CygwinProcesses { throw "Refusing to automatically manage Cygwin root '$RootDir'." }
            Mock Invoke-ChocolateyPackageCommand { $script:Order += "uninstall" }
            Mock Clear-CygwinRegistryRootDirs { $script:Order += "clear" }
            Mock Ensure-ChocolateyPackage { $script:Order += "install" }

            { Ensure-CygwinChocolateyPackage -Version $script:CygwinVersion -InstallRoot $script:PreferredRoot } |
                Should -Throw "*Refusing to automatically manage Cygwin root 'C:\Users'*"
            $script:Order.Count | Should -Be 0
        }
    }

    Context "Ensure-CygwinPackage" {
        It "does not run setup when the package is already installed in the selected root" {
            Set-TestPathMock -ExistingPaths @(
                (Join-Path $script:PreferredRoot "etc\setup\installed.db")
            )
            Mock Select-String { return $true }
            Mock Start-Process { throw "Cygwin setup should not be started for an installed package." }

            $null = Ensure-CygwinPackage -PackageName "openssh" -CygwinRoot $script:PreferredRoot
        }

        It "runs the setup executable from the selected root when a package is missing" {
            $script:StartedCygwinSetup = $null
            Set-TestPathMock -ExistingPaths @(
                (Join-Path $script:PreferredRoot "etc\setup\installed.db"),
                (Join-Path $script:PreferredRoot "cygwinsetup.exe")
            )
            Mock Select-String { return $false }
            Mock Start-Process {
                $script:StartedCygwinSetup = [pscustomobject]@{
                    FilePath = $FilePath
                    ArgumentList = @($ArgumentList)
                }
                return [pscustomobject]@{ ExitCode = 0 }
            }

            $null = Ensure-CygwinPackage -PackageName "openssh" -CygwinRoot $script:PreferredRoot

            $script:StartedCygwinSetup.FilePath | Should -Be (Join-Path $script:PreferredRoot "cygwinsetup.exe")
            ($script:StartedCygwinSetup.ArgumentList -join "|") |
                Should -Be '--quiet-mode|--root "C:\tools\cygwin"|--local-package-dir "C:\tools\cygwin\packages"|--no-desktop|--no-startmenu|--packages openssh'
        }
    }

    Context "Invoke-DevAlchemySelfSetup" {
        It "uses the default install path without installing VirtualBox" {
            Initialize-SelfSetupMainMocks

            $null = Invoke-DevAlchemySelfSetup

            @($script:EnsuredPackages | Where-Object { $_.PackageName -eq "virtualbox" }).Count | Should -Be 0
            $script:EnsuredPaths | Should -Not -Contain "C:\Program Files\Oracle\VirtualBox"
            $script:EnsuredPaths | Should -Contain "C:\Program Files\Git\bin"
            $script:CygwinEnsureCalls[0].InstallRoot | Should -Be $script:PreferredRoot
            $script:CygwinPackageCalls[0].CygwinRoot | Should -Be $script:PreferredRoot
            $script:CygwinPipPackageCalls[0].CygwinRoot | Should -Be $script:PreferredRoot
            $script:ShownToolVersionsRoot | Should -Be $script:PreferredRoot
        }

        It "installs VirtualBox and adds its path when -VirtualBox is supplied" {
            Initialize-SelfSetupMainMocks

            $null = Invoke-DevAlchemySelfSetup -VirtualBox

            @($script:EnsuredPackages | Where-Object { $_.PackageName -eq "virtualbox" }).Count | Should -Be 1
            $script:EnsuredPaths | Should -Contain "C:\Program Files\Oracle\VirtualBox"
            $script:CygwinEnsureCalls[0].InstallRoot | Should -Be $script:PreferredRoot
        }
    }
}

Describe "sailwright self setup Git helpers" {
    Context "Ensure-GitInstalled" -Skip:(-not $IsWindows) {
        BeforeEach {
            Initialize-SelfSetupMainMocks
        }

        It "keeps a newer unmanaged Git instead of downgrading it" {
            Mock Get-GitForWindowsVersion { return [version]"2.56.0" }

            Ensure-GitInstalled -Version "2.54.0"

            @($script:EnsuredPackages).Count | Should -Be 0
        }

        It "keeps an unmanaged Git that matches the pinned version" {
            Mock Get-GitForWindowsVersion { return [version]"2.54.0" }

            Ensure-GitInstalled -Version "2.54.0"

            @($script:EnsuredPackages).Count | Should -Be 0
        }

        It "keeps a newer Windows build of the pinned release" {
            Mock Get-GitForWindowsVersion { return [version]"2.54.0.3" }

            Ensure-GitInstalled -Version "2.54.0.2"

            @($script:EnsuredPackages).Count | Should -Be 0
        }

        It "installs the pinned Git when the unmanaged Git is an older build of the same release" {
            Mock Get-GitForWindowsVersion { return [version]"2.54.0" }

            Ensure-GitInstalled -Version "2.54.0.2"

            $script:EnsuredPackages[0].Version | Should -Be "2.54.0.2"
        }

        It "installs the pinned Git when no Git is available" {
            Ensure-GitInstalled -Version "2.54.0"

            $script:EnsuredPackages[0].PackageName | Should -Be "git"
            $script:EnsuredPackages[0].Version | Should -Be "2.54.0"
        }

        It "installs the pinned Git when the unmanaged Git is older" {
            Mock Get-GitForWindowsVersion { return [version]"2.40.1" }

            Ensure-GitInstalled -Version "2.54.0"

            $script:EnsuredPackages[0].PackageName | Should -Be "git"
        }

        It "keeps a newer Chocolatey-managed Git instead of downgrading it" {
            Mock Get-ChocolateyInstalledVersion { return "2.56.0" } -ParameterFilter { $PackageName -eq "git" }
            Mock Get-GitForWindowsVersion { return [version]"2.56.0" }

            Ensure-GitInstalled -Version "2.54.0"

            @($script:EnsuredPackages).Count | Should -Be 0
        }

        It "installs the exact pinned version when the pin is not numeric" {
            Mock Get-GitForWindowsVersion { throw "The installed version should not be checked for a non-numeric pin." }
            Mock Write-Warning {}

            Ensure-GitInstalled -Version "2.55.0-rc0"

            $script:EnsuredPackages[0].Version | Should -Be "2.55.0-rc0"
        }
    }

    Context "Get-GitForWindowsRoot" -Skip:(-not $IsWindows) {
        It "uses the install path recorded by the Git for Windows installer" {
            Set-TestPathMock -ExistingPaths @("HKLM:\SOFTWARE\GitForWindows")
            Mock Get-ItemProperty { return [pscustomobject]@{ InstallPath = "C:\Tools\Git" } }

            Get-GitForWindowsRoot | Should -Be "C:\Tools\Git"
        }

        It "falls back to Program Files when the installer key is absent" {
            Set-TestPathMock
            Mock Get-ItemProperty { throw "Registry should not be queried when the key is absent." }

            Get-GitForWindowsRoot | Should -Be (Join-Path $env:ProgramFiles "Git")
        }
    }

    Context "Get-GitForWindowsVersion" -Skip:(-not $IsWindows) {
        BeforeEach {
            Mock Get-GitForWindowsRoot { return "C:\Tools\Git" }
        }

        It "returns null when Git for Windows is not installed, even if another git is on PATH" {
            Mock Get-Command { throw "PATH lookup should not be used to find Git for Windows." }
            Mock Invoke-GitVersionCommand { throw "git should not run when Git for Windows is absent." }
            Set-TestPathMock

            Get-GitForWindowsVersion | Should -BeNullOrEmpty
        }

        It "reads the version of the installed Git for Windows" {
            Set-TestPathMock -ExistingPaths @("C:\Tools\Git\cmd\git.exe")
            Mock Invoke-GitVersionCommand { return "git version 2.56.0.windows.2" } -ParameterFilter { $GitExePath -eq "C:\Tools\Git\cmd\git.exe" }

            Get-GitForWindowsVersion | Should -Be ([version]"2.56.0.2")
        }

        It "returns null when git --version fails" {
            Set-TestPathMock -ExistingPaths @("C:\Tools\Git\cmd\git.exe")
            Mock Invoke-GitVersionCommand { return $null }

            Get-GitForWindowsVersion | Should -BeNullOrEmpty
        }

        It "warns and returns null when the version output is not a Git for Windows release" {
            Set-TestPathMock -ExistingPaths @("C:\Tools\Git\cmd\git.exe")
            Mock Invoke-GitVersionCommand { return "git version 2.56.0.vfs.0.0" }
            Mock Write-Warning {}

            Get-GitForWindowsVersion | Should -BeNullOrEmpty
            Should -Invoke Write-Warning -Times 1 -Exactly
        }
    }

    Context "ConvertTo-GitForWindowsVersion" {
        It "maps the first Windows build of a release to the plain release version" {
            ConvertTo-GitForWindowsVersion -VersionOutput "git version 2.54.0.windows.1" | Should -Be ([version]"2.54.0")
        }

        It "maps later Windows builds to the Chocolatey revision" {
            ConvertTo-GitForWindowsVersion -VersionOutput "git version 2.54.0.windows.3" | Should -Be ([version]"2.54.0.3")
        }

        It "parses versions without a Windows build suffix" {
            ConvertTo-GitForWindowsVersion -VersionOutput "git version 2.54.0" | Should -Be ([version]"2.54.0")
        }

        It "ignores surrounding whitespace" {
            ConvertTo-GitForWindowsVersion -VersionOutput "git version 2.54.0.windows.2`r`n" | Should -Be ([version]"2.54.0.2")
        }

        It "rejects release candidates" {
            ConvertTo-GitForWindowsVersion -VersionOutput "git version 2.54.0.rc1.windows.1" | Should -BeNullOrEmpty
        }

        It "rejects non-Git for Windows builds such as Microsoft's VFS fork" {
            ConvertTo-GitForWindowsVersion -VersionOutput "git version 2.54.0.vfs.0.0" | Should -BeNullOrEmpty
        }

        It "returns null for unrecognized output" {
            ConvertTo-GitForWindowsVersion -VersionOutput "not git" | Should -BeNullOrEmpty
        }
    }
}

param(
    [switch]$WithGo,
    [switch]$VirtualBox,
    [switch]$HyperV,
    [switch]$Vagrant
)

$ErrorActionPreference = "Stop"

# renovate: datasource=nuget depName=golang versioning=nuget registryUrl=https://community.chocolatey.org/api/v2/
$golangVersion = "1.26.3"
# renovate: datasource=nuget depName=git versioning=nuget registryUrl=https://community.chocolatey.org/api/v2/
$gitVersion = "2.54.0"
# renovate: datasource=nuget depName=make versioning=nuget registryUrl=https://community.chocolatey.org/api/v2/
$makeVersion = "4.4.1"
# renovate: datasource=nuget depName=packer versioning=nuget registryUrl=https://community.chocolatey.org/api/v2/
$packerVersion = "1.15.3"
# renovate: datasource=nuget depName=azure-cli versioning=nuget registryUrl=https://community.chocolatey.org/api/v2/
$azureCliVersion = "2.86.0"
# renovate: datasource=nuget depName=python313 versioning=nuget registryUrl=https://community.chocolatey.org/api/v2/
$nativePythonVersion = "3.13.13"
# renovate: datasource=nuget depName=cygwin versioning=nuget registryUrl=https://community.chocolatey.org/api/v2/
$cygwinVersion = "3.6.9"
# renovate: datasource=nuget depName=cyg-get versioning=nuget registryUrl=https://community.chocolatey.org/api/v2/
$cygGetVersion = "1.2.2"
# renovate: datasource=nuget depName=virtualbox versioning=nuget registryUrl=https://community.chocolatey.org/api/v2/
$virtualBoxVersion = "7.2.8"
# renovate: datasource=nuget depName=vagrant versioning=nuget registryUrl=https://community.chocolatey.org/api/v2/
$vagrantVersion = "2.4.9"
# renovate: datasource=pypi depName=ansible versioning=pep440
$ansibleVersion = "14.5.0"
# renovate: datasource=pypi depName=pywinrm versioning=pep440
$pywinrmVersion = "0.5.0"

$gitForWindowsRegistryKey = "HKLM:\SOFTWARE\GitForWindows"
# Only machine-wide (admin-writable) uninstall entries are trusted to describe
# an existing Azure CLI MSI installation.
$azureCliUninstallRegistryKeys = @(
    "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*",
    "HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*"
)
# PEP 514 registration written by per-machine python.org installs. Only the
# machine-wide (admin-writable) key is trusted, as for Git for Windows.
$pythonCoreRegistryKey = "HKLM:\SOFTWARE\Python\PythonCore"
$cygwinInstallRoot = "C:\tools\cygwin"
$cygwinPackages = @(
    "python312",
    "python312-pip",
    "python312-cryptography",
    "openssh",
    "git",
    "make",
    "gcc-core",
    "gcc-g++",
    "libffi-devel",
    "libssl-devel",
    "sshpass"
)

function Test-IsAdministrator {
    return ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
        [Security.Principal.WindowsBuiltInRole] "Administrator"
    )
}

function Invoke-SelfElevated {
    param(
        [switch]$WithGo,
        [switch]$VirtualBox,
        [switch]$HyperV,
        [switch]$Vagrant
    )

    $scriptPath = $PSCommandPath
    if (-not $scriptPath) {
        $scriptPath = $MyInvocation.MyCommand.Path
    }
    if (-not $scriptPath) {
        throw "Unable to determine the installer script path for elevation."
    }

    $argumentList = @(
        "-NoLogo",
        "-NoProfile",
        "-ExecutionPolicy", "Bypass",
        "-File", $scriptPath
    )
    if ($WithGo) {
        $argumentList += "-WithGo"
    }
    if ($VirtualBox) {
        $argumentList += "-VirtualBox"
    }
    if ($HyperV) {
        $argumentList += "-HyperV"
    }
    if ($Vagrant) {
        $argumentList += "-Vagrant"
    }

    Write-Output "Requesting administrator privileges through UAC..."

    try {
        $process = Start-Process -FilePath "powershell.exe" -ArgumentList $argumentList -Verb RunAs -WorkingDirectory (Get-Location).Path -Wait -PassThru
    } catch [System.ComponentModel.Win32Exception] {
        if ($_.Exception.NativeErrorCode -eq 1223) {
            throw "Administrator privileges are required to continue. The UAC prompt was cancelled."
        }
        throw
    }

    exit $process.ExitCode
}

function Refresh-ProcessPath {
    $machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $env:Path = @($machinePath, $userPath) -join ";"
}

function Ensure-PathContains {
    param(
        [Parameter(Mandatory = $true)]
        [string]$PathEntry
    )

    $machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
    $currentEntries = $machinePath -split ";" | Where-Object { $_ -ne "" }
    if ($currentEntries -contains $PathEntry) {
        return
    }

    [Environment]::SetEnvironmentVariable("Path", ($currentEntries + $PathEntry) -join ";", "Machine")
    Refresh-ProcessPath
}

function Ensure-PathEntriesFirst {
    param(
        [Parameter(Mandatory = $true)]
        [string[]]$PathEntries
    )

    $machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
    $currentEntries = @($machinePath -split ";" | Where-Object { $_ -ne "" })
    $remainingEntries = @($currentEntries | Where-Object { $PathEntries -notcontains $_ })
    $desiredEntries = @($PathEntries) + $remainingEntries

    if (($desiredEntries -join ";") -ne ($currentEntries -join ";")) {
        [Environment]::SetEnvironmentVariable("Path", ($desiredEntries -join ";"), "Machine")
    }
    Refresh-ProcessPath
}

function Add-GitHubPathIfAvailable {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Directory
    )

    if (-not $env:GITHUB_PATH) {
        return
    }

    if ((Test-Path $env:GITHUB_PATH) -and (Select-String -Path $env:GITHUB_PATH -SimpleMatch -Pattern $Directory -Quiet)) {
        return
    }

    $Directory | Out-File -FilePath $env:GITHUB_PATH -Encoding utf8 -Append
}

function Export-CommandDirectoryToGitHubPath {
    param(
        [Parameter(Mandatory = $true)]
        [string]$CommandName
    )

    $command = Get-Command $CommandName -ErrorAction SilentlyContinue
    if (-not $command -or -not $command.Source) {
        return
    }

    Add-GitHubPathIfAvailable -Directory (Split-Path -Parent $command.Source)
}

function Ensure-ChocolateyInstalled {
    if (Get-Command choco -ErrorAction SilentlyContinue) {
        Write-Output "Chocolatey is already installed."
        return
    }

    Write-Output "Chocolatey not found. Installing Chocolatey..."
    Set-ExecutionPolicy Bypass -Scope Process -Force
    [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.ServicePointManager]::SecurityProtocol -bor 3072
    Invoke-Expression ((New-Object System.Net.WebClient).DownloadString("https://community.chocolatey.org/install.ps1"))
    Refresh-ProcessPath
}

function Get-ChocolateyInstalledVersion {
    param(
        [Parameter(Mandatory = $true)]
        [string]$PackageName
    )

    $output = & choco list --local-only --exact $PackageName --limit-output 2>$null
    if ($LASTEXITCODE -ne 0) {
        return $null
    }

    foreach ($line in $output) {
        if ($line -match "^$([regex]::Escape($PackageName))\|(.+)$") {
            return $Matches[1].Trim()
        }
    }

    return $null
}

function Invoke-ChocolateyPackageCommand {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Command,
        [Parameter(Mandatory = $true)]
        [string]$PackageName,
        [Parameter(Mandatory = $true)]
        [string]$Version,
        [Parameter(Mandatory = $true)]
        [string[]]$Arguments
    )

    $maxAttempts = 3
    for ($attempt = 1; $attempt -le $maxAttempts; $attempt++) {
        $chocoOutput = & choco @Arguments 2>&1
        $exitCode = $LASTEXITCODE

        foreach ($line in $chocoOutput) {
            Write-Host $line
        }

        if ($exitCode -in @(0, 1641, 3010)) {
            Refresh-ProcessPath
            return
        }

        $shouldRetry = $attempt -lt $maxAttempts -and (Test-ShouldRetryChocolateyCommand -OutputLines $chocoOutput)
        if (-not $shouldRetry) {
            throw "Chocolatey failed to $Command $PackageName $Version with exit code $exitCode."
        }

        $sleepSeconds = 5 * $attempt
        Write-Warning "Chocolatey failed to $Command $PackageName $Version with exit code $exitCode. Retrying in $sleepSeconds seconds (attempt $($attempt + 1) of $maxAttempts)..."
        Start-Sleep -Seconds $sleepSeconds
    }
}

function Ensure-ChocolateyPackage {
    param(
        [Parameter(Mandatory = $true)]
        [string]$PackageName,
        [Parameter(Mandatory = $true)]
        [string]$Version,
        [string[]]$ExtraArgs = @()
    )

    $installedVersion = Get-ChocolateyInstalledVersion -PackageName $PackageName
    if ($installedVersion -eq $Version) {
        Write-Host "$PackageName $Version is already installed."
        return
    }

    if ($installedVersion) {
        Write-Host "$PackageName $installedVersion detected. Enforcing pinned version $Version..."
        $command = "upgrade"
    } else {
        Write-Host "$PackageName not found. Installing pinned version $Version..."
        $command = "install"
    }

    $packageArguments = @($command, $PackageName, "-y", "--no-progress", "--version", $Version)
    if ($command -eq "upgrade") {
        $packageArguments += "--allow-downgrade"
    }
    if ($ExtraArgs.Count -gt 0) {
        $packageArguments += $ExtraArgs
    }

    Invoke-ChocolateyPackageCommand -Command $command -PackageName $PackageName -Version $Version -Arguments $packageArguments
}

function ConvertTo-GitForWindowsVersion {
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyString()]
        [string]$VersionOutput
    )

    # Chocolatey publishes "git version 2.54.0.windows.2" as 2.54.0.2 and the
    # first build of a release (".windows.1") as plain 2.54.0. Anything else
    # (release candidates, forks such as "2.54.0.vfs.0.0") is not a Git for
    # Windows release, so it never counts as satisfying the pinned version.
    $versionMatch = [regex]::Match($VersionOutput.Trim(), "^git version (?<version>\d+\.\d+\.\d+)(?:\.windows\.(?<build>\d+))?$")
    if (-not $versionMatch.Success) {
        return $null
    }

    $version = $versionMatch.Groups["version"].Value
    $build = $versionMatch.Groups["build"]
    if ($build.Success -and [int]$build.Value -gt 1) {
        $version = "$version.$($build.Value)"
    }

    return [version]$version
}

function Get-GitForWindowsRoot {
    # Only the machine-wide HKLM key is trusted: it is admin-writable, whereas a
    # non-admin user could point HKCU at a git.exe the elevated setup would run.
    if (Test-Path $gitForWindowsRegistryKey) {
        $installPath = (Get-ItemProperty -Path $gitForWindowsRegistryKey -Name InstallPath -ErrorAction SilentlyContinue).InstallPath
        if ($installPath) {
            return $installPath
        }
    }

    $programFiles = if ($env:ProgramFiles) { $env:ProgramFiles } else { "C:\Program Files" }
    return Join-Path $programFiles "Git"
}

function Invoke-GitVersionCommand {
    param(
        [Parameter(Mandatory = $true)]
        [string]$GitExePath
    )

    $output = & $GitExePath --version 2>$null
    if ($LASTEXITCODE -ne 0) {
        return $null
    }

    return "$output"
}

function Get-GitForWindowsVersion {
    # Only an existing Git for Windows installation blocks a downgrade; other Git
    # builds on PATH (Scoop, MSYS2, Cygwin, ...) do not provide it, so ignore them.
    $gitExePath = Join-Path (Get-GitForWindowsRoot) "cmd\git.exe"
    if (-not (Test-Path -LiteralPath $gitExePath -PathType Leaf)) {
        return $null
    }

    $output = Invoke-GitVersionCommand -GitExePath $gitExePath
    if (-not $output) {
        return $null
    }

    $version = ConvertTo-GitForWindowsVersion -VersionOutput $output
    if (-not $version) {
        Write-Warning "Could not parse a Git for Windows release version from '$output' ($gitExePath)."
    }

    return $version
}

function Ensure-GitInstalled {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Version
    )

    $pinnedVersion = $null
    if (-not [version]::TryParse($Version, [ref]$pinnedVersion)) {
        Write-Warning "Pinned git version $Version is not numeric; skipping the installed version check."
        Ensure-ChocolateyPackage -PackageName "git" -Version $Version
        return
    }

    # The Git for Windows installer refuses to downgrade an existing newer
    # installation (e.g. the one preinstalled on GitHub-hosted runners), and a
    # Chocolatey downgrade runs that same installer. Treat the pin as a minimum,
    # whether or not Chocolatey manages the existing installation.
    $existingVersion = Get-GitForWindowsVersion
    if ($existingVersion -and $existingVersion -ge $pinnedVersion) {
        Write-Host "git $existingVersion is already installed and satisfies pinned version $Version."
        return
    }

    Ensure-ChocolateyPackage -PackageName "git" -Version $Version
}

function Ensure-GoInstallRootClean {
    param(
        [Parameter(Mandatory = $true)]
        [string]$DesiredVersion
    )

    $goInstallRoot = "C:\Program Files\Go"
    $goExePath = Join-Path $goInstallRoot "bin\go.exe"
    $staleSwissMapPath = Join-Path $goInstallRoot "src\internal\abi\map_swiss.go"
    $installedVersion = Get-ChocolateyInstalledVersion -PackageName "golang"

    $shouldRemove = $false

    if ($installedVersion -and $installedVersion -ne $DesiredVersion) {
        Write-Output "Go $installedVersion detected. Removing the existing GOROOT before installing $DesiredVersion to avoid stale standard-library files."
        $shouldRemove = $true
    } elseif (Test-Path $staleSwissMapPath) {
        Write-Output "A stale Go source file was detected at $staleSwissMapPath. Removing the existing GOROOT before reinstalling Go."
        $shouldRemove = $true
    } elseif ((-not $installedVersion) -and (Test-Path $goExePath)) {
        Write-Output "An unmanaged Go installation was detected at $goInstallRoot. Removing it so the pinned Chocolatey install can start from a clean state."
        $shouldRemove = $true
    }

    if (-not $shouldRemove) {
        return
    }

    if (Test-Path $goInstallRoot) {
        Remove-Item -Path $goInstallRoot -Recurse -Force
    }

    Refresh-ProcessPath
}

function Assert-GoToolchainLayout {
    $goExe = Get-Command go -ErrorAction SilentlyContinue
    if (-not $goExe) {
        throw "Go was not found on PATH after installation."
    }

    $goRoot = (& go env GOROOT).Trim()
    if (-not $goRoot) {
        throw "Unable to determine GOROOT after installing Go."
    }

    $stalePaths = @(
        (Join-Path $goRoot "src\internal\abi\map_swiss.go")
    ) | Where-Object { Test-Path $_ }

    if ($stalePaths.Count -gt 0) {
        $staleList = $stalePaths -join ", "
        throw "Detected stale Go standard-library source files after installation: $staleList. Remove $goRoot and reinstall Go."
    }
}

function Assert-NativePythonAvailable {
    $pythonExe = Get-Command python -ErrorAction SilentlyContinue
    if (-not $pythonExe) {
        throw "Native Windows Python was not found on PATH after installation."
    }
}

function Get-NativePythonChocolateyPackageName {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Version
    )

    $versionMatch = [regex]::Match($Version, "^(?<major>\d+)\.(?<minor>\d+)")
    if (-not $versionMatch.Success) {
        throw "Unable to derive the Chocolatey Python package name from version '$Version'."
    }

    return "python{0}{1}" -f $versionMatch.Groups["major"].Value, $versionMatch.Groups["minor"].Value
}

function ConvertTo-NativePythonVersion {
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyString()]
        [string]$VersionOutput
    )

    # Pre-releases ("Python 3.14.0rc1") never satisfy a pinned release version.
    $versionMatch = [regex]::Match($VersionOutput.Trim(), "^Python (?<version>\d+\.\d+\.\d+)$")
    if (-not $versionMatch.Success) {
        return $null
    }

    return [version]$versionMatch.Groups["version"].Value
}

function Invoke-NativePythonVersionCommand {
    param(
        [Parameter(Mandatory = $true)]
        [string]$PythonExePath
    )

    $output = & $PythonExePath --version 2>$null
    if ($LASTEXITCODE -ne 0) {
        return $null
    }

    return "$output"
}

function Get-NativePythonInstallation {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Version
    )

    $versionMatch = [regex]::Match($Version, "^(?<major>\d+)\.(?<minor>\d+)")
    if (-not $versionMatch.Success) {
        return $null
    }

    $installPathKey = Join-Path $pythonCoreRegistryKey "$($versionMatch.Groups["major"].Value).$($versionMatch.Groups["minor"].Value)\InstallPath"
    if (-not (Test-Path $installPathKey)) {
        return $null
    }

    $installPath = (Get-ItemProperty -Path $installPathKey -ErrorAction SilentlyContinue).'(default)'
    if (-not $installPath) {
        return $null
    }
    $installPath = $installPath.TrimEnd("\")

    $pythonExePath = Join-Path $installPath "python.exe"
    if (-not (Test-Path -LiteralPath $pythonExePath -PathType Leaf)) {
        return $null
    }

    $output = Invoke-NativePythonVersionCommand -PythonExePath $pythonExePath
    if (-not $output) {
        return $null
    }

    $installedVersion = ConvertTo-NativePythonVersion -VersionOutput $output
    if (-not $installedVersion) {
        Write-Warning "Could not parse a Python release version from '$output' ($pythonExePath)."
        return $null
    }

    return [pscustomobject]@{
        Version = $installedVersion
        InstallPath = $installPath
    }
}

function Ensure-NativePythonInstalled {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Version
    )

    $packageName = Get-NativePythonChocolateyPackageName -Version $Version

    $pinnedVersion = $null
    if (-not [version]::TryParse($Version, [ref]$pinnedVersion)) {
        Write-Warning "Pinned Python version $Version is not numeric; skipping the installed version check."
        Ensure-ChocolateyPackage -PackageName $packageName -Version $Version
        return
    }

    # The python.org installer refuses to downgrade an existing newer patch
    # release of the same minor version (e.g. the tool cache Python preinstalled
    # on GitHub-hosted runners) and fails with exit code 1603. Treat the pin as a
    # minimum and put the kept installation first on PATH, as the Chocolatey
    # package does with PrependPath=1.
    $existing = Get-NativePythonInstallation -Version $Version
    if ($existing -and $existing.Version -ge $pinnedVersion) {
        Write-Host "Python $($existing.Version) is already installed at $($existing.InstallPath) and satisfies pinned version $Version."
        Ensure-PathEntriesFirst -PathEntries @($existing.InstallPath, (Join-Path $existing.InstallPath "Scripts"))
        return
    }

    Ensure-ChocolateyPackage -PackageName $packageName -Version $Version
}

function Test-ShouldRetryChocolateyCommand {
    param(
        [Parameter(Mandatory = $true)]
        [object[]]$OutputLines
    )

    $combinedOutput = ($OutputLines | Out-String)
    return (
        $combinedOutput -match "Response status code does not indicate success:\s+(408|429|5\d\d)" -or
        $combinedOutput -match "Unable to connect to source" -or
        $combinedOutput -match "The operation has timed out" -or
        $combinedOutput -match "temporarily unavailable" -or
        $combinedOutput -match "The remote name could not be resolved" -or
        $combinedOutput -match "An error occurred while sending the request" -or
        $combinedOutput -match "The request was aborted"
    )
}

function Get-CygwinRootDirIfAvailable {
    $registryKeys = @(
        "HKLM:\SOFTWARE\Cygwin\setup",
        "HKLM:\SOFTWARE\WOW6432Node\Cygwin\setup"
    )

    foreach ($registryKey in $registryKeys) {
        if (-not (Test-Path $registryKey)) {
            continue
        }

        $rootDir = (Get-ItemProperty -Path $registryKey -Name rootdir -ErrorAction SilentlyContinue).rootdir
        if ($rootDir) {
            return $rootDir
        }
    }

    $fallbackCandidates = @(
        $cygwinInstallRoot,
        "C:\cygwin64",
        "C:\cygwin",
        "D:\cygwin"
    )
    foreach ($candidate in $fallbackCandidates) {
        # Join-Path resolves the drive and throws DriveNotFoundException in
        # Windows PowerShell 5.1 when a candidate drive such as D: is absent.
        if (Test-Path -LiteralPath ([System.IO.Path]::Combine($candidate, "bin\bash.exe"))) {
            return $candidate
        }
    }

    return $null
}

function Get-CygwinRootDir {
    $rootDir = Get-CygwinRootDirIfAvailable
    if ($rootDir) {
        return $rootDir
    }

    throw "Unable to locate the Cygwin installation root."
}

function Get-CygwinInstalledDbPath {
    param(
        [string]$CygwinRoot
    )

    if ([string]::IsNullOrWhiteSpace($CygwinRoot)) {
        $CygwinRoot = Get-CygwinRootDir
    }

    return Join-Path $CygwinRoot "etc\setup\installed.db"
}

function Get-CygwinBashPath {
    param(
        [string]$CygwinRoot
    )

    if ([string]::IsNullOrWhiteSpace($CygwinRoot)) {
        $CygwinRoot = Get-CygwinRootDir
    }

    return Join-Path $CygwinRoot "bin\bash.exe"
}

function Get-CygwinSetupPath {
    param(
        [Parameter(Mandatory = $true)]
        [string]$CygwinRoot
    )

    return Join-Path $CygwinRoot "cygwinsetup.exe"
}

function Assert-CygwinInstallRoot {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ExpectedRoot
    )

    $resolvedRoot = Get-CygwinRootDir
    if (-not (Test-SameWindowsPath -Left $resolvedRoot -Right $ExpectedRoot)) {
        throw "Cygwin resolved to '$resolvedRoot' after installation, but the selected install root was '$ExpectedRoot'. Check the Cygwin registry rootdir values and rerun the installer."
    }

    $requiredPaths = @(
        (Get-CygwinBashPath -CygwinRoot $resolvedRoot),
        (Get-CygwinInstalledDbPath -CygwinRoot $resolvedRoot)
    )
    foreach ($requiredPath in $requiredPaths) {
        if (-not (Test-Path -LiteralPath $requiredPath -PathType Leaf)) {
            throw "Cygwin install root '$resolvedRoot' is missing required file '$requiredPath'."
        }
    }

    Write-Host "Verified Cygwin root at $resolvedRoot."
    return $resolvedRoot
}

function Get-NormalizedWindowsPath {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    $fullPath = [System.IO.Path]::GetFullPath($Path)
    $pathRoot = [System.IO.Path]::GetPathRoot($fullPath)
    $normalizedPath = $fullPath.TrimEnd([char[]]@('\', '/'))
    if (-not [string]::IsNullOrWhiteSpace($pathRoot)) {
        $normalizedRoot = $pathRoot.TrimEnd([char[]]@('\', '/'))
        if ($normalizedPath.Equals($normalizedRoot, [System.StringComparison]::OrdinalIgnoreCase)) {
            return $pathRoot
        }
    }

    return $normalizedPath
}

function Test-SameWindowsPath {
    param(
        [string]$Left,
        [string]$Right
    )

    if ([string]::IsNullOrWhiteSpace($Left) -or [string]::IsNullOrWhiteSpace($Right)) {
        return $false
    }

    $normalizedLeft = Get-NormalizedWindowsPath -Path $Left
    $normalizedRight = Get-NormalizedWindowsPath -Path $Right
    return $normalizedLeft.Equals($normalizedRight, [System.StringComparison]::OrdinalIgnoreCase)
}

function Get-CygwinRemovalAllowedRoots {
    return @(
        $cygwinInstallRoot,
        "C:\cygwin64",
        "C:\cygwin",
        "D:\cygwin"
    )
}

function Test-CygwinRootInPreferredFamily {
    param(
        [Parameter(Mandatory = $true)]
        [string]$RootDir
    )

    $preferredRoot = Get-NormalizedWindowsPath -Path $cygwinInstallRoot
    $preferredParentPath = Split-Path -Parent $preferredRoot
    if ([string]::IsNullOrWhiteSpace($preferredParentPath)) {
        return $false
    }

    $preferredParent = Get-NormalizedWindowsPath -Path $preferredParentPath
    $preferredLeaf = Split-Path -Leaf $preferredRoot
    $normalizedRoot = Get-NormalizedWindowsPath -Path $RootDir
    $candidateParentPath = Split-Path -Parent $normalizedRoot
    if ([string]::IsNullOrWhiteSpace($candidateParentPath)) {
        return $false
    }

    $candidateParent = Get-NormalizedWindowsPath -Path $candidateParentPath
    $candidateLeaf = Split-Path -Leaf $normalizedRoot
    $versionedPreferredLeafPattern = "^$([regex]::Escape($preferredLeaf))-\d"

    return (
        $candidateParent.Equals($preferredParent, [System.StringComparison]::OrdinalIgnoreCase) -and
        (
            $candidateLeaf.Equals($preferredLeaf, [System.StringComparison]::OrdinalIgnoreCase) -or
            $candidateLeaf -match $versionedPreferredLeafPattern
        )
    )
}

function Test-CygwinRootInRemovalAllowlist {
    param(
        [Parameter(Mandatory = $true)]
        [string]$RootDir
    )

    foreach ($allowedRoot in (Get-CygwinRemovalAllowedRoots)) {
        if (Test-SameWindowsPath -Left $RootDir -Right $allowedRoot) {
            return $true
        }
    }

    return Test-CygwinRootInPreferredFamily -RootDir $RootDir
}

function Test-WindowsPathIsDriveRoot {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    $fullPath = [System.IO.Path]::GetFullPath($Path)
    $pathRoot = [System.IO.Path]::GetPathRoot($fullPath)
    if ([string]::IsNullOrWhiteSpace($pathRoot)) {
        return $false
    }

    $normalizedPath = $fullPath.TrimEnd([char[]]@('\', '/'))
    $normalizedRoot = $pathRoot.TrimEnd([char[]]@('\', '/'))
    return $normalizedPath.Equals($normalizedRoot, [System.StringComparison]::OrdinalIgnoreCase)
}

function Get-WindowsPathDepth {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    $fullPath = [System.IO.Path]::GetFullPath($Path)
    $pathRoot = [System.IO.Path]::GetPathRoot($fullPath)
    if ([string]::IsNullOrWhiteSpace($pathRoot)) {
        return 0
    }

    $relativePath = $fullPath.Substring($pathRoot.Length).Trim([char[]]@('\', '/'))
    if ([string]::IsNullOrWhiteSpace($relativePath)) {
        return 0
    }

    return @($relativePath -split "[\\/]" | Where-Object { $_ -ne "" }).Count
}

function Test-CygwinRootHasDeletionMarkers {
    param(
        [Parameter(Mandatory = $true)]
        [string]$RootDir
    )

    if (-not (Test-Path -LiteralPath (Join-Path $RootDir "bin\bash.exe") -PathType Leaf)) {
        return $false
    }

    $markerPaths = @(
        "etc\setup\installed.db",
        "etc\setup\setup.rc",
        "var\log\setup.log",
        "Cygwin.bat"
    )
    foreach ($markerPath in $markerPaths) {
        if (Test-Path -LiteralPath (Join-Path $RootDir $markerPath) -PathType Leaf) {
            return $true
        }
    }

    return $false
}

function Assert-CygwinRootSafeForAutomation {
    param(
        [Parameter(Mandatory = $true)]
        [string]$RootDir
    )

    $reasons = @()
    $normalizedRoot = $null
    try {
        $normalizedRoot = Get-NormalizedWindowsPath -Path $RootDir
    } catch {
        $reasons += "the path could not be normalized: $($_.Exception.Message)"
    }

    if ($normalizedRoot) {
        $isAllowedRoot = Test-CygwinRootInRemovalAllowlist -RootDir $normalizedRoot
        if ($RootDir -notmatch "^[A-Za-z]:[\\/]") {
            $reasons += "it is not an absolute local Windows path"
        }
        if (Test-WindowsPathIsDriveRoot -Path $normalizedRoot) {
            $reasons += "it resolves to a drive root"
        }
        if ((Get-WindowsPathDepth -Path $normalizedRoot) -le 1 -and -not $isAllowedRoot) {
            $reasons += "it resolves to a high-level directory"
        }
        if (-not $isAllowedRoot) {
            $allowedRoots = (Get-CygwinRemovalAllowedRoots) -join ", "
            $reasons += "it is outside the allowed Cygwin roots ($allowedRoots, or a versioned sibling of $cygwinInstallRoot)"
        }
        if (-not (Test-CygwinRootHasDeletionMarkers -RootDir $normalizedRoot)) {
            $reasons += "it does not contain bin\bash.exe plus a Cygwin setup marker"
        }
    }

    if ($reasons.Count -gt 0) {
        throw "Refusing to automatically manage Cygwin root '$RootDir' because $($reasons -join '; '). The Cygwin registry rootdir may be stale or corrupt. Fix HKLM:\SOFTWARE\Cygwin\setup rootdir and HKLM:\SOFTWARE\WOW6432Node\Cygwin\setup rootdir if present, or manually remove the directory after verifying it is a Cygwin installation, then rerun this installer."
    }
}

function Stop-CygwinProcesses {
    param(
        [Parameter(Mandatory = $true)]
        [string]$RootDir
    )

    if ([string]::IsNullOrWhiteSpace($RootDir) -or -not (Test-Path -LiteralPath $RootDir)) {
        return
    }

    Assert-CygwinRootSafeForAutomation -RootDir $RootDir

    $normalizedRoot = ([System.IO.Path]::GetFullPath($RootDir)).TrimEnd([char[]]@('\', '/'))
    $rootPrefix = $normalizedRoot + "\"
    $processes = @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue | Where-Object {
        $_.ProcessId -ne $PID -and
        $_.ExecutablePath -and
        (
            $_.ExecutablePath.Equals($normalizedRoot, [System.StringComparison]::OrdinalIgnoreCase) -or
            $_.ExecutablePath.StartsWith($rootPrefix, [System.StringComparison]::OrdinalIgnoreCase)
        )
    })

    foreach ($process in $processes) {
        Write-Host "Stopping Cygwin process $($process.ProcessId) ($($process.Name)) before reinstalling Cygwin."
        Stop-Process -Id $process.ProcessId -Force -ErrorAction SilentlyContinue
    }
}

function Remove-CygwinInstallRoot {
    param(
        [Parameter(Mandatory = $true)]
        [string]$RootDir
    )

    if ([string]::IsNullOrWhiteSpace($RootDir) -or -not (Test-Path -LiteralPath $RootDir)) {
        return
    }

    Assert-CygwinRootSafeForAutomation -RootDir $RootDir

    Write-Host "Removing existing Cygwin root at $RootDir."
    try {
        Remove-Item -LiteralPath $RootDir -Recurse -Force -ErrorAction Stop
        return $true
    } catch {
        Write-Warning "Unable to remove existing Cygwin root at $RootDir. A fresh Cygwin install will use a different root. $($_.Exception.Message)"
        return $false
    }
}

function Get-CygwinChocolateyExtraArgs {
    param(
        [Parameter(Mandatory = $true)]
        [string]$InstallRoot
    )

    return @("--params", "`"/InstallDir:$InstallRoot /NoStartMenu`"")
}

function Get-CygwinAlternateInstallRoot {
    param(
        [Parameter(Mandatory = $true)]
        [string]$PreferredRoot,
        [Parameter(Mandatory = $true)]
        [string]$Version
    )

    $parent = Split-Path -Parent $PreferredRoot
    $leaf = Split-Path -Leaf $PreferredRoot
    $safeVersion = $Version -replace "[^A-Za-z0-9.-]", "-"
    $baseCandidate = [System.IO.Path]::Combine($parent, "$leaf-$safeVersion")

    if (-not (Test-Path -LiteralPath $baseCandidate)) {
        return $baseCandidate
    }

    for ($index = 1; $index -le 20; $index++) {
        $candidate = "$baseCandidate-$index"
        if (-not (Test-Path -LiteralPath $candidate)) {
            return $candidate
        }
    }

    $timestamp = Get-Date -Format "yyyyMMddHHmmss"
    return "$baseCandidate-$timestamp"
}

function Get-CleanCygwinInstallRoot {
    param(
        [Parameter(Mandatory = $true)]
        [string]$PreferredRoot,
        [string]$ExistingRoot,
        [Parameter(Mandatory = $true)]
        [string]$Version
    )

    $targetRoot = $PreferredRoot
    $rootsToClean = @()
    if ($ExistingRoot) {
        $rootsToClean += $ExistingRoot
    }
    if (-not (Test-SameWindowsPath -Left $ExistingRoot -Right $PreferredRoot)) {
        $rootsToClean += $PreferredRoot
    }

    foreach ($root in $rootsToClean) {
        if ([string]::IsNullOrWhiteSpace($root) -or -not (Test-Path -LiteralPath $root)) {
            continue
        }

        Stop-CygwinProcesses -RootDir $root
        $removedRoot = Remove-CygwinInstallRoot -RootDir $root
        if ((-not $removedRoot) -and (Test-SameWindowsPath -Left $root -Right $targetRoot)) {
            $targetRoot = Get-CygwinAlternateInstallRoot -PreferredRoot $PreferredRoot -Version $Version
            Write-Host "Using alternate Cygwin install root at $targetRoot."
        }
    }

    return $targetRoot
}

function Clear-CygwinRegistryRootDirs {
    $registryKeys = @(
        "HKLM:\SOFTWARE\Cygwin\setup",
        "HKLM:\SOFTWARE\WOW6432Node\Cygwin\setup"
    )

    foreach ($registryKey in $registryKeys) {
        if (Test-Path $registryKey) {
            Write-Host "Clearing stale Cygwin registry root at $registryKey before installation."
            Remove-Item -LiteralPath $registryKey -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
}

function Uninstall-ChocolateyPackageIfPresent {
    param(
        [Parameter(Mandatory = $true)]
        [string]$PackageName
    )

    $installedVersion = Get-ChocolateyInstalledVersion -PackageName $PackageName
    if (-not $installedVersion) {
        return
    }

    Write-Host "Removing $PackageName $installedVersion before reinstalling Cygwin."
    $packageArguments = @("uninstall", $PackageName, "-y", "--no-progress")
    Invoke-ChocolateyPackageCommand -Command "uninstall" -PackageName $PackageName -Version $installedVersion -Arguments $packageArguments
}

function Ensure-CygwinChocolateyPackage {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Version,
        [Parameter(Mandatory = $true)]
        [string]$InstallRoot
    )

    $installedVersion = Get-ChocolateyInstalledVersion -PackageName "cygwin"
    if ($installedVersion -eq $Version) {
        Write-Host "cygwin $Version is already installed."
        return (Assert-CygwinInstallRoot -ExpectedRoot (Get-CygwinRootDir))
    }

    if (-not $installedVersion) {
        $existingRoot = Get-CygwinRootDirIfAvailable
        $targetRoot = Get-CleanCygwinInstallRoot -PreferredRoot $InstallRoot -ExistingRoot $existingRoot -Version $Version
        Clear-CygwinRegistryRootDirs
        Ensure-ChocolateyPackage -PackageName "cygwin" -Version $Version -ExtraArgs (Get-CygwinChocolateyExtraArgs -InstallRoot $targetRoot)
        return (Assert-CygwinInstallRoot -ExpectedRoot $targetRoot)
    }

    # The upstream Chocolatey upgrade path rewrites cygwinsetup.exe in-place and can fail on stateful runners.
    Write-Host "cygwin $installedVersion detected. Reinstalling cleanly with pinned version $Version..."
    $existingRoot = Get-CygwinRootDirIfAvailable
    if ($existingRoot) {
        Stop-CygwinProcesses -RootDir $existingRoot
    }

    Uninstall-ChocolateyPackageIfPresent -PackageName "cyg-get"
    Uninstall-ChocolateyPackageIfPresent -PackageName "cygwin"

    $targetRoot = Get-CleanCygwinInstallRoot -PreferredRoot $InstallRoot -ExistingRoot $existingRoot -Version $Version

    Clear-CygwinRegistryRootDirs
    Ensure-ChocolateyPackage -PackageName "cygwin" -Version $Version -ExtraArgs (Get-CygwinChocolateyExtraArgs -InstallRoot $targetRoot)
    return (Assert-CygwinInstallRoot -ExpectedRoot $targetRoot)
}

function Test-CygwinPackageInstalled {
    param(
        [Parameter(Mandatory = $true)]
        [string]$PackageName,
        [Parameter(Mandatory = $true)]
        [string]$CygwinRoot
    )

    $installedDb = Get-CygwinInstalledDbPath -CygwinRoot $CygwinRoot
    if (-not (Test-Path -LiteralPath $installedDb -PathType Leaf)) {
        throw "installed.db not found at $installedDb"
    }

    return Select-String -Path $installedDb -Pattern "^(?i)$([regex]::Escape($PackageName))\s" -Quiet
}

function Invoke-CygwinSetupPackageInstall {
    param(
        [Parameter(Mandatory = $true)]
        [string]$CygwinRoot,
        [Parameter(Mandatory = $true)]
        [string[]]$PackageNames
    )

    $setupPath = Get-CygwinSetupPath -CygwinRoot $CygwinRoot
    if (-not (Test-Path -LiteralPath $setupPath -PathType Leaf)) {
        throw "Cygwin setup executable not found at $setupPath."
    }

    $packageList = $PackageNames -join ","
    $localPackageDir = Join-Path $CygwinRoot "packages"
    $setupArguments = @(
        "--quiet-mode",
        "--root `"$CygwinRoot`"",
        "--local-package-dir `"$localPackageDir`"",
        "--no-desktop",
        "--no-startmenu",
        "--packages $packageList"
    )

    Write-Host "Attempting to install Cygwin packages into ${CygwinRoot}: $packageList"
    $process = Start-Process -FilePath $setupPath -ArgumentList $setupArguments -Wait -PassThru -WindowStyle Minimized
    if ($process.ExitCode -ne 0) {
        throw "Cygwin setup failed to install $packageList into $CygwinRoot with exit code $($process.ExitCode)."
    }
}

function Ensure-CygwinPackage {
    param(
        [Parameter(Mandatory = $true)]
        [string]$PackageName,
        [Parameter(Mandatory = $true)]
        [string]$CygwinRoot
    )

    if (Test-CygwinPackageInstalled -PackageName $PackageName -CygwinRoot $CygwinRoot) {
        Write-Host "$PackageName is installed in $CygwinRoot."
        return
    }

    Write-Host "$PackageName is not installed in $CygwinRoot. Installing..."
    Invoke-CygwinSetupPackageInstall -CygwinRoot $CygwinRoot -PackageNames @($PackageName)
}

function Get-CygwinPythonCommand {
    return "python3"
}

function Get-CygwinPipPackageVersion {
    param(
        [Parameter(Mandatory = $true)]
        [string]$PackageName,
        [Parameter(Mandatory = $true)]
        [string]$CygwinRoot
    )

    $bashExePath = Get-CygwinBashPath -CygwinRoot $CygwinRoot
    $pythonCommand = Get-CygwinPythonCommand
    $output = & $bashExePath -lc "$pythonCommand -m pip show $PackageName 2>/dev/null"
    if ($LASTEXITCODE -ne 0) {
        return $null
    }

    foreach ($line in $output) {
        if ($line -match "^Version:\s+(.+)$") {
            return $Matches[1].Trim()
        }
    }

    return $null
}

function Ensure-CygwinPipPackage {
    param(
        [Parameter(Mandatory = $true)]
        [string]$PackageName,
        [Parameter(Mandatory = $true)]
        [string]$Version,
        [Parameter(Mandatory = $true)]
        [string]$CygwinRoot
    )

    $installedVersion = Get-CygwinPipPackageVersion -PackageName $PackageName -CygwinRoot $CygwinRoot
    if ($installedVersion -eq $Version) {
        Write-Output "$PackageName $Version is already installed in Cygwin."
        return
    }

    $bashExePath = Get-CygwinBashPath -CygwinRoot $CygwinRoot
    $pythonCommand = Get-CygwinPythonCommand
    $constraint = "'" + $PackageName + "==" + $Version + "'"

    if ($installedVersion) {
        Write-Output "$PackageName $installedVersion detected in Cygwin. Enforcing pinned version $Version..."
    } else {
        Write-Output "$PackageName not found in Cygwin. Installing pinned version $Version..."
    }

    & $bashExePath -lc "$pythonCommand -m pip install --disable-pip-version-check --upgrade $constraint"
    if ($LASTEXITCODE -ne 0) {
        throw "pip failed to install $PackageName==$Version inside Cygwin with exit code $LASTEXITCODE."
    }
}

function Get-AzureCliMsiVersion {
    # The Azure CLI MSI can be installed outside Chocolatey (e.g. preinstalled on
    # GitHub-hosted runners), so Chocolatey reports it as missing. Read the
    # version from the MSI uninstall entries instead and return the highest one.
    $highestVersion = $null
    foreach ($registryKey in $azureCliUninstallRegistryKeys) {
        foreach ($entry in @(Get-ItemProperty -Path $registryKey -ErrorAction SilentlyContinue)) {
            if ("$($entry.DisplayName)" -notlike "Microsoft Azure CLI*") {
                continue
            }

            $entryVersion = $null
            if (-not [version]::TryParse("$($entry.DisplayVersion)", [ref]$entryVersion)) {
                continue
            }

            if (-not $highestVersion -or $entryVersion -gt $highestVersion) {
                $highestVersion = $entryVersion
            }
        }
    }

    return $highestVersion
}

function Ensure-AzureCliInstalled {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Version
    )

    $pinnedVersion = $null
    if (-not [version]::TryParse($Version, [ref]$pinnedVersion)) {
        Write-Warning "Pinned azure-cli version $Version is not numeric; skipping the installed version check."
        Ensure-ChocolateyPackage -PackageName "azure-cli" -Version $Version
        return
    }

    # The Azure CLI MSI refuses to downgrade an existing newer installation and
    # fails with exit code 1603, and a Chocolatey downgrade runs that same MSI.
    # Treat the pin as a minimum, whether or not Chocolatey manages the install.
    $existingVersion = Get-AzureCliMsiVersion
    if ($existingVersion -and $existingVersion -ge $pinnedVersion) {
        Write-Host "azure-cli $existingVersion is already installed and satisfies pinned version $Version."
        return
    }

    Ensure-ChocolateyPackage -PackageName "azure-cli" -Version $Version
}

# Windows client editions ship Hyper-V as optional features. The parent
# feature pulls in the hypervisor, the services and the management tools,
# including the Hyper-V PowerShell module sailwright uses.
$hyperVClientFeatureName = "Microsoft-Hyper-V-All"
$hyperVClientRequiredFeatures = @(
    "Microsoft-Hyper-V",
    "Microsoft-Hyper-V-Management-PowerShell"
)
# Windows Server installs Hyper-V as a role; -IncludeManagementTools adds the
# Hyper-V PowerShell module.
$hyperVServerRequiredFeatures = @(
    "Hyper-V",
    "Hyper-V-PowerShell"
)

function Test-IsWindowsServer {
    # Win32_OperatingSystem.ProductType: 1 = workstation, 2 = domain controller, 3 = server.
    return ((Get-CimInstance -ClassName Win32_OperatingSystem).ProductType -ne 1)
}

function Assert-HyperVCapableProcessor {
    # Once a hypervisor runs (Hyper-V itself, or the outer hypervisor of a
    # nested setup) Windows stops reporting the processor virtualization
    # flags, so they cannot be checked.
    if ((Get-CimInstance -ClassName Win32_ComputerSystem).HypervisorPresent) {
        return
    }

    foreach ($processor in @(Get-CimInstance -ClassName Win32_Processor)) {
        if (-not $processor.VMMonitorModeExtensions) {
            throw "Hyper-V needs hardware virtualization (Intel VT-x or AMD-V), but the processor '$($processor.Name)' does not expose it. When Windows runs as a virtual machine, enable nested virtualization on the outer hypervisor. Run 'sailwright install' without --hyperv to install the other dependencies without Hyper-V."
        }
        if (-not $processor.VirtualizationFirmwareEnabled) {
            throw "Hyper-V needs hardware virtualization, but it is disabled in the firmware of this host. Enable Intel VT-x or AMD-V (SVM) in the BIOS/UEFI settings, or run 'sailwright install' without --hyperv to install the other dependencies without Hyper-V."
        }
    }
}

# DISM reports freshly enabled features as Enabled before the restart that
# actually installs them, so the Hyper-V management service is still missing.
function Test-HyperVRestartPending {
    if (Get-Service -Name "vmms" -ErrorAction SilentlyContinue) {
        return $false
    }
    return (Test-Path -LiteralPath "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending")
}

# Returns $true when Windows must restart to finish enabling Hyper-V.
function Ensure-HyperVClientFeature {
    $features = @(
        foreach ($featureName in $hyperVClientRequiredFeatures) {
            $feature = Get-WindowsOptionalFeature -Online -FeatureName $featureName -ErrorAction SilentlyContinue
            if (-not $feature) {
                throw "Hyper-V is not available on this Windows edition (optional feature $featureName is missing). Hyper-V needs Windows 10/11 Pro, Enterprise or Education. Run 'sailwright install' without --hyperv to install the other dependencies without Hyper-V."
            }
            $feature
        }
    )

    $notEnabled = @($features | Where-Object { "$($_.State)" -ne "Enabled" })
    if ($notEnabled.Count -eq 0) {
        if (Test-HyperVRestartPending) {
            Write-Host "Hyper-V is enabled and waits for a Windows restart."
            return $true
        }
        Write-Host "Hyper-V is already enabled."
        return $false
    }
    if (@($notEnabled | Where-Object { "$($_.State)" -ne "EnablePending" }).Count -eq 0) {
        Write-Host "Hyper-V is enabled and waits for a Windows restart."
        return $true
    }

    Assert-HyperVCapableProcessor
    Write-Host "Enabling Hyper-V ($hyperVClientFeatureName)..."
    $result = Enable-WindowsOptionalFeature -Online -FeatureName $hyperVClientFeatureName -All -NoRestart
    return [bool]$result.RestartNeeded
}

# Returns $true when Windows must restart to finish installing the Hyper-V role.
function Ensure-HyperVServerFeature {
    if (-not (Get-Command Get-WindowsFeature -ErrorAction SilentlyContinue)) {
        throw "Get-WindowsFeature is not available, so the Hyper-V role cannot be installed on this Windows Server. Run 'sailwright install' without --hyperv to install the other dependencies without Hyper-V."
    }

    $features = @(Get-WindowsFeature -Name $hyperVServerRequiredFeatures)
    $notInstalled = @($features | Where-Object { "$($_.InstallState)" -ne "Installed" })
    if ($notInstalled.Count -eq 0) {
        if (Test-HyperVRestartPending) {
            Write-Host "The Hyper-V role is installed and waits for a Windows restart."
            return $true
        }
        Write-Host "The Hyper-V role is already installed."
        return $false
    }
    if (@($notInstalled | Where-Object { "$($_.InstallState)" -ne "InstallPending" }).Count -eq 0) {
        Write-Host "The Hyper-V role is installed and waits for a Windows restart."
        return $true
    }

    Assert-HyperVCapableProcessor
    Write-Host "Installing the Hyper-V role with management tools..."
    $result = Install-WindowsFeature -Name "Hyper-V" -IncludeManagementTools
    if (-not $result.Success) {
        throw "Installing the Hyper-V role failed (exit code $($result.ExitCode))."
    }
    return ("$($result.RestartNeeded)" -eq "Yes")
}

# Returns $true when Windows must restart to finish enabling Hyper-V.
function Ensure-HyperVEnabled {
    if (Test-IsWindowsServer) {
        return (Ensure-HyperVServerFeature)
    }
    return (Ensure-HyperVClientFeature)
}

# Packer's Hyper-V builder writes the unattended-install CD with oscdimg, which
# ships with the Deployment Tools of the Windows ADK.
$windowsAdkInstallerUrl = "https://go.microsoft.com/fwlink/?linkid=2289980" # Windows ADK for Windows 11, version 22H2
$windowsKitsRoot = Join-Path ${env:ProgramFiles(x86)} "Windows Kits"

function Get-OscdimgPath {
    $preferred = Join-Path $windowsKitsRoot "10\Assessment and Deployment Kit\Deployment Tools\amd64\Oscdimg\oscdimg.exe"
    if (Test-Path -LiteralPath $preferred) {
        return $preferred
    }
    if (-not (Test-Path -LiteralPath $windowsKitsRoot)) {
        return $null
    }

    $found = Get-ChildItem -LiteralPath $windowsKitsRoot -Recurse -Filter "oscdimg.exe" -ErrorAction SilentlyContinue |
        Sort-Object { $_.FullName -notlike "*\amd64\*" } |
        Select-Object -First 1
    if ($found) {
        return $found.FullName
    }
    return $null
}

function Ensure-OscdimgInstalled {
    $oscdimgPath = Get-OscdimgPath
    if ($oscdimgPath) {
        Write-Host "oscdimg is already installed at $oscdimgPath."
    } else {
        Write-Host "oscdimg not found. Installing the Windows ADK Deployment Tools (needed for Hyper-V builds)..."
        $installer = Join-Path $env:TEMP "sailwright-adksetup.exe"
        Invoke-WebRequest -Uri $windowsAdkInstallerUrl -OutFile $installer -UseBasicParsing
        $process = Start-Process -FilePath $installer -ArgumentList @("/features", "OptionId.DeploymentTools", "/quiet", "/norestart") -Wait -PassThru
        if ($process.ExitCode -notin @(0, 3010)) {
            throw "The Windows ADK installer failed with exit code $($process.ExitCode)."
        }

        $oscdimgPath = Get-OscdimgPath
        if (-not $oscdimgPath) {
            throw "oscdimg.exe was not found after installing the Windows ADK Deployment Tools."
        }
        Write-Host "Installed oscdimg at $oscdimgPath."
    }

    Ensure-PathContains -PathEntry (Split-Path -Parent $oscdimgPath)
}

function Show-HyperVStatus {
    param(
        [Parameter(Mandatory = $true)]
        [bool]$RestartRequired
    )

    if ($RestartRequired) {
        Write-Warning "Restart Windows to finish enabling Hyper-V, then run 'sailwright install --hyperv' again to verify the setup."
        return
    }

    $service = Get-Service -Name "vmms" -ErrorAction SilentlyContinue
    if (-not $service -or "$($service.Status)" -ne "Running") {
        Write-Warning "Hyper-V is enabled, but the Hyper-V Virtual Machine Management service (vmms) is not running. Check that the hypervisor starts (bcdedit /enum '{current}' should show hypervisorlaunchtype Auto) and that hardware or nested virtualization is available."
        return
    }

    Write-Output "Hyper-V is ready (vmms is running)."
    if (-not (Test-IsWindowsServer)) {
        if (Get-VMSwitch -Name "Default Switch" -ErrorAction SilentlyContinue) {
            Write-Output "Hyper-V Default Switch is available."
        } else {
            Write-Warning "The Hyper-V Default Switch was not found. Set VAGRANT_HYPERV_SWITCH to an existing switch for Hyper-V VMs."
        }
    }
}

function Show-InstalledToolVersions {
    param(
        [switch]$WithGo,
        [Parameter(Mandatory = $true)]
        [string]$CygwinRoot
    )

    $bashExePath = Get-CygwinBashPath -CygwinRoot $CygwinRoot
    python --version
    & $bashExePath -lc "python3 --version"
    & $bashExePath -lc "ansible --version"
    if ($WithGo) {
        go version
    }
    git --version
    make --version
    packer version
    az version
}

function Invoke-DevAlchemySelfSetup {
    param(
        [switch]$WithGo,
        [switch]$VirtualBox,
        [switch]$HyperV,
        [switch]$Vagrant
    )

    if (-not (Test-IsAdministrator)) {
        Invoke-SelfElevated -WithGo:$WithGo -VirtualBox:$VirtualBox -HyperV:$HyperV -Vagrant:$Vagrant
    }

    # Hyper-V is opt-in: it is only needed for VMs with the Hyper-V engine.
    # When requested, enable it first so missing virtualization support or an
    # unsupported Windows edition fails before the longer package installs.
    $hyperVRestartRequired = $false
    if ($HyperV) {
        $hyperVRestartRequired = Ensure-HyperVEnabled
    } else {
        Write-Output "Skipping Hyper-V setup because -HyperV was not specified. Hyper-V is only needed for VMs with the Hyper-V engine; run 'sailwright install --hyperv' to enable it."
    }

    Ensure-ChocolateyInstalled

    if ($WithGo) {
        Ensure-GoInstallRootClean -DesiredVersion $golangVersion
        Ensure-ChocolateyPackage -PackageName "golang" -Version $golangVersion
        Assert-GoToolchainLayout
    } else {
        Write-Output "Skipping Go installation because -WithGo was not specified."
    }
    Ensure-GitInstalled -Version $gitVersion
    Ensure-ChocolateyPackage -PackageName "make" -Version $makeVersion
    Ensure-ChocolateyPackage -PackageName "packer" -Version $packerVersion
    Ensure-AzureCliInstalled -Version $azureCliVersion
    Ensure-NativePythonInstalled -Version $nativePythonVersion
    Assert-NativePythonAvailable
    Export-CommandDirectoryToGitHubPath -CommandName "python"
    $cygwinRoot = Ensure-CygwinChocolateyPackage -Version $cygwinVersion -InstallRoot $cygwinInstallRoot
    Ensure-ChocolateyPackage -PackageName "cyg-get" -Version $cygGetVersion

    if ($VirtualBox) {
        Ensure-ChocolateyPackage -PackageName "virtualbox" -Version $virtualBoxVersion
        Ensure-PathContains -PathEntry "C:\Program Files\Oracle\VirtualBox"
    } else {
        Write-Output "Skipping VirtualBox installation because -VirtualBox was not specified."
    }

    # Vagrant is opt-in: sailwright drives VMs on Windows through it, but local
    # provisioning does not need it.
    if ($Vagrant) {
        Ensure-ChocolateyPackage -PackageName "vagrant" -Version $vagrantVersion
        Ensure-PathContains -PathEntry "C:\Program Files\Vagrant\bin"
    } else {
        Write-Output "Skipping Vagrant installation because -Vagrant was not specified. Vagrant is only needed to create and run sailwright VMs; run 'sailwright install --vagrant' to add it."
    }

    if ($HyperV) {
        Ensure-OscdimgInstalled
    }

    Ensure-PathContains -PathEntry (Join-Path (Get-GitForWindowsRoot) "bin")

    foreach ($package in $cygwinPackages) {
        Ensure-CygwinPackage -PackageName $package -CygwinRoot $cygwinRoot
    }

    Ensure-CygwinPipPackage -PackageName "ansible" -Version $ansibleVersion -CygwinRoot $cygwinRoot
    Ensure-CygwinPipPackage -PackageName "pywinrm" -Version $pywinrmVersion -CygwinRoot $cygwinRoot
    Show-InstalledToolVersions -WithGo:$WithGo -CygwinRoot $cygwinRoot

    if ($HyperV) {
        Show-HyperVStatus -RestartRequired $hyperVRestartRequired
    }
}

if ($env:DEV_ALCHEMY_SELF_SETUP_IMPORT_ONLY -ne "1") {
    Invoke-DevAlchemySelfSetup -WithGo:$WithGo -VirtualBox:$VirtualBox -HyperV:$HyperV -Vagrant:$Vagrant
}

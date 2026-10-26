# Install the sailwright CLI and the sailwright-gateway on Windows.
#
#   irm https://github.com/csautter/sailwright/releases/latest/download/install.ps1 | iex
#
# Downloads the release archives for this architecture, verifies them against
# the release checksums (and the checksums' cosign signature when cosign is
# installed), copies the binaries to %LOCALAPPDATA%\Programs\sailwright and
# adds that directory to the user PATH. No administrator rights needed.
#
# Settings: when piping into iex, use $env:SAILWRIGHT_VERSION,
# $env:SAILWRIGHT_INSTALL_DIR and $env:SAILWRIGHT_NO_GATEWAY = '1'. When
# running the downloaded file, use -Version, -InstallDir and -NoGateway.

# iex runs this file in the caller's scope, where a script-level param block
# would overwrite the caller's $Version, $InstallDir and $NoGateway.
# Everything, the parameters included, lives in a script block so that
# variables, preferences and helpers stay out of the user's session. @args
# forwards the parameters when the downloaded file is run directly.
& {
    # @args passes -NoGateway:$false as two arguments; without positional
    # binding the stray value fails instead of setting another parameter.
    [CmdletBinding(PositionalBinding = $false)]
    param(
        [string]$Version = $env:SAILWRIGHT_VERSION,
        [string]$InstallDir = $env:SAILWRIGHT_INSTALL_DIR,
        [switch]$NoGateway = ($env:SAILWRIGHT_NO_GATEWAY -eq '1')
    )

    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'

    if ($IsLinux -or $IsMacOS) {
        throw 'install.ps1 is for Windows; use install.sh on macOS and Linux.'
    }

    $RepoUrl = 'https://github.com/csautter/sailwright'
    # Base URL for release downloads. SAILWRIGHT_DOWNLOAD_URL is for tests
    # only, so they can serve local files.
    $DownloadUrl = if ($env:SAILWRIGHT_DOWNLOAD_URL) { $env:SAILWRIGHT_DOWNLOAD_URL } else { "$RepoUrl/releases/download" }
    $CosignIdentity = "$RepoUrl/.github/workflows/release-binaries.yml@refs/heads/main"
    $CosignIssuer = 'https://token.actions.githubusercontent.com'

    if (-not $InstallDir) {
        $InstallDir = Join-Path $env:LOCALAPPDATA 'Programs\sailwright'
    }
    # A relative directory would be added to PATH as-is and then resolve
    # against every future working directory.
    $InstallDir = [System.IO.Path]::GetFullPath($ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($InstallDir))
    if ($InstallDir -ne [System.IO.Path]::GetPathRoot($InstallDir)) {
        $InstallDir = $InstallDir.TrimEnd('\')
    }

    # Windows PowerShell 5.1 does not enable TLS 1.2 by default.
    if ($PSVersionTable.PSVersion.Major -lt 6) {
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    }

    function Get-File([string]$Url, [string]$Path) {
        Invoke-WebRequest -Uri $Url -OutFile $Path -UseBasicParsing
    }

    # Return the tag the "latest release" page redirects to, without the GitHub API.
    function Get-LatestTag {
        $request = [System.Net.HttpWebRequest]::Create("$RepoUrl/releases/latest")
        $request.AllowAutoRedirect = $false
        $response = $request.GetResponse()
        try {
            $location = $response.Headers['Location']
        } finally {
            $response.Close()
        }
        $tag = if ($location) { ($location -split '/')[-1] } else { '' }
        if ($tag -notmatch '^v\d') {
            throw 'Could not determine the latest release; pass -Version.'
        }
        return $tag
    }

    function Get-Arch {
        $arch = $null
        try {
            $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
        } catch {
            # Older .NET Framework builds lack RuntimeInformation.
            $arch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
        }
        switch ($arch.ToUpperInvariant()) {
            { $_ -in 'X64', 'AMD64' } { return 'amd64' }
            'ARM64' { return 'arm64' }
            default { throw "Unsupported architecture: $arch" }
        }
    }

    # Return the expected SHA-256 for an asset, matched by file name so that the
    # directory prefix written by sha256sum does not matter.
    function Get-ExpectedHash([string]$Asset, [string]$ChecksumsPath) {
        foreach ($line in Get-Content -LiteralPath $ChecksumsPath) {
            $fields = $line -split '\s+', 2
            if ($fields.Count -eq 2 -and (($fields[1].TrimStart('*') -split '/')[-1]) -eq $Asset) {
                return $fields[0].ToLowerInvariant()
            }
        }
        return $null
    }

    function Install-Binaries([string[]]$Names, [string]$SourceDir, [string]$DestinationDir) {
        New-Item -ItemType Directory -Path $DestinationDir -Force | Out-Null
        $replacements = @()
        try {
            # Stage every binary before changing an existing installation. A
            # missing archive member or full disk must leave it usable.
            foreach ($name in $Names) {
                $target = Join-Path $DestinationDir "$name.exe"
                $replacement = [pscustomobject]@{
                    Target = $target
                    Staged = "$target.$([System.Guid]::NewGuid().ToString('N')).tmp"
                    Backup = $null
                    Installed = $false
                }
                $replacements += $replacement
                Copy-Item -LiteralPath (Join-Path $SourceDir "$name\$name.exe") -Destination $replacement.Staged
            }

            foreach ($replacement in $replacements) {
                # Windows permits renaming a running executable, so keep it
                # alongside the replacement until every promotion succeeds.
                if (Test-Path -LiteralPath $replacement.Target) {
                    $replacement.Backup = "$($replacement.Target).$([System.Guid]::NewGuid().ToString('N')).old"
                    Move-Item -LiteralPath $replacement.Target -Destination $replacement.Backup
                }
                Move-Item -LiteralPath $replacement.Staged -Destination $replacement.Target
                $replacement.Installed = $true
            }
        } catch {
            $installError = $_
            for ($index = $replacements.Count - 1; $index -ge 0; $index--) {
                $replacement = $replacements[$index]
                try {
                    if ($replacement.Installed) {
                        Remove-Item -LiteralPath $replacement.Target -Force
                    }
                    if ($replacement.Backup -and (Test-Path -LiteralPath $replacement.Backup)) {
                        Move-Item -LiteralPath $replacement.Backup -Destination $replacement.Target
                    }
                } catch {
                    Write-Warning "Could not restore $($replacement.Target); previous binary retained at $($replacement.Backup). $_"
                }
            }
            throw $installError
        } finally {
            foreach ($replacement in $replacements) {
                Remove-Item -LiteralPath $replacement.Staged -Force -ErrorAction SilentlyContinue
            }
        }

        # Only discard backups after the complete install succeeds. A running
        # executable may remain as .old until a later successful update.
        foreach ($name in $Names) {
            Get-ChildItem -LiteralPath $DestinationDir -Filter "$name.exe.*.old" -ErrorAction SilentlyContinue |
                Remove-Item -Force -ErrorAction SilentlyContinue
            Write-Host "Installed $(Join-Path $DestinationDir "$name.exe")"
        }
    }

    $arch = Get-Arch

    if (-not $Version) {
        $Version = Get-LatestTag
    }
    if (-not $Version.StartsWith('v')) {
        $Version = "v$Version"
    }
    $plainVersion = $Version.Substring(1)

    Write-Host "Installing sailwright $Version (windows/$arch) to $InstallDir"

    $tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ("sailwright-install-" + [System.Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmpDir | Out-Null

    try {
        $checksums = "sailwright_${plainVersion}_checksums.txt"
        $checksumsPath = Join-Path $tmpDir $checksums
        try {
            Get-File "$DownloadUrl/$Version/$checksums" $checksumsPath
        } catch {
            throw "Could not download $checksums; does release $Version exist? $_"
        }

        if (Get-Command cosign -ErrorAction SilentlyContinue) {
            Write-Host 'Verifying the checksums signature with cosign'
            Get-File "$DownloadUrl/$Version/$checksums.sig" "$checksumsPath.sig"
            Get-File "$DownloadUrl/$Version/$checksums.pem" "$checksumsPath.pem"
            # cosign logs to stderr; keep native stderr from becoming a
            # terminating error so the exit code decides.
            $ErrorActionPreference = 'Continue'
            $cosignOutput = & cosign verify-blob `
                --certificate "$checksumsPath.pem" `
                --signature "$checksumsPath.sig" `
                --certificate-identity $CosignIdentity `
                --certificate-oidc-issuer $CosignIssuer `
                $checksumsPath 2>&1 | Out-String
            $ErrorActionPreference = 'Stop'
            if ($LASTEXITCODE -ne 0) {
                throw "cosign could not verify $checksums; aborting.`n$cosignOutput"
            }
        }

        $binaries = @('sailwright')
        if (-not $NoGateway) {
            $binaries += 'sailwright-gateway'
        }

        foreach ($name in $binaries) {
            $asset = "${name}_${plainVersion}_windows_${arch}.zip"
            $assetPath = Join-Path $tmpDir $asset
            Get-File "$DownloadUrl/$Version/$asset" $assetPath

            $expected = Get-ExpectedHash $asset $checksumsPath
            if (-not $expected) {
                throw "No checksum for $asset in $checksums."
            }
            $actual = (Get-FileHash -LiteralPath $assetPath -Algorithm SHA256).Hash.ToLowerInvariant()
            if ($actual -ne $expected) {
                throw "Checksum mismatch for $asset (expected $expected, got $actual); aborting."
            }

            Expand-Archive -LiteralPath $assetPath -DestinationPath (Join-Path $tmpDir $name) -Force
        }

        Install-Binaries -Names $binaries -SourceDir $tmpDir -DestinationDir $InstallDir
    } finally {
        Remove-Item -LiteralPath $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
    }

    # Edit the raw registry value: [Environment]::GetEnvironmentVariable returns
    # the expanded PATH, and writing that back would freeze entries such as
    # %USERPROFILE%\bin.
    $envKey = Get-Item -Path 'HKCU:\Environment'
    $userPath = $envKey.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    $userEntries = @($userPath -split ';' | Where-Object { $_ })
    $expandedEntries = @($userEntries | ForEach-Object { [Environment]::ExpandEnvironmentVariables($_).TrimEnd('\') })
    if ($expandedEntries -notcontains $InstallDir) {
        $newPath = ($userEntries + $InstallDir) -join ';'
        Set-ItemProperty -Path 'HKCU:\Environment' -Name 'Path' -Value $newPath -Type ExpandString
        # Setting any user variable broadcasts WM_SETTINGCHANGE so new
        # terminals started from Explorer see the updated PATH.
        [Environment]::SetEnvironmentVariable('SAILWRIGHT_INSTALL_REFRESH', '1', 'User')
        [Environment]::SetEnvironmentVariable('SAILWRIGHT_INSTALL_REFRESH', $null, 'User')
        Write-Host "Added $InstallDir to your user PATH. Open a new terminal to pick it up."
    }
    if (@($env:Path -split ';' | ForEach-Object { $_.TrimEnd('\') }) -notcontains $InstallDir) {
        $env:Path = "$env:Path;$InstallDir"
    }

    Write-Host ''
    Write-Host 'Next: sailwright install; sailwright build list'
} @args

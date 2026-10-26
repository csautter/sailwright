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

[CmdletBinding()]
param(
    [string]$Version = $env:SAILWRIGHT_VERSION,
    [string]$InstallDir = $env:SAILWRIGHT_INSTALL_DIR,
    [switch]$NoGateway = ($env:SAILWRIGHT_NO_GATEWAY -eq '1')
)

# iex runs this file in the caller's scope. Everything happens inside a
# function so that preferences, variables and helpers stay out of the user's
# session.
function Install-Sailwright([string]$Version, [string]$InstallDir, [switch]$NoGateway) {
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

        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
        foreach ($name in $binaries) {
            $target = Join-Path $InstallDir "$name.exe"
            # Windows cannot overwrite a running executable, for example a
            # running sailwright-gateway, but it can rename one. Move the old
            # binary aside and delete it once it is no longer running.
            Get-ChildItem -LiteralPath $InstallDir -Filter "$name.exe.*.old" -ErrorAction SilentlyContinue |
                Remove-Item -Force -ErrorAction SilentlyContinue
            $old = $null
            if (Test-Path -LiteralPath $target) {
                $old = "$target.$([System.Guid]::NewGuid().ToString('N')).old"
                Move-Item -LiteralPath $target -Destination $old
            }
            Copy-Item -LiteralPath (Join-Path $tmpDir "$name\$name.exe") -Destination $target
            if ($old) {
                Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue
            }
            Write-Host "Installed $target"
        }
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
}

Install-Sailwright -Version $Version -InstallDir $InstallDir -NoGateway:$NoGateway

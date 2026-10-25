# Expects the following environment variables (set by the composite action):
#   CACHE_FILES       - JSON array of cache file descriptors
#   CACHE_BACKEND     - Remote cache backend: ftp or azure
#   SUBSCRIPTION_ID   - Azure subscription ID (azure backend only)
#   FTP_*             - FTP server, port, username, password, and base dir. Explicit TLS is enforced.
#   LOCAL_CACHE_DIR   - Optional local runner cache directory
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'

$files          = $env:CACHE_FILES | ConvertFrom-Json
$cacheBackend   = $env:CACHE_BACKEND
if ([string]::IsNullOrWhiteSpace($cacheBackend)) { $cacheBackend = 'ftp' }
$cacheBackend   = $cacheBackend.ToLowerInvariant()
$resourceGroup  = 'gh-runner-storage-rg'
$localCacheDir  = $env:LOCAL_CACHE_DIR
$script:CurlExe = $null
$script:FtpBaseUrl = $null
$script:FtpBaseDirNormalized = $null
$script:FtpCurlCommonArgs = @()
$script:FtpCurlTransferArgs = @()
$script:FtpReady = $false

function Write-Fail {
    param([Parameter(Mandatory = $true)][string]$Message)
    Write-Error "  [error] $Message"
    throw $Message
}

function Get-AzureStorageAccount {
    if ([string]::IsNullOrWhiteSpace($env:SUBSCRIPTION_ID)) {
        Write-Fail "subscription-id is required when storage-backend is 'azure'."
    }

    $name = ("ghrunner$($env:SUBSCRIPTION_ID)" -replace '[^A-Za-z0-9]', '').ToLowerInvariant()
    if ($name.Length -gt 24) {
        $name = $name.Substring(0, 24)
    }

    return $name
}

function Normalize-FtpServer {
    param(
        [Parameter(Mandatory = $true)][string]$Server,
        [string]$Port
    )

    $normalized = $Server -replace '^ftps?://', ''
    $normalized = $normalized.TrimEnd('/')
    if ($normalized.Contains('/')) {
        Write-Fail 'FTP server must be a hostname, optionally with a port. Put paths in ftp-base-dir.'
    }

    if (-not [string]::IsNullOrWhiteSpace($Port) -and -not $normalized.Contains(':')) {
        $normalized = '{0}:{1}' -f $normalized, $Port
    }

    return "ftp://$normalized"
}

function Normalize-FtpBaseDir {
    param([string]$BaseDir)

    $normalized = $BaseDir
    if ([string]::IsNullOrWhiteSpace($normalized)) { $normalized = '/private' }
    $normalized = $normalized.Replace('\', '/')
    $normalized = '/' + $normalized.TrimStart([char[]]'/')
    $normalized = $normalized.TrimEnd([char[]]'/')
    if ([string]::IsNullOrWhiteSpace($normalized)) { $normalized = '/private' }

    return $normalized
}

function Initialize-Ftp {
    if ($script:FtpReady) {
        return
    }

    $ftpServer = $env:FTP_SERVER
    if ([string]::IsNullOrWhiteSpace($ftpServer)) { $ftpServer = $env:BUILD_CACHE_FTP_SERVER }
    $ftpPort = $env:FTP_PORT
    if ([string]::IsNullOrWhiteSpace($ftpPort)) { $ftpPort = $env:BUILD_CACHE_FTP_PORT }
    $ftpUsername = $env:FTP_USERNAME
    if ([string]::IsNullOrWhiteSpace($ftpUsername)) { $ftpUsername = $env:BUILD_CACHE_FTP_USERNAME }
    $ftpPassword = $env:FTP_PASSWORD
    if ([string]::IsNullOrWhiteSpace($ftpPassword)) { $ftpPassword = $env:BUILD_CACHE_FTP_PASSWORD }
    $ftpBaseDir = $env:FTP_BASE_DIR
    if ([string]::IsNullOrWhiteSpace($ftpBaseDir)) { $ftpBaseDir = $env:BUILD_CACHE_FTP_BASE_DIR }
    if ([string]::IsNullOrWhiteSpace($ftpBaseDir)) { $ftpBaseDir = '/private' }

    $missing = @()
    if ([string]::IsNullOrWhiteSpace($ftpServer)) { $missing += 'FTP_SERVER' }
    if ([string]::IsNullOrWhiteSpace($ftpUsername)) { $missing += 'FTP_USERNAME' }
    if ([string]::IsNullOrWhiteSpace($ftpPassword)) { $missing += 'FTP_PASSWORD' }
    if ($missing.Count -gt 0) {
        Write-Fail "Missing FTP configuration: $($missing -join ', ')."
    }

    $curl = Get-Command curl.exe -ErrorAction SilentlyContinue | Select-Object -First 1
    if (-not $curl) {
        $curl = Get-Command curl -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    }
    if (-not $curl) { Write-Fail 'FTP backend requires curl.exe.' }

    $script:CurlExe = $curl.Source
    $script:FtpBaseUrl = Normalize-FtpServer -Server $ftpServer -Port $ftpPort
    $script:FtpBaseDirNormalized = Normalize-FtpBaseDir -BaseDir $ftpBaseDir
    $script:FtpCurlCommonArgs = @(
        '--fail',
        '--silent',
        '--show-error',
        '--ssl-reqd',
        '--connect-timeout', '30',
        '--retry', '3',
        '--retry-delay', '5',
        '--user', ('{0}:{1}' -f $ftpUsername, $ftpPassword)
    )
    $script:FtpCurlTransferArgs = @(
        '--fail',
        '--show-error',
        '--progress-bar',
        '--ssl-reqd',
        '--connect-timeout', '30',
        '--retry', '3',
        '--retry-delay', '5',
        '--user', ('{0}:{1}' -f $ftpUsername, $ftpPassword)
    )
    $script:FtpReady = $true
}

function Get-FtpKeyForBlob {
    param([Parameter(Mandatory = $true)][string]$BlobName)

    return $BlobName.Replace('\', '/').TrimStart([char[]]'/')
}

function Get-FtpRemoteUrl {
    param([Parameter(Mandatory = $true)][string]$Key)

    return ('{0}{1}/{2}' -f $script:FtpBaseUrl, $script:FtpBaseDirNormalized, $Key)
}

function Invoke-FtpCurl {
    param(
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [switch]$AllowMissing
    )

    & $script:CurlExe @Arguments
    $exitCode = $LASTEXITCODE
    if ($exitCode -eq 0) {
        return $true
    }
    # 9: the server refused to change into the parent directory, i.e. the directory does not exist.
    if ($AllowMissing -and ($exitCode -eq 78 -or $exitCode -eq 19 -or $exitCode -eq 9)) {
        # The Actions PowerShell wrapper exits with $LASTEXITCODE, which still holds curl's code.
        $global:LASTEXITCODE = 0
        return $false
    }

    Write-Fail "FTP curl command failed with exit code $exitCode."
}

function Test-FtpObject {
    param([Parameter(Mandatory = $true)][string]$Url)

    $curlArgs = $script:FtpCurlCommonArgs + @('--head', '--output', 'NUL', '--dump-header', 'NUL', $Url)
    return Invoke-FtpCurl -Arguments $curlArgs -AllowMissing
}

function Save-ToLocalCache {
    param(
        [Parameter(Mandatory = $true)][string]$LocalPath,
        [Parameter(Mandatory = $true)][string]$BlobName
    )

    if ($localCacheDir -and (Test-Path $localCacheDir -PathType Container)) {
        $cached = Join-Path $localCacheDir $BlobName
        if (Test-Path $cached -PathType Leaf) {
            Write-Host "  [ok] Already in local runner cache at $cached."
        } else {
            $cachedParent = Split-Path -Parent $cached
            if ($cachedParent -and -not (Test-Path $cachedParent -PathType Container)) {
                New-Item -ItemType Directory -Path $cachedParent -Force | Out-Null
            }
            Write-Host "  [cache] Saving to local runner cache: $cached"
            # Copy under a temporary name first, so an interrupted copy never becomes a cache hit.
            $cachedPart = "$cached.part.$PID"
            Copy-Item -Path $LocalPath -Destination $cachedPart -Force
            Move-Item -Path $cachedPart -Destination $cached -Force
            Write-Host '  [ok] Saved to local runner cache.'
        }
    }
}

function Download-FromFtp {
    param(
        [Parameter(Mandatory = $true)][string]$LocalPath,
        [Parameter(Mandatory = $true)][string]$BlobName
    )

    Initialize-Ftp
    $key = Get-FtpKeyForBlob $BlobName
    $url = Get-FtpRemoteUrl $key

    Write-Host "  [download] Not in local cache. Attempting explicit FTPS download from '$script:FtpBaseDirNormalized/$key'..."

    if (Test-FtpObject -Url $url) {
        $dir = Split-Path -Path $LocalPath -Parent
        if ($dir -and -not (Test-Path $dir -PathType Container)) {
            New-Item -ItemType Directory -Path $dir -Force | Out-Null
        }

        $tmpPath = "$LocalPath.part"
        if (Test-Path $tmpPath -PathType Leaf) {
            Remove-Item -Path $tmpPath -Force
        }
        $curlArgs = $script:FtpCurlTransferArgs + @('--location', '--output', $tmpPath, $url)
        Invoke-FtpCurl -Arguments $curlArgs | Out-Null
        Move-Item -Path $tmpPath -Destination $LocalPath -Force
        Write-Host "  [ok] Downloaded $script:FtpBaseDirNormalized/$key -> $LocalPath"
        Save-ToLocalCache -LocalPath $LocalPath -BlobName $BlobName
    } else {
        Write-Host "  [miss] FTP object $script:FtpBaseDirNormalized/$key not found."
    }
}

function Download-FromAzure {
    param(
        [Parameter(Mandatory = $true)][string]$LocalPath,
        [Parameter(Mandatory = $true)][string]$BlobName,
        [Parameter(Mandatory = $true)][string]$Container
    )

    $storageAcct = Get-AzureStorageAccount

    Write-Host '  [download] Attempting Azure Blob Storage download...'
    $saArgs = @('storage', 'account', 'show', '--name', $storageAcct, '--resource-group', $resourceGroup)
    $saJson = az @saArgs 2>&1
    $sa = $null
    if ($LASTEXITCODE -eq 0) { $sa = $saJson | ConvertFrom-Json }

    if ($sa) {
        $existsArgs = @('storage', 'blob', 'exists', '--account-name', $storageAcct, '--container-name', $Container, '--name', $BlobName, '--auth-mode', 'login')
        $blobExists = (az @existsArgs | ConvertFrom-Json).exists

        if ($blobExists) {
            $dir = Split-Path -Path $LocalPath -Parent
            if ($dir -and -not (Test-Path $dir -PathType Container)) {
                New-Item -ItemType Directory -Path $dir -Force | Out-Null
            }
            $dlArgs = @('storage', 'blob', 'download', '--account-name', $storageAcct, '--container-name', $Container, '--name', $BlobName, '--file', $LocalPath, '--auth-mode', 'login')
            az @dlArgs
            if ($LASTEXITCODE -ne 0) {
                Write-Fail "Failed to download $BlobName from Azure container '$Container'."
            }
            Write-Host "  [ok] Downloaded $BlobName -> $LocalPath"
            Save-ToLocalCache -LocalPath $LocalPath -BlobName $BlobName
        } else {
            Write-Host "  [miss] Blob $BlobName not found in container '$Container'."
        }
    } else {
        Write-Host "  [miss] Storage account $storageAcct not found."
    }
}

function Get-ManifestObject {
    # Returns the parsed manifest, $null when it does not exist on the remote.
    param([Parameter(Mandatory = $true)][string]$ManifestKey)

    Initialize-Ftp
    $url = Get-FtpRemoteUrl $ManifestKey
    if (-not (Test-FtpObject -Url $url)) {
        return $null
    }

    $tmp = [System.IO.Path]::GetTempFileName()
    try {
        $curlArgs = $script:FtpCurlCommonArgs + @('--output', $tmp, $url)
        Invoke-FtpCurl -Arguments $curlArgs | Out-Null
        $raw = Get-Content -Path $tmp -Raw
        try {
            $manifest = $raw | ConvertFrom-Json
        } catch {
            Write-Fail "Manifest $ManifestKey is not valid."
        }
        # Same rules as LoadWin11Manifest: the object name ends up in a local path and an FTP URL.
        if ($manifest.schema -ne 1 -or
            "$($manifest.object)" -cnotmatch '^[A-Za-z0-9][A-Za-z0-9._-]*\.iso$' -or
            "$($manifest.sha256)" -cnotmatch '^[0-9a-f]{64}$') {
            Write-Fail "Manifest $ManifestKey is not valid."
        }
        return [pscustomobject]@{ Manifest = $manifest; Raw = $raw }
    } finally {
        Remove-Item -Path $tmp -Force -ErrorAction SilentlyContinue
    }
}

function Get-Sha256 {
    param([Parameter(Mandatory = $true)][string]$Path)

    return (Get-FileHash -Path $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Download-ManifestEntry {
    # Resolves a "manifest" descriptor to a versioned object, verifies its sha256 and writes a sidecar JSON.
    # Files from the workspace or the runner cache are hashed too: a name proves nothing about the bytes.
    # Returns an explicit outcome: ok, manifest-missing, or object-missing.
    # Only a missing manifest permits migration fallback to a legacy blob-name.
    param(
        [Parameter(Mandatory = $true)][string]$LocalPath,
        [Parameter(Mandatory = $true)][string]$ManifestKey
    )

    if ($cacheBackend -ne 'ftp' -and $cacheBackend -ne 'ftps') {
        Write-Fail "Manifest cache entries are only supported with the ftp backend (got '$cacheBackend')."
    }

    $sidecar = "$LocalPath.json"
    $resolved = Get-ManifestObject -ManifestKey $ManifestKey
    if (-not $resolved) {
        Write-Host "  [miss] Manifest $script:FtpBaseDirNormalized/$ManifestKey not found."
        return 'manifest-missing'
    }
    $object = $resolved.Manifest.object
    $sha256 = $resolved.Manifest.sha256
    Write-Host "  [ok] Manifest points to $object."

    # Workspace copy from an earlier run: keep it only if the sidecar names this object and the bytes match.
    if ((Test-Path $LocalPath -PathType Leaf) -and (Test-Path $sidecar -PathType Leaf)) {
        $current = $null
        try { $current = (Get-Content -Path $sidecar -Raw | ConvertFrom-Json).object } catch { $current = $null }
        if ($current -eq $object -and (Get-Sha256 -Path $LocalPath) -eq $sha256) {
            Write-Host "  [ok] Already present at $LocalPath and matches the manifest (sha256 verified) - skipping."
            return 'ok'
        }
    }
    if (Test-Path $LocalPath) {
        Write-Host "  [cache] Existing $LocalPath is stale or unverified; replacing it."
        Remove-Item -Path $LocalPath, $sidecar -Force -ErrorAction SilentlyContinue
    }

    $dir = Split-Path -Path $LocalPath -Parent
    if ($dir -and -not (Test-Path $dir -PathType Container)) {
        New-Item -ItemType Directory -Path $dir -Force | Out-Null
    }

    # Local runner cache is keyed by the versioned object name.
    if ($localCacheDir -and (Test-Path $localCacheDir -PathType Container)) {
        $cached = Join-Path $localCacheDir $object
        if (Test-Path $cached -PathType Leaf) {
            if ((Get-Sha256 -Path $cached) -eq $sha256) {
                Write-Host "  [ok] Found $object in local runner cache (sha256 verified) - copying."
                Copy-Item -Path $cached -Destination $LocalPath -Force
                Set-Content -Path $sidecar -Value $resolved.Raw -NoNewline
                return 'ok'
            }
            Write-Host "  [cache] Local runner cache copy of $object does not match the manifest sha256; evicting it."
            Remove-Item -Path $cached -Force
        }
    }

    $objectKey = '{0}/{1}' -f (($ManifestKey -replace '/[^/]*$', '')), $object
    $url = Get-FtpRemoteUrl $objectKey
    Write-Host "  [download] Downloading $script:FtpBaseDirNormalized/$objectKey..."
    if (-not (Test-FtpObject -Url $url)) {
        Write-Host "  [miss] Manifest $ManifestKey references missing FTP object $script:FtpBaseDirNormalized/$objectKey; legacy fallback is disabled."
        Write-Host "::error title=Build cache::Manifest $ManifestKey references missing FTP object $script:FtpBaseDirNormalized/$objectKey; run 'Refresh Windows 11 ISO' to repair missing objects."
        return 'object-missing'
    }

    $tmpPath = "$LocalPath.part"
    if (Test-Path $tmpPath -PathType Leaf) {
        Remove-Item -Path $tmpPath -Force
    }
    $curlArgs = $script:FtpCurlTransferArgs + @('--location', '--output', $tmpPath, $url)
    Invoke-FtpCurl -Arguments $curlArgs | Out-Null

    $actual = Get-Sha256 -Path $tmpPath
    if ($actual -ne $sha256) {
        Remove-Item -Path $tmpPath -Force
        Write-Fail "sha256 mismatch for ${object}: expected $sha256, got $actual. Deleted download."
    }
    Move-Item -Path $tmpPath -Destination $LocalPath -Force
    Set-Content -Path $sidecar -Value $resolved.Raw -NoNewline
    Write-Host "  [ok] Downloaded $object -> $LocalPath (sha256 verified)."
    Save-ToLocalCache -LocalPath $LocalPath -BlobName $object
    return 'ok'
}

foreach ($f in $files) {
    $localPath = $f.'local-path'
    $blobName  = $f.'blob-name'
    $legacyBlobName = $blobName
    if (-not $blobName) { $blobName = Split-Path -Leaf $localPath }
    $container = $f.'container'
    if (-not $container) { $container = "build-cache" }

    Write-Host "=== Build cache: $localPath [object=$blobName container=$container backend=$cacheBackend] ==="

    # 0. Manifest-resolved entries (versioned objects behind a pointer file). An explicit blob-name
    #    is the legacy flat object, used only while the manifest does not exist yet (migration).
    if ($null -ne $f.PSObject.Properties['manifest'] -and $f.'manifest') {
        $resolvedEntry = Download-ManifestEntry -LocalPath $localPath -ManifestKey $f.'manifest'
        if ($resolvedEntry -ne 'manifest-missing' -or -not $legacyBlobName) {
            continue
        }
        Write-Host "  [cache] Falling back to the legacy object '$blobName'."
    }

    # 1. Already present in workspace -> nothing to do
    if (Test-Path $localPath) {
        Write-Host "  [ok] Already present at $localPath - skipping."
        continue
    }

    # 2. Local runner cache -> copy
    if ($localCacheDir -and (Test-Path $localCacheDir -PathType Container)) {
        $cached = Join-Path $localCacheDir $blobName
        if (Test-Path $cached -PathType Leaf) {
            Write-Host '  [ok] Found in local runner cache - copying.'
            $dir = Split-Path -Path $localPath -Parent
            if ($dir -and -not (Test-Path $dir -PathType Container)) {
                New-Item -ItemType Directory -Path $dir -Force | Out-Null
            }
            Copy-Item -Path $cached -Destination $localPath -Force
            continue
        }
    }

    if ($cacheBackend -eq 'ftp' -or $cacheBackend -eq 'ftps') {
        Download-FromFtp -LocalPath $localPath -BlobName $blobName
    } elseif ($cacheBackend -eq 'azure' -or $cacheBackend -eq 'azure-blob') {
        Download-FromAzure -LocalPath $localPath -BlobName $blobName -Container $container
    } else {
        Write-Fail "Unsupported storage backend '$cacheBackend'. Use 'ftp' or 'azure'."
    }
}

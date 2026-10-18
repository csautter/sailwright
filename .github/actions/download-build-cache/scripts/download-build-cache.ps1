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
    if ($AllowMissing -and ($exitCode -eq 78 -or $exitCode -eq 19)) {
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
            Copy-Item -Path $LocalPath -Destination $cached -Force
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

foreach ($f in $files) {
    $localPath = $f.'local-path'
    $blobName  = $f.'blob-name'
    if (-not $blobName) { $blobName = Split-Path -Leaf $localPath }
    $container = $f.'container'
    if (-not $container) { $container = "build-cache" }

    Write-Host "=== Build cache: $localPath [object=$blobName container=$container backend=$cacheBackend] ==="

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

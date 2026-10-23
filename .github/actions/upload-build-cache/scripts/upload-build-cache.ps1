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
$localCacheDir  = $env:LOCAL_CACHE_DIR
$script:CurlExe = $null
$script:FtpBaseUrl = $null
$script:FtpBaseDirNormalized = $null
$script:FtpCurlCommonArgs = @()
$script:FtpReady = $false

function Write-Fail {
    param([Parameter(Mandatory = $true)][string]$Message)
    Write-Error "  [error] $Message"
    throw $Message
}

# The remote cache is an optimization: report problems without failing the job.
function Write-CacheSkippedWarning {
    param([Parameter(Mandatory = $true)][string]$Message)
    Write-Host "::warning title=Build cache upload skipped::$Message"
    # The Actions PowerShell wrapper exits with $LASTEXITCODE, which still holds curl's code.
    $global:LASTEXITCODE = 0
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

function Upload-ToFtp {
    param(
        [Parameter(Mandatory = $true)][string]$LocalPath,
        [Parameter(Mandatory = $true)][string]$BlobName,
        [Parameter(Mandatory = $true)][bool]$Overwrite
    )

    Initialize-Ftp
    $key = Get-FtpKeyForBlob $BlobName
    $url = Get-FtpRemoteUrl $key

    if (-not $Overwrite) {
        try {
            $exists = Test-FtpObject -Url $url
        } catch {
            Write-CacheSkippedWarning "Could not check FTP object $script:FtpBaseDirNormalized/$key; not uploading it."
            return
        }
        if ($exists) {
            Write-Host "  [ok] FTP object $script:FtpBaseDirNormalized/$key already exists - skipping upload."
            return
        }
    }

    if ($Overwrite) {
        Write-Host "  [upload] Uploading $script:FtpBaseDirNormalized/$key via explicit FTPS with overwrite enabled..."
    } else {
        Write-Host "  [upload] Uploading $script:FtpBaseDirNormalized/$key via explicit FTPS..."
    }

    $curlArgs = $script:FtpCurlCommonArgs + @('--ftp-create-dirs', '--upload-file', $LocalPath, $url)
    try {
        Invoke-FtpCurl -Arguments $curlArgs | Out-Null
    } catch {
        Write-CacheSkippedWarning "Failed to upload FTP object $script:FtpBaseDirNormalized/$key."
        return
    }
    Write-Host "  [ok] Uploaded $script:FtpBaseDirNormalized/$key."
}

function Upload-ToAzure {
    param(
        [Parameter(Mandatory = $true)][string]$LocalPath,
        [Parameter(Mandatory = $true)][string]$BlobName,
        [Parameter(Mandatory = $true)][string]$Container,
        [Parameter(Mandatory = $true)][bool]$Overwrite
    )

    $storageAcct = Get-AzureStorageAccount

    if ($Overwrite) {
        Write-Host "  [upload] Uploading $BlobName to container '$Container' with overwrite enabled..."
        $uploadArgs = @('storage', 'blob', 'upload', '--account-name', $storageAcct, '--container-name', $Container, '--name', $BlobName, '--file', $LocalPath, '--auth-mode', 'login', '--overwrite', 'true')
        az @uploadArgs
        if ($LASTEXITCODE -ne 0) {
            Write-Fail "Failed to upload $BlobName to Azure container '$Container'."
        }
        Write-Host "  [ok] Uploaded $BlobName."
    } else {
        $existsArgs = @('storage', 'blob', 'exists', '--account-name', $storageAcct, '--container-name', $Container, '--name', $BlobName, '--auth-mode', 'login', '--query', 'exists', '--output', 'tsv')
        $blobExists = az @existsArgs

        if ($blobExists -eq "true") {
            Write-Host "  [ok] Blob $BlobName already exists in '$Container' - skipping upload."
        } else {
            Write-Host "  [upload] Uploading $BlobName to container '$Container'..."
            $uploadArgs = @('storage', 'blob', 'upload', '--account-name', $storageAcct, '--container-name', $Container, '--name', $BlobName, '--file', $LocalPath, '--auth-mode', 'login', '--overwrite', 'false')
            az @uploadArgs
            if ($LASTEXITCODE -ne 0) {
                Write-Fail "Failed to upload $BlobName to Azure container '$Container'."
            }
            Write-Host "  [ok] Uploaded $BlobName."
        }
    }
}

foreach ($f in $files) {
    $localPath = $f.'local-path'
    $blobName  = $f.'blob-name'
    if (-not $blobName) { $blobName = Split-Path -Leaf $localPath }
    $container = $f.'container'
    if (-not $container) { $container = "build-cache" }
    $overwrite = $false
    if ($null -ne $f.PSObject.Properties['overwrite']) {
        $overwrite = [System.Convert]::ToBoolean($f.'overwrite')
    }

    Write-Host "=== Build cache upload: $localPath [object=$blobName container=$container overwrite=$overwrite backend=$cacheBackend] ==="

    if ($null -ne $f.PSObject.Properties['manifest'] -and $f.'manifest') {
        Write-Host "  [ok] Entry is resolved via manifest '$($f.'manifest')' - managed by refresh workflow, skipping upload."
        continue
    }

    if (-not (Test-Path $localPath -PathType Leaf)) {
        Write-Host "  [miss] File not found at $localPath - skipping."
        continue
    }

    # 1. Save to local runner cache when available.
    if ($localCacheDir -and (Test-Path $localCacheDir -PathType Container)) {
        $cached = Join-Path $localCacheDir $blobName
        $cachedParent = Split-Path -Parent $cached
        if ($cachedParent -and -not (Test-Path $cachedParent -PathType Container)) {
            New-Item -ItemType Directory -Path $cachedParent -Force | Out-Null
        }

        if ((Test-Path $cached -PathType Leaf) -and -not $overwrite) {
            Write-Host "  [ok] Already in local runner cache at $cached."
        } else {
            if (Test-Path $cached -PathType Leaf) {
                Write-Host "  [cache] Replacing local runner cache entry: $cached"
            } else {
                Write-Host "  [cache] Saving to local runner cache: $cached"
            }
            Copy-Item -Path $localPath -Destination $cached -Force
            Write-Host "  [ok] Saved to local runner cache."
        }
    }

    if ($cacheBackend -eq 'ftp' -or $cacheBackend -eq 'ftps') {
        Upload-ToFtp -LocalPath $localPath -BlobName $blobName -Overwrite $overwrite
    } elseif ($cacheBackend -eq 'azure' -or $cacheBackend -eq 'azure-blob') {
        Upload-ToAzure -LocalPath $localPath -BlobName $blobName -Container $container -Overwrite $overwrite
    } else {
        Write-Fail "Unsupported storage backend '$cacheBackend'. Use 'ftp' or 'azure'."
    }
}

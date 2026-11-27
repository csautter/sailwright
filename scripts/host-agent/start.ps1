#Requires -Version 7.3
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$PSNativeCommandArgumentPassing = 'Standard'

$pythonName = if ($IsWindows) { 'python.exe' } else { 'python3' }
$python = Get-Command $pythonName -CommandType Application -ErrorAction SilentlyContinue |
    Select-Object -First 1
if (-not $python) {
    [Console]::Error.WriteLine("host-agent: Python 3 is required on PATH as $pythonName")
    exit 1
}
& $python.Source -B (Join-Path $PSScriptRoot 'protocol.py') start @args
exit $LASTEXITCODE

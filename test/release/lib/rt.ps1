# Release test kit: helpers for deterministic, batched host checks on Windows.
# PowerShell 5.1 and 7 compatible. Dot-source from a phase script:
#
#   . "$PSScriptRoot\lib\rt.ps1"
#   Initialize-RtPhase cli
#   Invoke-RtCheck -Id CLI-02 -Desc '--version' -Match 'local-' -File $Bin -Arguments '--version'
#   Complete-RtPhase
#
# Each check writes <phase>\logs\<ID>.log and one line to <phase>\results.tsv
# (ID, STATUS, SECONDS, DESCRIPTION, DETAIL). Logs are redacted: JSON
# "password" values and `vm info` password lines never reach them.

$script:RtKitDir = Split-Path -Parent $PSScriptRoot
$script:RtRepoRoot = (Resolve-Path (Join-Path $script:RtKitDir '..\..')).Path

function Get-RtHost { ($env:COMPUTERNAME).ToLowerInvariant() }

function Initialize-RtPhase([string]$Phase) {
    if (-not $env:RT_RUN_DIR) {
        $env:RT_RUN_DIR = Join-Path $script:RtKitDir ("out\{0}-{1}" -f (Get-RtHost), (Get-Date -Format yyyyMMdd))
    }
    $script:RtPhase = $Phase
    $script:RtPhaseDir = Join-Path $env:RT_RUN_DIR $Phase
    $script:RtResults = Join-Path $script:RtPhaseDir 'results.tsv'
    New-Item -ItemType Directory -Force -Path (Join-Path $script:RtPhaseDir 'logs') | Out-Null
    Set-Content -Path $script:RtResults -Value $null -Encoding UTF8
    $script:RtStarted = Get-Date
    Write-Host "== phase $Phase  (results: $script:RtPhaseDir)"
}

function Add-RtResult([string]$Id, [string]$Status, [int]$Seconds, [string]$Desc, [string]$Detail = '') {
    $detail = ($Detail -replace "[`t`r`n]", ' ')
    if ($detail.Length -gt 300) { $detail = $detail.Substring(0, 300) }
    Add-Content -Path $script:RtResults -Value ("{0}`t{1}`t{2}`t{3}`t{4}" -f $Id, $Status, $Seconds, $Desc, $detail) -Encoding UTF8
    $suffix = if ($detail) { "  -- $($detail.Substring(0, [Math]::Min(160, $detail.Length)))" } else { '' }
    Write-Host ("  {0,-8} {1,-6} {2,4}s  {3}{4}" -f $Id, $Status, $Seconds, $Desc, $suffix)
}

function Protect-RtText([string]$Text) {
    if ($null -eq $Text) { return '' }
    $Text = [regex]::Replace($Text, '("password"\s*:\s*")[^"]*"', '$1***"')
    $Text = [regex]::Replace($Text, '(?m)^(Password: )[^*].*$', '$1***REDACTED***')
    [regex]::Replace($Text, '(ansible_password[=:]\s*)\S+', '$1***')
}

# ConvertTo-RtArgString quotes arguments the way CommandLineToArgvW parses them.
function ConvertTo-RtArgString([string[]]$Arguments) {
    $parts = foreach ($arg in $Arguments) {
        if ($arg -ne '' -and $arg -notmatch '[\s"]') { $arg; continue }
        $builder = New-Object System.Text.StringBuilder
        [void]$builder.Append('"')
        $backslashes = 0
        foreach ($ch in $arg.ToCharArray()) {
            if ($ch -eq '\') { $backslashes++; continue }
            if ($ch -eq '"') { [void]$builder.Append('\' * ($backslashes * 2 + 1)); [void]$builder.Append('"') }
            else { [void]$builder.Append('\' * $backslashes); [void]$builder.Append($ch) }
            $backslashes = 0
        }
        [void]$builder.Append('\' * ($backslashes * 2))
        [void]$builder.Append('"')
        $builder.ToString()
    }
    $parts -join ' '
}

# Invoke-RtProcess runs File with Arguments and Env (hashtable), stdin closed.
# Returns @{ ExitCode; Stdout; Stderr; TimedOut }.
function Invoke-RtProcess([string]$File, [string[]]$Arguments = @(), [hashtable]$Env = @{}, [int]$TimeoutSec = 900) {
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $File
    $psi.Arguments = ConvertTo-RtArgString $Arguments
    $psi.UseShellExecute = $false
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.RedirectStandardInput = $true
    $psi.CreateNoWindow = $true
    $psi.WorkingDirectory = $script:RtRepoRoot
    foreach ($key in $Env.Keys) { $psi.EnvironmentVariables[$key] = [string]$Env[$key] }
    $process = [System.Diagnostics.Process]::Start($psi)
    $process.StandardInput.Close()
    $stdout = $process.StandardOutput.ReadToEndAsync()
    $stderr = $process.StandardError.ReadToEndAsync()
    $timedOut = -not $process.WaitForExit($TimeoutSec * 1000)
    if ($timedOut) {
        & taskkill.exe /T /F /PID $process.Id 2>$null | Out-Null
        $process.WaitForExit(30000) | Out-Null
    }
    $process.WaitForExit()
    @{ ExitCode = if ($timedOut) { 124 } else { $process.ExitCode }; Stdout = $stdout.Result; Stderr = $stderr.Result; TimedOut = $timedOut }
}

# Invoke-RtCheck writes nothing to the pipeline; $script:RtLastPassed tells
# whether the check passed. -ExpectExit N (default 0) | -AnyExit | -NonZero, -Match/-NoMatch
# regexes (.NET, multiline), -Json { param($d) ... } over the parsed stdout.
function Invoke-RtCheck {
    param(
        [Parameter(Mandatory)] [string]$Id,
        [Parameter(Mandatory)] [string]$Desc,
        [Parameter(Mandatory)] [string]$File,
        [string[]]$Arguments = @(),
        [hashtable]$Env = @{},
        [int]$ExpectExit = 0,
        [switch]$AnyExit,
        [switch]$NonZero,
        [string[]]$Match = @(),
        [string[]]$NoMatch = @(),
        [scriptblock]$Json,
        [int]$TimeoutSec = 900
    )
    if ($Arguments -contains '--show-password') {
        Add-RtResult $Id FAIL 0 $Desc 'refusing to log a command with --show-password'
        $script:RtLastPassed = $false
        return
    }
    $started = Get-Date
    $result = Invoke-RtProcess -File $File -Arguments $Arguments -Env $Env -TimeoutSec $TimeoutSec
    $all = "$($result.Stdout)`n$($result.Stderr)"
    $log = Join-Path $script:RtPhaseDir "logs\$Id.log"
    $header = "> $File $(ConvertTo-RtArgString $Arguments)`n# exit $($result.ExitCode)`n--- stdout`n"
    Set-Content -Path $log -Encoding UTF8 -Value ($header + (Protect-RtText $result.Stdout) + "`n--- stderr`n" + (Protect-RtText $result.Stderr))
    $problems = New-Object System.Collections.Generic.List[string]
    if ($result.TimedOut) { $problems.Add("timed out after ${TimeoutSec}s") }
    elseif (-not $AnyExit) {
        if ($NonZero) { if ($result.ExitCode -eq 0) { $problems.Add('expected a non-zero exit') } }
        elseif ($result.ExitCode -ne $ExpectExit) { $problems.Add("exit $($result.ExitCode), expected $ExpectExit") }
    }
    foreach ($re in $Match) { if ($all -notmatch "(?m)$re") { $problems.Add("missing /$re/") } }
    foreach ($re in $NoMatch) { if ($all -match "(?m)$re") { $problems.Add("unexpected /$re/") } }
    if ($Json) {
        try {
            $d = $result.Stdout | ConvertFrom-Json
            if (-not (& $Json $d)) { $problems.Add("json check failed: $Json") }
        } catch { $problems.Add("json check failed: $($_.Exception.Message)") }
    }
    $seconds = [int]((Get-Date) - $started).TotalSeconds
    $script:RtLastPassed = ($problems.Count -eq 0)
    if ($script:RtLastPassed) { Add-RtResult $Id PASS $seconds $Desc; return }
    Add-RtResult $Id FAIL $seconds $Desc ("{0} (log: logs\{1}.log)" -f ($problems -join '; '), $Id)
}

# Invoke-RtScript runs a PowerShell script block in a child powershell.exe so that
# Windows cmdlets (Get-VM, Get-Service) get the same logging and timeout handling.
function Invoke-RtScript {
    param([Parameter(Mandatory)] [string]$Id, [Parameter(Mandatory)] [string]$Desc, [Parameter(Mandatory)] [string]$Script,
        [string[]]$Match = @(), [string[]]$NoMatch = @(), [switch]$AnyExit, [int]$TimeoutSec = 900, [hashtable]$Env = @{})
    $shell = (Get-Process -Id $PID).Path
    $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes("`$ErrorActionPreference='Stop'; $Script"))
    Invoke-RtCheck -Id $Id -Desc $Desc -File $shell -Arguments @('-NoProfile', '-NonInteractive', '-EncodedCommand', $encoded) `
        -Match $Match -NoMatch $NoMatch -AnyExit:$AnyExit -TimeoutSec $TimeoutSec -Env $Env
}

function Add-RtPass([string]$Id, [string]$Desc, [string]$Detail = '') { Add-RtResult $Id PASS 0 $Desc $Detail }
function Add-RtFail([string]$Id, [string]$Desc, [string]$Detail = '') { Add-RtResult $Id FAIL 0 $Desc $Detail }
function Add-RtSkip([string]$Id, [string]$Desc, [string]$Reason) { Add-RtResult $Id SKIP 0 $Desc $Reason }
function Add-RtManual([string]$Id, [string]$Desc, [string]$Instructions) {
    Add-RtResult $Id MANUAL 0 $Desc $Instructions
    Add-Content -Path (Join-Path $script:RtPhaseDir 'manual.txt') -Value "${Id}: $Desc`n$Instructions`n" -Encoding UTF8
}
function Write-RtNote([string]$Text) {
    Write-Host "  note: $Text"
    Add-Content -Path (Join-Path $script:RtPhaseDir 'notes.txt') -Value $Text -Encoding UTF8
}

function Assert-RtDisposable {
    if ($env:RT_DISPOSABLE -ne '1') {
        throw 'This phase changes the machine beyond the test directories. Run it only on a disposable VM, with RT_DISPOSABLE=1 in release.env.ps1.'
    }
}

# Invoke-RtE2E runs deterministic E2E scenarios and keeps their reports.
function Invoke-RtE2E([string]$Id, [string]$Desc, [string]$Run, [hashtable]$Env = @{}, [string]$Test = 'TestScenarios') {
    $out = Join-Path $script:RtPhaseDir "e2e-$Id"
    New-Item -ItemType Directory -Force -Path $out | Out-Null
    $Env['SAILWRIGHT_E2E_OUT'] = $out
    $timeout = if ($env:RT_E2E_TIMEOUT) { [int]$env:RT_E2E_TIMEOUT } else { 86400 }
    Invoke-RtCheck -Id $Id -Desc $Desc -File (Get-Command go).Source -Arguments @('test', '-tags', 'e2e', './test/e2e', '-run', $Run, '-count=1', "-timeout=${timeout}s", '-v') `
        -Env $Env -TimeoutSec $timeout -Match @('^ok\s', "--- PASS: $Test") -NoMatch @('--- FAIL')
}

function Complete-RtPhase {
    $rows = @(Get-Content -Path $script:RtResults -Encoding UTF8 | Where-Object { $_ } | ForEach-Object { , ($_ -split "`t") })
    $count = { param($s) @($rows | Where-Object { $_[1] -eq $s }).Count }
    $pass = & $count PASS; $fail = & $count FAIL; $skip = & $count SKIP; $manual = & $count MANUAL
    $lines = @("# Phase $script:RtPhase on $(Get-RtHost)", '',
        "- finished: $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss zzz'), duration $([int]((Get-Date) - $script:RtStarted).TotalSeconds)s",
        "- result: $pass passed, $fail failed, $skip skipped, $manual manual", '',
        '| ID | Status | s | Check | Detail |', '| --- | --- | --- | --- | --- |')
    foreach ($row in $rows) { $lines += ('| ' + (($row | ForEach-Object { $_ -replace '\|', '\|' }) -join ' | ') + ' |') }
    $manualFile = Join-Path $script:RtPhaseDir 'manual.txt'
    if (Test-Path $manualFile) { $lines += @('', '## Manual steps', '', '```text') + (Get-Content $manualFile) + '```' }
    $summary = Join-Path $script:RtPhaseDir 'summary.md'
    Set-Content -Path $summary -Value $lines -Encoding UTF8
    Write-Host "== phase ${script:RtPhase}: $pass passed, $fail failed, $skip skipped, $manual manual  -> $summary"
    $script:RtPhaseFailed = ($fail -ne 0)
}

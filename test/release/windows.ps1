<#
Release test phases for Windows hosts. See test/release/README.md.

    powershell -NoProfile -ExecutionPolicy Bypass -File test\release\windows.ps1 <phase> [<phase>...]

Run from an elevated PowerShell in the checkout (Hyper-V cmdlets need it).
Each phase is one approval: read it once, run it, then read
test\release\out\<host>-<date>\<phase>\summary.md. Settings come from
test\release\release.env.ps1 (copy release.env.ps1.example). Phases never
print guest passwords and never touch VMs that existed before the run.
#>
param([Parameter(ValueFromRemainingArguments = $true)] [string[]]$Phases)

$ErrorActionPreference = 'Continue'
. (Join-Path $PSScriptRoot 'lib\rt.ps1')
$envFile = Join-Path $PSScriptRoot 'release.env.ps1'
if (Test-Path $envFile) { . $envFile }

function Get-Setting([string]$Name, [string]$Default = '') {
    $value = [Environment]::GetEnvironmentVariable($Name)
    if ($value) { $value } else { $Default }
}

$HostArch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$Repo = $script:RtRepoRoot
$Commit = (& git -C $Repo rev-parse --short HEAD 2>$null)
$Version = Get-Setting RT_VERSION "local-$Commit"
$BinDir = Get-Setting RT_BIN_DIR (Join-Path $Repo "dist\windows-$HostArch")
$Bin = Join-Path $BinDir 'sailwright.exe'
$Gw = Join-Path $BinDir 'sailwright-gateway.exe'
if (-not $env:RT_RUN_DIR) { $env:RT_RUN_DIR = Join-Path $script:RtKitDir ("out\{0}-{1}" -f (Get-RtHost), (Get-Date -Format yyyyMMdd)) }
$RunDir = $env:RT_RUN_DIR
$AppData = Get-Setting RT_APPDATA (Join-Path $RunDir 'appdata')
$Target = Get-Setting RT_TARGET 'ubuntu/server/amd64'
$GatewayPort = Get-Setting RT_GATEWAY_PORT '7399'
$Parts = $Target.Split('/')
$TOs, $TType, $TArch = $Parts[0], $Parts[1], $Parts[2]
$TargetArgs = @($TOs, '--arch', $TArch)
if ($TType) { $TargetArgs += @('--type', $TType) }
$HyperVName = Get-Setting RT_HYPERV_VM_NAME ("linux-{0}-{1}-packer" -f $TOs, $TType)
$Insecure = if ((Get-Setting RT_ALLOW_INSECURE_CREDENTIALS) -eq '1') { @('--allow-insecure-credentials') } else { @() }

function Get-AppDataEnv { if ($AppData -eq 'default') { @{} } else { @{ DEV_ALCHEMY_APP_DATA_DIR = $AppData } } }
function Get-AppDataRoot { if ($AppData -eq 'default') { Join-Path $env:LOCALAPPDATA 'dev-alchemy' } else { $AppData } }

# Sw runs a CLI check with the selected app data.
function Sw([string]$Id, [string]$Desc, [string[]]$Arguments, [hashtable]$Extra = @{}, [int]$TimeoutSec = 3600, [string[]]$Match = @(), [string[]]$NoMatch = @(), [switch]$NonZero, [switch]$AnyExit, [scriptblock]$Json) {
    $envs = Get-AppDataEnv
    foreach ($key in $Extra.Keys) { $envs[$key] = $Extra[$key] }
    Invoke-RtCheck -Id $Id -Desc $Desc -File $Bin -Arguments $Arguments -Env $envs -TimeoutSec $TimeoutSec -Match $Match -NoMatch $NoMatch -NonZero:$NonZero -AnyExit:$AnyExit -Json $Json
}

function Get-TargetField([string]$Command, [string]$Field) {
    $envs = Get-AppDataEnv
    $result = Invoke-RtProcess -File $Bin -Arguments @($Command, 'list', '--json') -Env $envs -TimeoutSec 600
    try {
        $listing = $result.Stdout | ConvertFrom-Json
        foreach ($t in $listing.targets) {
            if ($t.os -eq $TOs -and ([string]$t.type) -eq $TType -and $t.arch -eq $TArch) { return [string]$t.fields.$Field }
        }
    } catch { }
    ''
}

function Get-InstanceId {
    $result = Invoke-RtProcess -File $Bin -Arguments (@('vm', 'identity') + $TargetArgs) -Env (Get-AppDataEnv) -TimeoutSec 600
    try { [string]($result.Stdout | ConvertFrom-Json).instance_id } catch { '' }
}

function Get-E2EEnv {
    $envs = Get-AppDataEnv
    $envs['SAILWRIGHT_E2E_BIN'] = $Bin
    $envs['SAILWRIGHT_E2E_TARGET'] = $Target
    if ($env:RT_PULL_REF) { $envs['SAILWRIGHT_E2E_PULL_REF'] = $env:RT_PULL_REF }
    if ($Insecure.Count) { $envs['SAILWRIGHT_E2E_ALLOW_INSECURE_CREDENTIALS'] = '1' }
    $envs
}

function Assert-Bin { if (-not (Test-Path $Bin) -or -not (Test-Path $Gw)) { throw "Missing $Bin or $Gw; run the kit phase first (or set RT_BIN_DIR)." } }

function Test-Elevated {
    ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

# ---------------------------------------------------------------- preflight
function Invoke-PhasePreflight {
    Initialize-RtPhase preflight
    Invoke-RtScript PRE-01 'Windows edition and build' 'Get-CimInstance Win32_OperatingSystem | Format-List Caption,Version,BuildNumber,OSArchitecture'
    Invoke-RtScript PRE-02 'hardware' 'Get-CimInstance Win32_ComputerSystem | Format-List Model,NumberOfLogicalProcessors,TotalPhysicalMemory,HypervisorPresent; Get-CimInstance Win32_Processor | Format-List Name,NumberOfCores,VirtualizationFirmwareEnabled'
    if (Test-Elevated) { Add-RtPass PRE-03 'elevated PowerShell' } else { Add-RtFail PRE-03 'elevated PowerShell' 'Hyper-V checks need an elevated shell' }
    Invoke-RtScript PRE-04 'Hyper-V service and Default Switch (informational)' -AnyExit 'Get-Service vmms | Format-List Name,Status; Get-VMSwitch | Format-Table Name,SwitchType; Get-NetIPAddress -InterfaceAlias "vEthernet (Default Switch)" -AddressFamily IPv4 | Format-Table IPAddress,PrefixLength'
    Invoke-RtScript PRE-05 'existing Hyper-V VMs (the kit never touches them)' -AnyExit 'Get-VM | Format-Table Name,State,Generation'
    Invoke-RtScript PRE-06 'free disk' 'Get-PSDrive -PSProvider FileSystem | Format-Table Name,Used,Free'
    Invoke-RtCheck -Id PRE-07 -Desc 'checkout commit' -File (Get-Command git).Source -Arguments @('-C', $Repo, 'log', '-1', '--format=%H %d %s')
    # The candidate is the unreleased origin/main; a branch based on it may differ in test/ and docs/ only.
    $git = (Get-Command git).Source
    Invoke-RtCheck -Id PRE-08 -Desc 'fetch origin/main' -File $git -Arguments @('-C', $Repo, 'fetch', '--quiet', 'origin', 'main') -TimeoutSec 120
    Invoke-RtCheck -Id PRE-08b -Desc 'checkout contains the current origin/main' -File $git -Arguments @('-C', $Repo, 'merge-base', '--is-ancestor', 'origin/main', 'HEAD')
    Invoke-RtCheck -Id PRE-08c -Desc 'product code equals origin/main (only test/ and docs/ may differ)' -File $git -Arguments @('-C', $Repo, 'diff', '--name-only', 'origin/main', 'HEAD', '--', '.', ':(exclude)test', ':(exclude)docs') -NoMatch @('.')
    Invoke-RtCheck -Id PRE-08f -Desc 'no uncommitted product changes (the binary is built from the working tree)' -File $git -Arguments @('-C', $Repo, 'status', '--porcelain', '--', '.', ':(exclude)test', ':(exclude)docs') -NoMatch @('.')
    Invoke-RtCheck -Id PRE-08d -Desc 'origin/main is unreleased (commits since the latest tag)' -File $git -Arguments @('-C', $Repo, 'describe', '--tags', '--long', 'origin/main') -Match @('-[1-9][0-9]*-g[0-9a-f]+\s*$')
    if ($env:RT_EXPECT_COMMIT) {
        Invoke-RtCheck -Id PRE-08e -Desc 'origin/main is still the commit all plans of this round test' -File $git -Arguments @('-C', $Repo, 'rev-parse', 'origin/main') -Match @("^$($env:RT_EXPECT_COMMIT)")
    } else { Write-RtNote "RT_EXPECT_COMMIT not set: testing origin/main at $(& git -C $Repo rev-parse --short origin/main)" }
    foreach ($tool in 'go', 'git', 'python', 'packer', 'vagrant', 'choco', 'ssh', 'oscdimg', 'cosign') {
        $cmd = Get-Command $tool -ErrorAction SilentlyContinue
        if ($cmd) { Add-RtPass "PRE-T-$tool" "tool $tool" $cmd.Source } else { Add-RtSkip "PRE-T-$tool" "tool $tool" 'not on PATH' }
    }
    if ($env:CYGWIN_BASH_PATH) { Add-RtPass PRE-09 'Cygwin bash for WinRM tests' $env:CYGWIN_BASH_PATH } else { Add-RtSkip PRE-09 'Cygwin bash for WinRM tests' 'CYGWIN_BASH_PATH not set' }
    if (Test-Path $Bin) {
        Invoke-RtScript PRE-10 'binary under test' "Get-FileHash '$Bin','$Gw' -Algorithm SHA256 | Format-Table Hash,Path; & '$Bin' version"
        foreach ($mode in 'default', 'isolated') {
            $envs = if ($mode -eq 'isolated') { Get-AppDataEnv } else { @{} }
            Invoke-RtCheck -Id "PRE-11-$mode" -Desc "no managed VM exists ($mode app data)" -File $Bin -Arguments @('destroy', 'list', '--json') -Env $envs `
                -Json { param($d) -not ($d.targets | Where-Object { $_.fields.state -ne 'missing' }) }
        }
    } else { Add-RtSkip PRE-10 'binary under test' 'run the kit phase first' }
    if ($env:RT_DISPOSABLE -eq '1') { Add-RtPass PRE-12 'operator declared this machine disposable' } else { Write-RtNote 'RT_DISPOSABLE is not set: host-modifying phases will refuse to run' }
    Complete-RtPhase
}

# ---------------------------------------------------------------- kit
function Invoke-PhaseKit {
    Initialize-RtPhase kit
    New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
    $go = (Get-Command go).Source
    Invoke-RtCheck -Id KIT-01 -Desc "build CLI $Version" -File $go -TimeoutSec 1800 -Arguments @('build', '-trimpath', '-ldflags', "-s -w -X github.com/csautter/dev-alchemy/cmd/cmd.version=$Version", '-o', $Bin, './cmd/main.go')
    Invoke-RtCheck -Id KIT-02 -Desc "build gateway $Version" -File $go -TimeoutSec 1800 -Arguments @('build', '-trimpath', '-ldflags', "-s -w -X main.version=$Version", '-o', $Gw, './cmd/sailwright-gateway')
    Invoke-RtCheck -Id KIT-03 -Desc 'CLI reports the version' -File $Bin -Arguments @('--version') -Match @([regex]::Escape($Version))
    Invoke-RtCheck -Id KIT-04 -Desc 'unit tests' -File $go -TimeoutSec 3600 -Arguments @('test', './cmd/...', './internal/...', './pkg/...', '-count=1')
    Invoke-RtCheck -Id KIT-05 -Desc 'E2E runner self-tests (fake CLI)' -File $go -TimeoutSec 1800 -Arguments @('test', './test/e2e/...', '-count=1')
    $python = Get-Command python -ErrorAction SilentlyContinue
    if ($python) {
        Invoke-RtCheck -Id KIT-06 -Desc 'self-update fixture tests on Windows (install.ps1, local release server)' -File $python.Source -TimeoutSec 1800 -Arguments @('-I', '-m', 'unittest', 'discover', '-s', 'test/selfupdate', '-v')
    } else { Add-RtSkip KIT-06 'self-update fixture tests' 'python not on PATH' }
    if (Get-Module -ListAvailable Pester | Where-Object { $_.Version.Major -ge 5 }) {
        Invoke-RtScript KIT-07 'install.ps1 Pester tests' "Invoke-Pester -Path '$Repo\test\install\install.Tests.ps1' -CI"
    } else { Add-RtSkip KIT-07 'install.ps1 Pester tests' 'Pester 5 not installed (Install-Module Pester -Scope CurrentUser)' }
    Invoke-RtScript KIT-08 'binary checksums' "Get-FileHash '$Bin','$Gw' -Algorithm SHA256 | Format-Table Hash,Path"
    Complete-RtPhase
}

# ---------------------------------------------------------------- cli (read-only)
function Invoke-PhaseCli {
    Assert-Bin
    Initialize-RtPhase cli
    $cfg = Join-Path $script:RtPhaseDir 'config'
    Remove-Item -Recurse -Force $cfg, (Join-Path $script:RtPhaseDir 'gateway-state') -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force -Path $cfg | Out-Null
    $c = @{ DEV_ALCHEMY_CONFIG_DIR = $cfg }
    Invoke-RtE2E CLI-01 'E2E smoke and cli-readonly scenarios' 'TestScenarios/(smoke|cli-readonly)$' @{ SAILWRIGHT_E2E_BIN = $Bin }
    Invoke-RtCheck -Id CLI-02 -Desc '--version prints the build version' -File $Bin -Arguments @('--version') -Match @([regex]::Escape($Version))
    Invoke-RtCheck -Id CLI-03 -Desc 'development build refuses self-update' -File $Bin -Arguments @('update', '--check') -ExpectExit 1 -Match @('development builds cannot self-update')
    Invoke-RtCheck -Id CLI-04 -Desc 'auto-check can be enabled' -File $Bin -Arguments @('update', '--auto-check', 'on') -Env $c
    Invoke-RtCheck -Id CLI-04b -Desc 'version shows enabled checks' -File $Bin -Arguments @('version') -Env $c -Match @('Automatic update checks: true')
    Invoke-RtCheck -Id CLI-05 -Desc 'interval below 15m is rejected' -File $Bin -Arguments @('update', '--auto-check-interval', '5m') -Env $c -NonZero -Match @('15m')
    Set-Content -Path (Join-Path $cfg 'update.json') -Value '{not json' -Encoding ASCII
    Invoke-RtCheck -Id CLI-07 -Desc 'invalid update.json is reported' -File $Bin -Arguments @('version') -Env $c -Match @('invalid settings')
    Invoke-RtCheck -Id CLI-08 -Desc 'auto-check off repairs invalid settings' -File $Bin -Arguments @('update', '--auto-check', 'off') -Env $c
    Invoke-RtCheck -Id CLI-08b -Desc 'version shows disabled checks' -File $Bin -Arguments @('version') -Env $c -Match @('Automatic update checks: false')
    Invoke-RtCheck -Id CLI-09 -Desc 'update source override outside loopback is refused' -File $Bin -Arguments @('update', '--check') -Env @{ DEV_ALCHEMY_CONFIG_DIR = $cfg; SAILWRIGHT_UPDATE_BASE_URL = 'https://example.com' } -NonZero
    Set-Content -Path (Join-Path $cfg 'config.yaml') -Value "security:`n  allow_insecure_credentials: maybe" -Encoding ASCII
    Invoke-RtCheck -Id CLI-10 -Desc 'invalid config.yaml blocks start even with the override (missing VM)' -File $Bin -Arguments (@('start') + $TargetArgs + '--allow-insecure-credentials') `
        -Env @{ DEV_ALCHEMY_CONFIG_DIR = $cfg; DEV_ALCHEMY_APP_DATA_DIR = (Join-Path $script:RtPhaseDir 'appdata') } -NonZero -Match @('config\.yaml')
    Remove-Item (Join-Path $cfg 'config.yaml')
    Invoke-RtCheck -Id CLI-11 -Desc 'build listing shows NET and CREDS columns' -File $Bin -Arguments @('build', 'list') -Match @('NET', 'CREDS')
    Invoke-RtCheck -Id CLI-12 -Desc 'install help documents opt-in Hyper-V and Vagrant' -File $Bin -Arguments @('install', '--help') -Match @('--hyperv', '--vagrant')
    $gws = Join-Path $script:RtPhaseDir 'gateway-state'
    Invoke-RtCheck -Id CLI-13 -Desc 'gateway token is created' -File $Gw -Arguments @('--state-dir', $gws, 'token')
    Invoke-RtScript CLI-14 'token ACL grants only the current user and SYSTEM/Administrators' "icacls '$gws\token'" -NoMatch @('Everyone', 'BUILTIN\\Users', 'Authenticated Users')
    Invoke-RtCheck -Id CLI-15 -Desc 'connect --check without a gateway fails clearly' -File $Gw -Arguments @('connect', '--check') -Env @{ SAILWRIGHT_GATEWAY_URL = 'http://127.0.0.1:9/mcp'; SAILWRIGHT_GATEWAY_DIR = (Join-Path $script:RtPhaseDir 'none') } -NonZero
    Invoke-RtCheck -Id CLI-16 -Desc 'gateway metadata reports lock protocol 2' -File $Bin -Arguments @('--gateway-metadata') -Match @('"lock_version":2')
    Complete-RtPhase
}

# ---------------------------------------------------------------- selfupdate
function Install-ReleaseInto([string]$Tag, [string]$Dir) {
    # install.ps1 adds its directory to the user PATH; restore the PATH afterwards.
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $url = "https://github.com/csautter/sailwright/releases/download/$Tag/install.ps1"
    Invoke-RtScript "SU-install-$Tag" "install.ps1 installs $Tag into $Dir" "& ([scriptblock]::Create((Invoke-RestMethod '$url'))) -Version '$Tag' -InstallDir '$Dir'" -TimeoutSec 900
    [Environment]::SetEnvironmentVariable('Path', $userPath, 'User')
}

function Invoke-PhaseSelfupdate {
    Assert-Bin
    Initialize-RtPhase selfupdate
    $c = @{ DEV_ALCHEMY_CONFIG_DIR = (Join-Path $script:RtPhaseDir 'config') }
    if ($env:RT_RELEASE_TAG) {
        $dir = Join-Path $script:RtPhaseDir 'release-install'
        Remove-Item -Recurse -Force $dir -ErrorAction SilentlyContinue
        Install-ReleaseInto $env:RT_RELEASE_TAG $dir
        Invoke-RtCheck -Id SU-02 -Desc 'installed release reports its version' -File "$dir\sailwright.exe" -Arguments @('--version') -Match @([regex]::Escape($env:RT_RELEASE_TAG.TrimStart('v')))
        Invoke-RtCheck -Id SU-04 -Desc 'update --check: 0 up to date or 2 newer' -File "$dir\sailwright.exe" -Arguments @('update', '--check') -Env $c -AnyExit -Match @('is up to date|→')
    } else { Add-RtSkip SU-01 'install released version' 'RT_RELEASE_TAG not set (pre-release run)' }
    if ($env:RT_UPDATE_FROM) {
        $old = Join-Path $script:RtPhaseDir 'update-from'
        Remove-Item -Recurse -Force $old -ErrorAction SilentlyContinue
        Install-ReleaseInto $env:RT_UPDATE_FROM $old
        Invoke-RtCheck -Id SU-11 -Desc 'update --check reports an available update (exit 2)' -File "$old\sailwright.exe" -Arguments @('update', '--check') -Env $c -ExpectExit 2
        # A running gateway keeps its executable locked; the updater must cope and say so.
        $state = Join-Path $script:RtPhaseDir 'old-gateway-state'
        $gwProcess = Start-Process -FilePath "$old\sailwright-gateway.exe" -ArgumentList @('--state-dir', $state, 'serve', '--binary', "$old\sailwright.exe", '--bypass-approvals', '--listen', "127.0.0.1:$GatewayPort") -PassThru -WindowStyle Hidden
        Start-Sleep -Seconds 5
        Invoke-RtCheck -Id SU-14 -Desc 'update --yes while the old gateway runs' -File "$old\sailwright.exe" -Arguments @('update', '--yes') -Env $c -TimeoutSec 900 -Match @('Installed Sailwright', 'restart it')
        Stop-Process -Id $gwProcess.Id -Force -ErrorAction SilentlyContinue
        Invoke-RtCheck -Id SU-15 -Desc 'updated CLI reports a newer version' -File "$old\sailwright.exe" -Arguments @('--version') -NoMatch @([regex]::Escape($env:RT_UPDATE_FROM.TrimStart('v')) + '\s*$')
        Invoke-RtCheck -Id SU-16 -Desc 'update --check is current afterwards' -File "$old\sailwright.exe" -Arguments @('update', '--check') -Env $c -ExpectExit 0
        Invoke-RtScript SU-17 '.old backups are removed on a later run' "Get-ChildItem '$old' -Filter *.old | Select-Object -ExpandProperty Name" -NoMatch @('\.old')
    } else { Add-RtSkip SU-10 'real update from an older release' 'RT_UPDATE_FROM not set; needs a release that already has update (>= v0.27.0)' }
    Complete-RtPhase
}

# ---------------------------------------------------------------- install (opt-in Hyper-V/Vagrant)
function Invoke-PhaseInstall {
    Assert-Bin
    Initialize-RtPhase install
    if ($env:RT_DISPOSABLE -ne '1' -and $env:RT_ALLOW_INSTALL -ne '1') { throw 'install changes the host: set RT_DISPOSABLE=1 (VM) or RT_ALLOW_INSTALL=1 (physical test host)' }
    Invoke-RtCheck -Id INS-01 -Desc 'non-interactive install skips Hyper-V and Vagrant with a hint' -File $Bin -Arguments @('install') -TimeoutSec 7200 -Match @('--hyperv', '--vagrant')
    Invoke-RtCheck -Id INS-02 -Desc 'second install is idempotent' -File $Bin -Arguments @('install', '--hyperv=false', '--vagrant=false') -TimeoutSec 7200
    Invoke-RtCheck -Id INS-03 -Desc 'Cygwin with Ansible is found' -File $Bin -Arguments @('provision', 'local', '--check', '--yes') -TimeoutSec 3600 -AnyExit -NoMatch @('(?i)cygwin.*not found')
    if ($env:RT_WITH_HYPERV -eq '1') {
        Invoke-RtCheck -Id INS-10 -Desc 'install --hyperv --vagrant (may request a restart)' -File $Bin -Arguments @('install', '--hyperv', '--vagrant') -TimeoutSec 7200 -AnyExit -Match @('(?i)hyper-v')
        Invoke-RtScript INS-11 'ADK installer signature was checked' "Get-Content '$($script:RtPhaseDir)\logs\INS-10.log'" -AnyExit -Match @('(?i)signature|authenticode|oscdimg')
        Add-RtManual INS-12 'restart, then verify' 'If INS-10 reported a pending restart: restart Windows, open an elevated PowerShell and run: test\release\windows.ps1 hyperv-verify'
    } else { Add-RtSkip INS-10 'opt-in Hyper-V setup' 'RT_WITH_HYPERV not set' }
    Complete-RtPhase
}

function Invoke-PhaseHypervVerify {
    Assert-Bin
    Initialize-RtPhase hyperv-verify
    Invoke-RtCheck -Id HV-01 -Desc 'install --hyperv verifies the active setup' -File $Bin -Arguments @('install', '--hyperv', '--vagrant') -TimeoutSec 7200 -Match @('(?i)vmms|management service', '(?i)default switch')
    Invoke-RtScript HV-02 'vmms runs and the Default Switch exists' '(Get-Service vmms).Status; (Get-VMSwitch -Name "Default Switch").SwitchType' -Match @('Running', 'Internal')
    Invoke-RtScript HV-03 'oscdimg on the machine PATH (new process)' '$env:Path = [Environment]::GetEnvironmentVariable("Path","Machine"); (Get-Command oscdimg).Source' -Match @('oscdimg')
    Invoke-RtScript HV-04 'oscdimg carries a valid Microsoft signature' '$env:Path = [Environment]::GetEnvironmentVariable("Path","Machine"); $s = Get-AuthenticodeSignature (Get-Command oscdimg).Source; $s.Status; $s.SignerCertificate.Subject' -Match @('Valid', 'Microsoft')
    Invoke-RtScript HV-05 'Vagrant installed in the pinned version' '$env:Path = [Environment]::GetEnvironmentVariable("Path","Machine"); vagrant --version' -Match @('Vagrant 2\.')
    Complete-RtPhase
}

# ---------------------------------------------------------------- provision-local
function Invoke-RecapCheck([string]$Id, [string]$Desc, [string]$Kind, [string[]]$Arguments) {
    $python = (Get-Command python).Source
    Invoke-RtCheck -Id $Id -Desc $Desc -File $Bin -Arguments $Arguments -TimeoutSec 10800 -AnyExit
    $log = Join-Path $script:RtPhaseDir "logs\$Id.log"
    Invoke-RtCheck -Id "$Id-recap" -Desc "$Desc (recap)" -File $python -Arguments @('-I', "$Repo\test\localprovision\scripts\check_recap.py", $log, $Kind)
}

function Invoke-PhaseProvisionLocal {
    Assert-RtDisposable
    Assert-Bin
    Initialize-RtPhase provision-local
    $playbook = Get-Setting RT_PLAYBOOK "$Repo\playbooks\setup.yml"
    Invoke-RecapCheck PL-01 'WinRM: setup in check mode' check @('provision', 'local', '--check', '--yes', '--playbook', $playbook)
    Invoke-RecapCheck PL-02 'WinRM: setup' apply @('provision', 'local', '--yes', '--playbook', $playbook)
    Invoke-RecapCheck PL-03 'WinRM: second run changes nothing' idempotency @('provision', 'local', '--yes', '--playbook', $playbook)
    Invoke-RecapCheck PL-04 'SSH transport in check mode, with forced cleanup' check @('provision', 'local', '--proto', 'ssh', '--check', '--yes', '--force-ssh-uninstall', '--playbook', $playbook)
    Invoke-RtScript PL-05 'WinRM and transient SSH setup removed afterwards' '(Get-Service sshd -ErrorAction SilentlyContinue).Status; Get-NetFirewallRule -DisplayName "*OpenSSH*" -ErrorAction SilentlyContinue | Format-Table DisplayName,Enabled' -AnyExit
    Add-RtManual PL-06 'interactive confirmation without --yes' "In a normal (non-elevated) PowerShell: & '$Bin' provision local --check ; expected: the confirmation prompt explains the host change, UAC appears, the run ends with failed=0."
    Complete-RtPhase
}

# ---------------------------------------------------------------- build (physical Hyper-V host)
function Invoke-PhaseBuild {
    Assert-Bin
    Initialize-RtPhase build
    $sampler = Join-Path $script:RtPhaseDir 'logs\BLD-02.samples.log'
    $job = Start-Job -ScriptBlock { param($file) while ($true) { Get-Process vmconnect, VirtualBox, packer, vagrant -ErrorAction SilentlyContinue | ForEach-Object { "$(Get-Date -Format HH:mm:ss) $($_.ProcessName) $($_.Id)" } | Add-Content $file; Start-Sleep 10 } } -ArgumentList $sampler
    Sw BLD-01 "full headless Hyper-V build of $Target" (@('build') + $TargetArgs + @('--engine', 'hyperv', '--headless')) -TimeoutSec 21600
    Stop-Job $job; Remove-Job $job
    Invoke-RtScript BLD-02 'headless: no vmconnect window during the build' "Get-Content '$sampler' -ErrorAction SilentlyContinue" -AnyExit -NoMatch @('vmconnect')
    $state = Get-TargetField build build
    if ($state -eq 'exists') { Add-RtPass BLD-03 'artifact is complete' } else { Add-RtFail BLD-03 'artifact is complete' "build state: $state" }
    Invoke-RtScript BLD-04 'staged .box promoted with a completion marker, no pending marker' "Get-ChildItem -Recurse '$(Get-AppDataRoot)' -Include '*.box','*.sailwright-complete','*.sailwright-pending*' | Select-Object -ExpandProperty FullName" -Match @('\.box', 'sailwright-complete') -NoMatch @('sailwright-pending')
    Sw BLD-05 'unchanged second build is skipped' (@('build') + $TargetArgs + @('--engine', 'hyperv', '--headless')) -TimeoutSec 1800
    if ((Get-TargetField build build) -eq 'exists') { Add-RtPass BLD-06 'artifact still complete after the skipped build' } else { Add-RtFail BLD-06 'artifact still complete after the skipped build' }
    $cancel = Get-Setting RT_CANCEL_TARGET 'ubuntu/desktop/amd64/hyperv'
    $envs = Get-AppDataEnv; $envs['SAILWRIGHT_E2E_BIN'] = $Bin; $envs['SAILWRIGHT_E2E_TARGET'] = $cancel
    Invoke-RtE2E BLD-07 "E2E build-cancel on $cancel (direct executor)" 'TestScenarios/build-cancel$' $envs
    Invoke-RtScript BLD-08 'processes left by the cancelled build (known Windows limitation: no Job Object)' 'Get-Process packer*, vagrant, ruby -ErrorAction SilentlyContinue | Format-Table ProcessName,Id,StartTime' -AnyExit
    if ($env:RT_WITH_WINDOWS -eq '1') {
        Sw BLD-10 'windows11 Hyper-V build reaches WinRM within the 4 h window (OOBE, DVD key press)' @('build', 'windows11', '--arch', 'amd64', '--engine', 'hyperv', '--headless') -TimeoutSec 21600
        Invoke-RtScript BLD-11 'windows11 .box has a completion marker' "Get-ChildItem -Recurse '$(Get-AppDataRoot)' -Include '*windows11*.sailwright-complete' | Select-Object -ExpandProperty FullName" -Match @('sailwright-complete')
    } else { Add-RtSkip BLD-10 'windows11 Hyper-V build' 'RT_WITH_WINDOWS not set' }
    Complete-RtPhase
}

# ---------------------------------------------------------------- vm (physical Hyper-V host)
function Invoke-PhaseVm {
    Assert-Bin
    Initialize-RtPhase vm
    Invoke-RtE2E VM-01 'E2E vm-access-cli: identity, info, two create/destroy cycles' 'TestScenarios/vm-access-cli$' (Get-E2EEnv)
    Invoke-RtE2E VM-02 'E2E vm-lifecycle and destroy-idempotent' 'TestScenarios/(vm-lifecycle|destroy-idempotent)$' (Get-E2EEnv)

    Sw VM-10 'create for identity checks' (@('create') + $TargetArgs + $Insecure)
    $id1 = Get-InstanceId
    Invoke-RtScript VM-11 'identity equals (Get-VM).Id while stopped' "(Get-VM -Name '$HyperVName').Id.Guid" -Match @("^$id1")
    Invoke-RtScript VM-12 'virtualization extensions exposed by default' "(Get-VMProcessor -VMName '$HyperVName').ExposeVirtualizationExtensions" -AnyExit -Match @('True|False')
    Sw VM-13 'start' (@('start') + $TargetArgs + $Insecure)
    Invoke-RtScript VM-14 'identity unchanged while running' "(Get-VM -Name '$HyperVName').Id.Guid" -Match @("^$id1")
    Sw VM-15 'vm info: host-reachable IPv4 on the Default Switch, NAT, masked password' (@('vm', 'info') + $TargetArgs) -TimeoutSec 600 -Match @('Network: nat', 'Port: 22', 'Password: \*{8}', 'Address: \d+\.\d+\.\d+\.\d+')
    Invoke-RtScript VM-16 'guest address is inside the Default Switch subnet' @"
`$info = & '$Bin' vm info $($TargetArgs -join ' ') | Select-String '^Address: (.+)$' | ForEach-Object { `$_.Matches[0].Groups[1].Value }
`$hostIp = Get-NetIPAddress -InterfaceAlias 'vEthernet (Default Switch)' -AddressFamily IPv4
`$mask = [uint32]([math]::Pow(2, 32) - [math]::Pow(2, 32 - `$hostIp.PrefixLength))
function ToInt([string]`$a) { `$b = ([Net.IPAddress]`$a).GetAddressBytes(); [Array]::Reverse(`$b); [BitConverter]::ToUInt32(`$b, 0) }
"guest `$info host `$(`$hostIp.IPAddress)/`$(`$hostIp.PrefixLength)"
if (((ToInt `$info) -band `$mask) -ne ((ToInt `$hostIp.IPAddress) -band `$mask)) { throw 'guest is outside the Default Switch subnet' }
"@ -Env (Get-AppDataEnv)
    $nested = Join-Path (Get-AppDataRoot) 'vagrant'
    Invoke-RtScript VM-17 'nested-virtualization fallback marker (present only if Hyper-V refused nested virtualization)' "Get-ChildItem -Recurse '$nested' -Filter 'sailwright-nested-virtualization-unsupported' -ErrorAction SilentlyContinue | Select-Object -ExpandProperty FullName; 'checked'" -AnyExit
    # Lock contention between two CLI processes.
    $stopLog = Join-Path $script:RtPhaseDir 'logs\VM-20.stop.log'
    $envBackup = $env:DEV_ALCHEMY_APP_DATA_DIR
    if ($AppData -ne 'default') { $env:DEV_ALCHEMY_APP_DATA_DIR = $AppData }
    $stop = Start-Process -FilePath $Bin -ArgumentList (@('stop') + $TargetArgs) -PassThru -NoNewWindow -RedirectStandardOutput $stopLog -RedirectStandardError "$stopLog.err"
    $env:DEV_ALCHEMY_APP_DATA_DIR = $envBackup
    Start-Sleep -Seconds 3
    Sw VM-20 'concurrent start is refused while stop holds the lock (lock protocol 2)' (@('start') + $TargetArgs + $Insecure) -Extra @{ SAILWRIGHT_LOCK_TIMEOUT = '2s' } -NonZero -Match @('resource busy', 'held by pid \d+')
    $stop.WaitForExit()
    if ($stop.ExitCode -eq 0) { Add-RtPass VM-21 'stop finished normally' } else { Add-RtFail VM-21 'stop finished normally' "exit $($stop.ExitCode)" }
    Sw VM-22 'destroy' (@('destroy') + $TargetArgs)
    Invoke-RtScript VM-23 'Hyper-V VM is gone' "if (Get-VM -Name '$HyperVName' -ErrorAction SilentlyContinue) { throw 'still exists' }; 'gone'"
    Sw VM-24 'recreate with VAGRANT_HYPERV_NESTED_VIRTUALIZATION=false' (@('create') + $TargetArgs + $Insecure) -Extra @{ VAGRANT_HYPERV_NESTED_VIRTUALIZATION = 'false' }
    $id2 = Get-InstanceId
    if ($id2 -and $id2 -ne $id1) { Add-RtPass VM-25 'instance id changed after recreate' "$id1 -> $id2" } else { Add-RtFail VM-25 'instance id changed after recreate' "'$id1' -> '$id2'" }
    Invoke-RtScript VM-26 'virtualization extensions are off when nested virtualization is disabled' "(Get-VMProcessor -VMName '$HyperVName').ExposeVirtualizationExtensions" -Match @('False')
    Sw VM-27 'cleanup destroy' (@('destroy') + $TargetArgs)
    if ((Get-TargetField destroy state) -eq 'missing') { Add-RtPass VM-28 'target is missing at the end' } else { Add-RtFail VM-28 'target is missing at the end' }
    if ($env:RT_FULL_MATRIX -eq '1') {
        $envs = Get-AppDataEnv; $envs['SAILWRIGHT_E2E_BIN'] = $Bin
        Invoke-RtE2E VM-30 'E2E vm-lifecycle-windows: every VM option of this host' 'TestScenarios/vm-lifecycle-windows' $envs
    } else { Add-RtSkip VM-30 'full per-host lifecycle matrix' 'RT_FULL_MATRIX not set' }
    Complete-RtPhase
}

# ---------------------------------------------------------------- gateway (physical Hyper-V host)
$script:GwProcess = $null
function Start-RtGateway([string]$Mode, [string]$Log) {
    $state = Join-Path $script:RtPhaseDir 'gateway-state'
    New-Item -ItemType Directory -Force -Path "$state\agent" | Out-Null
    Set-Content -Path "$state\gateway.yaml" -Encoding ASCII -Value "state_isolation: shared`nagent_dir: $($state -replace '\\','/')/agent`nlisten: 127.0.0.1:$GatewayPort"
    $backup = $env:DEV_ALCHEMY_APP_DATA_DIR
    if ($AppData -ne 'default') { $env:DEV_ALCHEMY_APP_DATA_DIR = $AppData }
    $script:GwProcess = Start-Process -FilePath $Gw -ArgumentList @('--state-dir', "`"$state`"", 'serve', '--config', "`"$state\gateway.yaml`"", '--binary', "`"$Bin`"", $Mode) `
        -PassThru -NoNewWindow -RedirectStandardOutput $Log -RedirectStandardError "$Log.err"
    $env:DEV_ALCHEMY_APP_DATA_DIR = $backup
    $env:SAILWRIGHT_GATEWAY_DIR = "$state\agent"
    $env:SAILWRIGHT_GATEWAY_URL = "http://127.0.0.1:$GatewayPort/mcp"
    for ($i = 0; $i -lt 60; $i++) {
        $probe = Invoke-RtProcess -File $Gw -Arguments @('connect', '--check') -TimeoutSec 30
        if ($probe.ExitCode -eq 0) { return $true }
        if ($script:GwProcess.HasExited) { return $false }
        Start-Sleep -Seconds 1
    }
    $false
}
function Stop-RtGateway {
    if ($script:GwProcess -and -not $script:GwProcess.HasExited) { & taskkill.exe /PID $script:GwProcess.Id 2>$null | Out-Null; Start-Sleep 3; if (-not $script:GwProcess.HasExited) { Stop-Process -Id $script:GwProcess.Id -Force } }
    $script:GwProcess = $null
}

function Invoke-PhaseGateway {
    Assert-Bin
    Initialize-RtPhase gateway
    $state = Join-Path $script:RtPhaseDir 'gateway-state'
    Remove-Item -Recurse -Force $state -ErrorAction SilentlyContinue
    $env:Path = "$BinDir;$env:Path"
    try {
        $log = Join-Path $script:RtPhaseDir 'logs\GW-serve-bypass.log'
        if (-not (Start-RtGateway '--bypass-approvals' $log)) { Add-RtFail GW-01 'gateway starts with --bypass-approvals' "see $log"; return }
        Add-RtPass GW-01 'gateway serves with --bypass-approvals (isolated state dir)'
        Invoke-RtScript GW-02 'banner shows bypassed approvals' "Get-Content '$log'" -Match @('approvals: BYPASSED')
        Invoke-RtScript GW-04 'audit records gateway_started with the binary hash' "Get-Content '$state\audit.jsonl'" -Match @('"gateway_started"', '[0-9a-f]{64}')
        Invoke-RtE2E GW-05 'E2E smoke, cli-readonly, destroy-idempotent through the gateway' 'TestScenarios/(smoke|cli-readonly|destroy-idempotent)$' @{ SAILWRIGHT_E2E_EXECUTOR = 'gateway'; SAILWRIGHT_E2E_TARGET = $Target }
        Invoke-RtE2E GW-06 'TestVMAccess natively on Windows (OpenSSH, SSH_ASKPASS)' '^TestVMAccess$' @{ SAILWRIGHT_VM_ACCESS_E2E = '1'; SAILWRIGHT_E2E_TARGET = $Target } 'TestVMAccess'
        Invoke-RtScript GW-07 'audit: vm_info and paired tunnels with traffic both ways' @"
`$events = Get-Content '$state\audit.jsonl' | Where-Object { `$_ } | ForEach-Object { `$_ | ConvertFrom-Json }
`$opened = @(`$events | Where-Object event -eq 'tunnel_opened').Count
`$closed = @(`$events | Where-Object event -eq 'tunnel_closed')
`$moved = @(`$closed | Where-Object { `$_.params.bytes_to_vm -gt 0 -and `$_.params.bytes_from_vm -gt 0 }).Count
"opened=`$opened closed=`$(`$closed.Count) moved=`$moved"
if (`$opened -lt 1 -or `$opened -ne `$closed.Count -or `$moved -lt 1) { throw 'tunnel audit incomplete' }
"@
        Invoke-RtScript GW-08 'ownership record has no VMs after cleanup' "`$f = '$state\owned_vms.json'; if (Test-Path `$f) { `$o = Get-Content `$f | ConvertFrom-Json; if (`$o.vms.PSObject.Properties.Count) { throw 'VMs left' } }; 'empty'"
        $own = Get-AppDataEnv; $own['SAILWRIGHT_VM_OWNERSHIP_E2E'] = '1'; $own['SAILWRIGHT_E2E_TARGET'] = $Target; $own['SAILWRIGHT_E2E_BIN'] = $Bin
        Invoke-RtE2E GW-09 'TestVMOwnership: CLI-created VM, approval, recreate invalidates access, lock contention' '^TestVMOwnership$' $own 'TestVMOwnership'
        if ($env:RT_WITH_WINDOWS -eq '1' -and $env:CYGWIN_BASH_PATH) {
            Invoke-RtE2E GW-10 'TestWindowsVMAccess: WinRM listener, PowerShell in the guest, SSH port refused' '^TestWindowsVMAccess$' @{ SAILWRIGHT_VM_ACCESS_E2E = '1'; SAILWRIGHT_E2E_TARGET = 'windows11//amd64'; CYGWIN_BASH_PATH = $env:CYGWIN_BASH_PATH } 'TestWindowsVMAccess'
        } else { Add-RtSkip GW-10 'TestWindowsVMAccess (#303)' 'needs RT_WITH_WINDOWS=1, a windows11 Hyper-V image and CYGWIN_BASH_PATH' }
        $cancel = Get-Setting RT_CANCEL_TARGET 'ubuntu/desktop/amd64/hyperv'
        Invoke-RtE2E GW-11 "E2E build-cancel through the gateway on $cancel" 'TestScenarios/build-cancel$' @{ SAILWRIGHT_E2E_EXECUTOR = 'gateway'; SAILWRIGHT_E2E_TARGET = $cancel }
        Stop-RtGateway
        Invoke-RtScript GW-12 'gateway logs and audit contain no guest password' "Get-Content '$state\audit.jsonl', '$state\logs\*.log' -ErrorAction SilentlyContinue" -AnyExit -NoMatch @('"password":"[^*]', 'P@ssw0rd!')

        $log = Join-Path $script:RtPhaseDir 'logs\GW-serve-noapprovals.log'
        if (Start-RtGateway '--no-approvals' $log) {
            Invoke-RtScript GW-20 'banner shows the safe set only' "Get-Content '$log'" -Match @('disabled \(safe set only\)')
            Sw GW-21 'CLI creates a VM outside the gateway' (@('create') + $TargetArgs + $Insecure)
            Sw GW-21b 'CLI starts it' (@('start') + $TargetArgs + $Insecure)
            Invoke-RtCheck -Id GW-22 -Desc 'tunnel to a VM the gateway did not create is refused' -File $Gw -Arguments @('vm', 'tunnel', $Target, '22') -NonZero -TimeoutSec 300 -Match @('not created by this gateway|403')
            Invoke-RtCheck -Id GW-23 -Desc 'provision_local is refused without an approval console' -File (Get-Command go).Source -Arguments @('test', '-tags', 'e2e', './test/e2e', '-run', 'TestScenarios/local-check$', '-count=1', '-v') `
                -Env @{ SAILWRIGHT_E2E_EXECUTOR = 'gateway'; SAILWRIGHT_E2E_OUT = (Join-Path $script:RtPhaseDir 'e2e-GW-23') } -NonZero -TimeoutSec 600 -Match @('no approval console')
            Sw GW-24 'cleanup CLI VM' (@('destroy') + $TargetArgs)
            Stop-RtGateway
        } else { Add-RtFail GW-20 'gateway starts with --no-approvals' "see $log" }
        Add-RtManual GW-30 'approval console: CLI-created VM via MCP vm_info' "Start '$Gw serve --binary $Bin' in an elevated terminal (no bypass). Create the VM with the CLI. Ask the agent to call vm_info for ${Target}: the console shows vm_access_decision with VM_NOT_CREATED_BY_GATEWAY; type approve <id>; tunnels work. Destroy/recreate with the CLI (once with list_targets between, once without): the tunnel answers 'VM was recreated outside this gateway' until vm_info is approved again."
    } finally {
        Stop-RtGateway
        Remove-Item Env:SAILWRIGHT_GATEWAY_DIR, Env:SAILWRIGHT_GATEWAY_URL -ErrorAction SilentlyContinue
    }
    Complete-RtPhase
}

# ---------------------------------------------------------------- cleanup and report
function Invoke-PhaseCleanup {
    Assert-Bin
    Initialize-RtPhase cleanup
    foreach ($mode in 'default', 'isolated') {
        $envs = if ($mode -eq 'isolated') { Get-AppDataEnv } else { @{} }
        Invoke-RtCheck -Id "CL-01-$mode" -Desc "no managed VM left ($mode app data)" -File $Bin -Arguments @('destroy', 'list', '--json') -Env $envs `
            -Json { param($d) -not ($d.targets | Where-Object { $_.fields.state -ne 'missing' }) }
    }
    Invoke-RtScript CL-02 'no kit gateway left' "Get-CimInstance Win32_Process | Where-Object { `$_.CommandLine -like '*$($RunDir -replace "'", "''")*' } | Select-Object ProcessId,CommandLine" -NoMatch @('sailwright-gateway')
    Invoke-RtScript CL-03 'run directory size (delete appdata after reporting)' "'{0:N1} GB' -f ((Get-ChildItem -Recurse -File '$RunDir' -ErrorAction SilentlyContinue | Measure-Object Length -Sum).Sum / 1GB)" -AnyExit
    Complete-RtPhase
}

function Invoke-PhaseReport {
    $report = Join-Path $RunDir 'report.md'
    $lines = @("# Release test report: $(Get-Setting RT_PLAN 'unspecified') on $(Get-RtHost)", '', "- commit: $(& git -C $Repo rev-parse HEAD)", "- binary version: $Version", "- target: $Target", "- app data: $AppData", '')
    foreach ($dir in Get-ChildItem -Directory $RunDir) {
        $summary = Join-Path $dir.FullName 'summary.md'
        if (Test-Path $summary) { $lines += (Get-Content $summary | ForEach-Object { $_ -replace '^# ', '## ' }) + '' }
    }
    Set-Content -Path $report -Value $lines -Encoding UTF8
    Write-Host "report: $report"
    Get-ChildItem -Recurse $RunDir -Filter results.tsv | Get-Content | Where-Object { ($_ -split "`t")[1] -eq 'FAIL' }
}

if (-not $Phases) {
    Get-Help $PSCommandPath | Out-String | Write-Host
    Write-Host 'phases: preflight kit cli selfupdate install hyperv-verify provision-local build vm gateway cleanup report'
    exit 2
}
$failed = $false
foreach ($phase in $Phases) {
    $script:RtPhaseFailed = $false
    switch ($phase) {
        'preflight' { Invoke-PhasePreflight }
        'kit' { Invoke-PhaseKit }
        'cli' { Invoke-PhaseCli }
        'selfupdate' { Invoke-PhaseSelfupdate }
        'install' { Invoke-PhaseInstall }
        'hyperv-verify' { Invoke-PhaseHypervVerify }
        'provision-local' { Invoke-PhaseProvisionLocal }
        'build' { Invoke-PhaseBuild }
        'vm' { Invoke-PhaseVm }
        'gateway' { Invoke-PhaseGateway }
        'cleanup' { Invoke-PhaseCleanup }
        'report' { Invoke-PhaseReport }
        default { Write-Error "unknown phase: $phase"; exit 2 }
    }
    if ($script:RtPhaseFailed) { $failed = $true }
}
if ($failed) { exit 1 }
exit 0

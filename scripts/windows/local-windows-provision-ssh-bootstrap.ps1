$ErrorActionPreference = 'Stop'

$statePath = $env:DEV_ALCHEMY_LOCAL_WINDOWS_PROVISION_STATE_PATH
$userName = $env:DEV_ALCHEMY_LOCAL_WINDOWS_ANSIBLE_USER
$passwordPlain = $env:DEV_ALCHEMY_LOCAL_WINDOWS_ANSIBLE_PASSWORD
$publicKey = $env:DEV_ALCHEMY_LOCAL_WINDOWS_ANSIBLE_SSH_PUBLIC_KEY
$sshPortString = $env:DEV_ALCHEMY_LOCAL_WINDOWS_ANSIBLE_SSH_PORT
$forceSSHUninstall = [System.Convert]::ToBoolean($env:DEV_ALCHEMY_LOCAL_WINDOWS_FORCE_SSH_UNINSTALL)

$openSSHCapabilityName = 'OpenSSH.Server~~~~0.0.1.0'
$openSSHInstallStatusIntervalSeconds = 15
$openSSHBuiltInFirewallRuleName = 'OpenSSH-Server-In-TCP'
$localFirewallRuleName = 'DevAlchemyLocalSSHDLoopback'
$defaultShellRegistryPath = 'HKLM:\SOFTWARE\OpenSSH'
$defaultShellRegistryName = 'DefaultShell'
$defaultShellPath = 'C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe'
$administratorsAuthorizedKeysPath = Join-Path $env:ProgramData 'ssh\administrators_authorized_keys'
$sshdConfigPath = Join-Path $env:ProgramData 'ssh\sshd_config'
$profileListRegistryPath = 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList'
$administratorsSid = '*S-1-5-32-544'
$systemSid = '*S-1-5-18'

if ([string]::IsNullOrWhiteSpace($statePath)) {
    throw 'DEV_ALCHEMY_LOCAL_WINDOWS_PROVISION_STATE_PATH is required.'
}
if ([string]::IsNullOrWhiteSpace($userName)) {
    throw 'DEV_ALCHEMY_LOCAL_WINDOWS_ANSIBLE_USER is required.'
}
if ([string]::IsNullOrWhiteSpace($passwordPlain)) {
    throw 'DEV_ALCHEMY_LOCAL_WINDOWS_ANSIBLE_PASSWORD is required.'
}
if ([string]::IsNullOrWhiteSpace($publicKey)) {
    throw 'DEV_ALCHEMY_LOCAL_WINDOWS_ANSIBLE_SSH_PUBLIC_KEY is required.'
}
[int]$sshPort = 0
if ([string]::IsNullOrWhiteSpace($sshPortString) -or -not [int]::TryParse($sshPortString, [ref]$sshPort) -or $sshPort -lt 1 -or $sshPort -gt 65535) {
    throw 'DEV_ALCHEMY_LOCAL_WINDOWS_ANSIBLE_SSH_PORT must be a valid TCP port.'
}

Write-Output 'Validating local Windows SSH provision bootstrap inputs.'
if ($sshPort -eq 22) {
    Write-Output 'Using the standard OpenSSH port 22 for this provisioning run.'
} else {
    Write-Output ('Using temporary loopback SSH port ' + $sshPortString + ' for this provisioning run because the standard SSH port 22 is not available for exclusive Windows sshd use.')
}

function Save-State($state) {
    $state | ConvertTo-Json -Compress | Set-Content -Path $statePath -Encoding Ascii
}

function Get-WindowsCapabilityState([string]$name) {
    $capability = Get-WindowsCapability -Online -Name $name -ErrorAction Stop | Select-Object -First 1
    if ($null -eq $capability) {
        throw ('Windows capability not found: ' + $name)
    }

    return @{
        Name = [string]$capability.Name
        State = [string]$capability.State
        Installed = ([string]$capability.State -eq 'Installed')
        Pending = ([string]$capability.State -like '*Pending')
    }
}

function Test-OpenSSHCapabilityInstallNeeded($capabilityState, $sshdServiceState) {
    if ([bool]$capabilityState.Installed) {
        return $false
    }
    if ([bool]$sshdServiceState.Exists) {
        return $false
    }

    return $true
}

function Assert-OpenSSHCapabilityStateIsUsable($capabilityState, $sshdServiceState) {
    if ([bool]$capabilityState.Pending -and -not [bool]$sshdServiceState.Exists) {
        throw ('OpenSSH Server capability is in state "' + [string]$capabilityState.State + '" and sshd is unavailable. Reboot Windows to finish the pending capability change, then retry provisioning.')
    }
}

function Write-OpenSSHPendingStateGuidance($capabilityState, $sshdServiceState) {
    if (-not [bool]$capabilityState.Pending) {
        return
    }

    if ([bool]$sshdServiceState.Exists) {
        Write-Output ('Windows reports the OpenSSH Server capability as "' + [string]$capabilityState.State + '" while sshd still exists. The current sshd installation will be reused for this run, but you should reboot Windows soon to finish the pending capability change and return the OS to a consistent state.')
        return
    }

    Write-Output ('Windows reports the OpenSSH Server capability as "' + [string]$capabilityState.State + '" and sshd is unavailable. A Windows reboot is required before SSH provisioning can continue.')
}

function Install-WindowsCapabilityWithHeartbeat([string]$name, [int]$statusIntervalSeconds) {
    $powerShell = [powershell]::Create()
    $asyncResult = $null
    try {
        $null = $powerShell.AddScript({
            param($capabilityName)
            $ErrorActionPreference = 'Stop'
            Add-WindowsCapability -Online -Name $capabilityName | Out-Null
        }).AddArgument($name)

        $stopwatch = [System.Diagnostics.Stopwatch]::StartNew()
        $asyncResult = $powerShell.BeginInvoke()

        while (-not $asyncResult.AsyncWaitHandle.WaitOne([TimeSpan]::FromSeconds($statusIntervalSeconds))) {
            $elapsedSeconds = [int][Math]::Floor($stopwatch.Elapsed.TotalSeconds)
            $currentCapabilityState = Get-WindowsCapabilityState $name
            Write-Output ('OpenSSH Server capability install is still running after ' + $elapsedSeconds + ' seconds (Windows currently reports state "' + [string]$currentCapabilityState.State + '").')
        }

        $powerShell.EndInvoke($asyncResult) | Out-Null
        if ($powerShell.HadErrors) {
            $errorMessages = @(
                $powerShell.Streams.Error |
                    ForEach-Object { [string]$_ } |
                    Where-Object { -not [string]::IsNullOrWhiteSpace($_) }
            )
            if ($errorMessages.Count -gt 0) {
                throw ('Add-WindowsCapability reported errors: ' + ($errorMessages -join ' | '))
            }

            throw 'Add-WindowsCapability reported one or more PowerShell errors.'
        }
    } finally {
        if ($null -ne $asyncResult) {
            $asyncResult.AsyncWaitHandle.Dispose()
        }
        $powerShell.Dispose()
    }
}

function Get-ServiceState([string]$name) {
    $service = Get-CimInstance -ClassName Win32_Service -Filter ("Name='" + $name + "'") -ErrorAction SilentlyContinue
    if ($null -eq $service) {
        return @{
            Exists = $false
            WasRunning = $false
            StartMode = ''
        }
    }

    return @{
        Exists = $true
        WasRunning = ([string]$service.State -eq 'Running')
        StartMode = [string]$service.StartMode
    }
}

function Get-NetFirewallRuleState([string]$name) {
    $rule = Get-NetFirewallRule -Name $name -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -eq $rule) {
        return @{
            Exists = $false
            Enabled = $false
            LocalPort = ''
        }
    }

    $localPort = ''
    $portFilter = Get-NetFirewallPortFilter -AssociatedNetFirewallRule $rule -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -ne $portFilter -and $null -ne $portFilter.LocalPort) {
        $localPort = [string]$portFilter.LocalPort
    }

    return @{
        Exists = $true
        Enabled = ($rule.Enabled -eq 'True')
        LocalPort = $localPort
    }
}

function Get-RegistryStringState([string]$path, [string]$name) {
    if (-not (Test-Path -Path $path)) {
        return @{
            Exists = $false
            Value = ''
        }
    }

    $property = Get-ItemProperty -Path $path -Name $name -ErrorAction SilentlyContinue
    if ($null -eq $property) {
        return @{
            Exists = $false
            Value = ''
        }
    }

    return @{
        Exists = $true
        Value = [string]$property.$name
    }
}

function Get-FileState([string]$path) {
    if (-not (Test-Path -Path $path)) {
        return @{
            Exists = $false
            ContentBase64 = ''
            Sddl = ''
        }
    }

    try {
        $bytes = [System.IO.File]::ReadAllBytes($path)
        $sddl = (Get-Acl -Path $path).Sddl
    } catch {
        Write-Output ('Taking temporary administrative ownership of "' + $path + '" so provisioning can snapshot and later restore it.')
        Grant-AdministrativePathAccess $path
        $bytes = [System.IO.File]::ReadAllBytes($path)
        $sddl = (Get-Acl -Path $path).Sddl
    }

    return @{
        Exists = $true
        ContentBase64 = [System.Convert]::ToBase64String($bytes)
        Sddl = $sddl
    }
}

function Get-UserProfileBasePath() {
    $profilesDirectoryProperty = Get-ItemProperty -Path $profileListRegistryPath -Name 'ProfilesDirectory' -ErrorAction SilentlyContinue
    if ($null -ne $profilesDirectoryProperty -and -not [string]::IsNullOrWhiteSpace([string]$profilesDirectoryProperty.ProfilesDirectory)) {
        return [Environment]::ExpandEnvironmentVariables([string]$profilesDirectoryProperty.ProfilesDirectory)
    }

    return (Join-Path $env:SystemDrive 'Users')
}

function Get-LocalUserProfilePath([string]$name, [string]$sid) {
    if (-not [string]::IsNullOrWhiteSpace($sid)) {
        $profileImagePathProperty = Get-ItemProperty -Path (Join-Path $profileListRegistryPath $sid) -Name 'ProfileImagePath' -ErrorAction SilentlyContinue
        if ($null -ne $profileImagePathProperty -and -not [string]::IsNullOrWhiteSpace([string]$profileImagePathProperty.ProfileImagePath)) {
            return [Environment]::ExpandEnvironmentVariables([string]$profileImagePathProperty.ProfileImagePath)
        }
    }

    return (Join-Path (Get-UserProfileBasePath) $name)
}

function Get-LocalUserAuthorizedKeysPath([string]$name, [string]$sid) {
    return (Join-Path (Join-Path (Get-LocalUserProfilePath $name $sid) '.ssh') 'authorized_keys')
}

function Ensure-LocalUserProfile([string]$name, [securestring]$password, [string]$sid) {
    if (-not [string]::IsNullOrWhiteSpace($sid)) {
        $profileRegistryPath = Join-Path $profileListRegistryPath $sid
        if (Test-Path -Path $profileRegistryPath) {
            Write-Output ('Keeping the existing local user profile registration for "' + $name + '".')
            return
        }
    }

    Write-Output ('Ensuring the temporary local Ansible account has a registered Windows user profile.')
    $credential = New-Object System.Management.Automation.PSCredential ('.\' + $name), $password
    $process = Start-Process -FilePath 'powershell.exe' -Credential $credential -LoadUserProfile -WorkingDirectory $env:SystemRoot -WindowStyle Hidden -ArgumentList @('-NoLogo', '-NoProfile', '-NonInteractive', '-Command', 'exit 0') -Wait -PassThru
    if ($process.ExitCode -ne 0) {
        throw ('Failed to initialize a local Windows profile for "' + $name + '" (exit code ' + [string]$process.ExitCode + ').')
    }
}

function Set-AdminAuthorizedKeysPermissions([string]$path) {
    Write-Output ('Setting OpenSSH administrators_authorized_keys ACLs with well-known SIDs on ' + $path)
    $icaclsOutput = & icacls.exe $path /inheritance:r /grant ($administratorsSid + ':F') /grant ($systemSid + ':F') 2>&1
    $exitCode = $LASTEXITCODE
    if ($null -ne $icaclsOutput) {
        $icaclsOutput | ForEach-Object { Write-Output ('icacls: ' + $_) }
    }
    if ($exitCode -ne 0) {
        throw ('icacls.exe failed while securing administrators_authorized_keys with exit code ' + $exitCode)
    }
}

function Set-UserAuthorizedKeysPermissions([string]$path, [string]$sid) {
    if ([string]::IsNullOrWhiteSpace($sid)) {
        throw 'A local user SID is required to secure the per-user authorized_keys file.'
    }

    Write-Output ('Setting OpenSSH per-user authorized_keys ACLs with well-known SIDs on ' + $path)
    $icaclsOutput = & icacls.exe $path /inheritance:r /grant ('*' + $sid + ':F') /grant ($systemSid + ':F') 2>&1
    $exitCode = $LASTEXITCODE
    if ($null -ne $icaclsOutput) {
        $icaclsOutput | ForEach-Object { Write-Output ('icacls: ' + $_) }
    }
    if ($exitCode -ne 0) {
        throw ('icacls.exe failed while securing per-user authorized_keys with exit code ' + $exitCode)
    }
}

function Set-PathOwnerBySid([string]$path, [string]$sid) {
    if ([string]::IsNullOrWhiteSpace($sid)) {
        throw 'A local user SID is required to set ownership on the path.'
    }
    if (-not (Test-Path -Path $path)) {
        return
    }

    $acl = Get-Acl -Path $path
    $acl.SetOwner([System.Security.Principal.SecurityIdentifier]::new($sid))
    Set-Acl -Path $path -AclObject $acl
}

function Grant-AdministrativePathAccess([string]$path) {
    if ([string]::IsNullOrWhiteSpace($path) -or -not (Test-Path -Path $path)) {
        return
    }

    $takeownArgs = @('/A', '/F', $path)
    $icaclsArgs = @($path, '/grant', ($administratorsSid + ':F'), ($systemSid + ':F'))
    $null = & takeown.exe @takeownArgs 2>$null
    $null = & icacls.exe @icaclsArgs 2>$null
}

function Write-TemporarySshdConfig([string]$path, [string]$port) {
    $lines = @()
    if (Test-Path -Path $path) {
        $lines = Get-Content -Path $path
    }
    if ($null -eq $lines) {
        $lines = @()
    }

    $updatedLines = New-Object System.Collections.Generic.List[string]
    $insideAdministratorsMatchBlock = $false

    foreach ($line in $lines) {
        if ($insideAdministratorsMatchBlock) {
            if ($line -match '^\s*Match\s+') {
                $insideAdministratorsMatchBlock = $false
            } elseif ([string]::IsNullOrWhiteSpace($line)) {
                $insideAdministratorsMatchBlock = $false
                $updatedLines.Add($line)
                continue
            } else {
                if ($line -notmatch '^\s*#') {
                    $updatedLines.Add('# ' + $line)
                } else {
                    $updatedLines.Add($line)
                }
                continue
            }
        }

        if ($line -match '^\s*Match\s+Group\s+administrators\s*$') {
            $insideAdministratorsMatchBlock = $true
            if ($line -notmatch '^\s*#') {
                $updatedLines.Add('# ' + $line)
            } else {
                $updatedLines.Add($line)
            }
            continue
        }

        if ($line -match '^\s*(Port|ListenAddress|AddressFamily)\s+' -and $line -notmatch '^\s*#') {
            $updatedLines.Add('# ' + $line)
            continue
        }

        $updatedLines.Add($line)
    }

    $directory = Split-Path -Parent $path
    if (-not [string]::IsNullOrWhiteSpace($directory) -and -not (Test-Path -Path $directory)) {
        New-Item -Path $directory -ItemType Directory -Force | Out-Null
    }

    $managedLines = @(
        '# Dev Alchemy temporary local Windows SSH provisioning settings',
        ('Port ' + $port),
        'ListenAddress 127.0.0.1',
        'AddressFamily inet',
        ''
    )
    $allLines = $managedLines + @($updatedLines.ToArray())
    [System.IO.File]::WriteAllText($path, (($allLines -join "`r`n") + "`r`n"), [System.Text.UTF8Encoding]::new($false))
}

function Write-StateSummary($state) {
    Write-Output ('OpenSSH capability state: installed=' + [string][bool]$state.CapabilityInstalled + ', state=' + [string]$state.CapabilityState)
    Write-Output ('sshd service state: existed=' + [string][bool]$state.SshdServiceExisted + ', running=' + [string][bool]$state.SshdServiceWasRunning + ', startMode=' + [string]$state.SshdServiceStartMode)
    Write-Output ('Firewall state: builtInRule existed=' + [string][bool]$state.BuiltInFirewallRuleExisted + ', enabled=' + [string][bool]$state.BuiltInFirewallRuleEnabled + '; loopbackRule existed=' + [string][bool]$state.LocalFirewallRuleExisted + ', enabled=' + [string][bool]$state.LocalFirewallRuleEnabled + ', localPort=' + [string]$state.LocalFirewallRulePort)
    Write-Output ('OpenSSH shell state: defaultShell existed=' + [string][bool]$state.DefaultShellExisted + ', value=' + [string]$state.DefaultShellValue)
    Write-Output ('OpenSSH sshd_config state: existed=' + [string][bool]$state.SshdConfigExisted + ', path=' + $sshdConfigPath)
    Write-Output ('Authorized keys state: existed=' + [string][bool]$state.AuthorizedKeysExisted + ', path=' + $administratorsAuthorizedKeysPath)
    Write-Output ('Per-user authorized keys state: existed=' + [string][bool]$state.UserAuthorizedKeysExisted + ', path=' + [string]$state.UserAuthorizedKeysPath)
    Write-Output ('Local user state: existed=' + [string][bool]$state.UserExisted + ', enabled=' + [string][bool]$state.UserWasEnabled + ', wasAdministrator=' + [string][bool]$state.UserWasAdministrator + ', description=' + [string]$state.UserDescription)
    Write-Output ('Temporary loopback SSH port: ' + [string]$state.ProvisionPort)
    Write-Output ('Force SSH uninstall requested: ' + [string][bool]$state.ForceSSHUninstall)
}

$capabilityState = Get-WindowsCapabilityState $openSSHCapabilityName
$sshdServiceState = Get-ServiceState 'sshd'
$builtInFirewallRuleState = Get-NetFirewallRuleState $openSSHBuiltInFirewallRuleName
$localFirewallRuleState = Get-NetFirewallRuleState $localFirewallRuleName
$defaultShellState = Get-RegistryStringState $defaultShellRegistryPath $defaultShellRegistryName
$sshdConfigState = Get-FileState $sshdConfigPath
$authorizedKeysState = Get-FileState $administratorsAuthorizedKeysPath
$administratorsGroupName = Get-LocalAdministratorsGroupName
$userState = Get-ManagedLocalUserState $userName $administratorsGroupName
$userWasAdministrator = [bool]$userState.WasAdministrator
$userAuthorizedKeysPath = Get-LocalUserAuthorizedKeysPath $userName ([string]$userState.Sid)
$userAuthorizedKeysState = Get-FileState $userAuthorizedKeysPath

$state = @{
    CapabilityInstalled = [bool]$capabilityState.Installed
    CapabilityState = [string]$capabilityState.State
    CapabilityPending = [bool]$capabilityState.Pending
    CapabilityInstallManaged = $false
    SshdServiceExisted = [bool]$sshdServiceState.Exists
    SshdServiceWasRunning = [bool]$sshdServiceState.WasRunning
    SshdServiceStartMode = [string]$sshdServiceState.StartMode
    BuiltInFirewallRuleExisted = [bool]$builtInFirewallRuleState.Exists
    BuiltInFirewallRuleEnabled = [bool]$builtInFirewallRuleState.Enabled
    LocalFirewallRuleExisted = [bool]$localFirewallRuleState.Exists
    LocalFirewallRuleEnabled = [bool]$localFirewallRuleState.Enabled
    LocalFirewallRulePort = [string]$localFirewallRuleState.LocalPort
    DefaultShellExisted = [bool]$defaultShellState.Exists
    DefaultShellValue = [string]$defaultShellState.Value
    SshdConfigExisted = [bool]$sshdConfigState.Exists
    SshdConfigContentBase64 = [string]$sshdConfigState.ContentBase64
    SshdConfigSddl = [string]$sshdConfigState.Sddl
    AuthorizedKeysExisted = [bool]$authorizedKeysState.Exists
    AuthorizedKeysContentBase64 = [string]$authorizedKeysState.ContentBase64
    AuthorizedKeysSddl = [string]$authorizedKeysState.Sddl
    UserAuthorizedKeysPath = [string]$userAuthorizedKeysPath
    UserAuthorizedKeysExisted = [bool]$userAuthorizedKeysState.Exists
    UserAuthorizedKeysContentBase64 = [string]$userAuthorizedKeysState.ContentBase64
    UserAuthorizedKeysSddl = [string]$userAuthorizedKeysState.Sddl
    UserName = $userName
    UserExisted = [bool]$userState.Exists
    UserWasEnabled = [bool]$userState.Enabled
    UserDescription = [string]$userState.Description
    UserWasAdministrator = [bool]$userWasAdministrator
    ProvisionPort = [string]$sshPortString
    ForceSSHUninstall = [bool]$forceSSHUninstall
}
Save-State $state
Write-Output 'Captured existing OpenSSH, firewall, shell, key, and local user state.'
Write-StateSummary $state
Write-OpenSSHPendingStateGuidance $capabilityState $sshdServiceState
Assert-OpenSSHCapabilityStateIsUsable $capabilityState $sshdServiceState

if (Test-OpenSSHCapabilityInstallNeeded $capabilityState $sshdServiceState) {
    Write-Output ('Installing the OpenSSH Server capability. Progress updates will be logged every ' + $openSSHInstallStatusIntervalSeconds + ' seconds while Windows completes the capability change.')
    Install-WindowsCapabilityWithHeartbeat $openSSHCapabilityName $openSSHInstallStatusIntervalSeconds
    $state.CapabilityInstallManaged = $true
    Save-State $state
    $postInstallCapabilityState = Get-WindowsCapabilityState $openSSHCapabilityName
    Write-Output ('OpenSSH Server capability install completed for this provisioning run with state "' + [string]$postInstallCapabilityState.State + '".')
} elseif ([bool]$state.CapabilityInstalled) {
    Write-Output ('Reusing the existing OpenSSH Server capability in state "' + [string]$state.CapabilityState + '".')
} elseif ([bool]$state.SshdServiceExisted) {
    Write-Output ('OpenSSH capability is reported as "' + [string]$state.CapabilityState + '" but sshd already exists, so the current installation will be reused without reinstalling the capability.')
} else {
    Write-Output ('OpenSSH capability is reported as "' + [string]$state.CapabilityState + '" and no install action was required.')
}

Write-Output 'Preparing the sshd service for loopback-only provisioning.'
$sshdService = Get-Service -Name 'sshd' -ErrorAction SilentlyContinue
if ($null -eq $sshdService) {
    throw 'The sshd service was not found after enabling OpenSSH Server.'
}
if (([string]$state.SshdServiceStartMode).ToLowerInvariant() -eq 'disabled') {
    Set-Service -Name 'sshd' -StartupType Manual
}

if (-not [bool]$state.BuiltInFirewallRuleExisted) {
    $builtInRule = Get-NetFirewallRule -Name $openSSHBuiltInFirewallRuleName -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -ne $builtInRule) {
        Write-Output 'Disabling the broad OpenSSH firewall rule created during install.'
        $builtInRule | Disable-NetFirewallRule | Out-Null
    }
}

if ([bool]$state.LocalFirewallRuleExisted) {
    Write-Output ('Reconfiguring the existing loopback-only OpenSSH firewall rule for temporary local port ' + $sshPortString + '.')
    Get-NetFirewallRule -Name $localFirewallRuleName -ErrorAction SilentlyContinue | Remove-NetFirewallRule | Out-Null
} else {
    Write-Output ('Creating the loopback-only OpenSSH firewall rule for temporary local port ' + $sshPortString + '.')
}
New-NetFirewallRule -Name $localFirewallRuleName -DisplayName 'Dev Alchemy Local OpenSSH Loopback' -Direction Inbound -Action Allow -Protocol TCP -LocalAddress 127.0.0.1 -LocalPort $sshPortString -Profile Any | Out-Null

Write-Output 'Setting the OpenSSH default shell to PowerShell for Ansible.'
if (-not (Test-Path -Path $defaultShellRegistryPath)) {
    New-Item -Path $defaultShellRegistryPath -Force | Out-Null
}
New-ItemProperty -Path $defaultShellRegistryPath -Name $defaultShellRegistryName -Value $defaultShellPath -PropertyType String -Force | Out-Null

Write-TemporarySshdConfig $sshdConfigPath $sshPortString
Write-Output ('Wrote a temporary loopback-only sshd_config for this provisioning run on port ' + $sshPortString + '.')

$securePassword = ConvertTo-SecureString -String $passwordPlain -AsPlainText -Force
$localUser = Ensure-ManagedLocalUserForProvisioning $userName $securePassword 'Sailwright Ansible acct' $administratorsGroupName
Ensure-LocalUserProfile $userName $securePassword ([string]$localUser.SID.Value)

Write-Output 'Installing the temporary SSH public key for Windows OpenSSH logins.'
$localUserSid = [string]$localUser.SID.Value
$userAuthorizedKeysPath = Get-LocalUserAuthorizedKeysPath $userName $localUserSid
$state.UserAuthorizedKeysPath = [string]$userAuthorizedKeysPath
Save-State $state

$sshDirectory = Split-Path -Parent $administratorsAuthorizedKeysPath
if (-not (Test-Path -Path $sshDirectory)) {
    Write-Output ('Creating the SSH directory "' + $sshDirectory + '".')
    New-Item -Path $sshDirectory -ItemType Directory -Force | Out-Null
}
$existingAuthorizedKeys = ''
if (Test-Path -Path $administratorsAuthorizedKeysPath) {
    Write-Output ('Reading the existing administrators_authorized_keys file from "' + $administratorsAuthorizedKeysPath + '".')
    $existingAuthorizedKeys = [System.IO.File]::ReadAllText($administratorsAuthorizedKeysPath)
}
$normalizedAuthorizedKeys = $existingAuthorizedKeys.TrimEnd("`r", "`n")
if (-not [string]::IsNullOrWhiteSpace($normalizedAuthorizedKeys)) {
    $normalizedAuthorizedKeys += "`r`n"
}
$normalizedAuthorizedKeys += ($publicKey.Trim() + "`r`n")
[System.IO.File]::WriteAllText($administratorsAuthorizedKeysPath, $normalizedAuthorizedKeys, [System.Text.UTF8Encoding]::new($false))
Set-AdminAuthorizedKeysPermissions $administratorsAuthorizedKeysPath

$userSSHDirectory = Split-Path -Parent $userAuthorizedKeysPath
$userProfilePath = Split-Path -Parent $userSSHDirectory
if (-not (Test-Path -Path $userProfilePath)) {
    Write-Output ('Creating the local user profile directory "' + $userProfilePath + '" for OpenSSH home resolution.')
    New-Item -Path $userProfilePath -ItemType Directory -Force | Out-Null
}
Set-PathOwnerBySid $userProfilePath $localUserSid

if (-not (Test-Path -Path $userSSHDirectory)) {
    Write-Output ('Creating the per-user SSH directory "' + $userSSHDirectory + '".')
    New-Item -Path $userSSHDirectory -ItemType Directory -Force | Out-Null
}
Set-PathOwnerBySid $userSSHDirectory $localUserSid
$existingUserAuthorizedKeys = ''
if (Test-Path -Path $userAuthorizedKeysPath) {
    Write-Output ('Reading the existing per-user authorized_keys file from "' + $userAuthorizedKeysPath + '".')
    try {
        $existingUserAuthorizedKeys = [System.IO.File]::ReadAllText($userAuthorizedKeysPath)
    } catch {
        Write-Output ('Taking temporary administrative ownership of "' + $userAuthorizedKeysPath + '" so provisioning can append the temporary key.')
        Grant-AdministrativePathAccess $userAuthorizedKeysPath
        $existingUserAuthorizedKeys = [System.IO.File]::ReadAllText($userAuthorizedKeysPath)
    }
}
$normalizedUserAuthorizedKeys = $existingUserAuthorizedKeys.TrimEnd("`r", "`n")
if (-not [string]::IsNullOrWhiteSpace($normalizedUserAuthorizedKeys)) {
    $normalizedUserAuthorizedKeys += "`r`n"
}
$normalizedUserAuthorizedKeys += ($publicKey.Trim() + "`r`n")
[System.IO.File]::WriteAllText($userAuthorizedKeysPath, $normalizedUserAuthorizedKeys, [System.Text.UTF8Encoding]::new($false))
Set-PathOwnerBySid $userAuthorizedKeysPath $localUserSid
Set-UserAuthorizedKeysPermissions $userAuthorizedKeysPath $localUserSid

Write-Output 'Starting or restarting the sshd service.'
if ((Get-Service -Name 'sshd').Status -eq 'Running') {
    Restart-Service -Name 'sshd' -Force
} else {
    Start-Service -Name 'sshd'
}

Write-Output 'Local Windows SSH provision bootstrap completed.'

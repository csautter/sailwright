$ErrorActionPreference = "Stop"

$VirtualizationFlavor = "__VIRTUALIZATION_FLAVOR__"
$LogDir = "C:\ProgramData\dev-alchemy\logs"
$SecretsDir = "C:\ProgramData\dev-alchemy\secrets"
$RunnerSetupLog = Join-Path $LogDir "runner-setup.log"
$RunnerPasswordFile = Join-Path $SecretsDir "ghrunner-password.txt"

New-Item -ItemType Directory -Path $LogDir -Force | Out-Null
New-Item -ItemType Directory -Path $SecretsDir -Force | Out-Null

function Write-SetupLog {
    param([string]$Message)

    $timestamp = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
    $line = "[$timestamp] $Message"
    Write-Host $line
    Add-Content -Path $RunnerSetupLog -Value $line
}

function Protect-SecretFile {
    param([string]$Path)

    try {
        $acl = New-Object System.Security.AccessControl.FileSecurity
        $acl.SetOwner([System.Security.Principal.NTAccount]"Administrators")
        $acl.SetAccessRuleProtection($true, $false)

        $inheritanceFlags = [System.Security.AccessControl.InheritanceFlags]::None
        $propagationFlags = [System.Security.AccessControl.PropagationFlags]::None
        $allow = [System.Security.AccessControl.AccessControlType]::Allow

        $administratorsRule = New-Object System.Security.AccessControl.FileSystemAccessRule("Administrators", "FullControl", $inheritanceFlags, $propagationFlags, $allow)
        $systemRule = New-Object System.Security.AccessControl.FileSystemAccessRule("SYSTEM", "FullControl", $inheritanceFlags, $propagationFlags, $allow)

        $null = $acl.AddAccessRule($administratorsRule)
        $null = $acl.AddAccessRule($systemRule)

        Set-Acl -Path $Path -AclObject $acl
    } catch {
        Write-SetupLog "Warning: Failed to tighten ACLs on secret file '$Path': $($_.Exception.Message)"
    }
}

function Get-HyperVSwitchHostAdapter {
    param([string]$SwitchName)

    $vmAdapters = @(Get-VMNetworkAdapter -ManagementOS -ErrorAction Stop | Where-Object SwitchName -eq $SwitchName)
    if ($vmAdapters.Count -gt 1) {
        throw "Found $($vmAdapters.Count) management OS adapters attached to the switch."
    }

    # Match the same device ID that Packer uses. Interface aliases can be renamed.
    $deviceId = $vmAdapters.DeviceId
    if ($deviceId) {
        Get-NetAdapter -IncludeHidden -ErrorAction Stop | Where-Object {
            $_.DeviceId -eq $deviceId -and $_.InterfaceIndex -gt 0
        }
    }
}

function Wait-HyperVHostIPv4 {
    param([string]$SwitchName, [switch]$AssignAddress)

    $ipAddress = "192.168.0.1"
    $prefixLength = 24
    $timeoutMs = 60000
    $pollIntervalMs = 2000
    $maxAttempts = $timeoutMs / $pollIntervalMs
    $timer = [System.Diagnostics.Stopwatch]::StartNew()
    $assignmentAttempted = $false

    for ($attempt = 1; $attempt -le $maxAttempts; $attempt++) {
        $stage = "discover host adapter"
        $adapterDescription = "management OS adapter"
        $observation = "No matching Windows network interface is visible."
        try {
            $adapters = @(Get-HyperVSwitchHostAdapter -SwitchName $SwitchName)
            if ($adapters.Count -gt 1) {
                throw "The switch device ID matches multiple interfaces (indexes $($adapters.InterfaceIndex -join ', '))."
            }
            if ($adapters.Count -eq 1) {
                $adapter = $adapters[0]
                $adapterDescription = "'$($adapter.Name)' (index $($adapter.InterfaceIndex))"
                $stage = "read active IPv4"
                # Enumerate IPv4 then match by index so an interface without an IPv4 row is an empty result.
                $addresses = @(Get-NetIPAddress -AddressFamily IPv4 -PolicyStore ActiveStore -ErrorAction Stop |
                    Where-Object { $_.InterfaceIndex -eq $adapter.InterfaceIndex -and $_.IPAddress -eq $ipAddress })
                if ($addresses.Count -gt 1) {
                    throw "Found multiple active entries for $ipAddress on this interface."
                }
                if ($addresses.Count -eq 1) {
                    $address = $addresses[0]
                    $observation = "Found $ipAddress/$($address.PrefixLength) (state $($address.AddressState), SkipAsSource $($address.SkipAsSource))."
                    if ($address.PrefixLength -eq $prefixLength -and $address.AddressState -eq 'Preferred' -and
                        $address.SkipAsSource -eq $false -and $timer.ElapsedMilliseconds -lt $timeoutMs) {
                        Write-SetupLog "Verified Hyper-V switch '$SwitchName' adapter $adapterDescription with $ipAddress/$prefixLength."
                        return $adapter
                    }
                } else {
                    $observation = "The expected IPv4 address is absent on this interface."
                    if ($AssignAddress -and -not $assignmentAttempted -and $timer.ElapsedMilliseconds -lt $timeoutMs) {
                        $stage = "assign IPv4"
                        New-NetIPAddress -IPAddress $ipAddress -PrefixLength $prefixLength -InterfaceIndex $adapter.InterfaceIndex -ErrorAction Stop | Out-Null
                        $assignmentAttempted = $true
                    }
                }
            }
        } catch {
            $failure = "Hyper-V switch '$SwitchName', adapter $adapterDescription, expected $ipAddress/$($prefixLength): failed to $($stage): $($_.Exception.Message)"
            Write-SetupLog $failure
            throw $failure
        }

        $remainingMs = $timeoutMs - $timer.ElapsedMilliseconds
        if ($remainingMs -le 0 -or $attempt -eq $maxAttempts) {
            break
        }
        Start-Sleep -Milliseconds ([Math]::Min($pollIntervalMs, $remainingMs))
        if ($timer.ElapsedMilliseconds -ge $timeoutMs) {
            break
        }
    }

    $failure = "Hyper-V switch '$SwitchName', adapter $adapterDescription, expected $ipAddress/$($prefixLength): timed out during $stage. $observation"
    Write-SetupLog $failure
    throw $failure
}

function Write-HyperVNetworkSnapshot {
    param([string]$Label)

    Write-SetupLog "Hyper-V network snapshot: $Label"

    try {
        $switches = Get-VMSwitch -ErrorAction SilentlyContinue | Format-List * | Out-String -Width 4096
        Add-Content -Path $RunnerSetupLog -Value $switches
    } catch {
        Write-SetupLog "Warning: Failed to list VMSwitches: $($_.Exception.Message)"
    }

    try {
        $adapters = Get-NetAdapter | Sort-Object Name | Format-List Name, InterfaceDescription, Status, MacAddress, LinkSpeed, ifIndex | Out-String -Width 4096
        Add-Content -Path $RunnerSetupLog -Value $adapters
    } catch {
        Write-SetupLog "Warning: Failed to list NetAdapters: $($_.Exception.Message)"
    }

    try {
        $addresses = Get-NetIPAddress | Sort-Object InterfaceAlias, AddressFamily, IPAddress | Format-Table -AutoSize InterfaceAlias, IPAddress, PrefixLength, AddressFamily, PrefixOrigin, SuffixOrigin | Out-String -Width 4096
        Add-Content -Path $RunnerSetupLog -Value $addresses
    } catch {
        Write-SetupLog "Warning: Failed to list NetIPAddresses: $($_.Exception.Message)"
    }

    try {
        $nat = Get-NetNat -ErrorAction SilentlyContinue | Format-List * | Out-String -Width 4096
        if ($nat) {
            Add-Content -Path $RunnerSetupLog -Value $nat
        }
    } catch {
        Write-SetupLog "Warning: Failed to list NetNat entries: $($_.Exception.Message)"
    }
}

Write-SetupLog "Selected virtualization flavor: $VirtualizationFlavor"

# write a file that logs if this file was executed
if (-not (Test-Path "C:\execution.log")) {
    New-Item -Path "C:\" -Name "execution.log" -ItemType "file" -Force
}
Add-Content -Path "C:\execution.log" -Value "runner-setup.ps1 executed on $(Get-Date)"

# Detect OS type
$OSCaption = (Get-CimInstance Win32_OperatingSystem).Caption
$IsServer = $OSCaption -like "*Server*"
Write-SetupLog "Detected OS: $OSCaption (Server: $IsServer)"

if($VirtualizationFlavor -eq "hyperv") {
    Write-SetupLog "Proceeding with Hyper-V setup..."

    # Function to ensure script re-runs after restart
    function Set-ScriptContinuation {
        param([string]$ScriptPath)
        
        $taskName = "ContinueRunnerSetup"
        $action = New-ScheduledTaskAction -Execute "PowerShell.exe" -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$ScriptPath`""
        $trigger = New-ScheduledTaskTrigger -AtStartup
        $principal = New-ScheduledTaskPrincipal -UserId "SYSTEM" -LogonType ServiceAccount -RunLevel Highest
        $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
        
        # Remove existing task if present
        Unregister-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue -Confirm:$false
        
        # Register new task
        Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Principal $principal -Settings $settings | Out-Null
        Write-SetupLog "Scheduled task '$taskName' created to continue after restart"
    }

    # Function to remove continuation task
    function Remove-ScriptContinuation {
        $taskName = "ContinueRunnerSetup"
        Unregister-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue -Confirm:$false
        Write-SetupLog "Scheduled task '$taskName' removed"
    }

    # Install Hyper-V based on OS type
    $needsRestart = $false

    if ($IsServer) {
        # Windows Server - use Install-WindowsFeature
        Write-SetupLog "Installing Hyper-V on Windows Server..."
        
        $hypervFeature = Get-WindowsFeature -Name Hyper-V
        if (-not $hypervFeature.Installed) {
            Write-SetupLog "Installing Hyper-V role..."
            $result = Install-WindowsFeature -Name Hyper-V -IncludeManagementTools
            if ($result.RestartNeeded -eq 'Yes') {
                $needsRestart = $true
            }
        }
        
        # Ensure PowerShell module is installed
        $hypervPowerShell = Get-WindowsFeature -Name Hyper-V-PowerShell
        if (-not $hypervPowerShell.Installed) {
            Write-SetupLog "Installing Hyper-V PowerShell module..."
            $result = Install-WindowsFeature -Name Hyper-V-PowerShell
            if ($result.RestartNeeded -eq 'Yes') {
                $needsRestart = $true
            }
        }
    } else {
        # Windows Client - use Enable-WindowsOptionalFeature
        Write-SetupLog "Installing Hyper-V on Windows Client..."
        
        $hypervFeature = Get-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V
        if ($hypervFeature.State -ne 'Enabled') {
            Write-SetupLog "Enabling Hyper-V feature..."
            Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V-All -All -NoRestart | Out-Null
            $needsRestart = $true
        }
    }

    # Handle restart if needed
    if ($needsRestart) {
        Write-SetupLog "Restart required to complete Hyper-V installation."
        
        # Set up continuation after restart
        $scriptPath = $MyInvocation.MyCommand.Path
        if (-not $scriptPath) {
            $scriptPath = "C:\runner-setup.ps1"  # Fallback path
        }
        Set-ScriptContinuation -ScriptPath $scriptPath
        
        Write-SetupLog "System will restart in 10 seconds. Script will continue automatically after restart."
        Start-Sleep -Seconds 10
        Restart-Computer -Force
        exit
    }

    # Remove continuation task since we're past the restart phase
    Remove-ScriptContinuation

    # Import Hyper-V module
    Write-SetupLog "Importing Hyper-V module..."
    Import-Module Hyper-V -ErrorAction Stop
    Write-HyperVNetworkSnapshot -Label "before-switch-setup"

    # Create the internal switch if it doesn't already exist.
    $switchName = "Default Switch"
    try {
        $existingSwitch = Get-VMSwitch -ErrorAction Stop | Where-Object Name -eq $switchName
    } catch {
        Write-SetupLog "Failed to look up Hyper-V switch '$switchName' during initial enumeration: $($_.Exception.Message)"
        throw
    }
    if (-not $existingSwitch) {
        Write-SetupLog "Creating Hyper-V switch '$switchName'..."
        try {
            New-VMSwitch -Name $switchName -SwitchType Internal -ErrorAction Stop | Out-Null
        } catch {
            Write-SetupLog "Failed to create Hyper-V switch '$switchName': $($_.Exception.Message)"
            try {
                $existingSwitch = Get-VMSwitch -ErrorAction Stop | Where-Object Name -eq $switchName
            } catch {
                Write-SetupLog "Failed to recheck Hyper-V switch '$switchName' after creation failed: $($_.Exception.Message)"
                throw
            }
            if (-not $existingSwitch) {
                throw
            }
        }
    }

    $switchAdapter = Wait-HyperVHostIPv4 -SwitchName $switchName -AssignAddress

    $existingNat = Get-NetNat -Name "DefaultSwitchNAT" -ErrorAction SilentlyContinue
    if (-not $existingNat) {
        New-NetNat -Name "DefaultSwitchNAT" -InternalIPInterfaceAddressPrefix "192.168.0.0/24" -ErrorAction SilentlyContinue
    }

    Write-HyperVNetworkSnapshot -Label "after-switch-setup"


    # Configure DHCP Server
    Write-SetupLog "Configuring DHCP Server..."
    if (-not (Get-WindowsFeature -Name DHCP -ErrorAction SilentlyContinue).Installed) {
        Write-SetupLog "Installing DHCP Server feature..."
        Install-WindowsFeature -Name DHCP -IncludeManagementTools
    }

    # Configure DHCP scope
    $scopeName = "DefaultSwitchDHCP"
    $existingScope = Get-DhcpServerv4Scope -ScopeId 192.168.0.0 -ErrorAction SilentlyContinue
    if (-not $existingScope) {
        Write-SetupLog "Creating DHCP scope..."
        Add-DhcpServerv4Scope -Name $scopeName `
            -StartRange 192.168.0.100 `
            -EndRange 192.168.0.200 `
            -SubnetMask 255.255.255.0 `
            -State Active -ErrorAction SilentlyContinue
        
        Set-DhcpServerv4OptionValue -ScopeId 192.168.0.0 `
            -Router 192.168.0.1 `
            -DnsServer 8.8.8.8,8.8.4.4 -ErrorAction SilentlyContinue
    }

    # Bind DHCP server only to the Default Switch adapter
    Write-SetupLog "Binding DHCP to Default Switch adapter..."
    Set-DhcpServerv4Binding -InterfaceAlias $switchAdapter.Name -BindingState $true -ErrorAction SilentlyContinue

    # Unbind from all other interfaces to avoid conflicts with Azure VNet DHCP
    Get-DhcpServerv4Binding | Where-Object { $_.InterfaceAlias -ne $switchAdapter.Name } |
        ForEach-Object { Set-DhcpServerv4Binding -InterfaceAlias $_.InterfaceAlias -BindingState $false -ErrorAction SilentlyContinue }

    # Authorize DHCP server (only if domain-joined)
    if ((Get-CimInstance Win32_ComputerSystem).PartOfDomain) {
        Write-SetupLog "Authorizing DHCP server in Active Directory..."
        Add-DhcpServerInDC -DnsName $env:COMPUTERNAME -ErrorAction SilentlyContinue
    }

    Write-HyperVNetworkSnapshot -Label "after-dhcp-setup"

    # Verify IPv4 before persisting credentials or registering the runner.
    Wait-HyperVHostIPv4 -SwitchName $switchName | Out-Null
}

$RunnerToken = "__RUNNER_TOKEN__"
$RepoUrl     = "__REPO_URL__"
$RunnerName  = $env:COMPUTERNAME
$RunnerDir   = "C:\actions-runner"
$RunnerUser   = "ghrunner"

New-Item -ItemType Directory -Force -Path $RunnerDir
Set-Location $RunnerDir

# Create a local user 'ghrunner' with a random password, add to Administrators and Hyper-V Administrators


Add-Type -AssemblyName System.Web
$Password = -join ((48..57) + (65..90) + (97..122) | Get-Random -Count 20 | ForEach-Object { [char]$_ })
$SecurePassword = ConvertTo-SecureString $Password -AsPlainText -Force
Set-Content -Path $RunnerPasswordFile -Value $Password -NoNewline -Force
Protect-SecretFile -Path $RunnerPasswordFile
Write-SetupLog "Stored runner password in protected secret file: $RunnerPasswordFile"

# Create the user if it doesn't exist
if (-not (Get-LocalUser -Name $RunnerUser -ErrorAction SilentlyContinue)) {
    New-LocalUser -Name $RunnerUser -Password $SecurePassword -FullName "GitHub Runner" -Description "Local user for GitHub Actions Runner" -PasswordNeverExpires
}

# Add user to Administrators and Hyper-V Administrators groups
Add-LocalGroupMember -Group "Administrators" -Member $RunnerUser -ErrorAction SilentlyContinue
Add-LocalGroupMember -Group "Hyper-V Administrators" -Member $RunnerUser -ErrorAction SilentlyContinue

.\config.cmd `
  --url $RepoUrl `
  --token $RunnerToken `
  --name $RunnerName `
  --labels windows,azure,nested,$RunnerName `
  --ephemeral `
  --unattended `
  --runasservice `
  --windowslogonaccount $RunnerUser `
  --windowslogonpassword $Password

# Allow the SCM a moment to register the service after config
Start-Sleep -Seconds 5

# Configure automatic service recovery on failure.
# For ephemeral runners we intentionally leave failureflag at its default (0) so that
# only a crash or nonzero exit triggers a restart. Restart delays are 30 s, 60 s,
# then 120 s. The counter resets after 1 h.
Write-SetupLog "Configuring GitHub Actions Runner service auto-recovery..."

# Derive the service name from the repo URL (https://github.com/<owner>/<repo>)
$urlParts   = $RepoUrl.TrimEnd('/').Split('/')
$repoOwner  = $urlParts[-2]
$repoName   = $urlParts[-1]
$ServiceName = "actions.runner.$repoOwner.$repoName.$RunnerName"

$svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue

# Fallback: search by wildcard in case the name differs slightly
if (-not $svc) {
    Write-Warning "Service '$ServiceName' not found; searching by pattern..."
    $svc = Get-Service | Where-Object { $_.Name -like "actions.runner*$RunnerName*" } | Select-Object -First 1
}

if ($svc) {
    Write-SetupLog "Applying recovery policy to service: $($svc.Name)"
    # reset=3600 resets the failure count after 3600 s (1 h) of stable operation.
    # actions sets restart delays of 30 s, 60 s, and 120 s for subsequent failures.
    sc.exe failure "$($svc.Name)" reset=3600 actions=restart/30000/restart/60000/restart/120000
    Write-SetupLog "Service auto-recovery configured successfully."
} else {
    Write-Warning "Could not locate the runner service to configure recovery. Skipping."
}

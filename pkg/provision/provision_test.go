package provision

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	alchemy_deploy "github.com/csautter/dev-alchemy/pkg/deploy"
)

func TestExtractWindowsIPv4FromIPConfig(t *testing.T) {
	output := `
Windows IP Configuration

Ethernet adapter Loopback:
   IPv4 Address. . . . . . . . . . . : 127.0.0.1

Ethernet adapter Ethernet:
   IPv4 Address. . . . . . . . . . . : 172.25.125.159
`

	ip, err := extractWindowsIPv4FromIPConfig(output)
	if err != nil {
		t.Fatalf("expected IP extraction to succeed, got error: %v", err)
	}
	if ip != "172.25.125.159" {
		t.Fatalf("expected 172.25.125.159, got %s", ip)
	}
}

func TestDiscoverLinuxVagrantIPv4_UsesProvidedVagrantEnv(t *testing.T) {
	previousRunner := runProvisionCommandWithCombinedOutputWithEnv
	t.Cleanup(func() {
		runProvisionCommandWithCombinedOutputWithEnv = previousRunner
	})

	callCount := 0
	runProvisionCommandWithCombinedOutputWithEnv = func(workingDir string, timeout time.Duration, executable string, args []string, extraEnv []string) (string, error) {
		callCount++
		if workingDir != "vagrant-dir" {
			t.Fatalf("expected working directory to be passed through, got %q", workingDir)
		}
		if executable != "vagrant" {
			t.Fatalf("expected vagrant executable, got %q", executable)
		}
		if timeout != 3*time.Minute {
			t.Fatalf("expected 3 minute timeout, got %s", timeout)
		}
		if strings.Join(args, " ") != "ssh-config" {
			t.Fatalf("expected ssh-config lookup args, got %v", args)
		}
		if len(extraEnv) != 1 || extraEnv[0] != "VAGRANT_DOTFILE_PATH=.vagrant/linux-ubuntu-desktop-packer" {
			t.Fatalf("expected Vagrant env to be forwarded, got %v", extraEnv)
		}
		return "Host default\n  HostName 172.25.125.159\n", nil
	}

	ip, err := discoverLinuxVagrantIPv4("vagrant-dir", []string{"VAGRANT_DOTFILE_PATH=.vagrant/linux-ubuntu-desktop-packer"})
	if err != nil {
		t.Fatalf("discoverLinuxVagrantIPv4 returned error: %v", err)
	}
	if ip != "172.25.125.159" {
		t.Fatalf("expected discovered IP 172.25.125.159, got %q", ip)
	}
	if callCount != 1 {
		t.Fatalf("expected ssh-config lookup to avoid fallback, got %d calls", callCount)
	}
}

func TestDiscoverLinuxVagrantIPv4_FallsBackToSSHCommandWhenSSHConfigLacksIP(t *testing.T) {
	previousRunner := runProvisionCommandWithCombinedOutputWithEnv
	t.Cleanup(func() {
		runProvisionCommandWithCombinedOutputWithEnv = previousRunner
	})

	var calls []string
	runProvisionCommandWithCombinedOutputWithEnv = func(workingDir string, timeout time.Duration, executable string, args []string, extraEnv []string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		switch strings.Join(args, " ") {
		case "ssh-config":
			return "Host default\n  HostName ::1\n", nil
		case "ssh -c hostname -I":
			return "127.0.0.1 172.25.125.159\n", nil
		default:
			t.Fatalf("unexpected vagrant args: %v", args)
			return "", nil
		}
	}

	ip, err := discoverLinuxVagrantIPv4("vagrant-dir", []string{"VAGRANT_DOTFILE_PATH=.vagrant/linux-ubuntu-desktop-packer"})
	if err != nil {
		t.Fatalf("discoverLinuxVagrantIPv4 returned error: %v", err)
	}
	if ip != "172.25.125.159" {
		t.Fatalf("expected discovered IP 172.25.125.159, got %q", ip)
	}
	if strings.Join(calls, " -> ") != "ssh-config -> ssh -c hostname -I" {
		t.Fatalf("expected ssh-config fallback sequence, got %v", calls)
	}
}

func TestDiscoverLinuxLibvirtVMIPv4_UsesGuestAgentBeforeLease(t *testing.T) {
	vm := alchemy_build.VirtualMachineConfig{
		OS:         "ubuntu",
		UbuntuType: "server",
		Arch:       "amd64",
	}

	var calls []string
	ip, err := discoverLinuxLibvirtVMIPv4WithOptions("project-dir", vm, linuxLibvirtIPv4DiscoveryOptions{
		runCommand: func(workingDir string, timeout time.Duration, executable string, args []string) (string, error) {
			calls = append(calls, strings.Join(args, " "))
			if workingDir != "project-dir" {
				t.Fatalf("expected working directory to be passed through, got %q", workingDir)
			}
			if timeout != 7*time.Second {
				t.Fatalf("expected command timeout to be passed through, got %s", timeout)
			}
			if executable != "virsh" {
				t.Fatalf("expected virsh executable, got %q", executable)
			}

			return `
 Name       MAC address          Protocol     Address
-------------------------------------------------------------------------------
 vnet0      52:54:00:12:34:56    ipv4         192.168.122.41/24
`, nil
		},
		sleep: func(time.Duration) {
			t.Fatal("did not expect retry sleep when guest-agent lookup succeeds")
		},
		maxAttempts:    1,
		commandTimeout: 7 * time.Second,
	})
	if err != nil {
		t.Fatalf("expected libvirt IPv4 discovery to succeed, got error: %v", err)
	}
	if ip != "192.168.122.41" {
		t.Fatalf("expected discovered IP 192.168.122.41, got %q", ip)
	}

	wantCall := "--connect qemu:///system domifaddr ubuntu-server-amd64-dev-alchemy --source agent"
	if strings.Join(calls, " -> ") != wantCall {
		t.Fatalf("expected only guest-agent lookup %q, got %v", wantCall, calls)
	}
}

func TestDiscoverLinuxLibvirtVMIPv4_FallsBackToLease(t *testing.T) {
	vm := alchemy_build.VirtualMachineConfig{
		OS:         "ubuntu",
		UbuntuType: "desktop",
		Arch:       "arm64",
	}

	var calls []string
	ip, err := discoverLinuxLibvirtVMIPv4WithOptions("project-dir", vm, linuxLibvirtIPv4DiscoveryOptions{
		runCommand: func(_ string, _ time.Duration, executable string, args []string) (string, error) {
			if executable != "virsh" {
				t.Fatalf("expected virsh executable, got %q", executable)
			}
			call := strings.Join(args, " ")
			calls = append(calls, call)
			switch {
			case strings.Contains(call, "--source agent"):
				return "error: QEMU guest agent is not connected", errors.New("agent unavailable")
			case strings.Contains(call, "--source lease"):
				return `
 Name       MAC address          Protocol     Address
-------------------------------------------------------------------------------
 vnet0      52:54:00:aa:bb:cc    ipv4         192.168.122.74/24
`, nil
			default:
				t.Fatalf("unexpected virsh args: %v", args)
				return "", nil
			}
		},
		maxAttempts: 1,
	})
	if err != nil {
		t.Fatalf("expected libvirt IPv4 discovery to fall back to leases, got error: %v", err)
	}
	if ip != "192.168.122.74" {
		t.Fatalf("expected discovered IP 192.168.122.74, got %q", ip)
	}

	gotCalls := strings.Join(calls, " -> ")
	for _, want := range []string{
		"--connect qemu:///system domifaddr ubuntu-desktop-arm64-dev-alchemy --source agent",
		"--connect qemu:///system domifaddr ubuntu-desktop-arm64-dev-alchemy --source lease",
	} {
		if !strings.Contains(gotCalls, want) {
			t.Fatalf("expected calls to contain %q, got %q", want, gotCalls)
		}
	}
}

func TestBuildWindowsProvisionArgs(t *testing.T) {
	projectDir := t.TempDir()
	config := windowsAnsibleConnectionConfig{
		User:           "Administrator",
		Password:       "Top$ecret!",
		Connection:     "winrm",
		WinrmTransport: "basic",
		Port:           "5985",
	}

	args, cleanup, err := buildWindowsProvisionArgs(projectDir, "172.25.125.159", config, ProvisionOptions{Check: true, Verbosity: defaultAnsibleVerbosity})
	if err != nil {
		t.Fatalf("buildWindowsProvisionArgs returned error: %v", err)
	}
	t.Cleanup(func() {
		if cleanupErr := cleanup(); cleanupErr != nil {
			t.Fatalf("failed to clean up extra vars file: %v", cleanupErr)
		}
	})

	if !strings.Contains(strings.Join(args, " "), "-i 172.25.125.159,") {
		t.Fatalf("expected inline inventory with discovered host IP, args: %v", args)
	}
	if !strings.Contains(strings.Join(args, " "), "-l 172.25.125.159") {
		t.Fatalf("expected limit to discovered host IP, args: %v", args)
	}
	if args[len(args)-1] != "--check" {
		t.Fatalf("expected --check to be passed through when requested, args: %v", args)
	}

	extraVarsIndex := -1
	for index, arg := range args {
		if arg == "--extra-vars" {
			extraVarsIndex = index + 1
			break
		}
	}
	if extraVarsIndex <= 0 || extraVarsIndex >= len(args) {
		t.Fatalf("expected --extra-vars with @temp file reference, args: %v", args)
	}
	if strings.Contains(strings.Join(args, " "), "Top$ecret!") {
		t.Fatalf("did not expect password in process arguments, args: %v", args)
	}
	if !strings.HasPrefix(args[extraVarsIndex], "@") {
		t.Fatalf("expected @<file> notation for --extra-vars, got: %q", args[extraVarsIndex])
	}

	extraVarsFilePath := filepath.Join(projectDir, strings.TrimPrefix(args[extraVarsIndex], "@"))
	content, readErr := os.ReadFile(extraVarsFilePath)
	if readErr != nil {
		t.Fatalf("failed to read extra vars file %q: %v", extraVarsFilePath, readErr)
	}

	extraVars := map[string]string{}
	if err := json.Unmarshal(content, &extraVars); err != nil {
		t.Fatalf("expected extra vars file to contain valid JSON, got error: %v", err)
	}

	if extraVars["ansible_user"] != "Administrator" {
		t.Fatalf("expected ansible_user in extra vars, got: %v", extraVars)
	}
	if extraVars["ansible_password"] != "Top$ecret!" {
		t.Fatalf("expected ansible_password in extra vars, got: %v", extraVars)
	}
	if extraVars["ansible_connection"] != "winrm" {
		t.Fatalf("expected ansible_connection in extra vars, got: %v", extraVars)
	}
	if extraVars["ansible_winrm_transport"] != "basic" {
		t.Fatalf("expected ansible_winrm_transport in extra vars, got: %v", extraVars)
	}
	if extraVars["ansible_port"] != "5985" {
		t.Fatalf("expected ansible_port in extra vars, got: %v", extraVars)
	}

	if cleanupErr := cleanup(); cleanupErr != nil {
		t.Fatalf("cleanup failed: %v", cleanupErr)
	}
	if _, statErr := os.Stat(extraVarsFilePath); !os.IsNotExist(statErr) {
		t.Fatalf("expected extra vars temp file to be deleted, stat error: %v", statErr)
	}
}

func TestBuildWindowsStaticInventoryProvisionArgsIncludesSecureWinRMSettings(t *testing.T) {
	projectDir := t.TempDir()
	config := windowsAnsibleConnectionConfig{
		User:                 localWindowsProvisionUserName,
		Password:             "N3wP@ssw0rd!",
		Connection:           "winrm",
		WinrmTransport:       "basic",
		Port:                 localWindowsWinRMHTTPSPort,
		WinrmScheme:          "https",
		ServerCertValidation: "ignore",
	}

	args, cleanup, err := buildWindowsStaticInventoryProvisionArgs(projectDir, localWindowsWinRMInventoryPath, localWindowsWinRMInventoryTarget, config, ProvisionOptions{Check: true, Verbosity: defaultAnsibleVerbosity})
	if err != nil {
		t.Fatalf("buildWindowsStaticInventoryProvisionArgs returned error: %v", err)
	}
	t.Cleanup(func() {
		if cleanupErr := cleanup(); cleanupErr != nil {
			t.Fatalf("failed to clean up extra vars file: %v", cleanupErr)
		}
	})

	if got := strings.Join(args, " "); !strings.Contains(got, "-i ./inventory/localhost_windows_winrm.yml") {
		t.Fatalf("expected windows localhost inventory, args: %v", args)
	}
	if got := strings.Join(args, " "); !strings.Contains(got, "-l windows_host") {
		t.Fatalf("expected windows localhost limit, args: %v", args)
	}
	if args[len(args)-1] != "--check" {
		t.Fatalf("expected --check to be passed through, args: %v", args)
	}

	extraVarsIndex := -1
	for index, arg := range args {
		if arg == "--extra-vars" {
			extraVarsIndex = index + 1
			break
		}
	}
	if extraVarsIndex <= 0 || extraVarsIndex >= len(args) {
		t.Fatalf("expected --extra-vars with @temp file reference, args: %v", args)
	}

	extraVarsFilePath := filepath.Join(projectDir, strings.TrimPrefix(args[extraVarsIndex], "@"))
	content, readErr := os.ReadFile(extraVarsFilePath)
	if readErr != nil {
		t.Fatalf("failed to read extra vars file %q: %v", extraVarsFilePath, readErr)
	}

	extraVars := map[string]string{}
	if err := json.Unmarshal(content, &extraVars); err != nil {
		t.Fatalf("expected extra vars file to contain valid JSON, got error: %v", err)
	}

	for key, expected := range map[string]string{
		"ansible_user":                         localWindowsProvisionUserName,
		"ansible_password":                     "N3wP@ssw0rd!",
		"ansible_connection":                   "winrm",
		"ansible_winrm_transport":              "basic",
		"ansible_port":                         localWindowsWinRMHTTPSPort,
		"ansible_winrm_scheme":                 "https",
		"ansible_winrm_server_cert_validation": "ignore",
	} {
		if extraVars[key] != expected {
			t.Fatalf("expected %s=%q in extra vars, got: %v", key, expected, extraVars)
		}
	}
}

func TestGenerateSecureLocalWindowsProvisionPasswordMeetsComplexityRequirements(t *testing.T) {
	password, err := generateSecureLocalWindowsProvisionPassword()
	if err != nil {
		t.Fatalf("generateSecureLocalWindowsProvisionPassword returned error: %v", err)
	}
	if len(password) != 24 {
		t.Fatalf("expected 24-character password, got %d", len(password))
	}

	var hasLower bool
	var hasUpper bool
	var hasDigit bool
	var hasSpecial bool
	for _, char := range password {
		switch {
		case char >= 'a' && char <= 'z':
			hasLower = true
		case char >= 'A' && char <= 'Z':
			hasUpper = true
		case char >= '0' && char <= '9':
			hasDigit = true
		default:
			hasSpecial = true
		}
	}

	if !hasLower || !hasUpper || !hasDigit || !hasSpecial {
		t.Fatalf("expected password to include lower, upper, digit, and special characters, got %q", password)
	}
}

func TestWriteSSHBytesEncodesSSHWireStringLength(t *testing.T) {
	var buffer bytes.Buffer
	if err := writeSSHBytes(&buffer, []byte("abc")); err != nil {
		t.Fatalf("writeSSHBytes returned error: %v", err)
	}

	expected := []byte{0, 0, 0, 3, 'a', 'b', 'c'}
	if !bytes.Equal(buffer.Bytes(), expected) {
		t.Fatalf("expected SSH wire encoding %v, got %v", expected, buffer.Bytes())
	}
}

func TestSSHWireValueLengthRejectsValuesLargerThanUint32(t *testing.T) {
	if got, err := sshWireValueLength(7); err != nil || got != 7 {
		t.Fatalf("expected small length conversion to succeed, got %d, %v", got, err)
	}

	if strconv.IntSize < 64 {
		t.Skip("overflow boundary test requires 64-bit ints")
	}

	maxUint32 := int(^uint32(0))
	if got, err := sshWireValueLength(maxUint32); err != nil || got != ^uint32(0) {
		t.Fatalf("expected max uint32 length conversion to succeed, got %d, %v", got, err)
	}

	if _, err := sshWireValueLength(maxUint32 + 1); err == nil {
		t.Fatal("expected oversized SSH wire value length to fail")
	}
}

func TestLocalWindowsProvisionBootstrapPowerShellHandlesMissingWSManPaths(t *testing.T) {
	if !strings.Contains(localWindowsWinRMProvisionBootstrapPowerShell, "function Get-WsmanBoolState") {
		t.Fatal("expected bootstrap script to tolerate missing WSMan paths")
	}
	if !strings.Contains(localWindowsWinRMProvisionBootstrapPowerShell, "function Get-WinRMServiceState") {
		t.Fatal("expected bootstrap script to capture the original WinRM service state")
	}
	if strings.Contains(localWindowsWinRMProvisionBootstrapPowerShell, "Enable-PSRemoting") {
		t.Fatal("expected bootstrap script to avoid Enable-PSRemoting so it does not create an HTTP listener")
	}
	if !strings.Contains(localWindowsWinRMProvisionBootstrapPowerShell, "Assert-WsmanPathExists 'WSMan:\\localhost\\Service\\Auth\\Basic'") {
		t.Fatal("expected bootstrap script to validate WSMan auth path after preparing WinRM")
	}
	if !strings.Contains(localWindowsWinRMProvisionBootstrapPowerShell, "BasicAuthPathExisted") {
		t.Fatal("expected bootstrap state to capture whether WSMan auth already existed")
	}
	if !strings.Contains(localWindowsWinRMProvisionBootstrapPowerShell, "WinRMServiceStartMode") {
		t.Fatal("expected bootstrap state to capture the original WinRM startup mode")
	}
	if !strings.Contains(localWindowsWinRMProvisionBootstrapPowerShell, "LocalAccountTokenFilterPolicy") {
		t.Fatal("expected bootstrap state to capture the LocalAccountTokenFilterPolicy setting")
	}
	if !strings.Contains(localWindowsWinRMProvisionBootstrapPowerShell, "Get-ManagedLocalUserState") {
		t.Fatal("expected bootstrap script to capture managed local user state through the shared helper")
	}
	if !strings.Contains(localWindowsWinRMProvisionBootstrapPowerShell, "UserExisted = [bool]$userState.Exists") {
		t.Fatal("expected bootstrap state to capture whether the local Ansible user already existed")
	}
	if !strings.Contains(localWindowsWinRMProvisionBootstrapPowerShell, "UserWasAdministrator = [bool]$userState.WasAdministrator") {
		t.Fatal("expected bootstrap state to capture prior local administrator membership")
	}
	if !strings.Contains(localWindowsWinRMProvisionBootstrapPowerShell, "Sailwright Ansible acct") {
		t.Fatal("expected bootstrap script to use a Windows-safe local user description")
	}
	if !strings.Contains(localWindowsWinRMProvisionBootstrapPowerShell, "S-1-5-32-544") {
		t.Fatal("expected bootstrap script to resolve the built-in Administrators group by SID")
	}
	if !strings.Contains(localWindowsWinRMProvisionBootstrapPowerShell, "Ensure-ManagedLocalUserForProvisioning") {
		t.Fatal("expected bootstrap script to provision the local Ansible account through the shared helper")
	}
	if !strings.Contains(localWindowsWinRMProvisionBootstrapPowerShell, "-Address IP:127.0.0.1") {
		t.Fatal("expected bootstrap script to bind the WinRM HTTPS listener to loopback only")
	}
	if strings.Contains(localWindowsWinRMProvisionBootstrapPowerShell, "-Address *") {
		t.Fatal("expected bootstrap script to avoid binding the WinRM HTTPS listener to every interface")
	}
	if !strings.Contains(localWindowsWinRMProvisionBootstrapPowerShell, "New-NetFirewallRule -Name 'DevAlchemyLocalWinRMHTTPS'") {
		t.Fatal("expected bootstrap script to create a dedicated HTTPS firewall rule")
	}
	if !strings.Contains(localWindowsWinRMProvisionBootstrapPowerShell, "-LocalAddress 127.0.0.1 -LocalPort 5986") {
		t.Fatal("expected bootstrap script to scope the WinRM HTTPS firewall rule to loopback")
	}
	if !strings.Contains(localWindowsWinRMProvisionBootstrapPowerShell, "Write-Output 'Local Windows provision bootstrap completed.'") {
		t.Fatal("expected bootstrap script to emit explicit progress output")
	}
}

func TestLocalWindowsProvisionCleanupPowerShellOnlyRestoresExistingWSManPaths(t *testing.T) {
	if !strings.Contains(localWindowsWinRMProvisionCleanupPowerShell, "$state.BasicAuthPathExisted") {
		t.Fatal("expected cleanup script to restore Basic auth only when the original path existed")
	}
	if !strings.Contains(localWindowsWinRMProvisionCleanupPowerShell, "$state.AllowUnencryptedPathExisted") {
		t.Fatal("expected cleanup script to restore AllowUnencrypted only when the original path existed")
	}
	if !strings.Contains(localWindowsWinRMProvisionCleanupPowerShell, "Restore-WinRMServiceState") {
		t.Fatal("expected cleanup script to restore the original WinRM service state")
	}
	if !strings.Contains(localWindowsWinRMProvisionCleanupPowerShell, "$originalListenerKeys") {
		t.Fatal("expected cleanup script to remove listeners that were added during provisioning")
	}
	if !strings.Contains(localWindowsWinRMProvisionCleanupPowerShell, "function Restore-WinRMServiceState") {
		t.Fatal("expected cleanup script to define its own WinRM restore helper")
	}
	if !strings.Contains(localWindowsWinRMProvisionCleanupPowerShell, "$forceWinRMUninstall") {
		t.Fatal("expected cleanup script to honor force WinRM uninstall mode")
	}
	if !strings.Contains(localWindowsWinRMProvisionCleanupPowerShell, "Restore-RegistryDWORDState") {
		t.Fatal("expected cleanup script to restore LocalAccountTokenFilterPolicy")
	}
	if !strings.Contains(localWindowsWinRMProvisionCleanupPowerShell, "Get-ManagedLocalUserCleanupPlan ([bool]$state.UserExisted) $forceWinRMUninstall 'WinRM'") {
		t.Fatal("expected cleanup script to route WinRM local-user cleanup through the shared helper")
	}
	if !strings.Contains(localWindowsWinRMProvisionCleanupPowerShell, "Restore-ManagedLocalUserState $administratorsGroupName $state $userName") {
		t.Fatal("expected cleanup script to restore pre-existing local Ansible users")
	}
	if !strings.Contains(localWindowsWinRMProvisionCleanupPowerShell, "Remove-ManagedLocalUserIfPresent $administratorsGroupName $userName") {
		t.Fatal("expected cleanup script to remove only temporary local Ansible users")
	}
	if !strings.Contains(localWindowsWinRMProvisionCleanupPowerShell, "preserving the pre-existing local Ansible account and restoring its original state.") {
		t.Fatal("expected cleanup script helper to preserve pre-existing local users during force WinRM uninstall")
	}
	if strings.Contains(localWindowsWinRMProvisionCleanupPowerShell, "Disabling the temporary local Ansible account.") {
		t.Fatal("expected cleanup script to restore or remove the local Ansible account instead of only disabling it")
	}
	if strings.Contains(localWindowsWinRMProvisionCleanupPowerShell, "Disable-PSRemoting") {
		t.Fatal("expected cleanup script to avoid Disable-PSRemoting and restore secure state directly")
	}
	if !strings.Contains(localWindowsWinRMProvisionCleanupPowerShell, "Write-Output 'Local Windows provision cleanup completed.'") {
		t.Fatal("expected cleanup script to emit explicit progress output")
	}
}

func TestBuildElevatedLocalWindowsPowerShellScriptIncludesEnvAndOutputCapture(t *testing.T) {
	script := buildElevatedLocalWindowsPowerShellScript("Write-Output 'hello'", []string{
		"DEV_ALCHEMY_LOCAL_WINDOWS_ANSIBLE_USER=devalchemy_ansible",
		"DEV_ALCHEMY_LOCAL_WINDOWS_ANSIBLE_PASSWORD=S3cret!'Value",
	}, `C:\Temp\alchemy-output.log`)

	if !strings.Contains(script, "$env:DEV_ALCHEMY_LOCAL_WINDOWS_ANSIBLE_USER = 'devalchemy_ansible'") {
		t.Fatal("expected elevated script to populate process environment values")
	}
	if !strings.Contains(script, "$env:DEV_ALCHEMY_LOCAL_WINDOWS_ANSIBLE_PASSWORD = 'S3cret!''Value'") {
		t.Fatal("expected elevated script to escape embedded single quotes in environment values")
	}
	if !strings.Contains(script, "} *>> $outputPath") {
		t.Fatal("expected elevated script to redirect all PowerShell streams to the output file")
	}
	if !strings.Contains(script, "Write-Output 'hello'") {
		t.Fatal("expected elevated script to include the original bootstrap body")
	}
}

func TestBuildLocalWindowsElevationLauncherPowerShellDetectsElevatedShellBeforeRunAs(t *testing.T) {
	launcher := buildLocalWindowsElevationLauncherPowerShell(`C:\Temp\bootstrap.ps1`, `C:\Temp\bootstrap.log`)

	if !strings.Contains(launcher, "[Security.Principal.WindowsIdentity]::GetCurrent()") {
		t.Fatal("expected launcher to inspect the current Windows identity")
	}
	if !strings.Contains(launcher, "WindowsBuiltInRole]::Administrator") {
		t.Fatal("expected launcher to check whether the current shell is already elevated")
	}
	if !strings.Contains(launcher, "& 'powershell.exe' -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $elevatedScriptPath") {
		t.Fatal("expected launcher to run the generated script directly when already elevated")
	}
	if !strings.Contains(launcher, "Start-Process -FilePath 'powershell.exe'") {
		t.Fatal("expected launcher to start a new powershell process")
	}
	if !strings.Contains(launcher, "-Verb RunAs") {
		t.Fatal("expected launcher to request UAC elevation with RunAs")
	}
	if !strings.Contains(launcher, "-WindowStyle Hidden") {
		t.Fatal("expected launcher to hide the elevated powershell window")
	}
	if !strings.Contains(launcher, `-File "C:\Temp\bootstrap.ps1"`) {
		t.Fatal("expected launcher to run the generated elevated script file")
	}
}

func TestBuildLocalWindowsWinRMProvisionScriptEnvIncludesForceFlag(t *testing.T) {
	env := buildLocalWindowsWinRMProvisionScriptEnv("state.json", "P@ssw0rd!", ProvisionOptions{LocalWindowsForceWinRMUninstall: true})
	got := strings.Join(env, "\n")

	if !strings.Contains(got, localWindowsForceWinRMUninstallEnvVar+"=true") {
		t.Fatal("expected force winrm uninstall env var to be included")
	}
	if !strings.Contains(got, localWindowsProvisionUserEnvVar+"="+localWindowsProvisionUserName) {
		t.Fatal("expected bootstrap env to include the temporary ansible user")
	}
	if !strings.Contains(got, localWindowsProvisionPasswordEnvVar+"=P@ssw0rd!") {
		t.Fatal("expected bootstrap env to include the generated password")
	}
}

func TestBuildLocalWindowsSSHProvisionScriptEnvIncludesForceFlag(t *testing.T) {
	env := buildLocalWindowsSSHProvisionScriptEnv("state.json", "P@ssw0rd!", "ssh-rsa AAAA", "2222", ProvisionOptions{LocalWindowsForceSSHUninstall: true})
	got := strings.Join(env, "\n")

	if !strings.Contains(got, localWindowsForceSSHUninstallEnvVar+"=true") {
		t.Fatal("expected force ssh uninstall env var to be included")
	}
	if !strings.Contains(got, localWindowsProvisionUserEnvVar+"="+localWindowsProvisionUserName) {
		t.Fatal("expected ssh bootstrap env to include the temporary ansible user")
	}
	if !strings.Contains(got, localWindowsProvisionPasswordEnvVar+"=P@ssw0rd!") {
		t.Fatal("expected ssh bootstrap env to include the generated password")
	}
	if !strings.Contains(got, localWindowsProvisionSSHPublicKeyEnvVar+"=ssh-rsa AAAA") {
		t.Fatal("expected ssh bootstrap env to include the generated public key")
	}
	if !strings.Contains(got, localWindowsSSHPortEnvVar+"=2222") {
		t.Fatal("expected ssh bootstrap env to include the temporary ssh port")
	}
}

func TestCanUseStandardLocalWindowsSSHPort(t *testing.T) {
	if !canUseStandardLocalWindowsSSHPort(nil) {
		t.Fatal("expected the standard ssh port to be usable when no listener is present")
	}
	if !canUseStandardLocalWindowsSSHPort([]localWindowsSSHListenerProcess{{ID: 1234, ProcessName: "sshd"}}) {
		t.Fatal("expected the standard ssh port to be usable when only sshd owns it")
	}
	if canUseStandardLocalWindowsSSHPort([]localWindowsSSHListenerProcess{{ID: 1234, ProcessName: "wslrelay"}}) {
		t.Fatal("did not expect the standard ssh port to be usable when another process owns it")
	}
	if canUseStandardLocalWindowsSSHPort([]localWindowsSSHListenerProcess{
		{ID: 1234, ProcessName: "sshd"},
		{ID: 5678, ProcessName: "wslrelay"},
	}) {
		t.Fatal("did not expect the standard ssh port to be usable when a non-sshd process shares it")
	}
}

func TestParseLocalWindowsSSHListenerProcessesSupportsArrays(t *testing.T) {
	processes, err := parseLocalWindowsSSHListenerProcesses(`[{"Id":6172,"ProcessName":"sshd"},{"Id":19388,"ProcessName":"wslrelay"}]`)
	if err != nil {
		t.Fatalf("parseLocalWindowsSSHListenerProcesses returned error: %v", err)
	}
	if len(processes) != 2 {
		t.Fatalf("expected 2 listener processes, got %d", len(processes))
	}
	if processes[0].ProcessName != "sshd" || processes[1].ProcessName != "wslrelay" {
		t.Fatalf("unexpected parsed listener processes: %+v", processes)
	}
}

func TestValidateLocalWindowsSSHListenerRejectsWSLRelay(t *testing.T) {
	err := validateLocalWindowsSSHListener("22", []localWindowsSSHListenerProcess{
		{ID: 6172, ProcessName: "sshd"},
		{ID: 19388, ProcessName: "wslrelay"},
	})
	if err == nil {
		t.Fatal("expected listener validation to fail when wslrelay shares the ssh port")
	}
	if !strings.Contains(err.Error(), "WSL SSH forwarding") {
		t.Fatalf("expected WSL-specific listener guidance, got: %v", err)
	}
}

func TestValidateLocalWindowsSSHListenerAcceptsWindowsSSHDOnly(t *testing.T) {
	err := validateLocalWindowsSSHListener("2222", []localWindowsSSHListenerProcess{
		{ID: 6172, ProcessName: "sshd"},
	})
	if err != nil {
		t.Fatalf("expected sshd-only listener validation to succeed, got: %v", err)
	}
}

func TestValidateLocalWindowsSSHRemoteBannerRejectsNonWindowsServer(t *testing.T) {
	output := "debug1: Remote protocol version 2.0, remote software version OpenSSH_8.4p1 Debian-5+deb11u5"

	err := validateLocalWindowsSSHRemoteBanner("22", output)
	if err == nil {
		t.Fatal("expected remote banner validation to fail for non-Windows OpenSSH")
	}
	if !strings.Contains(err.Error(), "Debian") {
		t.Fatalf("expected banner validation error to include the unexpected banner, got: %v", err)
	}
}

func TestValidateLocalWindowsSSHRemoteBannerAcceptsWindowsOpenSSH(t *testing.T) {
	output := "debug1: Remote protocol version 2.0, remote software version OpenSSH_for_Windows_9.5"

	err := validateLocalWindowsSSHRemoteBanner("2222", output)
	if err != nil {
		t.Fatalf("expected Windows OpenSSH banner validation to succeed, got: %v", err)
	}
}

func TestDecodeLocalWindowsPowerShellOutputHandlesUTF16LE(t *testing.T) {
	decoded := decodeLocalWindowsPowerShellOutput([]byte{0xFF, 0xFE, 'O', 0x00, 'K', 0x00})
	if decoded != "OK" {
		t.Fatalf("expected UTF-16LE output to decode, got %q", decoded)
	}
}

func TestLogLocalWindowsPowerShellOutputChunkLogsCompleteLinesWithPrefix(t *testing.T) {
	var buffer bytes.Buffer
	previousOutput := log.Writer()
	previousFlags := log.Flags()
	previousPrefix := log.Prefix()
	log.SetOutput(&buffer)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})

	pending := logLocalWindowsPowerShellOutputChunk(localWindowsWinRMBootstrapLogPrefix, "first line\r\nsecond", "", false)
	if pending != "second" {
		t.Fatalf("expected incomplete line to be buffered, got %q", pending)
	}

	pending = logLocalWindowsPowerShellOutputChunk(localWindowsWinRMBootstrapLogPrefix, " line\n\nthird line", pending, false)
	if pending != "third line" {
		t.Fatalf("expected trailing partial line to remain buffered, got %q", pending)
	}

	pending = logLocalWindowsPowerShellOutputChunk(localWindowsWinRMBootstrapLogPrefix, "", pending, true)
	if pending != "" {
		t.Fatalf("expected flush to clear the buffered line, got %q", pending)
	}

	logs := strings.TrimSpace(buffer.String())
	if !strings.Contains(logs, localWindowsWinRMBootstrapLogPrefix+" powershell: first line") {
		t.Fatalf("expected first log line with prefix, got %q", logs)
	}
	if !strings.Contains(logs, localWindowsWinRMBootstrapLogPrefix+" powershell: second line") {
		t.Fatalf("expected second log line with prefix, got %q", logs)
	}
	if !strings.Contains(logs, localWindowsWinRMBootstrapLogPrefix+" powershell: third line") {
		t.Fatalf("expected flushed partial line with prefix, got %q", logs)
	}
}

func TestRunLocalWindowsProvisionAlwaysCleansUpSecureSession(t *testing.T) {
	previousSetup := setupLocalWindowsWinRMProvisionSessionFunc
	previousCleanup := cleanupLocalWindowsWinRMProvisionSessionFunc
	previousRunner := runAnsibleProvisionCommandFunc
	t.Cleanup(func() {
		setupLocalWindowsWinRMProvisionSessionFunc = previousSetup
		cleanupLocalWindowsWinRMProvisionSessionFunc = previousCleanup
		runAnsibleProvisionCommandFunc = previousRunner
	})

	projectDir := t.TempDir()
	statePath := filepath.Join(projectDir, "session-state.json")
	var cleanedUp bool

	setupLocalWindowsWinRMProvisionSessionFunc = func(_ string, _ ProvisionOptions) (localWindowsWinRMProvisionSession, error) {
		return localWindowsWinRMProvisionSession{
			ConnectionConfig: windowsAnsibleConnectionConfig{
				User:                 localWindowsProvisionUserName,
				Password:             "N3wP@ssw0rd!",
				Connection:           "winrm",
				WinrmTransport:       "basic",
				Port:                 localWindowsWinRMHTTPSPort,
				WinrmScheme:          "https",
				ServerCertValidation: "ignore",
			},
			StatePath: statePath,
		}, nil
	}
	cleanupLocalWindowsWinRMProvisionSessionFunc = func(_ string, session localWindowsWinRMProvisionSession, _ ProvisionOptions) error {
		if session.StatePath != statePath {
			t.Fatalf("expected cleanup to receive state path %q, got %q", statePath, session.StatePath)
		}
		cleanedUp = true
		return nil
	}
	runAnsibleProvisionCommandFunc = func(_ string, _ []string, _ time.Duration, _ string) error {
		return errors.New("ansible failed")
	}

	err := runLocalWindowsProvision(projectDir, ProvisionOptions{Check: true, Verbosity: defaultAnsibleVerbosity})
	if err == nil {
		t.Fatal("expected local windows provision to return an ansible error")
	}
	if !strings.Contains(err.Error(), "ansible provisioning failed for local host windows") {
		t.Fatalf("expected wrapped ansible error, got: %v", err)
	}
	if !cleanedUp {
		t.Fatal("expected secure local windows cleanup to run after ansible failure")
	}
}

func TestRunLocalWindowsSSHProvisionStopsAfterPreflightFailureAndCleansUp(t *testing.T) {
	previousSetup := setupLocalWindowsSSHProvisionSessionFunc
	previousCleanup := cleanupLocalWindowsSSHProvisionSessionFunc
	previousPreflight := runLocalWindowsSSHPreflightFunc
	previousRunner := runAnsibleProvisionCommandFunc
	t.Cleanup(func() {
		setupLocalWindowsSSHProvisionSessionFunc = previousSetup
		cleanupLocalWindowsSSHProvisionSessionFunc = previousCleanup
		runLocalWindowsSSHPreflightFunc = previousPreflight
		runAnsibleProvisionCommandFunc = previousRunner
	})

	projectDir := t.TempDir()
	statePath := filepath.Join(projectDir, "session-state.json")
	privateKeyPath := filepath.Join(projectDir, ".local-windows-provision-key-test.pem")
	var cleanedUp bool
	var ansibleRan bool

	setupLocalWindowsSSHProvisionSessionFunc = func(_ string, _ ProvisionOptions) (localWindowsSSHProvisionSession, error) {
		return localWindowsSSHProvisionSession{
			ConnectionConfig: sshAnsibleConnectionConfig{
				User:            localWindowsProvisionUserName,
				Connection:      "ssh",
				SshCommonArgs:   localWindowsSSHCommonArgs,
				SshTimeout:      "120",
				SshRetries:      "3",
				PrivateKeyFile:  filepath.Base(privateKeyPath),
				ShellType:       "powershell",
				ShellExecutable: "powershell.exe",
			},
			StatePath:      statePath,
			PrivateKeyPath: privateKeyPath,
		}, nil
	}
	cleanupLocalWindowsSSHProvisionSessionFunc = func(_ string, session localWindowsSSHProvisionSession, _ ProvisionOptions) error {
		if session.StatePath != statePath {
			t.Fatalf("expected cleanup to receive state path %q, got %q", statePath, session.StatePath)
		}
		cleanedUp = true
		return nil
	}
	runLocalWindowsSSHPreflightFunc = func(_ string, _ sshAnsibleConnectionConfig) error {
		return errors.New("permission denied (publickey)")
	}
	runAnsibleProvisionCommandFunc = func(_ string, _ []string, _ time.Duration, _ string) error {
		ansibleRan = true
		return nil
	}

	err := runLocalWindowsSSHProvision(projectDir, ProvisionOptions{Check: true, Verbosity: defaultAnsibleVerbosity})
	if err == nil {
		t.Fatal("expected local windows ssh provision to fail when the direct ssh preflight fails")
	}
	if !strings.Contains(err.Error(), "direct SSH preflight failed") {
		t.Fatalf("expected preflight error context, got: %v", err)
	}
	if ansibleRan {
		t.Fatal("did not expect ansible to start after the direct ssh preflight failed")
	}
	if !cleanedUp {
		t.Fatal("expected secure local windows cleanup to run after the direct ssh preflight failure")
	}
}

func TestRunLocalWindowsProvisionCleansUpWhenArgumentBuildFails(t *testing.T) {
	previousSetup := setupLocalWindowsWinRMProvisionSessionFunc
	previousCleanup := cleanupLocalWindowsWinRMProvisionSessionFunc
	t.Cleanup(func() {
		setupLocalWindowsWinRMProvisionSessionFunc = previousSetup
		cleanupLocalWindowsWinRMProvisionSessionFunc = previousCleanup
	})

	var cleanedUp bool
	setupLocalWindowsWinRMProvisionSessionFunc = func(_ string, _ ProvisionOptions) (localWindowsWinRMProvisionSession, error) {
		return localWindowsWinRMProvisionSession{
			ConnectionConfig: windowsAnsibleConnectionConfig{
				User:       localWindowsProvisionUserName,
				Connection: "winrm",
			},
			StatePath: filepath.Join(t.TempDir(), "session-state.json"),
		}, nil
	}
	cleanupLocalWindowsWinRMProvisionSessionFunc = func(_ string, _ localWindowsWinRMProvisionSession, _ ProvisionOptions) error {
		cleanedUp = true
		return nil
	}

	err := runLocalWindowsProvision("/definitely/missing/project-dir", ProvisionOptions{Verbosity: defaultAnsibleVerbosity})
	if err == nil {
		t.Fatal("expected local windows provision to fail when arg build cannot create temp files")
	}
	if !strings.Contains(err.Error(), "failed to build ansible arguments") {
		t.Fatalf("expected arg-build error, got: %v", err)
	}
	if !cleanedUp {
		t.Fatal("expected secure local windows cleanup to run after arg-build failure")
	}
}

func TestRunLocalWindowsProvisionSessionWrapsBuildArgFailuresWithCleanupErrors(t *testing.T) {
	err := runLocalWindowsProvisionSession(t.TempDir(), ProvisionOptions{}, localWindowsProvisionSessionRunner[string]{
		setup: func(string, ProvisionOptions) (string, error) {
			return "session", nil
		},
		buildArgs: func(string, string, ProvisionOptions) ([]string, func() error, error) {
			return nil, nil, errors.New("build failed")
		},
		cleanup: func(string, string, ProvisionOptions) error {
			return errors.New("cleanup failed")
		},
		buildArgsError: func(err error, cleanupErr error) error {
			return formatLocalWindowsProvisionStepError(
				"failed to build ansible arguments for secure local windows WinRM provision",
				err,
				cleanupErr,
				"WinRM",
			)
		},
		provisionResult: func(error, error, error) error {
			t.Fatal("did not expect provisionResult to run when buildArgs failed")
			return nil
		},
	})
	if err == nil {
		t.Fatal("expected build arg failure to be returned")
	}
	if !strings.Contains(err.Error(), "failed to build ansible arguments for secure local windows WinRM provision: build failed") {
		t.Fatalf("expected build arg failure context, got: %v", err)
	}
	if !strings.Contains(err.Error(), "also failed to restore secure WinRM state: cleanup failed") {
		t.Fatalf("expected cleanup failure to be merged into build arg failure, got: %v", err)
	}
}

func TestRunLocalWindowsProvisionSessionMergesAnsibleAndCleanupFailures(t *testing.T) {
	previousRunner := runAnsibleProvisionCommandFunc
	t.Cleanup(func() {
		runAnsibleProvisionCommandFunc = previousRunner
	})

	runAnsibleProvisionCommandFunc = func(_ string, _ []string, _ time.Duration, _ string) error {
		return errors.New("ansible failed")
	}

	err := runLocalWindowsProvisionSession(t.TempDir(), ProvisionOptions{}, localWindowsProvisionSessionRunner[string]{
		setup: func(string, ProvisionOptions) (string, error) {
			return "session", nil
		},
		buildArgs: func(string, string, ProvisionOptions) ([]string, func() error, error) {
			return []string{"ansible-playbook"}, func() error {
				return errors.New("temp cleanup failed")
			}, nil
		},
		cleanup: func(string, string, ProvisionOptions) error {
			return errors.New("session cleanup failed")
		},
		buildArgsError: func(error, error) error {
			t.Fatal("did not expect buildArgsError to run when buildArgs succeeded")
			return nil
		},
		provisionResult: func(runErr error, argsCleanupErr error, cleanupErr error) error {
			return formatLocalWindowsProvisionOutcome("ssh", "SSH", runErr, argsCleanupErr, cleanupErr)
		},
		ansibleLogPrefix: "local:windows:ssh:provision",
		runTimeout:       time.Minute,
	})
	if err == nil {
		t.Fatal("expected ansible failure to be returned")
	}
	if !strings.Contains(err.Error(), "ansible provisioning failed for local host windows via ssh: ansible failed") {
		t.Fatalf("expected ansible failure context, got: %v", err)
	}
	if !strings.Contains(err.Error(), "also failed to clean ansible temp files: temp cleanup failed; cleanup failed: session cleanup failed") {
		t.Fatalf("expected both cleanup failures to be merged, got: %v", err)
	}
}

func TestBuildLocalProvisionArgsForWindows(t *testing.T) {
	args, err := buildLocalProvisionArgs(alchemy_build.HostOsWindows, ProvisionOptions{Check: true, Verbosity: defaultAnsibleVerbosity})
	if err != nil {
		t.Fatalf("buildLocalProvisionArgs returned error: %v", err)
	}

	if got := strings.Join(args, " "); !strings.Contains(got, "-i ./inventory/localhost_windows_winrm.yml") {
		t.Fatalf("expected windows localhost winrm inventory, args: %v", args)
	}
	if got := strings.Join(args, " "); !strings.Contains(got, "-l windows_host") {
		t.Fatalf("expected windows localhost limit, args: %v", args)
	}
	if args[len(args)-1] != "--check" {
		t.Fatalf("expected --check to be passed through when requested, args: %v", args)
	}
}

func TestBuildLocalProvisionArgsForWindowsSSH(t *testing.T) {
	args, err := buildLocalProvisionArgs(alchemy_build.HostOsWindows, ProvisionOptions{
		Check:                true,
		Verbosity:            defaultAnsibleVerbosity,
		LocalWindowsProtocol: LocalWindowsProvisionProtocolSSH,
	})
	if err != nil {
		t.Fatalf("buildLocalProvisionArgs returned error: %v", err)
	}

	if got := strings.Join(args, " "); !strings.Contains(got, "-i ./inventory/localhost_windows_ssh.yml") {
		t.Fatalf("expected windows localhost ssh inventory, args: %v", args)
	}
	if got := strings.Join(args, " "); !strings.Contains(got, "-l windows_host") {
		t.Fatalf("expected windows localhost limit, args: %v", args)
	}
	if args[len(args)-1] != "--check" {
		t.Fatalf("expected --check to be passed through when requested, args: %v", args)
	}
}

func TestBuildLocalProvisionArgsForDarwin(t *testing.T) {
	args, err := buildLocalProvisionArgs(alchemy_build.HostOsDarwin, ProvisionOptions{Verbosity: defaultAnsibleVerbosity})
	if err != nil {
		t.Fatalf("buildLocalProvisionArgs returned error: %v", err)
	}

	if got := strings.Join(args, " "); !strings.Contains(got, "-i ./inventory/localhost.yaml") {
		t.Fatalf("expected unix localhost inventory, args: %v", args)
	}
	if got := strings.Join(args, " "); !strings.Contains(got, "-l localhost") {
		t.Fatalf("expected localhost limit for darwin, args: %v", args)
	}
	if args[len(args)-1] == "--check" {
		t.Fatalf("did not expect --check when not requested, args: %v", args)
	}
}

func TestBuildLocalProvisionArgsForLinux(t *testing.T) {
	args, err := buildLocalProvisionArgs(alchemy_build.HostOsLinux, ProvisionOptions{Check: true, Verbosity: defaultAnsibleVerbosity})
	if err != nil {
		t.Fatalf("buildLocalProvisionArgs returned error: %v", err)
	}

	if got := strings.Join(args, " "); !strings.Contains(got, "-i ./inventory/localhost.yaml") {
		t.Fatalf("expected unix localhost inventory, args: %v", args)
	}
	if got := strings.Join(args, " "); !strings.Contains(got, "-l localhost") {
		t.Fatalf("expected localhost limit for linux, args: %v", args)
	}
	if args[len(args)-1] != "--check" {
		t.Fatalf("expected --check to be passed through when requested, args: %v", args)
	}
}

func TestBuildLocalProvisionArgsReturnsErrorForUnsupportedHost(t *testing.T) {
	_, err := buildLocalProvisionArgs(alchemy_build.HostOsType("solaris"), ProvisionOptions{Verbosity: defaultAnsibleVerbosity})
	if err == nil {
		t.Fatal("expected unsupported host OS to return an error")
	}
	if !strings.Contains(err.Error(), "local provision is not implemented") {
		t.Fatalf("expected unsupported local provision error, got: %v", err)
	}
}

func TestBuildLocalProvisionArgsUsesCustomInventoryWithoutDefaultLimit(t *testing.T) {
	args, err := buildLocalProvisionArgs(alchemy_build.HostOsLinux, ProvisionOptions{
		Verbosity:     1,
		InventoryPath: "./inventory/custom.yml",
		ExtraArgs:     []string{"--limit", "workstation", "--diff"},
	})
	if err != nil {
		t.Fatalf("buildLocalProvisionArgs returned error: %v", err)
	}

	got := strings.Join(args, " ")
	if !strings.Contains(got, "-i ./inventory/custom.yml") {
		t.Fatalf("expected custom inventory path, args: %v", args)
	}
	if strings.Contains(got, "-l localhost") {
		t.Fatalf("did not expect default localhost limit with custom inventory, args: %v", args)
	}
	if !strings.Contains(got, "--limit workstation --diff") {
		t.Fatalf("expected extra ansible args to be appended, args: %v", args)
	}
	if !strings.Contains(got, "-v") || strings.Contains(got, "-vv") {
		t.Fatalf("expected verbosity level 1 to render as -v, args: %v", args)
	}
}

func TestBuildStaticInventoryProvisionArgsAppendsProvisionOptions(t *testing.T) {
	args, err := buildStaticInventoryProvisionArgs("./inventory/localhost.yaml", "localhost", ProvisionOptions{
		Check:     true,
		Verbosity: 2,
		ExtraArgs: []string{"--diff", "--tags", "java"},
	})
	if err != nil {
		t.Fatalf("buildStaticInventoryProvisionArgs returned error: %v", err)
	}

	got := strings.Join(args, " ")
	if !strings.Contains(got, "-l localhost") {
		t.Fatalf("expected inventory limit to be preserved, args: %v", args)
	}
	if !strings.Contains(got, "-vv") || strings.Contains(got, "-vvv") {
		t.Fatalf("expected verbosity level 2 to render as -vv, args: %v", args)
	}
	if !strings.Contains(got, "--check --diff --tags java") {
		t.Fatalf("expected check flag and extra ansible args to be appended, args: %v", args)
	}
}

func TestBuildStaticInventoryProvisionArgsUsesCustomPlaybookPath(t *testing.T) {
	args, err := buildStaticInventoryProvisionArgs("./inventory/localhost.yaml", "localhost", ProvisionOptions{
		PlaybookPath: "./playbooks/bootstrap.yml",
		Verbosity:    defaultAnsibleVerbosity,
	})
	if err != nil {
		t.Fatalf("buildStaticInventoryProvisionArgs returned error: %v", err)
	}

	if len(args) == 0 {
		t.Fatal("expected ansible args to be returned")
	}
	if args[0] != "./playbooks/bootstrap.yml" {
		t.Fatalf("expected custom playbook path as first arg, got %q", args[0])
	}
}

func TestBuildStaticInventoryProvisionArgsRejectsVerbosityOutsideSupportedRange(t *testing.T) {
	_, err := buildStaticInventoryProvisionArgs("./inventory/localhost.yaml", "localhost", ProvisionOptions{
		Verbosity: maxAnsibleVerbosity + 1,
	})
	if err == nil {
		t.Fatal("expected buildStaticInventoryProvisionArgs to reject unsupported verbosity")
	}
	if !strings.Contains(err.Error(), "ansible verbosity must be between 0 and 4") {
		t.Fatalf("expected explicit verbosity validation error, got: %v", err)
	}
}

func TestLoadWindowsHypervAnsibleConnectionConfig_UsesDotEnvValues(t *testing.T) {
	projectDir := t.TempDir()
	dotEnvPath := filepath.Join(projectDir, ".env")

	content := strings.Join([]string{
		hypervWindowsAnsibleUserEnvVar + "=Administrator",
		hypervWindowsAnsiblePasswordEnvVar + "='P@ssw0rd! with spaces'",
		hypervWindowsAnsibleConnectionEnvVar + "=winrm",
		hypervWindowsAnsibleWinrmTransportEnvVar + "=basic",
		hypervWindowsAnsiblePortEnvVar + "=5985",
		"",
	}, "\n")
	if err := os.WriteFile(dotEnvPath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create .env fixture: %v", err)
	}

	connectionConfig, err := loadWindowsHypervAnsibleConnectionConfig(projectDir)
	if err != nil {
		t.Fatalf("loadWindowsHypervAnsibleConnectionConfig returned error: %v", err)
	}

	if connectionConfig.User != "Administrator" {
		t.Fatalf("expected user from .env, got %q", connectionConfig.User)
	}
	if connectionConfig.Password != "P@ssw0rd! with spaces" {
		t.Fatalf("expected password from .env, got %q", connectionConfig.Password)
	}
	if connectionConfig.Connection != "winrm" {
		t.Fatalf("expected connection from .env, got %q", connectionConfig.Connection)
	}
	if connectionConfig.WinrmTransport != "basic" {
		t.Fatalf("expected winrm transport from .env, got %q", connectionConfig.WinrmTransport)
	}
	if connectionConfig.Port != "5985" {
		t.Fatalf("expected port from .env, got %q", connectionConfig.Port)
	}
}

func TestLoadWindowsUtmAnsibleConnectionConfig_UsesDotEnvValues(t *testing.T) {
	projectDir := t.TempDir()
	dotEnvPath := filepath.Join(projectDir, ".env")

	content := strings.Join([]string{
		utmWindowsAnsibleUserEnvVar + "=Administrator",
		utmWindowsAnsiblePasswordEnvVar + "='P@ssw0rd! with spaces'",
		utmWindowsAnsibleConnectionEnvVar + "=winrm",
		utmWindowsAnsibleWinrmTransportEnvVar + "=basic",
		utmWindowsAnsiblePortEnvVar + "=5985",
		"",
	}, "\n")
	if err := os.WriteFile(dotEnvPath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create .env fixture: %v", err)
	}

	connectionConfig, err := loadWindowsUtmAnsibleConnectionConfig(projectDir)
	if err != nil {
		t.Fatalf("loadWindowsUtmAnsibleConnectionConfig returned error: %v", err)
	}

	if connectionConfig.User != "Administrator" {
		t.Fatalf("expected user from .env, got %q", connectionConfig.User)
	}
	if connectionConfig.Password != "P@ssw0rd! with spaces" {
		t.Fatalf("expected password from .env, got %q", connectionConfig.Password)
	}
	if connectionConfig.Connection != "winrm" {
		t.Fatalf("expected connection from .env, got %q", connectionConfig.Connection)
	}
	if connectionConfig.WinrmTransport != "basic" {
		t.Fatalf("expected winrm transport from .env, got %q", connectionConfig.WinrmTransport)
	}
	if connectionConfig.Port != "5985" {
		t.Fatalf("expected port from .env, got %q", connectionConfig.Port)
	}
}

func TestLoadWindowsLibvirtAnsibleConnectionConfig_UsesDotEnvValues(t *testing.T) {
	projectDir := t.TempDir()
	dotEnvPath := filepath.Join(projectDir, ".env")

	content := strings.Join([]string{
		libvirtWindowsAnsibleUserEnvVar + "=Administrator",
		libvirtWindowsAnsiblePasswordEnvVar + "='P@ssw0rd! with spaces'",
		libvirtWindowsAnsibleConnectionEnvVar + "=winrm",
		libvirtWindowsAnsibleWinrmTransportEnvVar + "=basic",
		libvirtWindowsAnsiblePortEnvVar + "=5985",
		"",
	}, "\n")
	if err := os.WriteFile(dotEnvPath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create .env fixture: %v", err)
	}

	connectionConfig, err := loadWindowsLibvirtAnsibleConnectionConfig(projectDir)
	if err != nil {
		t.Fatalf("loadWindowsLibvirtAnsibleConnectionConfig returned error: %v", err)
	}

	if connectionConfig.User != "Administrator" {
		t.Fatalf("expected user from .env, got %q", connectionConfig.User)
	}
	if connectionConfig.Password != "P@ssw0rd! with spaces" {
		t.Fatalf("expected password from .env, got %q", connectionConfig.Password)
	}
	if connectionConfig.Connection != "winrm" {
		t.Fatalf("expected connection from .env, got %q", connectionConfig.Connection)
	}
	if connectionConfig.WinrmTransport != "basic" {
		t.Fatalf("expected winrm transport from .env, got %q", connectionConfig.WinrmTransport)
	}
	if connectionConfig.Port != "5985" {
		t.Fatalf("expected port from .env, got %q", connectionConfig.Port)
	}
}

func TestLoadWindowsHypervAnsibleConnectionConfig_EnvOverridesDotEnv(t *testing.T) {
	projectDir := t.TempDir()
	dotEnvPath := filepath.Join(projectDir, ".env")

	content := strings.Join([]string{
		hypervWindowsAnsibleUserEnvVar + "=file-user",
		hypervWindowsAnsiblePasswordEnvVar + "=file-pass",
		"",
	}, "\n")
	if err := os.WriteFile(dotEnvPath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create .env fixture: %v", err)
	}

	t.Setenv(hypervWindowsAnsibleUserEnvVar, "env-user")
	t.Setenv(hypervWindowsAnsiblePasswordEnvVar, "env-pass")
	t.Setenv(hypervWindowsAnsibleConnectionEnvVar, "winrm")
	t.Setenv(hypervWindowsAnsibleWinrmTransportEnvVar, "basic")
	t.Setenv(hypervWindowsAnsiblePortEnvVar, "5986")

	connectionConfig, err := loadWindowsHypervAnsibleConnectionConfig(projectDir)
	if err != nil {
		t.Fatalf("loadWindowsHypervAnsibleConnectionConfig returned error: %v", err)
	}

	if connectionConfig.User != "env-user" {
		t.Fatalf("expected environment user to override .env, got %q", connectionConfig.User)
	}
	if connectionConfig.Password != "env-pass" {
		t.Fatalf("expected environment password to override .env, got %q", connectionConfig.Password)
	}
	if connectionConfig.Port != "5986" {
		t.Fatalf("expected environment port to override .env, got %q", connectionConfig.Port)
	}
}

func TestLoadWindowsHypervAnsibleConnectionConfig_ReturnsErrorWhenRequiredValuesMissing(t *testing.T) {
	projectDir := t.TempDir()

	_, err := loadWindowsHypervAnsibleConnectionConfig(projectDir)
	if err == nil {
		t.Fatal("expected missing configuration to return an error")
	}
	if !strings.Contains(err.Error(), hypervWindowsAnsibleUserEnvVar) {
		t.Fatalf("expected missing user env var name in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), hypervWindowsAnsiblePasswordEnvVar) {
		t.Fatalf("expected missing password env var name in error, got: %v", err)
	}
}

func TestLoadWindowsUtmAnsibleConnectionConfig_ReturnsErrorWhenRequiredValuesMissing(t *testing.T) {
	projectDir := t.TempDir()

	_, err := loadWindowsUtmAnsibleConnectionConfig(projectDir)
	if err == nil {
		t.Fatal("expected missing configuration to return an error")
	}
	if !strings.Contains(err.Error(), utmWindowsAnsibleUserEnvVar) {
		t.Fatalf("expected missing user env var name in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), utmWindowsAnsiblePasswordEnvVar) {
		t.Fatalf("expected missing password env var name in error, got: %v", err)
	}
}

func TestLoadWindowsLibvirtAnsibleConnectionConfig_ReturnsErrorWhenRequiredValuesMissing(t *testing.T) {
	projectDir := t.TempDir()

	_, err := loadWindowsLibvirtAnsibleConnectionConfig(projectDir)
	if err == nil {
		t.Fatal("expected missing configuration to return an error")
	}
	if !strings.Contains(err.Error(), libvirtWindowsAnsibleUserEnvVar) {
		t.Fatalf("expected missing user env var name in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), libvirtWindowsAnsiblePasswordEnvVar) {
		t.Fatalf("expected missing password env var name in error, got: %v", err)
	}
}

func TestParseDotEnvFile_ReturnsErrorForInvalidLine(t *testing.T) {
	projectDir := t.TempDir()
	dotEnvPath := filepath.Join(projectDir, ".env")

	if err := os.WriteFile(dotEnvPath, []byte("INVALID_LINE"), 0o644); err != nil {
		t.Fatalf("failed to create .env fixture: %v", err)
	}

	_, err := parseDotEnvFile(dotEnvPath)
	if err == nil {
		t.Fatal("expected parseDotEnvFile to return an error")
	}
	if !strings.Contains(err.Error(), "expected KEY=VALUE") {
		t.Fatalf("unexpected parse error: %v", err)
	}
}

func TestRunProvisionReturnsNotImplementedForUnsupportedConfig(t *testing.T) {
	vm := alchemy_build.VirtualMachineConfig{
		OS:                   "windows11",
		Arch:                 "amd64",
		HostOs:               alchemy_build.HostOsWindows,
		VirtualizationEngine: alchemy_build.VirtualizationEngineVirtualBox,
	}

	err := RunProvision(vm, false)
	if err == nil {
		t.Fatal("expected RunProvision to fail for unsupported vm configuration, got nil")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("expected not implemented message, got: %v", err)
	}
}

func TestWindowsPathToCygwinPath(t *testing.T) {
	got, err := windowsPathToCygwinPath(`C:\workspace\dev-alchemy`)
	if err != nil {
		t.Fatalf("windowsPathToCygwinPath returned error: %v", err)
	}

	want := "/cygdrive/c/workspace/dev-alchemy"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestWindowsPathToCygwinPath_ReturnsErrorForInvalidPath(t *testing.T) {
	_, err := windowsPathToCygwinPath(`/workspaces/dev-alchemy`)
	if err == nil {
		t.Fatal("expected windowsPathToCygwinPath to fail for non-windows path")
	}
}

func TestAnsibleArgsForCygwinConvertsGeneratedWindowsPaths(t *testing.T) {
	got, err := ansibleArgsForCygwin([]string{
		`C:\workspace\dev-alchemy\playbooks\custom.yml`,
		"-i",
		`D:\inventory\hosts.yml`,
		"--check",
	})
	if err != nil {
		t.Fatalf("ansibleArgsForCygwin returned error: %v", err)
	}

	want := []string{
		"/cygdrive/c/workspace/dev-alchemy/playbooks/custom.yml",
		"-i",
		"/cygdrive/d/inventory/hosts.yml",
		"--check",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestAnsibleArgsForCygwinLeavesRelativeAndCygwinPaths(t *testing.T) {
	args := []string{
		"./playbooks/setup.yml",
		"-i",
		"/cygdrive/c/inventory/hosts.yml",
	}

	got, err := ansibleArgsForCygwin(args)
	if err != nil {
		t.Fatalf("ansibleArgsForCygwin returned error: %v", err)
	}
	if !reflect.DeepEqual(got, args) {
		t.Fatalf("expected args to remain unchanged, got %v", got)
	}
}

func TestBashSingleQuote_EscapesEmbeddedQuotes(t *testing.T) {
	input := `C:\Users\O'Connor\dev-alchemy`
	got := bashSingleQuote(input)
	want := `'C:\Users\O'"'"'Connor\dev-alchemy'`

	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestResolveCygwinBashPath_ConvertsMinttyToBash(t *testing.T) {
	got := resolveCygwinBashPath(`C:\tools\cygwin\bin\mintty.exe`)
	want := `C:\tools\cygwin\bin\bash.exe`

	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestResolveCygwinBashPath_LeavesBashPathUntouched(t *testing.T) {
	input := `C:\tools\cygwin\bin\bash.exe`
	got := resolveCygwinBashPath(input)

	if got != input {
		t.Fatalf("expected %q, got %q", input, got)
	}
}

func TestGetCygwinBashExecutable_UsesConfiguredBashPath(t *testing.T) {
	tempDir := t.TempDir()
	bashPath := filepath.Join(tempDir, "bash.exe")
	if err := os.WriteFile(bashPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("failed to create fake bash executable: %v", err)
	}

	t.Setenv("CYGWIN_BASH_PATH", bashPath)
	t.Setenv("CYGWIN_TERMINAL_PATH", "")

	got, err := getCygwinBashExecutable()
	if err != nil {
		t.Fatalf("expected configured cygwin bash path to succeed, got error: %v", err)
	}
	if got != bashPath {
		t.Fatalf("expected %q, got %q", bashPath, got)
	}
}

func TestGetCygwinBashExecutable_UsesConfiguredTerminalPath(t *testing.T) {
	tempDir := t.TempDir()
	terminalPath := filepath.Join(tempDir, "mintty.exe")
	bashPath := filepath.Join(tempDir, "bash.exe")
	if err := os.WriteFile(terminalPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("failed to create fake terminal executable: %v", err)
	}
	if err := os.WriteFile(bashPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("failed to create fake bash executable: %v", err)
	}

	t.Setenv("CYGWIN_BASH_PATH", "")
	t.Setenv("CYGWIN_TERMINAL_PATH", terminalPath)

	got, err := getCygwinBashExecutable()
	if err != nil {
		t.Fatalf("expected configured cygwin terminal path to succeed, got error: %v", err)
	}
	if got != bashPath {
		t.Fatalf("expected %q, got %q", bashPath, got)
	}
}

func TestGetCygwinBashExecutable_ReturnsErrorForInvalidConfiguredPath(t *testing.T) {
	t.Setenv("CYGWIN_BASH_PATH", filepath.Join(t.TempDir(), "missing-bash.exe"))
	t.Setenv("CYGWIN_TERMINAL_PATH", "")

	_, err := getCygwinBashExecutable()
	if err == nil {
		t.Fatal("expected getCygwinBashExecutable to fail for invalid configured path")
	}
	if !strings.Contains(err.Error(), "CYGWIN_BASH_PATH") {
		t.Fatalf("expected CYGWIN_BASH_PATH in error, got: %v", err)
	}
}

func TestAnsibleColorEnv(t *testing.T) {
	entries := ansibleColorEnv()
	combined := strings.Join(entries, ";")

	for _, required := range []string{"ANSIBLE_FORCE_COLOR=true", "PY_COLORS=1", "TERM=xterm-256color"} {
		if !strings.Contains(combined, required) {
			t.Fatalf("expected env %q in %q", required, combined)
		}
	}
}

func TestAnsibleRuntimeEnv(t *testing.T) {
	entries := ansibleRuntimeEnv()
	combined := strings.Join(entries, ";")

	for _, required := range []string{"ANSIBLE_FORCE_COLOR=true", "PY_COLORS=1", "TERM=xterm-256color"} {
		if !strings.Contains(combined, required) {
			t.Fatalf("expected env %q in %q", required, combined)
		}
	}

	if runtime.GOOS == "darwin" && !strings.Contains(combined, "OBJC_DISABLE_INITIALIZE_FORK_SAFETY=YES") {
		t.Fatalf("expected macOS ansible runtime env to disable fork safety, got %q", combined)
	}
	if runtime.GOOS != "darwin" && strings.Contains(combined, "OBJC_DISABLE_INITIALIZE_FORK_SAFETY=YES") {
		t.Fatalf("did not expect macOS-specific env on %s, got %q", runtime.GOOS, combined)
	}
}

func TestExtractLinuxIPv4FromHostOutput(t *testing.T) {
	output := `
default:
  127.0.0.1
  172.24.78.254 172.24.78.255
`

	ip, err := extractLinuxIPv4FromHostOutput(output)
	if err != nil {
		t.Fatalf("expected IP extraction to succeed, got error: %v", err)
	}
	if ip != "172.24.78.254" {
		t.Fatalf("expected 172.24.78.254, got %s", ip)
	}
}

func TestExtractLinuxIPv4FromSSHConfig(t *testing.T) {
	output := `
Host default
  HostName 127.0.0.1

Host vm
  HostName 172.24.78.254
`

	ip, err := extractLinuxIPv4FromSSHConfig(output)
	if err != nil {
		t.Fatalf("expected ssh-config IP extraction to succeed, got error: %v", err)
	}
	if ip != "172.24.78.254" {
		t.Fatalf("expected 172.24.78.254, got %s", ip)
	}
}

func TestExtractLinuxIPv4FromSSHConfig_IgnoresIPv6Entries(t *testing.T) {
	output := `
Host default
  HostName ::1

Host vm
  HostName 172.24.78.254
`

	ip, err := extractLinuxIPv4FromSSHConfig(output)
	if err != nil {
		t.Fatalf("expected ssh-config IP extraction to succeed, got error: %v", err)
	}
	if ip != "172.24.78.254" {
		t.Fatalf("expected 172.24.78.254, got %s", ip)
	}
}

func TestExtractUtmMacAddressFromConfig(t *testing.T) {
	content := `
<dict>
  <key>Network</key>
  <array>
    <dict>
      <key>MacAddress</key>
      <string>A6:1:B:0C:0d:EF</string>
    </dict>
  </array>
</dict>
`

	macAddress, err := extractUtmMacAddressFromConfig(content)
	if err != nil {
		t.Fatalf("expected UTM MAC extraction to succeed, got error: %v", err)
	}
	if macAddress != "a6:01:0b:0c:0d:ef" {
		t.Fatalf("expected normalized UTM MAC address, got %q", macAddress)
	}
}

func TestExtractIPv4ForMacAddress(t *testing.T) {
	output := `
? (127.0.0.1) at 00:00:00:00:00:00 on lo0 ifscope [loopback]
? (192.168.64.21) at a6:1:b:c:d:ef on en0 ifscope [ethernet]
`

	ip, err := extractIPv4ForMacAddress(output, "A6:01:0B:0C:0D:EF")
	if err != nil {
		t.Fatalf("expected ARP IP extraction to succeed, got error: %v", err)
	}
	if ip != "192.168.64.21" {
		t.Fatalf("expected 192.168.64.21, got %s", ip)
	}
}

func TestUtmConfigPlistPath(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	path, err := utmConfigPlistPath(alchemy_build.VirtualMachineConfig{
		OS:   "windows11",
		Arch: "amd64",
	})
	if err != nil {
		t.Fatalf("utmConfigPlistPath returned error: %v", err)
	}

	expected := filepath.Join(homeDir, "Library", "Containers", "com.utmapp.UTM", "Data", "Documents", "windows11-amd64-dev-alchemy.utm", "config.plist")
	if path != expected {
		t.Fatalf("expected %q, got %q", expected, path)
	}
}

func TestDiscoverUtmVMIPv4_RetriesAfterPrimingArpCache(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	vm := alchemy_build.VirtualMachineConfig{
		OS:   "windows11",
		Arch: "amd64",
	}

	configPath, err := utmConfigPlistPath(vm)
	if err != nil {
		t.Fatalf("utmConfigPlistPath returned error: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("failed to create config directory: %v", err)
	}

	configContent := `
<dict>
  <key>Network</key>
  <array>
    <dict>
      <key>MacAddress</key>
      <string>A6:1:B:0C:0d:EF</string>
    </dict>
  </array>
</dict>
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatalf("failed to create UTM config fixture: %v", err)
	}

	arpOutputs := []string{
		"? (192.168.64.1) at 00:11:22:33:44:55 on en0 ifscope [ethernet]",
		"? (192.168.64.21) at a6:1:b:c:d:ef on en0 ifscope [ethernet]",
	}

	var arpCalls int
	var primeCalls int

	ip, err := discoverUtmVMIPv4WithOptions(t.TempDir(), vm, utmIPv4DiscoveryOptions{
		readFile: os.ReadFile,
		runCommand: func(_ string, _ time.Duration, executable string, args []string) (string, error) {
			if executable != "arp" {
				t.Fatalf("expected arp lookup, got executable %q", executable)
			}
			if len(args) != 1 || args[0] != "-a" {
				t.Fatalf("expected arp -a invocation, got args %v", args)
			}
			if arpCalls >= len(arpOutputs) {
				return arpOutputs[len(arpOutputs)-1], nil
			}
			output := arpOutputs[arpCalls]
			arpCalls++
			return output, nil
		},
		primeARPCache: func() error {
			primeCalls++
			return nil
		},
		sleep:         func(time.Duration) {},
		retryInterval: time.Millisecond,
		maxAttempts:   3,
	})
	if err != nil {
		t.Fatalf("expected UTM IPv4 discovery to succeed after retry, got error: %v", err)
	}
	if ip != "192.168.64.21" {
		t.Fatalf("expected discovered IP 192.168.64.21, got %q", ip)
	}
	if arpCalls != 2 {
		t.Fatalf("expected 2 arp lookups before success, got %d", arpCalls)
	}
	if primeCalls != 1 {
		t.Fatalf("expected ARP cache probe to run once after the first miss, got %d", primeCalls)
	}
}

func TestDiscoverUtmVMIPv4_RePrimesArpCacheUntilMacAddressAppears(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	vm := alchemy_build.VirtualMachineConfig{
		OS:   "windows11",
		Arch: "amd64",
	}

	configPath, err := utmConfigPlistPath(vm)
	if err != nil {
		t.Fatalf("utmConfigPlistPath returned error: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("failed to create config directory: %v", err)
	}

	configContent := `
<dict>
  <key>Network</key>
  <array>
    <dict>
      <key>MacAddress</key>
      <string>A6:1:B:0C:0d:EF</string>
    </dict>
  </array>
</dict>
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatalf("failed to create UTM config fixture: %v", err)
	}

	arpOutputs := []string{
		"? (192.168.64.1) at 00:11:22:33:44:55 on en0 ifscope [ethernet]",
		"? (192.168.64.1) at 00:11:22:33:44:55 on en0 ifscope [ethernet]",
		"? (192.168.64.21) at a6:1:b:c:d:ef on en0 ifscope [ethernet]",
	}

	var arpCalls int
	var primeCalls int

	ip, err := discoverUtmVMIPv4WithOptions(t.TempDir(), vm, utmIPv4DiscoveryOptions{
		readFile: os.ReadFile,
		runCommand: func(_ string, _ time.Duration, executable string, args []string) (string, error) {
			if executable != "arp" {
				t.Fatalf("expected arp lookup, got executable %q", executable)
			}
			if len(args) != 1 || args[0] != "-a" {
				t.Fatalf("expected arp -a invocation, got args %v", args)
			}
			if arpCalls >= len(arpOutputs) {
				return arpOutputs[len(arpOutputs)-1], nil
			}
			output := arpOutputs[arpCalls]
			arpCalls++
			return output, nil
		},
		primeARPCache: func() error {
			primeCalls++
			return nil
		},
		sleep:         func(time.Duration) {},
		retryInterval: time.Millisecond,
		maxAttempts:   4,
	})
	if err != nil {
		t.Fatalf("expected UTM IPv4 discovery to succeed after repeated ARP cache priming, got error: %v", err)
	}
	if ip != "192.168.64.21" {
		t.Fatalf("expected discovered IP 192.168.64.21, got %q", ip)
	}
	if arpCalls != 3 {
		t.Fatalf("expected 3 arp lookups before success, got %d", arpCalls)
	}
	if primeCalls != 2 {
		t.Fatalf("expected ARP cache probe to run after each miss before success, got %d", primeCalls)
	}
}

func TestBuildSSHProvisionArgs(t *testing.T) {
	projectDir := t.TempDir()
	config := sshAnsibleConnectionConfig{
		User:           "packer",
		Password:       "P@ssw0rd!",
		BecomePassword: "P@ssw0rd!",
		Connection:     "ssh",
		SshCommonArgs:  "-o StrictHostKeyChecking=no",
		SshTimeout:     "120",
		SshRetries:     "3",
	}

	args, cleanup, err := buildSSHProvisionArgs(projectDir, "172.24.78.254", config, ProvisionOptions{Check: true, Verbosity: defaultAnsibleVerbosity})
	if err != nil {
		t.Fatalf("buildSSHProvisionArgs returned error: %v", err)
	}
	t.Cleanup(func() {
		if cleanupErr := cleanup(); cleanupErr != nil {
			t.Fatalf("failed to clean up extra vars file: %v", cleanupErr)
		}
	})

	if !strings.Contains(strings.Join(args, " "), "-i 172.24.78.254,") {
		t.Fatalf("expected inline inventory with discovered host IP, args: %v", args)
	}
	if !strings.Contains(strings.Join(args, " "), "-l 172.24.78.254") {
		t.Fatalf("expected limit to discovered host IP, args: %v", args)
	}
	if args[len(args)-1] != "--check" {
		t.Fatalf("expected --check to be passed through when requested, args: %v", args)
	}

	extraVarsIndex := -1
	for index, arg := range args {
		if arg == "--extra-vars" {
			extraVarsIndex = index + 1
			break
		}
	}
	if extraVarsIndex <= 0 || extraVarsIndex >= len(args) {
		t.Fatalf("expected --extra-vars with @temp file reference, args: %v", args)
	}
	if strings.Contains(strings.Join(args, " "), "P@ssw0rd!") {
		t.Fatalf("did not expect password in process arguments, args: %v", args)
	}
	if !strings.HasPrefix(args[extraVarsIndex], "@") {
		t.Fatalf("expected @<file> notation for --extra-vars, got: %q", args[extraVarsIndex])
	}

	extraVarsFilePath := filepath.Join(projectDir, strings.TrimPrefix(args[extraVarsIndex], "@"))
	content, readErr := os.ReadFile(extraVarsFilePath)
	if readErr != nil {
		t.Fatalf("failed to read extra vars file %q: %v", extraVarsFilePath, readErr)
	}

	extraVars := map[string]string{}
	if err := json.Unmarshal(content, &extraVars); err != nil {
		t.Fatalf("expected extra vars file to contain valid JSON, got error: %v", err)
	}

	for key, expected := range map[string]string{
		"ansible_user":            "packer",
		"ansible_password":        "P@ssw0rd!",
		"ansible_become_password": "P@ssw0rd!",
		"ansible_connection":      "ssh",
		"ansible_ssh_common_args": "-o StrictHostKeyChecking=no",
		"ansible_ssh_timeout":     "120",
		"ansible_ssh_retries":     "3",
	} {
		if extraVars[key] != expected {
			t.Fatalf("expected %s=%q in extra vars, got: %v", key, expected, extraVars)
		}
	}
}

func TestEnsureSSHPasswordAuthDependenciesRequiresSSHPass(t *testing.T) {
	previousLookPath := lookPathProvisionCommand
	t.Cleanup(func() {
		lookPathProvisionCommand = previousLookPath
	})

	var lookedUp []string
	lookPathProvisionCommand = func(file string) (string, error) {
		lookedUp = append(lookedUp, file)
		return "", errors.New("not found")
	}

	err := ensureSSHPasswordAuthDependencies(sshAnsibleConnectionConfig{
		Connection: "ssh",
		Password:   "P@ssw0rd!",
	})
	if err == nil {
		t.Fatal("expected missing sshpass error")
	}
	if strings.Join(lookedUp, ",") != "sshpass" {
		t.Fatalf("expected sshpass lookup, got %v", lookedUp)
	}
	if !strings.Contains(err.Error(), "sudo apt install sshpass") {
		t.Fatalf("expected install hint in error, got %v", err)
	}
}

func stubWindowsCygwinSSHPassPreflight(t *testing.T, output string, runErr error) *[]string {
	t.Helper()

	bashPath := filepath.Join(t.TempDir(), "bash.exe")
	if err := os.WriteFile(bashPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("failed to create fake bash executable: %v", err)
	}
	t.Setenv("CYGWIN_BASH_PATH", bashPath)
	t.Setenv("CYGWIN_TERMINAL_PATH", "")

	previousGOOS := runtimeGOOS
	previousLookPath := lookPathProvisionCommand
	previousRunner := runProvisionCommandWithCombinedOutputWithEnv
	t.Cleanup(func() {
		runtimeGOOS = previousGOOS
		lookPathProvisionCommand = previousLookPath
		runProvisionCommandWithCombinedOutputWithEnv = previousRunner
	})

	runtimeGOOS = "windows"
	lookPathProvisionCommand = func(file string) (string, error) {
		t.Fatalf("did not expect Windows PATH lookup for %s", file)
		return "", nil
	}

	var invocation []string
	runProvisionCommandWithCombinedOutputWithEnv = func(_ string, _ time.Duration, executable string, args []string, _ []string) (string, error) {
		invocation = append([]string{executable}, args...)
		return output, runErr
	}

	return &invocation
}

func TestEnsureSSHPasswordAuthDependenciesUsesCygwinOnWindows(t *testing.T) {
	invocation := stubWindowsCygwinSSHPassPreflight(t, "/usr/bin/sshpass\n", nil)

	err := ensureSSHPasswordAuthDependencies(sshAnsibleConnectionConfig{
		Connection: "ssh",
		Password:   "P@ssw0rd!",
	})
	if err != nil {
		t.Fatalf("expected cygwin sshpass to satisfy preflight, got %v", err)
	}
	if got := strings.Join((*invocation)[1:], " "); got != "-l -c command -v sshpass" {
		t.Fatalf("expected cygwin login shell sshpass lookup, got %q", got)
	}
	if !strings.HasSuffix((*invocation)[0], "bash.exe") {
		t.Fatalf("expected cygwin bash executable, got %q", (*invocation)[0])
	}
}

func TestEnsureSSHPasswordAuthDependenciesReportsMissingCygwinSSHPass(t *testing.T) {
	stubWindowsCygwinSSHPassPreflight(t, "", errors.New("exit status 1"))

	err := ensureSSHPasswordAuthDependencies(sshAnsibleConnectionConfig{
		Connection: "ssh",
		Password:   "P@ssw0rd!",
	})
	if err == nil {
		t.Fatal("expected missing cygwin sshpass error")
	}
	if !strings.Contains(err.Error(), "Cygwin sshpass package") {
		t.Fatalf("expected cygwin install hint in error, got %v", err)
	}
}

func TestEnsureSSHPasswordAuthDependenciesSkipsNonOpenSSHPasswordAuth(t *testing.T) {
	previousLookPath := lookPathProvisionCommand
	t.Cleanup(func() {
		lookPathProvisionCommand = previousLookPath
	})

	lookPathProvisionCommand = func(file string) (string, error) {
		t.Fatalf("did not expect command lookup for %s", file)
		return "", nil
	}

	for _, config := range []sshAnsibleConnectionConfig{
		{Connection: "ssh"},
		{Connection: "paramiko", Password: "P@ssw0rd!"},
		{Connection: "winrm", Password: "P@ssw0rd!"},
	} {
		if err := ensureSSHPasswordAuthDependencies(config); err != nil {
			t.Fatalf("did not expect dependency error for config %+v: %v", config, err)
		}
	}
}

func TestBuildSSHStaticInventoryProvisionArgsIncludesWindowsShellSettings(t *testing.T) {
	projectDir := t.TempDir()
	config := sshAnsibleConnectionConfig{
		User:            localWindowsProvisionUserName,
		Connection:      "ssh",
		Port:            "2222",
		SshCommonArgs:   localWindowsSSHCommonArgs,
		SshTimeout:      "120",
		SshRetries:      "3",
		PrivateKeyFile:  ".local-windows-provision-key-test.pem",
		ShellType:       "powershell",
		ShellExecutable: "powershell.exe",
	}

	args, cleanup, err := buildSSHStaticInventoryProvisionArgs(projectDir, localWindowsSSHInventoryPath, localWindowsSSHInventoryTarget, config, ProvisionOptions{Verbosity: defaultAnsibleVerbosity})
	if err != nil {
		t.Fatalf("buildSSHStaticInventoryProvisionArgs returned error: %v", err)
	}
	t.Cleanup(func() {
		if cleanupErr := cleanup(); cleanupErr != nil {
			t.Fatalf("failed to clean up extra vars file: %v", cleanupErr)
		}
	})

	if got := strings.Join(args, " "); !strings.Contains(got, "-i ./inventory/localhost_windows_ssh.yml") {
		t.Fatalf("expected windows localhost ssh inventory, args: %v", args)
	}

	extraVarsIndex := -1
	for index, arg := range args {
		if arg == "--extra-vars" {
			extraVarsIndex = index + 1
			break
		}
	}
	if extraVarsIndex <= 0 || extraVarsIndex >= len(args) {
		t.Fatalf("expected --extra-vars with @temp file reference, args: %v", args)
	}

	extraVarsFilePath := filepath.Join(projectDir, strings.TrimPrefix(args[extraVarsIndex], "@"))
	content, readErr := os.ReadFile(extraVarsFilePath)
	if readErr != nil {
		t.Fatalf("failed to read extra vars file %q: %v", extraVarsFilePath, readErr)
	}

	extraVars := map[string]string{}
	if err := json.Unmarshal(content, &extraVars); err != nil {
		t.Fatalf("expected extra vars file to contain valid JSON, got error: %v", err)
	}

	for key, expected := range map[string]string{
		"ansible_user":                 localWindowsProvisionUserName,
		"ansible_connection":           "ssh",
		"ansible_port":                 "2222",
		"ansible_ssh_common_args":      localWindowsSSHCommonArgs,
		"ansible_ssh_timeout":          "120",
		"ansible_ssh_retries":          "3",
		"ansible_ssh_private_key_file": ".local-windows-provision-key-test.pem",
		"ansible_shell_type":           "powershell",
		"ansible_shell_executable":     "powershell.exe",
	} {
		if extraVars[key] != expected {
			t.Fatalf("expected %s=%q in extra vars, got: %v", key, expected, extraVars)
		}
	}
}

func TestLocalWindowsSSHProvisionBootstrapPowerShellConfiguresOpenSSHServer(t *testing.T) {
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "Installing the OpenSSH Server capability.") {
		t.Fatal("expected ssh bootstrap script to install OpenSSH Server when needed")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "Progress updates will be logged every") {
		t.Fatal("expected ssh bootstrap script to announce periodic capability install progress logging")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "Add-WindowsCapability -Online -Name $capabilityName") {
		t.Fatal("expected ssh bootstrap script to install OpenSSH Server through the heartbeat helper")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "Install-WindowsCapabilityWithHeartbeat") {
		t.Fatal("expected ssh bootstrap script to wrap OpenSSH capability install with heartbeat logging")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "OpenSSH Server capability install is still running after") {
		t.Fatal("expected ssh bootstrap script to emit heartbeat logs while the capability install is running")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "Test-OpenSSHCapabilityInstallNeeded") {
		t.Fatal("expected ssh bootstrap script to decide capability install based on both capability and service state")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "Assert-OpenSSHCapabilityStateIsUsable") {
		t.Fatal("expected ssh bootstrap script to fail fast when OpenSSH is stuck in a pending unusable state")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "Write-OpenSSHPendingStateGuidance") {
		t.Fatal("expected ssh bootstrap script to emit explicit reboot guidance for pending OpenSSH capability states")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "reboot Windows soon") {
		t.Fatal("expected ssh bootstrap script to recommend a reboot when a pending capability state can still be reused")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "A Windows reboot is required before SSH provisioning can continue.") {
		t.Fatal("expected ssh bootstrap script to clearly require a reboot when pending capability state blocks sshd")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "OpenSSH capability is reported as \"") {
		t.Fatal("expected ssh bootstrap script to log when it reuses an existing sshd installation despite a non-installed capability state")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "*S-1-5-32-544") {
		t.Fatal("expected ssh bootstrap script to secure administrators_authorized_keys with the built-in administrators SID")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "*S-1-5-18") {
		t.Fatal("expected ssh bootstrap script to secure administrators_authorized_keys with the local system SID")
	}
	if strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "'Administrators:F'") {
		t.Fatal("expected ssh bootstrap script to avoid hardcoded English Administrators ACL entries")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "Set-AdminAuthorizedKeysPermissions") {
		t.Fatal("expected ssh bootstrap script to harden administrators_authorized_keys permissions")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "Get-LocalUserAuthorizedKeysPath") {
		t.Fatal("expected ssh bootstrap script to resolve a per-user authorized_keys path for localized Windows SSH auth")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "Set-UserAuthorizedKeysPermissions") {
		t.Fatal("expected ssh bootstrap script to harden the per-user authorized_keys file")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "Set-PathOwnerBySid") {
		t.Fatal("expected ssh bootstrap script to set ownership on the temporary user's SSH paths")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "Grant-AdministrativePathAccess") {
		t.Fatal("expected ssh bootstrap script to recover access to stale per-user SSH files from previous runs")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "Ensure-LocalUserProfile") {
		t.Fatal("expected ssh bootstrap script to ensure the temporary local ssh user has a Windows profile")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "Write-TemporarySshdConfig") {
		t.Fatal("expected ssh bootstrap script to write a temporary loopback-only sshd_config")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "Wrote a temporary loopback-only sshd_config for this provisioning run on port") {
		t.Fatal("expected ssh bootstrap script to log the temporary sshd_config port override")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "ListenAddress 127.0.0.1") {
		t.Fatal("expected ssh bootstrap script to constrain the temporary sshd_config to loopback")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "AddressFamily inet") {
		t.Fatal("expected ssh bootstrap script to constrain the temporary sshd_config to IPv4 loopback")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "Temporary loopback SSH port: ") {
		t.Fatal("expected ssh bootstrap script to log the selected temporary ssh port")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "DEV_ALCHEMY_LOCAL_WINDOWS_ANSIBLE_SSH_PORT") {
		t.Fatal("expected ssh bootstrap script to require the temporary ssh port environment variable")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "Using the standard OpenSSH port 22 for this provisioning run.") {
		t.Fatal("expected ssh bootstrap script to log when it can safely keep port 22")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "standard SSH port 22 is not available for exclusive Windows sshd use") {
		t.Fatal("expected ssh bootstrap script to explain when it switches to a temporary port")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "Per-user authorized keys state: existed=") {
		t.Fatal("expected ssh bootstrap script to log per-user authorized_keys state for debugging")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "Creating the per-user SSH directory") {
		t.Fatal("expected ssh bootstrap script to create a per-user SSH directory when needed")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "Creating the local user profile directory") {
		t.Fatal("expected ssh bootstrap script to create a local user profile directory when OpenSSH home resolution needs it")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "registered Windows user profile") {
		t.Fatal("expected ssh bootstrap script to initialize a registered Windows profile for the temporary local ssh user")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "Reading the existing per-user authorized_keys file") {
		t.Fatal("expected ssh bootstrap script to preserve any existing per-user authorized_keys content")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "Write-StateSummary") {
		t.Fatal("expected ssh bootstrap script to emit a captured state summary for debugging")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "DevAlchemyLocalSSHDLoopback") {
		t.Fatal("expected ssh bootstrap script to manage a dedicated loopback firewall rule")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "Reconfiguring the existing loopback-only OpenSSH firewall rule for temporary local port") {
		t.Fatal("expected ssh bootstrap script to recreate the loopback firewall rule on the selected temporary port")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "OpenSSH-Server-In-TCP") {
		t.Fatal("expected ssh bootstrap script to manage the broad built-in firewall rule")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "DefaultShell") {
		t.Fatal("expected ssh bootstrap script to set the default shell for OpenSSH")
	}
	if !strings.Contains(localWindowsSSHProvisionBootstrapPowerShell, "Starting or restarting the sshd service.") {
		t.Fatal("expected ssh bootstrap script to restart sshd when configuration changes require it")
	}
}

func TestLocalWindowsSSHProvisionCleanupPowerShellRestoresOpenSSHState(t *testing.T) {
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "Restore-FileState") {
		t.Fatal("expected ssh cleanup script to restore the administrator authorized_keys file")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "state.UserAuthorizedKeysPath") {
		t.Fatal("expected ssh cleanup script to restore the per-user authorized_keys file")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "SshdConfigContentBase64") {
		t.Fatal("expected ssh cleanup script to restore sshd_config after provisioning")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "takeown.exe") {
		t.Fatal("expected ssh cleanup script to take ownership before restoring per-user SSH files")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "Grant-AdministrativePathAccess") {
		t.Fatal("expected ssh cleanup script to recover access to stale per-user SSH paths before restoring them")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "Restore-LoopbackFirewallRuleState") {
		t.Fatal("expected ssh cleanup script to restore the dedicated loopback firewall rule with its original port")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "state.LocalFirewallRulePort") {
		t.Fatal("expected ssh cleanup script to remember the original loopback firewall rule port")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "Reload-ServiceRuntimeConfiguration") {
		t.Fatal("expected ssh cleanup script to reload sshd after restoring the original ssh configuration")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "Reloading sshd so the restored SSH configuration takes effect.") {
		t.Fatal("expected ssh cleanup script to log when it reloads sshd after restoring configuration")
	}
	if strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "'/D', 'Y'") {
		t.Fatal("expected ssh cleanup script to avoid locale-sensitive takeown /D Y usage")
	}
	if strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "Remove-WindowsCapability -Online") {
		t.Fatal("expected ssh cleanup script to avoid uninstalling OpenSSH Server because that requires a reboot")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "Restore-ServiceState 'sshd'") {
		t.Fatal("expected ssh cleanup script to restore the original sshd service state")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "Disable-ServiceState 'sshd'") {
		t.Fatal("expected ssh cleanup script to disable sshd instead of uninstalling OpenSSH when cleanup created it")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "function Remove-ManagedLocalUserIfPresent") {
		t.Fatal("expected ssh cleanup script to include the shared helper for removing temporary local users")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "Remove-LocalUser -Name $name") {
		t.Fatal("expected ssh cleanup script helper to remove the temporary local user when it did not exist before")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "$forceSSHUninstall") {
		t.Fatal("expected ssh cleanup script to honor force ssh uninstall mode")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "without uninstalling OpenSSH Server to avoid requiring a reboot") {
		t.Fatal("expected ssh cleanup script to explain why cleanup disables sshd instead of uninstalling OpenSSH")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "cleanup does not require a reboot") {
		t.Fatal("expected ssh cleanup script to explain why provisioning-installed sshd is disabled instead of removed")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "CapabilityInstallManaged") {
		t.Fatal("expected ssh cleanup script to detect when this provisioning run installed OpenSSH")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "Leaving the OpenSSH Server capability state unchanged") {
		t.Fatal("expected ssh cleanup script to log when it preserves a pre-existing pending capability state")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "Reboot Windows to finish that pending change") {
		t.Fatal("expected ssh cleanup script to remind the operator to reboot after preserving a pending capability state")
	}
}

func TestLocalWindowsSSHProvisionCleanupPowerShellPreservesPreExistingUserDuringForceUninstall(t *testing.T) {
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "function Get-ManagedLocalUserCleanupPlan") {
		t.Fatal("expected ssh cleanup script to include the shared helper documenting local user cleanup semantics")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "Get-ManagedLocalUserCleanupPlan ([bool]$state.UserExisted) $forceSSHUninstall 'SSH'") {
		t.Fatal("expected ssh cleanup script to route force ssh uninstall and prior user existence through the shared helper")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "A pre-existing local user is always restored; only the temporary user created") {
		t.Fatal("expected ssh cleanup helper to document that force ssh uninstall only removes temporary users")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "RestoreUser = $true") {
		t.Fatal("expected ssh cleanup helper to return an explicit restore action for pre-existing users")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "RestoreUser = $false") {
		t.Fatal("expected ssh cleanup helper to return an explicit removal action for temporary users")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "preserving the pre-existing local Ansible account and restoring its original state.") {
		t.Fatal("expected ssh cleanup script helper to preserve a pre-existing local user during force ssh uninstall")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "Restore-ManagedLocalUserState $administratorsGroupName $state $userName") {
		t.Fatal("expected ssh cleanup script to restore a pre-existing local user during force ssh uninstall")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "removing the temporary local Ansible account created for provisioning.") {
		t.Fatal("expected ssh cleanup script helper to limit force ssh uninstall account removal to temporary users")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "Remove-ManagedLocalUserIfPresent $administratorsGroupName $userName") {
		t.Fatal("expected ssh cleanup script to remove only temporary local users during cleanup")
	}
	if strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "Force SSH uninstall mode is enabled; removing the local Ansible account.") {
		t.Fatal("expected ssh cleanup script to avoid unconditionally deleting pre-existing local users during force ssh uninstall")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "if ([bool]$savedState.UserWasAdministrator)") {
		t.Fatal("expected ssh cleanup script to restore pre-existing local administrator membership")
	}
	if !strings.Contains(localWindowsSSHProvisionCleanupPowerShell, "Add-LocalGroupMember -Group $groupName -Member $name -ErrorAction SilentlyContinue") {
		t.Fatal("expected ssh cleanup script to restore administrator membership when the pre-existing user originally had it")
	}
}

func TestLocalWindowsSSHProvisionUsesLongerBootstrapAndCleanupTimeouts(t *testing.T) {
	if localWindowsSSHBootstrapTimeout <= localWindowsBootstrapTimeout {
		t.Fatalf("expected ssh bootstrap timeout %s to exceed shared local windows bootstrap timeout %s", localWindowsSSHBootstrapTimeout, localWindowsBootstrapTimeout)
	}
	if localWindowsSSHCleanupTimeout <= localWindowsCleanupTimeout {
		t.Fatalf("expected ssh cleanup timeout %s to exceed shared local windows cleanup timeout %s", localWindowsSSHCleanupTimeout, localWindowsCleanupTimeout)
	}
}

func TestLoadUbuntuHypervAnsibleConnectionConfig_UsesDefaults(t *testing.T) {
	projectDir := t.TempDir()

	connectionConfig, err := loadUbuntuHypervAnsibleConnectionConfig(projectDir)
	if err != nil {
		t.Fatalf("loadUbuntuHypervAnsibleConnectionConfig returned error: %v", err)
	}

	if connectionConfig.User != "packer" {
		t.Fatalf("expected default user packer, got %q", connectionConfig.User)
	}
	if connectionConfig.Password != "P@ssw0rd!" {
		t.Fatalf("expected default password, got %q", connectionConfig.Password)
	}
	if connectionConfig.BecomePassword != "P@ssw0rd!" {
		t.Fatalf("expected default become password, got %q", connectionConfig.BecomePassword)
	}
	if connectionConfig.Connection != "ssh" {
		t.Fatalf("expected default connection ssh, got %q", connectionConfig.Connection)
	}
}

func TestLoadUbuntuHypervAnsibleConnectionConfig_EnvOverridesDotEnv(t *testing.T) {
	projectDir := t.TempDir()
	dotEnvPath := filepath.Join(projectDir, ".env")

	content := strings.Join([]string{
		hypervUbuntuAnsibleUserEnvVar + "=file-user",
		hypervUbuntuAnsiblePasswordEnvVar + "=file-pass",
		hypervUbuntuAnsibleBecomePasswordEnvVar + "=file-become",
		"",
	}, "\n")
	if err := os.WriteFile(dotEnvPath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create .env fixture: %v", err)
	}

	t.Setenv(hypervUbuntuAnsibleUserEnvVar, "env-user")
	t.Setenv(hypervUbuntuAnsiblePasswordEnvVar, "env-pass")
	t.Setenv(hypervUbuntuAnsibleBecomePasswordEnvVar, "env-become")

	connectionConfig, err := loadUbuntuHypervAnsibleConnectionConfig(projectDir)
	if err != nil {
		t.Fatalf("loadUbuntuHypervAnsibleConnectionConfig returned error: %v", err)
	}

	if connectionConfig.User != "env-user" {
		t.Fatalf("expected environment user to override .env, got %q", connectionConfig.User)
	}
	if connectionConfig.Password != "env-pass" {
		t.Fatalf("expected environment password to override .env, got %q", connectionConfig.Password)
	}
	if connectionConfig.BecomePassword != "env-become" {
		t.Fatalf("expected environment become password to override .env, got %q", connectionConfig.BecomePassword)
	}
}

func TestLoadUbuntuUtmAnsibleConnectionConfig_UsesDefaults(t *testing.T) {
	projectDir := t.TempDir()

	connectionConfig, err := loadUbuntuUtmAnsibleConnectionConfig(projectDir)
	if err != nil {
		t.Fatalf("loadUbuntuUtmAnsibleConnectionConfig returned error: %v", err)
	}

	if connectionConfig.User != "packer" {
		t.Fatalf("expected default user packer, got %q", connectionConfig.User)
	}
	if connectionConfig.Password != "P@ssw0rd!" {
		t.Fatalf("expected default password, got %q", connectionConfig.Password)
	}
	if connectionConfig.BecomePassword != "P@ssw0rd!" {
		t.Fatalf("expected default become password, got %q", connectionConfig.BecomePassword)
	}
	if connectionConfig.Connection != "ssh" {
		t.Fatalf("expected default connection ssh, got %q", connectionConfig.Connection)
	}
}

func TestLoadUbuntuUtmAnsibleConnectionConfig_EnvOverridesDotEnv(t *testing.T) {
	projectDir := t.TempDir()
	dotEnvPath := filepath.Join(projectDir, ".env")

	content := strings.Join([]string{
		utmUbuntuAnsibleUserEnvVar + "=file-user",
		utmUbuntuAnsiblePasswordEnvVar + "=file-pass",
		utmUbuntuAnsibleBecomePasswordEnvVar + "=file-become",
		"",
	}, "\n")
	if err := os.WriteFile(dotEnvPath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create .env fixture: %v", err)
	}

	t.Setenv(utmUbuntuAnsibleUserEnvVar, "env-user")
	t.Setenv(utmUbuntuAnsiblePasswordEnvVar, "env-pass")
	t.Setenv(utmUbuntuAnsibleBecomePasswordEnvVar, "env-become")

	connectionConfig, err := loadUbuntuUtmAnsibleConnectionConfig(projectDir)
	if err != nil {
		t.Fatalf("loadUbuntuUtmAnsibleConnectionConfig returned error: %v", err)
	}

	if connectionConfig.User != "env-user" {
		t.Fatalf("expected environment user to override .env, got %q", connectionConfig.User)
	}
	if connectionConfig.Password != "env-pass" {
		t.Fatalf("expected environment password to override .env, got %q", connectionConfig.Password)
	}
	if connectionConfig.BecomePassword != "env-become" {
		t.Fatalf("expected environment become password to override .env, got %q", connectionConfig.BecomePassword)
	}
}

func TestLoadUbuntuLibvirtAnsibleConnectionConfig_UsesDefaults(t *testing.T) {
	projectDir := t.TempDir()

	connectionConfig, err := loadUbuntuLibvirtAnsibleConnectionConfig(projectDir)
	if err != nil {
		t.Fatalf("loadUbuntuLibvirtAnsibleConnectionConfig returned error: %v", err)
	}

	if connectionConfig.User != "packer" {
		t.Fatalf("expected default user packer, got %q", connectionConfig.User)
	}
	if connectionConfig.Password != "P@ssw0rd!" {
		t.Fatalf("expected default password, got %q", connectionConfig.Password)
	}
	if connectionConfig.BecomePassword != "P@ssw0rd!" {
		t.Fatalf("expected default become password, got %q", connectionConfig.BecomePassword)
	}
	if connectionConfig.Connection != "ssh" {
		t.Fatalf("expected default connection ssh, got %q", connectionConfig.Connection)
	}
}

func TestLoadUbuntuLibvirtAnsibleConnectionConfig_EnvOverridesDotEnv(t *testing.T) {
	projectDir := t.TempDir()
	dotEnvPath := filepath.Join(projectDir, ".env")

	content := strings.Join([]string{
		libvirtUbuntuAnsibleUserEnvVar + "=file-user",
		libvirtUbuntuAnsiblePasswordEnvVar + "=file-pass",
		libvirtUbuntuAnsibleBecomePasswordEnvVar + "=file-become",
		"",
	}, "\n")
	if err := os.WriteFile(dotEnvPath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create .env fixture: %v", err)
	}

	t.Setenv(libvirtUbuntuAnsibleUserEnvVar, "env-user")
	t.Setenv(libvirtUbuntuAnsiblePasswordEnvVar, "env-pass")
	t.Setenv(libvirtUbuntuAnsibleBecomePasswordEnvVar, "env-become")

	connectionConfig, err := loadUbuntuLibvirtAnsibleConnectionConfig(projectDir)
	if err != nil {
		t.Fatalf("loadUbuntuLibvirtAnsibleConnectionConfig returned error: %v", err)
	}

	if connectionConfig.User != "env-user" {
		t.Fatalf("expected environment user to override .env, got %q", connectionConfig.User)
	}
	if connectionConfig.Password != "env-pass" {
		t.Fatalf("expected environment password to override .env, got %q", connectionConfig.Password)
	}
	if connectionConfig.BecomePassword != "env-become" {
		t.Fatalf("expected environment become password to override .env, got %q", connectionConfig.BecomePassword)
	}
}

func TestDiscoverTartVMIPv4_UsesFallbackARPResolver(t *testing.T) {
	var calls int

	ip, err := discoverTartVMIPv4WithOptions(t.TempDir(), "sequoia-base", tartIPv4DiscoveryOptions{
		runCommand: func(_ string, _ time.Duration, executable string, args []string) (string, error) {
			if executable != "tart" {
				t.Fatalf("expected tart executable, got %q", executable)
			}

			calls++
			switch calls {
			case 1:
				if len(args) != 2 || args[0] != "ip" || args[1] != "sequoia-base" {
					t.Fatalf("unexpected default resolver args: %v", args)
				}
				return "", errors.New("vm is not running")
			case 2:
				if len(args) != 3 || args[0] != "ip" || args[1] != "--resolver=arp" || args[2] != "sequoia-base" {
					t.Fatalf("unexpected arp resolver args: %v", args)
				}
				return "192.168.64.21\n", nil
			default:
				t.Fatalf("unexpected extra call with args %v", args)
				return "", nil
			}
		},
		sleep:         func(time.Duration) {},
		retryInterval: time.Millisecond,
		maxAttempts:   1,
	})
	if err != nil {
		t.Fatalf("expected Tart IPv4 discovery to succeed, got error: %v", err)
	}
	if ip != "192.168.64.21" {
		t.Fatalf("expected discovered IP 192.168.64.21, got %q", ip)
	}
	if calls != 2 {
		t.Fatalf("expected two tart ip attempts, got %d", calls)
	}
}

func TestLoadMacOSTartAnsibleConnectionConfig_UsesDefaults(t *testing.T) {
	projectDir := t.TempDir()

	connectionConfig, err := loadMacOSTartAnsibleConnectionConfig(projectDir)
	if err != nil {
		t.Fatalf("loadMacOSTartAnsibleConnectionConfig returned error: %v", err)
	}

	if connectionConfig.User != "admin" {
		t.Fatalf("expected default user admin, got %q", connectionConfig.User)
	}
	if connectionConfig.Password != "admin" {
		t.Fatalf("expected default password admin, got %q", connectionConfig.Password)
	}
	if connectionConfig.BecomePassword != "admin" {
		t.Fatalf("expected default become password admin, got %q", connectionConfig.BecomePassword)
	}
	if connectionConfig.Connection != "ssh" {
		t.Fatalf("expected default connection ssh, got %q", connectionConfig.Connection)
	}
}

func TestLoadMacOSTartAnsibleConnectionConfig_EnvOverridesDotEnv(t *testing.T) {
	projectDir := t.TempDir()
	dotEnvPath := filepath.Join(projectDir, ".env")

	content := strings.Join([]string{
		tartMacOSAnsibleUserEnvVar + "=file-user",
		tartMacOSAnsiblePasswordEnvVar + "=file-pass",
		tartMacOSAnsibleBecomePasswordEnvVar + "=file-become",
		"",
	}, "\n")
	if err := os.WriteFile(dotEnvPath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create .env fixture: %v", err)
	}

	t.Setenv(tartMacOSAnsibleUserEnvVar, "env-user")
	t.Setenv(tartMacOSAnsiblePasswordEnvVar, "env-pass")
	t.Setenv(tartMacOSAnsibleBecomePasswordEnvVar, "env-become")

	connectionConfig, err := loadMacOSTartAnsibleConnectionConfig(projectDir)
	if err != nil {
		t.Fatalf("loadMacOSTartAnsibleConnectionConfig returned error: %v", err)
	}

	if connectionConfig.User != "env-user" {
		t.Fatalf("expected environment user to override .env, got %q", connectionConfig.User)
	}
	if connectionConfig.Password != "env-pass" {
		t.Fatalf("expected environment password to override .env, got %q", connectionConfig.Password)
	}
	if connectionConfig.BecomePassword != "env-become" {
		t.Fatalf("expected environment become password to override .env, got %q", connectionConfig.BecomePassword)
	}
}

func TestEnsureTartVMReadyForProvision_ReturnsHelpfulErrorWhenVMDoesNotExist(t *testing.T) {
	_, err := ensureTartVMReadyForProvision(t.TempDir(), "tahoe-base-alchemy", tartProvisionAvailabilityOptions{
		localVMExists: func(_ string, vmName string) (bool, error) {
			if vmName != "tahoe-base-alchemy" {
				t.Fatalf("unexpected vmName %q", vmName)
			}
			return false, nil
		},
		discoverIPv4: func(_ string, _ string) (string, error) {
			t.Fatal("discoverIPv4 should not be called when the VM does not exist")
			return "", nil
		},
	})
	if err == nil {
		t.Fatal("expected missing Tart VM to return an error")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected missing-vm error, got %v", err)
	}
	if !strings.Contains(err.Error(), "sailwright create macos --arch arm64") {
		t.Fatalf("expected create hint in error, got %v", err)
	}
}

func TestEnsureTartVMReadyForProvision_ReturnsHelpfulErrorWhenVMIsNotRunning(t *testing.T) {
	_, err := ensureTartVMReadyForProvision(t.TempDir(), "tahoe-base-alchemy", tartProvisionAvailabilityOptions{
		localVMExists: func(_ string, _ string) (bool, error) {
			return true, nil
		},
		discoverIPv4: func(_ string, vmName string) (string, error) {
			if vmName != "tahoe-base-alchemy" {
				t.Fatalf("unexpected vmName %q", vmName)
			}
			return "", errors.New("default resolver failed: vm is not running")
		},
	})
	if err == nil {
		t.Fatal("expected stopped Tart VM to return an error")
	}
	if !strings.Contains(err.Error(), "is not running") {
		t.Fatalf("expected not-running error, got %v", err)
	}
	if !strings.Contains(err.Error(), "sailwright start macos --arch arm64") {
		t.Fatalf("expected start hint in error, got %v", err)
	}
}

func TestEnsureProvisionTargetRunning_ReturnsCreateHintWhenMissing(t *testing.T) {
	previousInspector := inspectProvisionTarget
	t.Cleanup(func() {
		inspectProvisionTarget = previousInspector
	})

	inspectProvisionTarget = func(vm alchemy_build.VirtualMachineConfig) (alchemy_deploy.StartTargetState, error) {
		return alchemy_deploy.StartTargetState{State: "missing"}, nil
	}

	err := ensureProvisionTargetRunning(alchemy_build.VirtualMachineConfig{
		OS:         "ubuntu",
		UbuntuType: "server",
		Arch:       "arm64",
	})
	if err == nil {
		t.Fatal("expected missing provision target to return an error")
	}
	if !strings.Contains(err.Error(), "sailwright create ubuntu --type server --arch arm64") {
		t.Fatalf("expected create hint, got %v", err)
	}
}

func TestEnsureProvisionTargetRunning_ReturnsStartHintWhenStopped(t *testing.T) {
	previousInspector := inspectProvisionTarget
	t.Cleanup(func() {
		inspectProvisionTarget = previousInspector
	})

	inspectProvisionTarget = func(vm alchemy_build.VirtualMachineConfig) (alchemy_deploy.StartTargetState, error) {
		return alchemy_deploy.StartTargetState{Exists: true, State: "stopped"}, nil
	}

	err := ensureProvisionTargetRunning(alchemy_build.VirtualMachineConfig{
		OS:         "ubuntu",
		UbuntuType: "server",
		Arch:       "arm64",
	})
	if err == nil {
		t.Fatal("expected stopped provision target to return an error")
	}
	if !strings.Contains(err.Error(), "state=stopped") {
		t.Fatalf("expected state in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "sailwright start ubuntu --type server --arch arm64") {
		t.Fatalf("expected start hint, got %v", err)
	}
}

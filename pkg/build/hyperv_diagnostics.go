package build

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/csautter/dev-alchemy/internal/winpsenv"
)

type hypervDiagnosticCommand struct {
	fileName   string
	executable string
	args       []string
}

func captureHypervDiagnostics(config VirtualMachineConfig, label string, buildErr error) {
	if runtime.GOOS != "windows" || config.HostOs != HostOsWindows || config.VirtualizationEngine != VirtualizationEngineHyperv {
		return
	}

	baseDir := filepath.Join(GetDirectoriesInstance().CacheDir, "windows", "hyperv-diagnostics")
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		log.Printf("Failed to create Hyper-V diagnostics directory %s: %v", baseDir, err)
		return
	}

	runDir := filepath.Join(baseDir, fmt.Sprintf("%s-%s", time.Now().UTC().Format("20060102T150405Z"), sanitizeDiagnosticsLabel(label)))
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		log.Printf("Failed to create Hyper-V diagnostics run directory %s: %v", runDir, err)
		return
	}

	summary := []string{
		fmt.Sprintf("timestamp_utc=%s", time.Now().UTC().Format(time.RFC3339)),
		fmt.Sprintf("label=%s", label),
		fmt.Sprintf("os=%s", config.OS),
		fmt.Sprintf("arch=%s", config.Arch),
		fmt.Sprintf("host_os=%s", config.HostOs),
		fmt.Sprintf("virtualization_engine=%s", config.VirtualizationEngine),
		fmt.Sprintf("cache_dir=%s", GetDirectoriesInstance().CacheDir),
	}
	if buildErr != nil {
		summary = append(summary, fmt.Sprintf("build_error=%s", sanitizeSensitiveText(buildErr.Error())))
	}
	if err := os.WriteFile(filepath.Join(runDir, "summary.txt"), []byte(strings.Join(summary, "\n")+"\n"), 0o600); err != nil {
		log.Printf("Failed to write Hyper-V diagnostics summary: %v", err)
	}

	for _, diagnosticCommand := range hypervDiagnosticCommands() {
		output, err := runDiagnosticCommand(diagnosticCommand.executable, diagnosticCommand.args...)
		if err != nil {
			output = append(output, []byte(fmt.Sprintf("\ncommand_error=%s\n", sanitizeSensitiveText(err.Error())))...)
		}
		output = []byte(sanitizeSensitiveText(string(output)))

		targetPath := filepath.Join(runDir, diagnosticCommand.fileName)
		if writeErr := os.WriteFile(targetPath, output, 0o600); writeErr != nil {
			log.Printf("Failed to write Hyper-V diagnostics file %s: %v", targetPath, writeErr)
		}
	}

	log.Printf("Captured Hyper-V diagnostics in %s", runDir)
}

func hypervDiagnosticCommands() []hypervDiagnosticCommand {
	return []hypervDiagnosticCommand{
		{
			fileName:   "ipconfig-all.txt",
			executable: "ipconfig",
			args:       []string{"/all"},
		},
		{
			fileName:   "route-print.txt",
			executable: "route",
			args:       []string{"print"},
		},
		{
			fileName:   "vmswitch.txt",
			executable: "powershell",
			args: []string{
				"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
				"-Command", `Import-Module Hyper-V -ErrorAction SilentlyContinue; Get-VMSwitch | Format-List * | Out-String -Width 4096`,
			},
		},
		{
			fileName:   "managementos-vmnetworkadapter.txt",
			executable: "powershell",
			args: []string{
				"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
				"-Command", `Import-Module Hyper-V -ErrorAction SilentlyContinue; Get-VMNetworkAdapter -ManagementOS | Format-List * | Out-String -Width 4096`,
			},
		},
		{
			fileName:   "netadapter.txt",
			executable: "powershell",
			args: []string{
				"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
				"-Command", `Get-NetAdapter | Sort-Object Name | Format-List Name,InterfaceDescription,Status,MacAddress,LinkSpeed,InterfaceGuid,ifIndex | Out-String -Width 4096`,
			},
		},
		{
			fileName:   "netipaddress.txt",
			executable: "powershell",
			args: []string{
				"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
				"-Command", `Get-NetIPAddress | Sort-Object InterfaceAlias,AddressFamily,IPAddress | Format-Table -AutoSize InterfaceAlias,IPAddress,PrefixLength,AddressFamily,Type,PrefixOrigin,SuffixOrigin,SkipAsSource | Out-String -Width 4096`,
			},
		},
		{
			fileName:   "netipconfiguration.txt",
			executable: "powershell",
			args: []string{
				"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
				"-Command", `Get-NetIPConfiguration | Format-List * | Out-String -Width 4096`,
			},
		},
		{
			fileName:   "netnat.txt",
			executable: "powershell",
			args: []string{
				"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
				"-Command", `if (Get-Command Get-NetNat -ErrorAction SilentlyContinue) { Get-NetNat | Format-List * | Out-String -Width 4096 } else { 'Get-NetNat unavailable on this host' }`,
			},
		},
		{
			fileName:   "dhcp.txt",
			executable: "powershell",
			args: []string{
				"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
				"-Command", `if (Get-Command Get-DhcpServerv4Scope -ErrorAction SilentlyContinue) { "Scopes:"; Get-DhcpServerv4Scope | Format-List *; "Bindings:"; Get-DhcpServerv4Binding | Format-List * } else { 'DHCP Server cmdlets unavailable on this host' } | Out-String -Width 4096`,
			},
		},
		{
			fileName:   "hyperv-vmswitch-eventlog.txt",
			executable: "powershell",
			args: []string{
				"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
				"-Command", `if (Get-WinEvent -ListLog 'Microsoft-Windows-Hyper-V-VmSwitch/Admin' -ErrorAction SilentlyContinue) { Get-WinEvent -LogName 'Microsoft-Windows-Hyper-V-VmSwitch/Admin' -MaxEvents 100 | Format-List TimeCreated,Id,LevelDisplayName,Message | Out-String -Width 4096 } else { 'Hyper-V VmSwitch admin log unavailable on this host' }`,
			},
		},
		{
			fileName:   "hyperv-vmms-eventlog.txt",
			executable: "powershell",
			args: []string{
				"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
				"-Command", `if (Get-WinEvent -ListLog 'Microsoft-Windows-Hyper-V-VMMS/Admin' -ErrorAction SilentlyContinue) { Get-WinEvent -LogName 'Microsoft-Windows-Hyper-V-VMMS/Admin' -MaxEvents 100 | Format-List TimeCreated,Id,LevelDisplayName,Message | Out-String -Width 4096 } else { 'Hyper-V VMMS admin log unavailable on this host' }`,
			},
		},
	}
}

func runDiagnosticCommand(executable string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// #nosec G204 -- diagnostic commands and args are hard-coded and only used for host inspection.
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env = winpsenv.CommandEnv(executable, GetDirectoriesInstance().ManagedEnv())

	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return output, ctx.Err()
	}
	return output, err
}

func sanitizeDiagnosticsLabel(label string) string {
	replacer := strings.NewReplacer(
		" ", "-",
		"/", "-",
		"\\", "-",
		":", "-",
	)
	return replacer.Replace(strings.ToLower(label))
}

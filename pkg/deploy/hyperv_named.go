package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	build "github.com/csautter/dev-alchemy/pkg/build"
)

func hasHypervVagrantInstanceState(vm build.VirtualMachineConfig) bool {
	_, err := os.Stat(filepath.Join(hypervVagrantDotfilePath(build.InstanceName(vm)), "machines", "default", "hyperv", "id"))
	return err == nil
}

// Named VMs discovered from another app-data directory must not be recreated by
// vagrant up, or silently skipped by vagrant destroy. Address them by provider name.
func runNamedHypervAction(vm build.VirtualMachineConfig, action string) error {
	if vm.Name == "" {
		return fmt.Errorf("direct Hyper-V actions require a named instance")
	}
	if err := build.ValidateInstance(vm); err != nil {
		return err
	}
	script, err := namedHypervActionScript(vm, action)
	if err != nil {
		return err
	}
	_, err = runHypervCommandWithCombinedOutput(build.GetDirectoriesInstance().ProjectDir, 2*time.Minute, "powershell", []string{"-NoProfile", "-NonInteractive", "-Command", script})
	if err != nil {
		return fmt.Errorf("Hyper-V %s %s: %w", action, build.InstanceName(vm), err)
	}
	return nil
}

func namedHypervActionScript(vm build.VirtualMachineConfig, action string) (string, error) {
	prefix := "$ErrorActionPreference='Stop'; $vm = Get-VM -Name " + powershellSingleQuote(build.InstanceName(vm)) + " -ErrorAction SilentlyContinue; "
	switch action {
	case "start":
		return prefix + "if ($null -eq $vm) { throw 'VM is missing' }; Start-VM -VM $vm", nil
	case "stop":
		return prefix + "if ($null -ne $vm -and $vm.State -ne 'Off') { Stop-VM -VM $vm -TurnOff -Force }", nil
	case "destroy":
		return prefix + `if ($null -ne $vm) {
 $disks = @(Get-VMHardDiskDrive -VM $vm | Select-Object -ExpandProperty Path)
 if ($vm.State -ne 'Off') { Stop-VM -VM $vm -TurnOff -Force }
 Remove-VM -VM $vm -Force
 foreach ($disk in $disks) {
  $users = @(Get-VM | Get-VMHardDiskDrive | Where-Object { $_.Path -eq $disk })
  if ($users.Count -eq 0 -and (Test-Path -LiteralPath $disk)) { Remove-Item -LiteralPath $disk -Force }
 }
}`, nil
	default:
		return "", fmt.Errorf("unsupported Hyper-V action %q", action)
	}
}

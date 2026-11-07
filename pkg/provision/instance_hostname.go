package provision

import (
	"fmt"
	"os"

	build "github.com/csautter/dev-alchemy/pkg/build"
)

// ConfigureTartInstanceHostname uses the existing guest SSH connection. Only a
// built-in play is used; no host role configuration is loaded.
func ConfigureTartInstanceHostname(vm build.VirtualMachineConfig) error {
	if vm.Name == "" {
		return nil
	}
	if err := build.ValidateInstance(vm); err != nil {
		return err
	}
	file, err := os.CreateTemp("", "sailwright-instance-hostname-*.yml")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	play := fmt.Sprintf(`- hosts: all
  gather_facts: false
  become: true
  tasks:
    - name: Set guest network names
      ansible.builtin.command:
        argv: [scutil, --set, "{{ item }}", %s]
      loop: [HostName, LocalHostName, ComputerName]
`, build.InstanceHostname(vm))
	if _, err = file.WriteString(play); err != nil {
		// Preserve the write error; closing cannot make the incomplete file usable.
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	options := ProvisionOptions{Verbosity: defaultAnsibleVerbosity}
	options.PlaybookPath = file.Name()
	options.PlaybookPathExplicit = true
	return runTartMacOSProvision(vm, options)
}

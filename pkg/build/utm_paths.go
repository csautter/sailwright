package build

import (
	"fmt"
	"os"
	"path/filepath"
)

// UtmVMDirectory resolves the destination shared by all UTM lifecycle commands.
// The legacy default directory is retained for existing VMs.
func UtmVMDirectory() (string, error) {
	if directory := os.Getenv("SAILWRIGHT_UTM_VM_DIR"); directory != "" {
		return filepath.Abs(directory)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve user home directory for UTM VM path: %w", err)
	}
	return filepath.Join(home, "Library", "Containers", "com.utmapp.UTM", "Data", "Documents"), nil
}

// UtmVirtualMachinePath identifies the bundle created for config.
func UtmVirtualMachinePath(config VirtualMachineConfig) (string, error) {
	directory, err := UtmVMDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, InstanceName(config)+".utm"), nil
}

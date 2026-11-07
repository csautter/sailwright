package build

import (
	"fmt"
	"os"
	"path/filepath"
)

// CreationResourceWarnings is advisory: missing telemetry never blocks create.
func CreationResourceWarnings(vm VirtualMachineConfig, storageDir string) []string {
	for {
		if _, err := os.Stat(storageDir); err == nil {
			break
		}
		parent := filepath.Dir(storageDir)
		if parent == storageDir {
			break
		}
		storageDir = parent
	}
	disk, diskErr := availableDiskBytes(storageDir)
	memory, memoryErr := availableMemoryMB()
	var warnings []string
	if diskErr == nil && disk < 20<<30 {
		warnings = append(warnings, fmt.Sprintf("only %.1f GiB free for another VM disk", float64(disk)/(1<<30)))
	}
	required := uint64(GetVmMemoryMB(vm)) + 2048 // #nosec G115 -- GetVmMemoryMB returns a positive override or at least minVmMemoryMB.
	if memoryErr == nil && memory < required {
		warnings = append(warnings, fmt.Sprintf("only %d MiB RAM available; this VM requests %d MiB plus host headroom", memory, GetVmMemoryMB(vm)))
	}
	return warnings
}

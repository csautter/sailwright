package build

import (
	"context"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var qemuVersionPattern = regexp.MustCompile(`QEMU emulator version (\d+)\.(\d+)`)

const qemuVersionProbeTimeout = 10 * time.Second

// arm64QemuMachineEnvVar is read by Packer as the arm64_machine variable of the QEMU templates.
// Users can set it to try another machine type without editing the templates.
const arm64QemuMachineEnvVar = "PKR_VAR_arm64_machine"

const (
	// arm64QemuPinnedMachine is the machine verified with the pinned Debian AAVMF firmware.
	// Newer machines (virt-11.x) hang in that firmware under HVF.
	arm64QemuPinnedMachine = "virt-10.0"
	// arm64QemuFallbackMachine is used when QEMU is older than 10.0, e.g. QEMU 8.2 on Ubuntu 24.04.
	arm64QemuFallbackMachine = "virt"
)

// runQemuAarch64 runs qemu-system-aarch64 with a fixed argv and a timeout.
func runQemuAarch64(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), qemuVersionProbeTimeout)
	defer cancel()
	// #nosec G204 -- the binary name is fixed and callers pass constant arguments.
	return exec.CommandContext(ctx, "qemu-system-aarch64", args...).Output()
}

// logArm64QemuVersion logs the QEMU version of an ARM64 build on hosts without a
// minimum version, so a UEFI hang report shows which QEMU was used.
func logArm64QemuVersion(config VirtualMachineConfig) {
	if config.Arch != "arm64" {
		return
	}
	output, err := runQemuAarch64("--version")
	if err != nil {
		log.Printf("Warning: could not determine the QEMU version: %v", err)
		return
	}
	match := qemuVersionPattern.FindStringSubmatch(string(output))
	if match == nil {
		log.Printf("Warning: could not parse the QEMU version from %q", string(output))
		return
	}
	log.Printf("Using QEMU %s.%s for the ARM64 build", match[1], match[2])
}

// arm64QemuMachineEnv returns the Packer environment that selects the ARM64 QEMU machine.
// It picks the machine by what the installed QEMU supports instead of by host OS.
// An arm64_machine set by the user wins; without a usable probe the template default applies.
func arm64QemuMachineEnv(config VirtualMachineConfig) []string {
	if config.Arch != "arm64" {
		return nil
	}
	if machine := os.Getenv(arm64QemuMachineEnvVar); machine != "" {
		log.Printf("Using ARM64 QEMU machine %q from %s", machine, arm64QemuMachineEnvVar)
		return nil
	}
	output, err := runQemuAarch64("-machine", "help")
	if err != nil {
		log.Printf("Warning: could not list the QEMU machine types, using the template default: %v", err)
		return nil
	}
	machine := selectArm64QemuMachine(string(output)) + ",highmem=on"
	log.Printf("Using ARM64 QEMU machine %q", machine)
	return []string{arm64QemuMachineEnvVar + "=" + machine}
}

// selectArm64QemuMachine returns the pinned machine when `qemu-system-aarch64 -machine help`
// lists it and the unversioned virt machine otherwise.
func selectArm64QemuMachine(machineHelp string) string {
	for _, line := range strings.Split(machineHelp, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == arm64QemuPinnedMachine {
			return arm64QemuPinnedMachine
		}
	}
	return arm64QemuFallbackMachine
}

package build

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

// testedMacOSQemuMajors lists the Homebrew QEMU major versions the macOS ARM64
// builds were verified with. The templates pin the virt-10.0 machine, which
// needs QEMU 10.0 or newer, and use the pinned Debian AAVMF firmware.
var testedMacOSQemuMajors = map[int]bool{10: true, 11: true}

var qemuVersionPattern = regexp.MustCompile(`QEMU emulator version (\d+)\.(\d+)`)

const qemuVersionProbeTimeout = 10 * time.Second

type macOSQemuVersionStatus int

const (
	macOSQemuVersionOK macOSQemuVersionStatus = iota
	// macOSQemuVersionWarn means the build may work but was not verified.
	macOSQemuVersionWarn
	// macOSQemuVersionTooOld means QEMU cannot start the pinned virt-10.0 machine.
	macOSQemuVersionTooOld
)

func RunQemuUbuntuBuildOnMacOS(config VirtualMachineConfig) error {
	if err := checkMacOSArm64QemuVersion(config); err != nil {
		return err
	}
	return runQemuUbuntuBuild(config, "build/packer/linux/ubuntu/linux-ubuntu-on-macos.sh")
}

func RunQemuWindowsBuildOnMacOS(config VirtualMachineConfig) error {
	if err := checkMacOSArm64QemuVersion(config); err != nil {
		return err
	}
	return runQemuWindowsBuild(config, "build/packer/windows/windows11-on-macos.sh")
}

// checkMacOSArm64QemuVersion logs the QEMU version and fails before any
// artifacts are staged when QEMU is too old for the virt-10.0 machine.
func checkMacOSArm64QemuVersion(config VirtualMachineConfig) error {
	if config.Arch != "arm64" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), qemuVersionProbeTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, "qemu-system-aarch64", "--version").Output()
	if err != nil {
		log.Printf("Warning: could not determine the QEMU version: %v", err)
		return nil
	}
	message, status := macOSQemuVersionCheck(string(output))
	switch status {
	case macOSQemuVersionOK:
		log.Print(message)
	case macOSQemuVersionWarn:
		log.Printf("Warning: %s", message)
	default:
		return fmt.Errorf("%s; run 'brew upgrade qemu'", message)
	}
	return nil
}

// macOSQemuVersionCheck reports the QEMU version from `qemu-system-aarch64 --version`
// and whether the ARM64 builds can use and were tested with it.
func macOSQemuVersionCheck(versionOutput string) (string, macOSQemuVersionStatus) {
	match := qemuVersionPattern.FindStringSubmatch(versionOutput)
	if match == nil {
		return fmt.Sprintf("could not parse the QEMU version from %q", versionOutput), macOSQemuVersionWarn
	}
	major, _ := strconv.Atoi(match[1])
	version := match[1] + "." + match[2]
	if major < 10 {
		return fmt.Sprintf("QEMU %s is too old for macOS ARM64 builds; the virt-10.0 machine needs QEMU 10.0 or newer", version), macOSQemuVersionTooOld
	}
	if !testedMacOSQemuMajors[major] {
		return fmt.Sprintf("QEMU %s has not been tested with macOS ARM64 builds; if the VM hangs in the UEFI boot screen, see build/packer/linux/ubuntu/README.md", version), macOSQemuVersionWarn
	}
	return fmt.Sprintf("Using QEMU %s for the macOS ARM64 build", version), macOSQemuVersionOK
}

func runQemuWindowsBuild(config VirtualMachineConfig, relativeScriptPath string) error {
	config, err := withStagedBuildArtifactsForNoCache(config)
	if err != nil {
		return err
	}

	scriptPath := filepath.Join(GetDirectoriesInstance().GetDirectories().ProjectDir, relativeScriptPath)
	args := []string{
		scriptPath,
		"--project-root", GetDirectoriesInstance().GetDirectories().ProjectDir,
		"--build-output-dir", getQemuBuildOutputDir(config),
		"--arch", config.Arch,
		"--vnc-port", fmt.Sprintf("%d", config.VncPort),
		"--cpus", getVmCpuCountString(config),
		"--memory", fmt.Sprintf("%d", getVmMemoryMB(config)),
	}
	if stagedArtifact, ok := firstStagedBuildArtifact(config); ok {
		args = append(args, "--artifact-output-path", stagedArtifact)
	}
	if config.Headless {
		args = append(args, "--headless")
	}
	if config.Verbose {
		args = append(args, "--verbose")
	}
	return RunBuildScript(config, "bash", args)
}

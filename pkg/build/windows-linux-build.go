package build

import (
	"context"
	"fmt"
)

const (
	ubuntuHypervPackerFile = "build/packer/linux/ubuntu/linux-ubuntu-hyperv.pkr.hcl"
)

// RunHypervUbuntuBuildOnWindows builds an Ubuntu VM using Hyper-V on Windows.
func RunHypervUbuntuBuildOnWindows(ctx context.Context, config VirtualMachineConfig) error {
	config, err := withStagedBuildArtifacts(config)
	if err != nil {
		return err
	}

	if err := initializePacker(ctx, config, ubuntuHypervPackerFile); err != nil {
		return fmt.Errorf("failed to initialize packer: %w", err)
	}

	args := hypervUbuntuPackerArgs(config)
	return RunBuildScript(ctx, config, packerExecutable, args)
}

// hypervUbuntuPackerArgs builds the packer arguments for the Ubuntu Hyper-V template.
func hypervUbuntuPackerArgs(config VirtualMachineConfig) []string {
	args := []string{
		"build",
		"-var", fmt.Sprintf("iso_url=%s", ubuntuLiveServerISOPath("amd64", ubuntuLiveServerAMD64Version)),
		"-var", fmt.Sprintf("iso_checksum=sha256:%s", ubuntuLiveServerAMD64SHA256),
		"-var", fmt.Sprintf("cache_dir=%s", GetDirectoriesInstance().CacheDir),
		"-var", fmt.Sprintf("ubuntu_type=%s", defaultUbuntuType(config.UbuntuType)),
		"-var", fmt.Sprintf("cpus=%s", getVmCpuCountString(config)),
		"-var", fmt.Sprintf("memory=%d", getVmMemoryMB(config)),
	}
	if config.Headless {
		// The hyperv-iso plugin opens a vmconnect console unless headless is set.
		args = append(args, "-var", "headless=true")
	}
	if stagedArtifact, ok := firstStagedBuildArtifact(config); ok {
		args = append(args, "-var", fmt.Sprintf("artifact_output_path=%s", stagedArtifact))
	}
	return append(args, ubuntuHypervPackerFile)
}

func defaultUbuntuType(ubuntuType string) string {
	if ubuntuType == "" {
		return "server"
	}
	return ubuntuType
}

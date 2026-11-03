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

	args := []string{
		"build",
		"-var", fmt.Sprintf("iso_url=%s", ubuntuLiveServerISOPath("amd64", ubuntuLiveServerAMD64Version)),
		"-var", fmt.Sprintf("iso_checksum=sha256:%s", ubuntuLiveServerAMD64SHA256),
		"-var", fmt.Sprintf("cache_dir=%s", GetDirectoriesInstance().CacheDir),
		"-var", fmt.Sprintf("ubuntu_type=%s", defaultUbuntuType(config.UbuntuType)),
		"-var", fmt.Sprintf("cpus=%s", getVmCpuCountString(config)),
		"-var", fmt.Sprintf("memory=%d", getVmMemoryMB(config)),
		ubuntuHypervPackerFile,
	}
	if stagedArtifact, ok := firstStagedBuildArtifact(config); ok {
		args = append(args[:len(args)-1], "-var", fmt.Sprintf("artifact_output_path=%s", stagedArtifact), args[len(args)-1])
	}

	return RunBuildScript(ctx, config, packerExecutable, args)
}

func defaultUbuntuType(ubuntuType string) string {
	if ubuntuType == "" {
		return "server"
	}
	return ubuntuType
}

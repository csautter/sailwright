package cmd

import (
	"context"
	"fmt"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	alchemy_oci "github.com/csautter/dev-alchemy/pkg/oci"
	"github.com/spf13/cobra"
)

// prebuiltUbuntuRepository holds the Ubuntu artifacts published by the Linux build workflow.
const prebuiltUbuntuRepository = "ghcr.io/csautter/ubuntu-24"

var pullPrebuiltArtifactFunc = func(ctx context.Context, vm alchemy_build.VirtualMachineConfig, reference string, progress alchemy_oci.TransferProgress) (alchemy_oci.TransferResult, error) {
	return alchemy_oci.Pull(ctx, vm, reference, alchemy_oci.PullOptions{
		Progress: progress,
		// The user already accepted the Linux-built artifact in the build prompt.
		ConfirmForeignArtifactUse: func(context.Context, alchemy_oci.ForeignArtifact) (bool, error) {
			return true, nil
		},
	})
}

// prebuiltArtifactReference returns the published artifact for vm, or "" when none is published.
func prebuiltArtifactReference(vm alchemy_build.VirtualMachineConfig) string {
	if vm.HostOs != alchemy_build.HostOsLinux && vm.HostOs != alchemy_build.HostOsDarwin {
		return ""
	}
	if vm.OS != "ubuntu" || vm.UbuntuType == "" || vm.Arch == "" {
		return ""
	}
	return fmt.Sprintf("%s:%s-%s-linux-build", prebuiltUbuntuRepository, vm.UbuntuType, vm.Arch)
}

// offerPrebuiltArtifact asks whether to pull the published artifact instead of
// building locally. It returns true only when the artifact was pulled; declining,
// a non-interactive session, or a failed pull all fall back to the local build.
func offerPrebuiltArtifact(cmd *cobra.Command, vm alchemy_build.VirtualMachineConfig) bool {
	reference := prebuiltArtifactReference(vm)
	if reference == "" || vm.NoCache || !isInteractiveInput(cmd.InOrStdin()) {
		return false
	}
	if exists, err := inspectBuildArtifactExists(vm); err != nil || exists {
		return false
	}

	prompt := fmt.Sprintf("💡 Local VM builds take a long time. A ready-to-use image is published at %s", reference)
	if vm.HostOs == alchemy_build.HostOsDarwin {
		prompt += " (built on Linux with QEMU, usable with UTM)"
	}
	prompt += ". Pull it instead of building locally? [y/N]: "

	confirmed, err := promptForConfirmationFunc(cmd.InOrStdin(), cmd.OutOrStdout(), prompt)
	if err != nil || !confirmed {
		return false
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := pullPrebuiltArtifactFunc(ctx, vm, reference, newOCIProgressReporter("pulling", cmd.ErrOrStderr()))
	if err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "⚠️  Pulling %s failed: %v\nFalling back to a local build.\n", reference, err)
		return false
	}
	printOCITransferResult("Pulled", result)
	return true
}

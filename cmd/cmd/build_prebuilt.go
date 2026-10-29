package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	alchemy_oci "github.com/csautter/dev-alchemy/pkg/oci"
	"github.com/spf13/cobra"
)

// noPrebuiltOfferEnvVar disables the prebuilt offer for wrappers that run builds
// under a pty without anyone to answer, such as terminal recorders.
const noPrebuiltOfferEnvVar = "SAILWRIGHT_NO_PREBUILT_OFFER"

var noPrebuiltOffer bool

var pullPrebuiltArtifactFunc = func(ctx context.Context, vm alchemy_build.VirtualMachineConfig, reference string, progress alchemy_oci.TransferProgress) (alchemy_oci.TransferResult, error) {
	return alchemy_oci.Pull(ctx, vm, reference, prebuiltPullOptions(progress))
}

func prebuiltPullOptions(progress alchemy_oci.TransferProgress) alchemy_oci.PullOptions {
	return alchemy_oci.PullOptions{
		// The image is public; stale Docker logins or missing credential helpers
		// would only make the pull fail.
		RegistryOptions: alchemy_oci.RegistryOptions{DisableDockerCredentials: true},
		Progress:        progress,
		// The user already accepted the Linux-built artifact in the build prompt.
		ConfirmForeignArtifactUse: func(context.Context, alchemy_oci.ForeignArtifact) (bool, error) {
			return true, nil
		},
	}
}

// prebuiltArtifactReference returns the published artifact for vm, or "" when none is published.
func prebuiltArtifactReference(vm alchemy_build.VirtualMachineConfig) string {
	if vm.HostOs != alchemy_build.HostOsLinux && vm.HostOs != alchemy_build.HostOsDarwin {
		return ""
	}
	if vm.OS != "ubuntu" || vm.UbuntuType == "" || vm.Arch == "" {
		return ""
	}
	return alchemy_oci.PublishedUbuntuReference(vm.UbuntuType, vm.Arch)
}

func prebuiltOfferDisabled() bool {
	return noPrebuiltOffer || os.Getenv(noPrebuiltOfferEnvVar) != ""
}

// offerPrebuiltArtifact asks whether to pull the published artifact instead of
// building locally. It returns true only when the artifact was pulled; declining,
// a non-interactive session, or a failed pull all fall back to the local build.
// An interrupted pull returns an error so the build does not start afterwards.
func offerPrebuiltArtifact(cmd *cobra.Command, vm alchemy_build.VirtualMachineConfig) (bool, error) {
	reference := prebuiltArtifactReference(vm)
	if reference == "" || vm.NoCache || prebuiltOfferDisabled() || !isInteractiveInput(cmd.InOrStdin()) {
		return false, nil
	}
	if exists, err := inspectBuildArtifactExists(vm); err != nil || exists {
		if err != nil && vm.Verbose {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Skipping the prebuilt image offer: %v\n", err)
		}
		return false, nil
	}

	prompt := fmt.Sprintf("💡 Local VM builds take a long time. A ready-to-use image (several GB download) is published at %s", reference)
	if vm.HostOs == alchemy_build.HostOsDarwin {
		prompt += " (built on Linux with QEMU, usable with UTM)"
	}
	prompt += ". Pull it instead of building locally? [y/N]: "

	confirmed, err := promptForConfirmationFunc(cmd.InOrStdin(), cmd.OutOrStdout(), prompt)
	if err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "⚠️  Could not read the answer (%v); building locally.\n", err)
		return false, nil
	}
	if !confirmed {
		return false, nil
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	// Cancel the download on Ctrl+C so the pull removes its staging directory.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	result, err := pullPrebuiltArtifactFunc(ctx, vm, reference, newOCIProgressReporter("pulling", cmd.ErrOrStderr()))
	if ctx.Err() != nil {
		return false, fmt.Errorf("pulling %s was interrupted", reference)
	}
	if err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "⚠️  Pulling %s failed: %v\nFalling back to a local build.\n", reference, err)
		return false, nil
	}
	printOCITransferResult("Pulled", result)
	return true, nil
}

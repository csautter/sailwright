package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	alchemy_oci "github.com/csautter/dev-alchemy/pkg/oci"
	"github.com/spf13/cobra"
)

type prebuiltOfferStubs struct {
	prompted bool
	prompt   string
	pulled   string
}

func stubPrebuiltOffer(t *testing.T, artifactExists bool, answer bool, pullErr error) *prebuiltOfferStubs {
	t.Helper()
	previousInspector, previousPrompt, previousPull := inspectBuildArtifactExists, promptForConfirmationFunc, pullPrebuiltArtifactFunc
	t.Cleanup(func() {
		inspectBuildArtifactExists, promptForConfirmationFunc, pullPrebuiltArtifactFunc = previousInspector, previousPrompt, previousPull
	})

	stubs := &prebuiltOfferStubs{}
	inspectBuildArtifactExists = func(alchemy_build.VirtualMachineConfig) (bool, error) {
		return artifactExists, nil
	}
	promptForConfirmationFunc = func(_ io.Reader, _ io.Writer, prompt string) (bool, error) {
		stubs.prompted = true
		stubs.prompt = prompt
		return answer, nil
	}
	pullPrebuiltArtifactFunc = func(_ context.Context, _ alchemy_build.VirtualMachineConfig, reference string, _ alchemy_oci.TransferProgress) (alchemy_oci.TransferResult, error) {
		stubs.pulled = reference
		return alchemy_oci.TransferResult{Reference: reference}, pullErr
	}
	return stubs
}

func prebuiltOfferCommand() (*cobra.Command, *bytes.Buffer) {
	var stderr bytes.Buffer
	command := &cobra.Command{}
	command.SetIn(strings.NewReader(""))
	command.SetOut(io.Discard)
	command.SetErr(&stderr)
	return command, &stderr
}

func ubuntuBuildConfig(hostOs alchemy_build.HostOsType) alchemy_build.VirtualMachineConfig {
	return alchemy_build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "arm64", HostOs: hostOs}
}

func TestPrebuiltArtifactReference(t *testing.T) {
	tests := []struct {
		name string
		vm   alchemy_build.VirtualMachineConfig
		want string
	}{
		{"linux ubuntu", ubuntuBuildConfig(alchemy_build.HostOsLinux), "ghcr.io/csautter/ubuntu-24:server-arm64-linux-build"},
		{"darwin ubuntu", ubuntuBuildConfig(alchemy_build.HostOsDarwin), "ghcr.io/csautter/ubuntu-24:server-arm64-linux-build"},
		{"windows host", ubuntuBuildConfig(alchemy_build.HostOsWindows), ""},
		{"windows guest", alchemy_build.VirtualMachineConfig{OS: "windows11", Arch: "amd64", HostOs: alchemy_build.HostOsLinux}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := prebuiltArtifactReference(tt.vm); got != tt.want {
				t.Fatalf("prebuiltArtifactReference() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOfferPrebuiltArtifactPullsWhenAccepted(t *testing.T) {
	stubs := stubPrebuiltOffer(t, false, true, nil)
	command, _ := prebuiltOfferCommand()

	if !offerPrebuiltArtifact(command, ubuntuBuildConfig(alchemy_build.HostOsDarwin)) {
		t.Fatal("expected accepted offer to skip the local build")
	}
	if stubs.pulled != "ghcr.io/csautter/ubuntu-24:server-arm64-linux-build" {
		t.Fatalf("unexpected pulled reference %q", stubs.pulled)
	}
	if !strings.Contains(stubs.prompt, "[y/N]") || !strings.Contains(stubs.prompt, "usable with UTM") {
		t.Fatalf("unexpected prompt %q", stubs.prompt)
	}
}

func TestOfferPrebuiltArtifactBuildsLocallyByDefault(t *testing.T) {
	stubs := stubPrebuiltOffer(t, false, false, nil)
	command, _ := prebuiltOfferCommand()

	if offerPrebuiltArtifact(command, ubuntuBuildConfig(alchemy_build.HostOsLinux)) {
		t.Fatal("expected declined offer to fall back to the local build")
	}
	if !stubs.prompted || stubs.pulled != "" {
		t.Fatalf("expected a prompt without a pull, got prompted=%v pulled=%q", stubs.prompted, stubs.pulled)
	}
}

func TestOfferPrebuiltArtifactFallsBackWhenPullFails(t *testing.T) {
	stubPrebuiltOffer(t, false, true, errors.New("registry unreachable"))
	command, stderr := prebuiltOfferCommand()

	if offerPrebuiltArtifact(command, ubuntuBuildConfig(alchemy_build.HostOsLinux)) {
		t.Fatal("expected failed pull to fall back to the local build")
	}
	if !strings.Contains(stderr.String(), "registry unreachable") || !strings.Contains(stderr.String(), "Falling back to a local build") {
		t.Fatalf("unexpected stderr %q", stderr.String())
	}
}

func TestOfferPrebuiltArtifactSkipsPrompt(t *testing.T) {
	noCache := ubuntuBuildConfig(alchemy_build.HostOsLinux)
	noCache.NoCache = true

	tests := []struct {
		name           string
		vm             alchemy_build.VirtualMachineConfig
		artifactExists bool
	}{
		{"windows host", ubuntuBuildConfig(alchemy_build.HostOsWindows), false},
		{"no cache", noCache, false},
		{"artifact exists", ubuntuBuildConfig(alchemy_build.HostOsLinux), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubs := stubPrebuiltOffer(t, tt.artifactExists, true, nil)
			command, _ := prebuiltOfferCommand()

			if offerPrebuiltArtifact(command, tt.vm) || stubs.prompted {
				t.Fatalf("expected no prompt, got prompted=%v", stubs.prompted)
			}
		})
	}
}

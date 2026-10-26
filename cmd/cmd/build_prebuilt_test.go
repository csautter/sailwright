package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	alchemy_oci "github.com/csautter/dev-alchemy/pkg/oci"
	"github.com/spf13/cobra"
)

type prebuiltOfferStubs struct {
	artifactExists bool
	inspectErr     error
	answer         bool
	promptErr      error
	pullErr        error

	prompted bool
	prompt   string
	pulled   string
}

// stubPrebuiltOffer replaces the offer's seams; tests adjust the returned fields
// before calling the offer.
func stubPrebuiltOffer(t *testing.T) *prebuiltOfferStubs {
	t.Helper()
	previousInspector, previousPrompt, previousPull, previousNoPrebuilt := inspectBuildArtifactExists, promptForConfirmationFunc, pullPrebuiltArtifactFunc, noPrebuiltOffer
	t.Cleanup(func() {
		inspectBuildArtifactExists, promptForConfirmationFunc, pullPrebuiltArtifactFunc, noPrebuiltOffer = previousInspector, previousPrompt, previousPull, previousNoPrebuilt
	})
	t.Setenv(noPrebuiltOfferEnvVar, "")
	noPrebuiltOffer = false

	stubs := &prebuiltOfferStubs{}
	inspectBuildArtifactExists = func(alchemy_build.VirtualMachineConfig) (bool, error) {
		return stubs.artifactExists, stubs.inspectErr
	}
	promptForConfirmationFunc = func(_ io.Reader, _ io.Writer, prompt string) (bool, error) {
		stubs.prompted = true
		stubs.prompt = prompt
		return stubs.answer, stubs.promptErr
	}
	pullPrebuiltArtifactFunc = func(ctx context.Context, _ alchemy_build.VirtualMachineConfig, reference string, _ alchemy_oci.TransferProgress) (alchemy_oci.TransferResult, error) {
		stubs.pulled = reference
		if err := ctx.Err(); err != nil {
			return alchemy_oci.TransferResult{}, err
		}
		return alchemy_oci.TransferResult{Reference: reference}, stubs.pullErr
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
		{"missing type", alchemy_build.VirtualMachineConfig{OS: "ubuntu", Arch: "amd64", HostOs: alchemy_build.HostOsLinux}, ""},
		{"missing arch", alchemy_build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", HostOs: alchemy_build.HostOsLinux}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := prebuiltArtifactReference(tt.vm); got != tt.want {
				t.Fatalf("prebuiltArtifactReference() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrebuiltPullOptionsPullAnonymouslyAndAcceptForeignArtifacts(t *testing.T) {
	opts := prebuiltPullOptions(nil)
	if !opts.RegistryOptions.DisableDockerCredentials {
		t.Fatal("expected the public prebuilt pull to skip Docker credentials")
	}
	if opts.ConfirmForeignArtifactUse == nil {
		t.Fatal("expected a foreign artifact confirmation")
	}
	confirmed, err := opts.ConfirmForeignArtifactUse(context.Background(), alchemy_oci.ForeignArtifact{})
	if err != nil || !confirmed {
		t.Fatalf("expected the build prompt to count as foreign artifact confirmation, got %v, %v", confirmed, err)
	}
}

func TestOfferPrebuiltArtifactPullsWhenAccepted(t *testing.T) {
	stubs := stubPrebuiltOffer(t)
	stubs.answer = true
	command, _ := prebuiltOfferCommand()

	pulled, err := offerPrebuiltArtifact(command, ubuntuBuildConfig(alchemy_build.HostOsDarwin))
	if err != nil || !pulled {
		t.Fatalf("expected accepted offer to skip the local build, got pulled=%v err=%v", pulled, err)
	}
	if stubs.pulled != "ghcr.io/csautter/ubuntu-24:server-arm64-linux-build" {
		t.Fatalf("unexpected pulled reference %q", stubs.pulled)
	}
	if !strings.Contains(stubs.prompt, "[y/N]") || !strings.Contains(stubs.prompt, "usable with UTM") {
		t.Fatalf("unexpected prompt %q", stubs.prompt)
	}
}

func TestOfferPrebuiltArtifactBuildsLocallyByDefault(t *testing.T) {
	stubs := stubPrebuiltOffer(t)
	command, _ := prebuiltOfferCommand()

	pulled, err := offerPrebuiltArtifact(command, ubuntuBuildConfig(alchemy_build.HostOsLinux))
	if err != nil || pulled {
		t.Fatalf("expected declined offer to fall back to the local build, got pulled=%v err=%v", pulled, err)
	}
	if !stubs.prompted || stubs.pulled != "" {
		t.Fatalf("expected a prompt without a pull, got prompted=%v pulled=%q", stubs.prompted, stubs.pulled)
	}
	if strings.Contains(stubs.prompt, "UTM") {
		t.Fatalf("expected no UTM hint on Linux, got %q", stubs.prompt)
	}
}

func TestOfferPrebuiltArtifactFallsBack(t *testing.T) {
	tests := []struct {
		name       string
		promptErr  error
		pullErr    error
		wantStderr []string
	}{
		{"pull fails", nil, errors.New("registry unreachable"), []string{"registry unreachable", "Falling back to a local build"}},
		{"prompt fails", errors.New("read failed"), nil, []string{"read failed", "building locally"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubs := stubPrebuiltOffer(t)
			stubs.answer, stubs.promptErr, stubs.pullErr = true, tt.promptErr, tt.pullErr
			command, stderr := prebuiltOfferCommand()

			pulled, err := offerPrebuiltArtifact(command, ubuntuBuildConfig(alchemy_build.HostOsLinux))
			if err != nil || pulled {
				t.Fatalf("expected fallback to the local build, got pulled=%v err=%v", pulled, err)
			}
			for _, want := range tt.wantStderr {
				if !strings.Contains(stderr.String(), want) {
					t.Fatalf("expected stderr to contain %q, got %q", want, stderr.String())
				}
			}
		})
	}
}

func TestOfferPrebuiltArtifactReturnsErrorWhenInterrupted(t *testing.T) {
	stubs := stubPrebuiltOffer(t)
	stubs.answer = true
	command, _ := prebuiltOfferCommand()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	command.SetContext(ctx)

	pulled, err := offerPrebuiltArtifact(command, ubuntuBuildConfig(alchemy_build.HostOsLinux))
	if pulled || err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("expected an interrupted pull to stop the build, got pulled=%v err=%v", pulled, err)
	}
}

func TestOfferPrebuiltArtifactSkipsPrompt(t *testing.T) {
	noCache := ubuntuBuildConfig(alchemy_build.HostOsLinux)
	noCache.NoCache = true

	tests := []struct {
		name  string
		vm    alchemy_build.VirtualMachineConfig
		setup func(*testing.T, *prebuiltOfferStubs, *cobra.Command)
	}{
		{"windows host", ubuntuBuildConfig(alchemy_build.HostOsWindows), nil},
		{"no cache", noCache, nil},
		{"artifact exists", ubuntuBuildConfig(alchemy_build.HostOsLinux), func(_ *testing.T, stubs *prebuiltOfferStubs, _ *cobra.Command) {
			stubs.artifactExists = true
		}},
		{"inspect error", ubuntuBuildConfig(alchemy_build.HostOsLinux), func(_ *testing.T, stubs *prebuiltOfferStubs, _ *cobra.Command) {
			stubs.inspectErr = errors.New("permission denied")
		}},
		{"no-prebuilt flag", ubuntuBuildConfig(alchemy_build.HostOsLinux), func(*testing.T, *prebuiltOfferStubs, *cobra.Command) {
			noPrebuiltOffer = true
		}},
		{"opt-out env var", ubuntuBuildConfig(alchemy_build.HostOsLinux), func(t *testing.T, _ *prebuiltOfferStubs, _ *cobra.Command) {
			t.Setenv(noPrebuiltOfferEnvVar, "1")
		}},
		{"stdin is a pipe", ubuntuBuildConfig(alchemy_build.HostOsLinux), func(t *testing.T, _ *prebuiltOfferStubs, command *cobra.Command) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatalf("failed to create pipe: %v", err)
			}
			t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
			command.SetIn(reader)
		}},
		{"stdin is /dev/null", ubuntuBuildConfig(alchemy_build.HostOsLinux), func(t *testing.T, _ *prebuiltOfferStubs, command *cobra.Command) {
			devNull, err := os.Open(os.DevNull)
			if err != nil {
				t.Fatalf("failed to open %s: %v", os.DevNull, err)
			}
			t.Cleanup(func() { _ = devNull.Close() })
			command.SetIn(devNull)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubs := stubPrebuiltOffer(t)
			stubs.answer = true
			command, _ := prebuiltOfferCommand()
			if tt.setup != nil {
				tt.setup(t, stubs, command)
			}

			pulled, err := offerPrebuiltArtifact(command, tt.vm)
			if pulled || err != nil || stubs.prompted {
				t.Fatalf("expected no prompt, got prompted=%v pulled=%v err=%v", stubs.prompted, pulled, err)
			}
		})
	}
}

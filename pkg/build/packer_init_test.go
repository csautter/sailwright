package build

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func stubPackerInit(t *testing.T, outputs []string) *int {
	t.Helper()
	calls := 0
	origRun, origDelays := runPackerInitCommand, packerInitRetryDelays
	t.Cleanup(func() {
		runPackerInitCommand, packerInitRetryDelays = origRun, origDelays
	})
	packerInitRetryDelays = []time.Duration{0, 0}
	runPackerInitCommand = func(string, string, []string, []string) ([]byte, error) {
		output := outputs[calls]
		calls++
		if output == "" {
			return nil, nil
		}
		return []byte(output), errors.New("exit status 1")
	}
	return &calls
}

func TestInitializePackerRetriesNetworkErrors(t *testing.T) {
	calls := stubPackerInit(t, []string{
		"wsarecv: An existing connection was forcibly closed by the remote host.",
		"",
	})

	if err := initializePacker(VirtualMachineConfig{}, hypervPackerFile); err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if *calls != 2 {
		t.Fatalf("expected 2 packer init calls, got %d", *calls)
	}
}

func TestInitializePackerDoesNotRetryNonNetworkErrors(t *testing.T) {
	calls := stubPackerInit(t, []string{"Error: Unsupported Packer Core version", ""})

	if err := initializePacker(VirtualMachineConfig{}, hypervPackerFile); err == nil {
		t.Fatal("expected error, got nil")
	}
	if *calls != 1 {
		t.Fatalf("expected 1 packer init call, got %d", *calls)
	}
}

func TestInitializePackerExplainsReleasesFallbackAfterRetries(t *testing.T) {
	fallback := `could not get sha256 checksum file for github.com/hashicorp/vagrant version 1.1.7. GET ...: 404`
	calls := stubPackerInit(t, []string{fallback, fallback, fallback})

	err := initializePacker(VirtualMachineConfig{}, hypervPackerFile)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if *calls != 3 {
		t.Fatalf("expected 3 packer init calls, got %d", *calls)
	}
	if !strings.Contains(err.Error(), "releases.hashicorp.com") {
		t.Errorf("expected releases.hashicorp.com hint in error, got %q", err.Error())
	}
}

func TestRunHypervWindowsBuildDoesNotRetryPackerInitFailure(t *testing.T) {
	calls := stubPackerInit(t, []string{"Error: Unsupported Packer Core version", "", ""})

	err := RunHypervWindowsBuildOnWindows(VirtualMachineConfig{
		OS:                   "windows11",
		Arch:                 "amd64",
		HostOs:               HostOsWindows,
		VirtualizationEngine: VirtualizationEngineHyperv,
	})
	if err == nil || !strings.Contains(err.Error(), "failed to initialize packer") {
		t.Fatalf("expected packer init error, got %v", err)
	}
	if *calls != 1 {
		t.Fatalf("expected packer init to run once, got %d", *calls)
	}
}

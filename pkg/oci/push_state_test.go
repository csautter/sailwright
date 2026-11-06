package oci

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

func TestPushRefusesIncompleteArtifact(t *testing.T) {
	artifactPath := filepath.Join(t.TempDir(), "cache", "ubuntu", "incomplete.qcow2")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, []byte("partly replaced"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A build or pull that stopped while replacing the artifact leaves it pending.
	if _, err := alchemy_build.MarkBuildArtifactsPending([]string{artifactPath}); err != nil {
		t.Fatal(err)
	}
	vm := alchemy_build.VirtualMachineConfig{
		OS:                     "ubuntu",
		UbuntuType:             "server",
		Arch:                   "amd64",
		HostOs:                 alchemy_build.HostOsLinux,
		VirtualizationEngine:   alchemy_build.VirtualizationEngineQemu,
		ExpectedBuildArtifacts: []string{artifactPath},
	}

	_, err := Push(context.Background(), vm, "registry.invalid/sailwright/test:incomplete", PushOptions{})
	if err == nil || !strings.Contains(err.Error(), string(alchemy_build.ArtifactStateIncomplete)) {
		t.Fatalf("expected push of an incomplete artifact to be refused, got %v", err)
	}
}

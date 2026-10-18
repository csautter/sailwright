package build

import (
	"path/filepath"
	"testing"
)

func TestGetQemuBuildOutputDir(t *testing.T) {
	config := VirtualMachineConfig{
		OS:         "ubuntu",
		Arch:       "amd64",
		UbuntuType: "server",
	}

	got := getQemuBuildOutputDir(config)
	want := filepath.Join(GetDirectoriesInstance().CacheDir, "ubuntu", "qemu-out-ubuntu-server-amd64")
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

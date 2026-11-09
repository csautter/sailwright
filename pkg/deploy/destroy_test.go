package deploy

import (
	"strings"
	"testing"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

func TestSupportsDestroy(t *testing.T) {
	tests := []struct {
		name string
		vm   alchemy_build.VirtualMachineConfig
		want bool
	}{
		{
			name: "utm supported",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "windows11",
				Arch:                 "arm64",
				HostOs:               alchemy_build.HostOsDarwin,
				VirtualizationEngine: alchemy_build.VirtualizationEngineUtm,
			},
			want: true,
		},
		{
			name: "tart supported",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "macos",
				Arch:                 "arm64",
				HostOs:               alchemy_build.HostOsDarwin,
				VirtualizationEngine: alchemy_build.VirtualizationEngineTart,
			},
			want: true,
		},
		{
			name: "hyperv supported",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "ubuntu",
				UbuntuType:           "desktop",
				Arch:                 "amd64",
				HostOs:               alchemy_build.HostOsWindows,
				VirtualizationEngine: alchemy_build.VirtualizationEngineHyperv,
			},
			want: true,
		},
		{
			name: "linux qemu supported",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "ubuntu",
				UbuntuType:           "desktop",
				Arch:                 "amd64",
				HostOs:               alchemy_build.HostOsLinux,
				VirtualizationEngine: alchemy_build.VirtualizationEngineQemu,
			},
			want: true,
		},
		{
			name: "linux qemu windows supported",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "windows11",
				Arch:                 "amd64",
				HostOs:               alchemy_build.HostOsLinux,
				VirtualizationEngine: alchemy_build.VirtualizationEngineQemu,
			},
			want: true,
		},
		{
			name: "virtualbox unsupported",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "windows11",
				Arch:                 "amd64",
				HostOs:               alchemy_build.HostOsWindows,
				VirtualizationEngine: alchemy_build.VirtualizationEngineVirtualBox,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		if got := SupportsDestroy(tt.vm); got != tt.want {
			t.Fatalf("%s: expected %v, got %v", tt.name, tt.want, got)
		}
	}
}

func TestRunDestroyReturnsHelpfulUnsupportedError(t *testing.T) {
	vm := alchemy_build.VirtualMachineConfig{
		OS:                   "windows11",
		Arch:                 "amd64",
		HostOs:               alchemy_build.HostOsWindows,
		VirtualizationEngine: alchemy_build.VirtualizationEngineVirtualBox,
	}

	err := RunDestroy(vm)
	if err == nil {
		t.Fatal("expected unsupported destroy to return an error")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("expected not implemented error, got %v", err)
	}
}

func TestDestroyTargetExistsTartWithoutTartExecutable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	vm := alchemy_build.VirtualMachineConfig{
		OS:                   "macos",
		Arch:                 "arm64",
		HostOs:               alchemy_build.HostOsDarwin,
		VirtualizationEngine: alchemy_build.VirtualizationEngineTart,
	}
	exists, err := DestroyTargetExists(vm)
	if err != nil || exists {
		t.Fatalf("host without tart must report the target as absent, got exists=%v err=%v", exists, err)
	}
}

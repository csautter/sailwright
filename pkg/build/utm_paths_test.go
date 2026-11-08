package build

import (
	"path/filepath"
	"testing"
)

func TestUtmVirtualMachinePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	defaultDir := filepath.Join(home, "Library", "Containers", "com.utmapp.UTM", "Data", "Documents")
	override := filepath.Join(t.TempDir(), "UTM bundles with spaces")
	relative, err := filepath.Abs("relative bundles")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, value, want string }{
		{"empty uses legacy default", "", defaultDir},
		{"absolute override", override, override},
		{"relative override", "relative bundles", relative},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SAILWRIGHT_UTM_VM_DIR", test.value)
			got, err := UtmVirtualMachinePath(VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "arm64"})
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(test.want, "ubuntu-server-arm64-dev-alchemy.utm")
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

func TestUtmVirtualMachinePathForNamedInstance(t *testing.T) {
	override := t.TempDir()
	t.Setenv("SAILWRIGHT_UTM_VM_DIR", override)
	got, err := UtmVirtualMachinePath(VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "arm64", Name: "work", VirtualizationEngine: VirtualizationEngineUtm})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(override, "ubuntu-server-arm64-work-dev-alchemy.utm"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

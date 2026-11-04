package provision

import (
	"os"
	"path/filepath"
	"testing"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

func TestGuestCredentialSecurityMatchesProvisioning(t *testing.T) {
	dirs := alchemy_build.GetDirectoriesInstance()
	old := dirs.ProjectDir
	t.Cleanup(func() { dirs.ProjectDir = old })
	dirs.ProjectDir = t.TempDir()
	for _, tc := range []struct {
		os     string
		engine alchemy_build.VirtualizationEngine
		prefix string
	}{
		{"windows11", alchemy_build.VirtualizationEngineHyperv, "HYPERV_WINDOWS_"},
		{"ubuntu", alchemy_build.VirtualizationEngineHyperv, "HYPERV_UBUNTU_"},
		{"windows11", alchemy_build.VirtualizationEngineUtm, "UTM_WINDOWS_"},
		{"ubuntu", alchemy_build.VirtualizationEngineUtm, "UTM_UBUNTU_"},
		{"windows11", alchemy_build.VirtualizationEngineQemu, "LIBVIRT_WINDOWS_"},
		{"ubuntu", alchemy_build.VirtualizationEngineQemu, "LIBVIRT_UBUNTU_"},
		{"macos", alchemy_build.VirtualizationEngineTart, "TART_MACOS_"},
	} {
		t.Run(tc.prefix, func(t *testing.T) {
			vm := alchemy_build.VirtualMachineConfig{OS: tc.os, VirtualizationEngine: tc.engine}
			t.Setenv(tc.prefix+"ANSIBLE_USER", "")
			t.Setenv(tc.prefix+"ANSIBLE_PASSWORD", "")
			if err := os.WriteFile(filepath.Join(dirs.ProjectDir, ".env"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if origin, private, err := GuestCredentialSecurity(vm); err != nil || origin != "legacy" || private {
				t.Fatalf("legacy: %s %t %v", origin, private, err)
			}
			if err := os.WriteFile(filepath.Join(dirs.ProjectDir, ".env"), []byte(tc.prefix+"ANSIBLE_PASSWORD=Unique-Guest-Passphrase\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if origin, private, err := GuestCredentialSecurity(vm); err != nil || origin != "user" || !private {
				t.Fatalf("dotenv: %s %t %v", origin, private, err)
			}
			t.Setenv(tc.prefix+"ANSIBLE_PASSWORD", "admin")
			if origin, private, err := GuestCredentialSecurity(vm); err != nil || origin != "user" || private {
				t.Fatalf("env precedence: %s %t %v", origin, private, err)
			}
			t.Setenv(tc.prefix+"ANSIBLE_PASSWORD", "Another-Private-Passphrase")
			if origin, private, err := GuestCredentialSecurity(vm); err != nil || origin != "user" || !private {
				t.Fatalf("env: %s %t %v", origin, private, err)
			}
		})
	}
}

package build

import (
	"path/filepath"
	"testing"
)

var ubuntuQemuUserDataPaths = []string{
	"build/packer/linux/ubuntu/cloud-init/qemu-server/user-data",
	"build/packer/linux/ubuntu/cloud-init/qemu-desktop/user-data",
}

func TestUbuntuQemuCloudInitConfiguresPersistentDHCPNetworking(t *testing.T) {
	t.Parallel()

	const explicitDHCPNetwork = `  network:
    version: 2
    ethernets:
      default:
        dhcp4: true
        dhcp6: true
        match:
          name: e*`

	for _, userDataPath := range ubuntuQemuUserDataPaths {
		t.Run(filepath.Base(filepath.Dir(userDataPath)), func(t *testing.T) {
			t.Parallel()

			assertContainsAll(t, userDataPath, readRepoFile(t, userDataPath), explicitDHCPNetwork)
		})
	}
}

func TestUbuntuQemuCloudInitExtendsInstallerBusctlTimeout(t *testing.T) {
	t.Parallel()

	for _, userDataPath := range ubuntuQemuUserDataPaths {
		t.Run(filepath.Base(filepath.Dir(userDataPath)), func(t *testing.T) {
			t.Parallel()

			assertContainsAll(t, userDataPath, readRepoFile(t, userDataPath),
				"early-commands:",
				"/usr/bin/busctl.dev-alchemy-original --timeout=120",
			)
		})
	}
}

func TestWindowsAmd64AutounattendInstallsQxlDriverFromVirtioIso(t *testing.T) {
	t.Parallel()

	autounattendPath := "build/packer/windows/qemu-amd64/autounattend.xml"
	assertContainsAll(t, autounattendPath, readRepoFile(t, autounattendPath),
		`C:\QXLDriverInstall.log`,
		`Starting QXL DOD driver install/stage during first logon.`,
		`qxldod\w10\amd64\qxldod.inf`,
		`pnputil.exe /add-driver`,
	)
}

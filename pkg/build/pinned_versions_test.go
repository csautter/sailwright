package build

import (
	"path/filepath"
	"regexp"
	"testing"
)

// These tests keep the version pins in dependencies.go in sync with the
// scripts, templates, workflows and docs that repeat them.

func TestUbuntuLiveServerPinsStayInSync(t *testing.T) {
	t.Parallel()

	for _, scriptPath := range []string{ubuntuQemuScriptPath, ubuntuMacOSQemuScriptPath} {
		t.Run(filepath.Base(scriptPath), func(t *testing.T) {
			t.Parallel()

			assertContainsAll(t, scriptPath, readRepoFile(t, scriptPath),
				`UBUNTU_LIVE_SERVER_AMD64_VERSION="`+ubuntuLiveServerAMD64Version+`"`,
				`UBUNTU_LIVE_SERVER_AMD64_SHA256="`+ubuntuLiveServerAMD64SHA256+`"`,
				`UBUNTU_LIVE_SERVER_ARM64_VERSION="`+ubuntuLiveServerArm64Version+`"`,
				`UBUNTU_LIVE_SERVER_ARM64_SHA256="`+ubuntuLiveServerArm64SHA256+`"`,
				`-var "iso_checksum=sha256:$iso_checksum"`,
			)
		})
	}

	assertContainsAll(t, ubuntuHypervTemplatePath, readRepoFile(t, ubuntuHypervTemplatePath),
		`default = "`+ubuntuLiveServerAMD64Version+`"`,
	)

	for _, filePath := range []string{
		".github/workflows/test-build-linux.yml",
		".github/workflows/test-build-macos.yml",
		"build/packer/linux/ubuntu/README.md",
	} {
		t.Run(filepath.Base(filePath)+" static ISO references", func(t *testing.T) {
			t.Parallel()
			assertUbuntuLiveServerISOReferencesUsePinnedVersions(t, filePath)
		})
	}
}

func TestVirtioWinPinsStayInSync(t *testing.T) {
	t.Parallel()

	scriptPath := "scripts/macos/download-virtio-win-iso.sh"
	assertContainsAll(t, scriptPath, readRepoFile(t, scriptPath),
		`VIRTIO_WIN_VERSION="`+virtioWinVersion+`"`,
		`VIRTIO_WIN_SHA256="`+virtioWinSHA256+`"`,
		`verify_sha256 "$virtio_iso_path"`,
		`verify_sha256 "$tmp_path"`,
		`sha256sum -c -`,
		`shasum -a 256`,
	)
}

var ubuntuLiveServerISOReferenceRE = regexp.MustCompile(`ubuntu-([0-9]{2}\.[0-9]{2}(?:\.[0-9]+)?)-live-server-(amd64|arm64)\.iso`)

func assertUbuntuLiveServerISOReferencesUsePinnedVersions(t *testing.T, filePath string) {
	t.Helper()

	matches := ubuntuLiveServerISOReferenceRE.FindAllStringSubmatch(readRepoFile(t, filePath), -1)
	if len(matches) == 0 {
		t.Fatalf("expected %q to contain Ubuntu live-server ISO references", filePath)
	}

	for _, match := range matches {
		version, arch := match[1], match[2]
		wantVersion := ubuntuLiveServerAMD64Version
		if arch == "arm64" {
			wantVersion = ubuntuLiveServerArm64Version
		}
		if version != wantVersion {
			t.Errorf("expected %q to use Ubuntu %s for %s ISO reference, found %q", filePath, wantVersion, arch, match[0])
		}
	}
}

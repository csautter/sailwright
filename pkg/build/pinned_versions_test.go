package build

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
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
		`virtio_iso_file_name="virtio-win-${VIRTIO_WIN_VERSION}.iso"`,
		`virtio_iso_path="$CACHE_DIR/windows/$virtio_iso_file_name"`,
		`verify_sha256 "$virtio_iso_path"`,
		`verify_sha256 "$tmp_path"`,
		`sha256sum -c -`,
		`shasum -a 256`,
	)
}

// TestVirtioWinCacheFileNamesFollowPin keeps the versioned virtio-win cache file name, and with it the
// CI build-cache key, in step with the pinned version.
func TestVirtioWinCacheFileNamesFollowPin(t *testing.T) {
	t.Parallel()

	wantName := virtioWinISOCacheFileName(virtioWinVersion)
	if wantName != "virtio-win-"+virtioWinVersion+".iso" {
		t.Fatalf("unexpected virtio-win cache file name %q", wantName)
	}

	templatePath := "build/packer/windows/windows11-qemu.pkr.hcl"
	assertContainsAll(t, templatePath, readRepoFile(t, templatePath),
		`win11_virtio_iso  = "${local.cache_directory}/windows/${var.virtio_win_iso_name}"`,
	)
	buildScriptPath := "build/packer/windows/windows11-qemu.sh"
	assertContainsAll(t, buildScriptPath, readRepoFile(t, buildScriptPath),
		`virtio_win_iso_name="$(bash "${project_root}/scripts/macos/download-virtio-win-iso.sh" --print-file-name)"`,
		`-var "virtio_win_iso_name=${virtio_win_iso_name}"`,
	)

	for _, workflowPath := range []string{
		".github/workflows/test-build-linux.yml",
		".github/workflows/test-build-macos.yml",
	} {
		content := readRepoFile(t, workflowPath)
		if strings.Contains(content, "cache/windows/"+virtioWinLegacyISOFileName) {
			t.Errorf("%s caches the virtio-win ISO under the unversioned name %s", workflowPath, virtioWinLegacyISOFileName)
		}
		matches := virtioWinISOReferenceRE.FindAllString(content, -1)
		if len(matches) == 0 {
			t.Errorf("%s does not cache the virtio-win ISO", workflowPath)
		}
		for _, match := range matches {
			if match != wantName {
				t.Errorf("%s caches %s; want %s to match virtioWinVersion", workflowPath, match, wantName)
			}
		}
		assertRenovateUpdatesCacheReferences(t, workflowPath, content, len(matches), "virtio-win", "custom.virtio-win", virtioWinVersion)
	}
}

// TestVirtioWinDownloadScriptPrintsCacheFileName runs the download script in --print-file-name mode,
// which windows11-qemu.sh uses to pass the cache file name to Packer. It must print the Go cache file
// name and nothing else, without touching the network.
func TestVirtioWinDownloadScriptPrintsCacheFileName(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("the shell build scripts run on Linux and macOS hosts only")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	cmd := exec.Command(bash, repoPath(t, "scripts/macos/download-virtio-win-iso.sh"), "--print-file-name")
	cmd.Env = append(cmd.Environ(), "DEV_ALCHEMY_CACHE_DIR="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("download-virtio-win-iso.sh --print-file-name failed: %v\n%s", err, out)
	}
	if got, want := string(out), virtioWinISOCacheFileName(virtioWinVersion)+"\n"; got != want {
		t.Fatalf("download-virtio-win-iso.sh --print-file-name printed %q, want %q", got, want)
	}
}

var virtioWinISOReferenceRE = regexp.MustCompile(`virtio-win-[0-9][0-9.]*-[0-9]+\.iso`)

// assertRenovateUpdatesCacheReferences checks that a Renovate manager covers the workflow and
// captures every versioned reference, so updates change the remote cache keys too.
func assertRenovateUpdatesCacheReferences(t *testing.T, path, content string, want int, depName, datasource, version string) {
	t.Helper()

	var config struct {
		CustomManagers []struct {
			ManagerFilePatterns []string `json:"managerFilePatterns"`
			MatchStrings        []string `json:"matchStrings"`
			DepNameTemplate     string   `json:"depNameTemplate"`
			DatasourceTemplate  string   `json:"datasourceTemplate"`
		} `json:"customManagers"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, "renovate.json")), &config); err != nil {
		t.Fatalf("failed to parse renovate.json: %v", err)
	}

	for _, manager := range config.CustomManagers {
		if manager.DepNameTemplate != depName || manager.DatasourceTemplate != datasource {
			continue
		}
		coversFile := false
		for _, filePattern := range manager.ManagerFilePatterns {
			re, err := regexp.Compile(strings.TrimSuffix(strings.TrimPrefix(filePattern, "/"), "/"))
			if err == nil && re.MatchString(path) {
				coversFile = true
			}
		}
		if !coversFile {
			continue
		}
		for _, matchString := range manager.MatchStrings {
			re, err := regexp.Compile(matchString)
			if err != nil {
				t.Fatalf("invalid Renovate matchString %q: %v", matchString, err)
			}
			matches := re.FindAllStringSubmatch(content, -1)
			if len(matches) != want {
				t.Errorf("%s: Renovate matchString %q finds %d %s references, want %d", path, matchString, len(matches), depName, want)
			}
			for _, match := range matches {
				if got := match[re.SubexpIndex("currentValue")]; got != version {
					t.Errorf("%s: Renovate captures %s version %q, want %q", path, depName, got, version)
				}
			}
			return
		}
	}
	t.Errorf("%s: no Renovate regex manager updates the %s cache file names", path, depName)
}

func TestFirmwareAndGuestToolsCacheNamesFollowPins(t *testing.T) {
	t.Parallel()

	assertContainsAll(t, "scripts/macos/download-utm-guest-tools.sh", readRepoFile(t, "scripts/macos/download-utm-guest-tools.sh"),
		`UTM_GUEST_TOOLS_VERSION="`+utmGuestToolsVersion+`"`,
		`UTM_GUEST_TOOLS_RELEASE="`+utmGuestToolsRelease+`"`,
		`UTM_GUEST_TOOLS_SHA256="`+utmGuestToolsSHA256+`"`,
	)
	assertContainsAll(t, windowsQemuTemplatePath, readRepoFile(t, windowsQemuTemplatePath),
		`win11_guest_tools = "${local.cache_directory}/utm/${var.utm_guest_tools_iso_name}"`,
	)
	assertContainsAll(t, windowsQemuScriptPath, readRepoFile(t, windowsQemuScriptPath),
		`utm_guest_tools_iso_name="$(bash "${project_root}/scripts/macos/download-utm-guest-tools.sh" --print-file-name)"`,
		`-var "utm_guest_tools_iso_name=${utm_guest_tools_iso_name}"`,
	)

	for _, tc := range []struct {
		script, name, legacy, pattern string
	}{
		{"download-arm64-uefi.sh", qemuEfiAarch64CacheFileName(qemuEfiAarch64Version), "qemu-efi-aarch64_all.deb", `qemu-efi-aarch64_[0-9A-Za-z.+~-]+_all\.deb`},
		{"download-utm-guest-tools.sh", utmGuestToolsISOFileName(utmGuestToolsVersion), "utm-guest-tools-latest.iso", `utm-guest-tools-[0-9]+\.[0-9]+\.[0-9]+\.iso`},
	} {
		t.Run(tc.script, func(t *testing.T) {
			if runtime.GOOS != "windows" {
				cmd := exec.Command("bash", repoPath(t, "scripts/macos/"+tc.script), "--print-file-name")
				cacheDir := filepath.Join(t.TempDir(), "cache")
				cmd.Env = append(cmd.Environ(), "DEV_ALCHEMY_CACHE_DIR="+cacheDir)
				out, err := cmd.CombinedOutput()
				if err != nil || string(out) != tc.name+"\n" {
					t.Fatalf("--print-file-name: got %q, %v; want %q", out, err, tc.name+"\n")
				}
				assertFilesMissing(t, cacheDir)
			}
			for _, path := range []string{".github/workflows/test-build-linux.yml", ".github/workflows/test-build-macos.yml"} {
				content := readRepoFile(t, path)
				if strings.Contains(content, tc.legacy) {
					t.Errorf("%s still caches %s", path, tc.legacy)
				}
				matches := regexp.MustCompile(tc.pattern).FindAllString(content, -1)
				if len(matches) == 0 {
					t.Fatalf("%s has no cache references for %s", path, tc.script)
				}
				for _, name := range matches {
					if name != tc.name {
						t.Errorf("%s caches %s; want %s", path, name, tc.name)
					}
				}
				if tc.script == "download-arm64-uefi.sh" {
					assertRenovateUpdatesCacheReferences(t, path, content, len(matches), "qemu-efi-aarch64", "deb", qemuEfiAarch64Version)
				}
			}
		})
	}
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

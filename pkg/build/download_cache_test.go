package build

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUTMGuestToolsDependencyIsPinned(t *testing.T) {
	for _, config := range []VirtualMachineConfig{
		{OS: "windows11", Arch: "amd64", HostOs: HostOsDarwin, VirtualizationEngine: VirtualizationEngineUtm},
		{OS: "windows11", Arch: "arm64", HostOs: HostOsDarwin, VirtualizationEngine: VirtualizationEngineUtm},
		{OS: "windows11", Arch: "amd64", HostOs: HostOsLinux, VirtualizationEngine: VirtualizationEngineQemu},
		{OS: "windows11", Arch: "arm64", HostOs: HostOsLinux, VirtualizationEngine: VirtualizationEngineQemu},
	} {
		found := false
		for _, dep := range webFileDependenciesForVMConfig(config) {
			if dep.LocalPath != utmGuestToolsISOPath() {
				continue
			}
			found = true
			if dep.Checksum != "sha256:"+utmGuestToolsSHA256 || dep.Source != utmGuestToolsISOURL() || dep.PrepareHook == nil {
				t.Errorf("%+v: UTM Guest Tools must use its pinned URL, checksum and cache preparation", config)
			}
		}
		if !found {
			t.Errorf("%+v: UTM Guest Tools dependency missing", config)
		}
	}
}

func TestFirmwareAndGuestToolsPrepareCache(t *testing.T) {
	dirs := GetDirectoriesInstance()
	original := dirs.CacheDir
	dirs.CacheDir = t.TempDir()
	t.Cleanup(func() { dirs.CacheDir = original })

	for _, dep := range getWebFileDependencies() {
		var legacyName, oldName string
		switch filepath.Base(dep.LocalPath) {
		case qemuEfiAarch64CacheFileName(qemuEfiAarch64Version):
			legacyName, oldName = "qemu-efi-aarch64_all.deb", "qemu-efi-aarch64_2024.11-1_all.deb"
		case utmGuestToolsISOFileName(utmGuestToolsVersion):
			legacyName, oldName = "utm-guest-tools-latest.iso", "utm-guest-tools-0.1.100.iso"
		default:
			continue
		}
		dir := filepath.Dir(dep.LocalPath)
		legacy, old := filepath.Join(dir, legacyName), filepath.Join(dir, oldName)
		writeDownloadCacheTestFile(t, legacy, []byte("stale legacy"))
		writeDownloadCacheTestFile(t, old, []byte("old version"))
		writeDownloadCacheTestFile(t, dep.LocalPath, []byte("current version"))
		writeDownloadCacheTestFile(t, dep.LocalPath+".partial", []byte("in progress"))
		dep.PrepareHook()
		assertFilesMissing(t, legacy, old)
		assertFileContent(t, dep.LocalPath, []byte("current version"))
		assertFileContent(t, dep.LocalPath+".partial", []byte("in progress"))
	}
}

// Exercise the real shell scripts with a small payload and a curl stub. Seed firmware extraction
// outputs so these cases test cache/download behavior without needing a real Debian package.
func TestFirmwareAndGuestToolsDownloadCaches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell downloaders run on Linux and macOS")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	content := []byte("verified download")
	checksum := fmt.Sprintf("%x", sha256.Sum256(content))
	for _, file := range []struct {
		script, subdir, name, legacy, old, pin string
	}{
		{"download-arm64-uefi.sh", "", qemuEfiAarch64CacheFileName(qemuEfiAarch64Version), "qemu-efi-aarch64_all.deb", "qemu-efi-aarch64_2024.11-1_all.deb", qemuEfiAarch64SHA256},
		{"download-utm-guest-tools.sh", "utm", utmGuestToolsISOFileName(utmGuestToolsVersion), "utm-guest-tools-latest.iso", "utm-guest-tools-0.1.100.iso", utmGuestToolsSHA256},
	} {
		for _, scenario := range []string{"matching legacy", "stale legacy", "warm cache", "corrupt current", "HTTP failure", "checksum failure"} {
			t.Run(file.script+"/"+scenario, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				cacheDir, binDir := filepath.Join(root, "cache"), filepath.Join(root, "bin")
				dest := filepath.Join(cacheDir, file.subdir, file.name)
				legacy, old := filepath.Join(cacheDir, file.subdir, file.legacy), filepath.Join(cacheDir, file.subdir, file.old)
				writeDownloadCacheTestFile(t, old, []byte("old version"))
				switch scenario {
				case "matching legacy":
					writeDownloadCacheTestFile(t, legacy, content)
				case "stale legacy":
					writeDownloadCacheTestFile(t, legacy, []byte("stale"))
				case "warm cache":
					writeDownloadCacheTestFile(t, dest, content)
				case "corrupt current":
					writeDownloadCacheTestFile(t, dest, []byte("corrupt"))
				}
				for _, name := range []string{"usr/share/qemu-efi-aarch64/QEMU_EFI.fd", "usr/share/AAVMF/AAVMF_CODE.no-secboot.fd", "usr/share/AAVMF/AAVMF_VARS.fd"} {
					writeDownloadCacheTestFile(t, filepath.Join(cacheDir, "qemu-uefi", name), []byte("firmware"))
				}
				writeDownloadCacheTestFile(t, filepath.Join(cacheDir, "qemu-uefi", ".qemu-efi-aarch64.sha256"), []byte(checksum+"\n"))
				script := strings.ReplaceAll(readRepoFile(t, "scripts/macos/"+file.script), file.pin, checksum)
				scriptPath := filepath.Join(root, file.script)
				writeDownloadCacheTestFile(t, scriptPath, []byte(script))
				stub := `#!/bin/bash
set -eu
echo "$*" >> "$CURL_LOG"
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then output="$2"; shift; fi
  shift
done
printf 'verified download' > "$output"
case "$DOWNLOAD_SCENARIO" in
  'HTTP failure') exit 22 ;;
  'checksum failure') printf 'bad download' > "$output" ;;
esac
`
				stubPath := filepath.Join(binDir, "curl")
				writeDownloadCacheTestFile(t, stubPath, []byte(stub))
				if err := os.Chmod(stubPath, 0o755); err != nil {
					t.Fatal(err)
				}
				logPath := filepath.Join(root, "curl.log")
				cmd := exec.Command(bash, scriptPath)
				cmd.Env = append(cmd.Environ(), "DEV_ALCHEMY_CACHE_DIR="+cacheDir, "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"), "CURL_LOG="+logPath, "DOWNLOAD_SCENARIO="+scenario)
				out, err := cmd.CombinedOutput()
				if strings.HasSuffix(scenario, "failure") {
					if err == nil {
						t.Fatalf("download should fail: %s", out)
					}
					assertFilesMissing(t, dest)
				} else {
					if err != nil {
						t.Fatalf("download failed: %v\n%s", err, out)
					}
					assertFileContent(t, dest, content)
				}
				assertFilesMissing(t, old, legacy)
				if scenario == "matching legacy" || scenario == "warm cache" {
					assertFilesMissing(t, logPath)
				} else if _, err := os.Stat(logPath); err != nil {
					t.Fatalf("expected a download attempt: %v", err)
				}
				partials, err := filepath.Glob(dest + ".*")
				if err != nil || len(partials) != 0 {
					t.Fatalf("temporary downloads left behind: %v, %v", partials, err)
				}
			})
		}
	}
}

func writeDownloadCacheTestFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestBytes(t, path, content)
}

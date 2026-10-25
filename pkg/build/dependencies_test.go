package build

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBootstrapPythonEnv_VenvCreationFails verifies that bootstrapPythonEnv returns
// a wrapped error (not nil) when the python executable does not exist and the venv
// directory is absent, so callers can fail fast with an actionable message.
func TestBootstrapPythonEnv_VenvCreationFails(t *testing.T) {
	workdir := t.TempDir()
	// Point at a path that is guaranteed not to exist.
	badPython := filepath.Join(t.TempDir(), "nonexistent-python")

	err := bootstrapPythonEnv(workdir, badPython)

	if err == nil {
		t.Fatal("expected error when Python executable does not exist, got nil")
	}
	if !strings.Contains(err.Error(), "failed to create Python venv") {
		t.Errorf("expected error to mention venv creation failure, got: %v", err)
	}
}

// TestBootstrapPythonEnv_IncompleteVenvRecreateFails verifies that an existing
// but incomplete venv is recreated before dependency installation begins.
func TestBootstrapPythonEnv_IncompleteVenvRecreateFails(t *testing.T) {
	workdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workdir, ".venv"), 0755); err != nil {
		t.Fatalf("could not create mock venv dir: %v", err)
	}
	badPython := filepath.Join(t.TempDir(), "nonexistent-python")

	err := bootstrapPythonEnv(workdir, badPython)

	if err == nil {
		t.Fatal("expected error when incomplete venv cannot be recreated, got nil")
	}
	if !strings.Contains(err.Error(), "failed to recreate Python venv") {
		t.Errorf("expected error to mention venv recreation failure, got: %v", err)
	}
}

func TestMissingPythonVenvExecutables(t *testing.T) {
	workdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workdir, ".venv"), 0755); err != nil {
		t.Fatalf("could not create mock venv dir: %v", err)
	}

	missing, err := missingPythonVenvExecutables(workdir)
	if err != nil {
		t.Fatalf("missingPythonVenvExecutables returned error: %v", err)
	}
	if len(missing) != 2 {
		t.Fatalf("expected empty venv to miss 2 executables, got %d: %v", len(missing), missing)
	}

	venvPython, pipPath := pythonVenvExecutablePaths(workdir)
	for _, path := range []string{venvPython, pipPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("could not create parent directory for %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte{}, 0755); err != nil {
			t.Fatalf("could not create fake executable %s: %v", path, err)
		}
	}

	missing, err = missingPythonVenvExecutables(workdir)
	if err != nil {
		t.Fatalf("missingPythonVenvExecutables returned error after fake executables were created: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("expected complete venv to miss no executables, got: %v", missing)
	}
}

func TestResolvePythonVenvCommandPrefersPython3OnUnix(t *testing.T) {
	command, err := resolvePythonVenvCommandForOS("linux", func(name string) (string, error) {
		if name == "python3" {
			return "/usr/bin/python3", nil
		}
		return "", fmt.Errorf("%s not found", name)
	})
	if err != nil {
		t.Fatalf("resolvePythonVenvCommandForOS returned error: %v", err)
	}
	if command.executable != "/usr/bin/python3" {
		t.Fatalf("expected python3 path, got %q", command.executable)
	}
	if len(command.argsPrefix) != 0 {
		t.Fatalf("expected no args prefix for python3, got %v", command.argsPrefix)
	}
}

func TestResolvePythonVenvCommandFallsBackToPyLauncherOnWindows(t *testing.T) {
	command, err := resolvePythonVenvCommandForOS("windows", func(name string) (string, error) {
		if name == "py" {
			return `C:\Windows\py.exe`, nil
		}
		return "", fmt.Errorf("%s not found", name)
	})
	if err != nil {
		t.Fatalf("resolvePythonVenvCommandForOS returned error: %v", err)
	}
	if command.executable != `C:\Windows\py.exe` {
		t.Fatalf("expected py launcher path, got %q", command.executable)
	}
	if got := strings.Join(command.argsPrefix, " "); got != "-3" {
		t.Fatalf("expected py launcher args prefix -3, got %q", got)
	}
}

func TestResolvePythonVenvCommandReportsCheckedCandidates(t *testing.T) {
	_, err := resolvePythonVenvCommandForOS("windows", func(name string) (string, error) {
		return "", fmt.Errorf("%s not found", name)
	})
	if err == nil {
		t.Fatal("expected error when no Python command is found, got nil")
	}
	for _, want := range []string{"python", "py -3", "python3"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error %q to mention checked candidate %q", err.Error(), want)
		}
	}
}

func TestWindows11Amd64LinuxQemuDependencyReconciliationIncludesVirtioWinISO(t *testing.T) {
	dirs := GetDirectoriesInstance()
	originalCacheDir := dirs.CacheDir
	dirs.CacheDir = t.TempDir()
	defer func() {
		dirs.CacheDir = originalCacheDir
	}()

	vmconfig := VirtualMachineConfig{
		OS:                   "windows11",
		Arch:                 "amd64",
		HostOs:               HostOsLinux,
		VirtualizationEngine: VirtualizationEngineQemu,
	}

	deps := webFileDependenciesForVMConfig(vmconfig)
	wantPath := filepath.Join(dirs.CacheDir, "windows", "virtio-win.iso")
	wantChecksum := "sha256:" + virtioWinSHA256
	gotPaths := make([]string, 0, len(deps))
	for _, dep := range deps {
		gotPaths = append(gotPaths, dep.LocalPath)
		if dep.LocalPath == wantPath {
			if dep.Checksum != wantChecksum {
				t.Fatalf("expected virtio-win dependency checksum %q, got %q", wantChecksum, dep.Checksum)
			}
			if len(dep.FallbackSources) != 2 {
				t.Fatalf("expected virtio-win dependency to include 2 fallback sources, got %d: %v", len(dep.FallbackSources), dep.FallbackSources)
			}
			return
		}
	}

	t.Fatalf("expected dependency reconciliation for Windows 11 amd64 Linux QEMU to include %s, got %v", wantPath, gotPaths)
}

func TestIntegrationDependencyReconciliation(t *testing.T) {
	if os.Getenv("RUN_INTEGRATION_TESTS") == "" {
		t.Skip("skipping integration test; set RUN_INTEGRATION_TESTS=1 to run")
	}
	tests := []VirtualMachineConfig{
		{
			OS:   "windows11",
			Arch: "amd64",
		},
		{
			OS:   "windows11",
			Arch: "arm64",
		},
		{
			OS:         "ubuntu",
			Arch:       "amd64",
			UbuntuType: "desktop",
		},
		{
			OS:         "ubuntu",
			Arch:       "arm64",
			UbuntuType: "desktop",
		},
		{
			OS:         "ubuntu",
			Arch:       "amd64",
			UbuntuType: "server",
		},
		{
			OS:         "ubuntu",
			Arch:       "arm64",
			UbuntuType: "server",
		},
	}

	for _, vmconfig := range tests {
		DependencyReconciliation(vmconfig)
	}
}

// TestIntegrationBootstrapWindows11DownloadEnv covers the cold start of the Playwright environment
// (venv, pip, Chromium) without contacting Microsoft.
func TestIntegrationBootstrapWindows11DownloadEnv(t *testing.T) {
	if os.Getenv("RUN_INTEGRATION_TESTS") == "" {
		t.Skip("skipping integration test; set RUN_INTEGRATION_TESTS=1 to run")
	}
	pythonCommand, err := resolvePythonVenvCommand()
	if err != nil {
		t.Fatalf("Failed to resolve Python: %v", err)
	}
	workdir := filepath.Join(GetDirectoriesInstance().ProjectDir, "scripts", "macos")
	if err := bootstrapPythonEnvWithCommand(workdir, pythonCommand); err != nil {
		t.Fatalf("Failed to bootstrap the Windows 11 download environment: %v", err)
	}
	if missing, err := missingPythonVenvExecutables(workdir); err != nil || len(missing) > 0 {
		t.Fatalf("venv executables missing after bootstrap: %v (err=%v)", missing, err)
	}
}

func TestGetWindows11DownloadAmd64(t *testing.T) {
	if os.Getenv("RUN_INTEGRATION_TESTS") == "" {
		t.Skip("skipping integration test; set RUN_INTEGRATION_TESTS=1 to run")
	}
	_, err := getWindows11Download("amd64", windows11ISOPath("amd64"), false)
	if errors.Is(err, ErrMicrosoftRefused) {
		t.Skipf("Microsoft refused the request: %v", err)
	}
	if err != nil {
		t.Fatalf("Failed to get Windows 11 download: %v", err)
	}
}

func TestGetWindows11DownloadArm64(t *testing.T) {
	if os.Getenv("RUN_INTEGRATION_TESTS") == "" {
		t.Skip("skipping integration test; set RUN_INTEGRATION_TESTS=1 to run")
	}
	_, err := getWindows11Download("arm64", windows11ISOPath("arm64"), false)
	if errors.Is(err, ErrMicrosoftRefused) {
		t.Skipf("Microsoft refused the request: %v", err)
	}
	if err != nil {
		t.Fatalf("Failed to get Windows 11 download: %v", err)
	}
}

func TestResolveDebianPackageURL(t *testing.T) {
	if os.Getenv("RUN_INTEGRATION_TESTS") == "" {
		t.Skip("skipping integration test; set RUN_INTEGRATION_TESTS=1 to run")
	}
	url, err := resolveDebianPackageURL("trixie", "qemu-efi-aarch64")
	if err != nil {
		t.Fatalf("resolveDebianPackageURL returned error: %v", err)
	}
	if !strings.HasPrefix(url, "https://deb.debian.org/debian/pool/") {
		t.Errorf("unexpected URL prefix: %s", url)
	}
	if !strings.Contains(url, "qemu-efi-aarch64") {
		t.Errorf("URL does not contain package name: %s", url)
	}
	t.Logf("Resolved URL: %s", url)

	// Verify the resolved URL is actually reachable
	resp, err := http.Head(url)
	if err != nil {
		t.Fatalf("HTTP HEAD request to resolved URL failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("resolved URL returned HTTP %d: %s", resp.StatusCode, url)
	}
	t.Logf("HTTP HEAD status: %d", resp.StatusCode)
}

func TestResolveAndDownloadQemuEfiAarch64(t *testing.T) {
	if os.Getenv("RUN_INTEGRATION_TESTS") == "" {
		t.Skip("skipping integration test; set RUN_INTEGRATION_TESTS=1 to run")
	}
	url, err := resolveDebianPackageURL("trixie", "qemu-efi-aarch64")
	if err != nil {
		t.Fatalf("resolveDebianPackageURL returned error: %v", err)
	}
	t.Logf("Resolved URL: %s", url)

	destPath := filepath.Join(t.TempDir(), "qemu-efi-aarch64_all.deb")
	dep := WebFileDependency{
		LocalPath: destPath,
		Source:    url,
	}
	if err := downloadWebFileDependency(nil, dep); err != nil {
		t.Fatalf("downloadWebFileDependency failed: %v", err)
	}

	info, err := os.Stat(destPath)
	if err != nil {
		t.Fatalf("downloaded file not found at %s: %v", destPath, err)
	}
	if info.Size() == 0 {
		t.Errorf("downloaded file is empty: %s", destPath)
	}
	t.Logf("Downloaded %s (%d bytes)", destPath, info.Size())
}

func TestDownloadWebFileDependencyWithoutProgressBar(t *testing.T) {
	expected := []byte("test deb payload")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(expected)
	}))
	defer server.Close()

	destPath := filepath.Join(t.TempDir(), "downloaded.deb")
	dep := WebFileDependency{
		LocalPath: destPath,
		Source:    server.URL + "/qemu-efi-aarch64_all.deb",
	}

	if err := downloadWebFileDependency(nil, dep); err != nil {
		t.Fatalf("downloadWebFileDependency failed without progress bar: %v", err)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("failed to read downloaded file: %v", err)
	}
	if !bytes.Equal(got, expected) {
		t.Fatalf("downloaded file contents mismatch: got %q want %q", string(got), string(expected))
	}
}

func TestVirtioWinURLsUseFedoraOriginAndMirror(t *testing.T) {
	urls := virtioWinISOURLs("0.1.285-1")
	if len(urls) != 2 {
		t.Fatalf("expected 2 virtio-win URLs, got %d: %v", len(urls), urls)
	}

	wantOrigin := "https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/archive-virtio/virtio-win-0.1.285-1/virtio-win-0.1.285.iso"
	wantMirror := "https://fedora-virt.repo.nfrance.com/virtio-win/direct-downloads/archive-virtio/virtio-win-0.1.285-1/virtio-win-0.1.285.iso"
	if urls[0] != wantOrigin {
		t.Fatalf("unexpected Fedora origin URL: got %s want %s", urls[0], wantOrigin)
	}
	if urls[1] != wantMirror {
		t.Fatalf("unexpected mirror URL: got %s want %s", urls[1], wantMirror)
	}
}

func TestSelectFastestDownloadURL(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 1024)
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer fast.Close()

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		_, _ = w.Write(payload)
	}))
	defer slow.Close()

	fastURL := fast.URL + "/artifact.iso"
	slowURL := slow.URL + "/artifact.iso"
	selected, err := selectFastestDownloadURL([]string{slowURL, fastURL}, 128, 2*time.Second)
	if err != nil {
		t.Fatalf("selectFastestDownloadURL returned error: %v", err)
	}
	if selected != fastURL {
		t.Fatalf("selected URL mismatch: got %s want %s", selected, fastURL)
	}
}

func TestSelectFastestDownloadURLAcceptsPartialProbeAtDeadline(t *testing.T) {
	trickle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer trickle.Close()

	trickleURL := trickle.URL + "/artifact.iso"
	selected, err := selectFastestDownloadURL([]string{trickleURL}, 1024, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("expected a probe that read bytes before the deadline to count: %v", err)
	}
	if selected != trickleURL {
		t.Fatalf("selected URL mismatch: got %s want %s", selected, trickleURL)
	}
}

func TestSelectFastestDownloadURLOrDefaultFallsBackWhenAllProbesFail(t *testing.T) {
	hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer hanging.Close()

	primary := hanging.URL + "/primary.iso"
	mirror := hanging.URL + "/mirror.iso"
	selected := selectFastestDownloadURLOrDefault([]string{primary, mirror}, 1024, 100*time.Millisecond)
	if selected != primary {
		t.Fatalf("expected the first URL when every probe fails: got %s want %s", selected, primary)
	}
}

func TestDownloadWebFileDependencySelectsFastestURL(t *testing.T) {
	expected := bytes.Repeat([]byte("fast virtio payload"), 128)

	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(expected)
	}))
	defer fast.Close()

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		_, _ = w.Write([]byte("slow virtio payload"))
	}))
	defer slow.Close()

	fastURL := fast.URL + "/artifact.iso"
	slowURL := slow.URL + "/artifact.iso"
	destPath := filepath.Join(t.TempDir(), "artifact.iso")
	dep := WebFileDependency{
		LocalPath: destPath,
		Source:    slowURL,
		BeforeHook: func() (string, error) {
			return selectFastestDownloadURL([]string{slowURL, fastURL}, 128, 2*time.Second)
		},
	}

	if err := downloadWebFileDependency(nil, dep); err != nil {
		t.Fatalf("downloadWebFileDependency returned error: %v", err)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("failed to read downloaded file: %v", err)
	}
	if !bytes.Equal(got, expected) {
		t.Fatalf("downloaded file contents mismatch: got %q want %q", string(got), string(expected))
	}
}

func TestDownloadWebFileDependencyRetriesFallbackAfterChecksumMismatch(t *testing.T) {
	expected := []byte("expected virtio payload")
	checksum := sha256.Sum256(expected)

	webFileRetryDelay = 0
	t.Cleanup(func() { webFileRetryDelay = 5 * time.Second })

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("unexpected virtio payload"))
	}))
	defer bad.Close()

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(expected)
	}))
	defer good.Close()

	destPath := filepath.Join(t.TempDir(), "artifact.iso")
	dep := WebFileDependency{
		LocalPath:       destPath,
		Source:          bad.URL + "/artifact.iso",
		FallbackSources: []string{good.URL + "/artifact.iso"},
		Checksum:        fmt.Sprintf("sha256:%x", checksum),
	}

	if err := downloadWebFileDependency(nil, dep); err != nil {
		t.Fatalf("downloadWebFileDependency returned error after fallback source: %v", err)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("failed to read downloaded file: %v", err)
	}
	if !bytes.Equal(got, expected) {
		t.Fatalf("downloaded file contents mismatch: got %q want %q", string(got), string(expected))
	}
}

func TestDownloadWebFileDependencyRetriesCorruptedDownload(t *testing.T) {
	webFileRetryDelay = 0
	t.Cleanup(func() { webFileRetryDelay = 5 * time.Second })

	expected := []byte("expected virtio payload")
	checksum := sha256.Sum256(expected)

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			_, _ = w.Write([]byte("corrupted"))
			return
		}
		_, _ = w.Write(expected)
	}))
	defer server.Close()

	dep := WebFileDependency{
		LocalPath: filepath.Join(t.TempDir(), "artifact.iso"),
		Source:    server.URL + "/artifact.iso",
		Checksum:  fmt.Sprintf("sha256:%x", checksum),
	}
	if err := downloadWebFileDependency(nil, dep); err != nil {
		t.Fatalf("expected retry to recover from a corrupted download: %v", err)
	}
	if requests != 2 {
		t.Fatalf("expected 2 requests, got %d", requests)
	}
}

func TestDownloadWebFileDependencyRejectsChecksumMismatch(t *testing.T) {
	webFileRetryDelay = 0
	t.Cleanup(func() { webFileRetryDelay = 5 * time.Second })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("unexpected payload"))
	}))
	defer server.Close()

	destPath := filepath.Join(t.TempDir(), "artifact.iso")
	dep := WebFileDependency{
		LocalPath: destPath,
		Source:    server.URL + "/artifact.iso",
		Checksum:  "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	}

	if err := downloadWebFileDependency(nil, dep); err == nil {
		t.Fatal("expected downloadWebFileDependency to reject checksum mismatch")
	}
	if _, err := os.Stat(destPath); !os.IsNotExist(err) {
		t.Fatalf("expected checksum-mismatched download to be removed, stat err: %v", err)
	}
}

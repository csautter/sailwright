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
	"runtime"
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

// TestBootstrapPythonEnv_UnusableVenvIsRecreated verifies that a venv whose executables
// exist but cannot run (e.g. its base interpreter was removed) is recreated.
func TestBootstrapPythonEnv_UnusableVenvIsRecreated(t *testing.T) {
	workdir := t.TempDir()
	venvPython, pipPath := pythonVenvExecutablePaths(workdir)
	for _, path := range []string{venvPython, pipPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("could not create parent directory for %s: %v", path, err)
		}
		// An empty file is not a runnable executable on any platform.
		if err := os.WriteFile(path, []byte{}, 0755); err != nil {
			t.Fatalf("could not create fake executable %s: %v", path, err)
		}
	}
	badPython := filepath.Join(t.TempDir(), "nonexistent-python")

	err := bootstrapPythonEnv(workdir, badPython)

	if err == nil {
		t.Fatal("expected error when unusable venv cannot be recreated, got nil")
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

// fixedPythonVersions returns a version probe that reports the given version per
// resolved executable path.
func fixedPythonVersions(versions map[string][2]int) pythonVersionProbe {
	return func(command pythonVenvCommand) (int, int, error) {
		version, ok := versions[command.executable]
		if !ok {
			return 0, 0, fmt.Errorf("unexpected probe of %s", command.executable)
		}
		return version[0], version[1], nil
	}
}

func TestResolvePythonVenvCommandPrefersPython3OnUnix(t *testing.T) {
	command, err := resolvePythonVenvCommandForOS("linux", func(name string) (string, error) {
		if name == "python3" || name == "python3.12" {
			return "/usr/bin/" + name, nil
		}
		return "", fmt.Errorf("%s not found", name)
	}, fixedPythonVersions(map[string][2]int{"/usr/bin/python3": {3, 13}, "/usr/bin/python3.12": {3, 12}}))
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
	}, fixedPythonVersions(map[string][2]int{`C:\Windows\py.exe`: {3, 13}}))
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
	}, fixedPythonVersions(nil))
	if err == nil {
		t.Fatal("expected error when no Python command is found, got nil")
	}
	for _, want := range []string{"python", "py -3", "python3"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error %q to mention checked candidate %q", err.Error(), want)
		}
	}
}

// macOS ships Python 3.9 as /usr/bin/python3, which the pinned Playwright no
// longer supports, so a versioned Homebrew interpreter must win.
func TestResolvePythonVenvCommandSkipsTooOldPython3(t *testing.T) {
	command, err := resolvePythonVenvCommandForOS("darwin", func(name string) (string, error) {
		switch name {
		case "python3":
			return "/usr/bin/python3", nil
		case "python3.13":
			return "/opt/homebrew/bin/python3.13", nil
		}
		return "", fmt.Errorf("%s not found", name)
	}, fixedPythonVersions(map[string][2]int{"/usr/bin/python3": {3, 9}, "/opt/homebrew/bin/python3.13": {3, 13}}))
	if err != nil {
		t.Fatalf("resolvePythonVenvCommandForOS returned error: %v", err)
	}
	if command.executable != "/opt/homebrew/bin/python3.13" {
		t.Fatalf("expected Homebrew python3.13, got %q", command.executable)
	}
}

func TestResolvePythonVenvCommandReportsTooOldVersions(t *testing.T) {
	_, err := resolvePythonVenvCommandForOS("darwin", func(name string) (string, error) {
		if name == "python3" {
			return "/usr/bin/python3", nil
		}
		return "", fmt.Errorf("%s not found", name)
	}, fixedPythonVersions(map[string][2]int{"/usr/bin/python3": {3, 9}}))
	if err == nil {
		t.Fatal("expected error when only Python 3.9 is available, got nil")
	}
	for _, want := range []string{"Python 3.10 or newer", "/usr/bin/python3 (Python 3.9)", "python3.13"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error %q to contain %q", err.Error(), want)
		}
	}
}

func TestParsePythonVersion(t *testing.T) {
	tests := []struct {
		output       string
		major, minor int
		wantErr      bool
	}{
		{output: "3.9\n", major: 3, minor: 9},
		{output: "3.13", major: 3, minor: 13},
		{output: "Python 3.13", wantErr: true},
		{output: "", wantErr: true},
	}
	for _, tt := range tests {
		major, minor, err := parsePythonVersion(tt.output)
		if (err != nil) != tt.wantErr {
			t.Fatalf("parsePythonVersion(%q) error = %v, wantErr %v", tt.output, err, tt.wantErr)
		}
		if !tt.wantErr && (major != tt.major || minor != tt.minor) {
			t.Fatalf("parsePythonVersion(%q) = %d.%d, want %d.%d", tt.output, major, minor, tt.major, tt.minor)
		}
	}
}

// A venv created by an older system Python must be rebuilt instead of reused.
func TestCheckPythonVenvUsableRejectsTooOldInterpreter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell script as fake interpreter")
	}
	workdir := t.TempDir()
	venvPython, _ := pythonVenvExecutablePaths(workdir)
	if err := os.MkdirAll(filepath.Dir(venvPython), 0o755); err != nil {
		t.Fatal(err)
	}
	fake := "#!/bin/sh\nif [ \"$1\" = -c ]; then echo 3.9; fi\n"
	if err := os.WriteFile(venvPython, []byte(fake), 0o755); err != nil { // #nosec G306 -- test needs an executable fake interpreter.
		t.Fatal(err)
	}

	err := checkPythonVenvUsable(workdir)
	if err == nil || !strings.Contains(err.Error(), "Python 3.9 is older than 3.10") {
		t.Fatalf("expected too-old venv error, got %v", err)
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

func TestQemuEfiAarch64URLsUsePinnedDebianPoolAndSnapshot(t *testing.T) {
	urls := qemuEfiAarch64URLs("2025.02-8+deb13u1")
	want := []string{
		"https://deb.debian.org/debian/pool/main/e/edk2/qemu-efi-aarch64_2025.02-8+deb13u1_all.deb",
		"https://snapshot.debian.org/archive/debian/" + qemuEfiAarch64SnapshotTimestamp + "/pool/main/e/edk2/qemu-efi-aarch64_2025.02-8+deb13u1_all.deb",
	}
	if strings.Join(urls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected qemu-efi-aarch64 URLs:\ngot  %v\nwant %v", urls, want)
	}
}

// The ARM64 firmware must not follow "latest in trixie": a firmware update
// together with a new QEMU already left ARM64 builds hanging in UEFI on macOS.
func TestArm64DependencyReconciliationPinsQemuEfiAarch64(t *testing.T) {
	dirs := GetDirectoriesInstance()
	originalCacheDir := dirs.CacheDir
	dirs.CacheDir = t.TempDir()
	defer func() {
		dirs.CacheDir = originalCacheDir
	}()

	wantPath := filepath.Join(dirs.CacheDir, "qemu-efi-aarch64_all.deb")
	for _, vmconfig := range []VirtualMachineConfig{
		{OS: "ubuntu", UbuntuType: "server", Arch: "arm64", HostOs: HostOsDarwin, VirtualizationEngine: VirtualizationEngineUtm},
		{OS: "ubuntu", UbuntuType: "desktop", Arch: "arm64", HostOs: HostOsDarwin, VirtualizationEngine: VirtualizationEngineUtm},
		{OS: "windows11", Arch: "arm64", HostOs: HostOsDarwin, VirtualizationEngine: VirtualizationEngineUtm},
		{OS: "windows11", Arch: "arm64", HostOs: HostOsLinux, VirtualizationEngine: VirtualizationEngineQemu},
	} {
		found := false
		for _, dep := range webFileDependenciesForVMConfig(vmconfig) {
			if dep.LocalPath != wantPath {
				continue
			}
			found = true
			if dep.Checksum != "sha256:"+qemuEfiAarch64SHA256 {
				t.Errorf("%+v: expected pinned qemu-efi-aarch64 checksum, got %q", vmconfig, dep.Checksum)
			}
			if dep.BeforeHook != nil {
				t.Errorf("%+v: qemu-efi-aarch64 must not resolve its URL at download time", vmconfig)
			}
			urls := qemuEfiAarch64URLs(qemuEfiAarch64Version)
			if dep.Source != urls[0] || strings.Join(dep.FallbackSources, " ") != strings.Join(urls[1:], " ") {
				t.Errorf("%+v: unexpected qemu-efi-aarch64 sources %q %v", vmconfig, dep.Source, dep.FallbackSources)
			}
		}
		if !found {
			t.Errorf("%+v: expected dependency reconciliation to include %s", vmconfig, wantPath)
		}
	}
}

func TestDownloadPinnedQemuEfiAarch64(t *testing.T) {
	if os.Getenv("RUN_INTEGRATION_TESTS") == "" {
		t.Skip("skipping integration test; set RUN_INTEGRATION_TESTS=1 to run")
	}
	for _, url := range qemuEfiAarch64URLs(qemuEfiAarch64Version) {
		t.Run(url, func(t *testing.T) {
			dep := WebFileDependency{
				LocalPath: filepath.Join(t.TempDir(), "qemu-efi-aarch64_all.deb"),
				Checksum:  "sha256:" + qemuEfiAarch64SHA256,
				Source:    url,
			}
			if err := downloadWebFileDependency(nil, dep); err != nil {
				t.Fatalf("downloadWebFileDependency failed: %v", err)
			}
		})
	}
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

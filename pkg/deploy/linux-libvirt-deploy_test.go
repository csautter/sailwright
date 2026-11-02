package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

func TestIsLinuxLibvirtTarget(t *testing.T) {
	if !isLinuxLibvirtTarget(alchemy_build.VirtualMachineConfig{
		OS:                   "ubuntu",
		UbuntuType:           "server",
		Arch:                 "amd64",
		HostOs:               alchemy_build.HostOsLinux,
		VirtualizationEngine: alchemy_build.VirtualizationEngineQemu,
	}) {
		t.Fatal("expected linux ubuntu qemu config to be a supported libvirt target")
	}

	if !isLinuxLibvirtTarget(alchemy_build.VirtualMachineConfig{
		OS:                   "windows11",
		Arch:                 "amd64",
		HostOs:               alchemy_build.HostOsLinux,
		VirtualizationEngine: alchemy_build.VirtualizationEngineQemu,
	}) {
		t.Fatal("expected linux windows qemu config to be a supported libvirt target")
	}

	if isLinuxLibvirtTarget(alchemy_build.VirtualMachineConfig{
		OS:                   "windows11",
		UbuntuType:           "server",
		Arch:                 "amd64",
		HostOs:               alchemy_build.HostOsLinux,
		VirtualizationEngine: alchemy_build.VirtualizationEngineQemu,
	}) {
		t.Fatal("did not expect windows config with Ubuntu type to be a libvirt target")
	}
}

func TestLinuxLibvirtDomainName(t *testing.T) {
	got := linuxLibvirtDomainName(alchemy_build.VirtualMachineConfig{
		OS:         "ubuntu",
		UbuntuType: "desktop",
		Arch:       "arm64",
	})

	if got != "ubuntu-desktop-arm64-dev-alchemy" {
		t.Fatalf("unexpected domain name %q", got)
	}
}

func TestLinuxQemuArtifactPathFallsBackToCache(t *testing.T) {
	dirs := alchemy_build.GetDirectoriesInstance()
	originalCacheDir := dirs.CacheDir
	dirs.CacheDir = t.TempDir()
	t.Cleanup(func() {
		dirs.CacheDir = originalCacheDir
	})

	got := linuxQemuArtifactPath(alchemy_build.VirtualMachineConfig{
		OS:         "ubuntu",
		UbuntuType: "server",
		Arch:       "amd64",
	})

	want := filepath.Join(dirs.CacheDir, "ubuntu", "qemu-ubuntu-server-packer-amd64.qcow2")
	if got != want {
		t.Fatalf("expected artifact path %q, got %q", want, got)
	}

	got = linuxQemuArtifactPath(alchemy_build.VirtualMachineConfig{
		OS:   "windows11",
		Arch: "arm64",
	})

	want = filepath.Join(dirs.CacheDir, "windows11", "qemu-windows11-arm64.qcow2")
	if got != want {
		t.Fatalf("expected windows artifact path %q, got %q", want, got)
	}
}

func TestLinuxLibvirtImageDirDefaultsToManagedAppDataForSession(t *testing.T) {
	dirs := alchemy_build.GetDirectoriesInstance()
	originalAppDataDir := dirs.AppDataDir
	dirs.AppDataDir = t.TempDir()
	t.Setenv(linuxLibvirtURIEnvVar, "qemu:///session")
	t.Setenv(linuxLibvirtImageDirEnvVar, "")
	t.Cleanup(func() {
		dirs.AppDataDir = originalAppDataDir
	})

	got := linuxLibvirtImageDir()
	want := filepath.Join(dirs.AppDataDir, "libvirt", "images")
	if got != want {
		t.Fatalf("expected session image dir %q, got %q", want, got)
	}
}

func TestLinuxLibvirtURIDefaultsToSystemConnection(t *testing.T) {
	t.Setenv(linuxLibvirtURIEnvVar, "")

	if got := linuxLibvirtURI(); got != linuxLibvirtDefaultURI {
		t.Fatalf("expected default libvirt URI %q, got %q", linuxLibvirtDefaultURI, got)
	}
}

func TestEnsureLinuxLibvirtCommandsAvailable(t *testing.T) {
	originalLookPath := lookPathLinuxLibvirtCommand
	t.Cleanup(func() {
		lookPathLinuxLibvirtCommand = originalLookPath
	})

	lookPathLinuxLibvirtCommand = func(file string) (string, error) {
		if file == "virsh" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + file, nil
	}

	err := ensureLinuxLibvirtCommandsAvailable("qemu-img", "virsh")
	if err == nil {
		t.Fatal("expected missing command error")
	}
	if !strings.Contains(err.Error(), `"virsh"`) {
		t.Fatalf("expected error to mention missing virsh, got %v", err)
	}
	if !strings.Contains(err.Error(), "sailwright install") {
		t.Fatalf("expected error to recommend sailwright install, got %v", err)
	}
}

func TestLinuxLibvirtImageDirUsesSystemDefaultWhenConfigured(t *testing.T) {
	t.Setenv(linuxLibvirtURIEnvVar, "qemu:///system")
	t.Setenv(linuxLibvirtImageDirEnvVar, "")

	if got := linuxLibvirtImageDir(); got != linuxLibvirtSystemImageDir {
		t.Fatalf("expected system image dir %q, got %q", linuxLibvirtSystemImageDir, got)
	}
}

func TestLinuxLibvirtNetworkArg(t *testing.T) {
	amd64Config := alchemy_build.VirtualMachineConfig{
		OS:         "ubuntu",
		UbuntuType: "desktop",
		Arch:       "amd64",
	}
	if got := linuxLibvirtNetworkArg(amd64Config, "qemu:///system"); got != "network=default,model=e1000" {
		t.Fatalf("unexpected system network arg %q", got)
	}
	if got := linuxLibvirtNetworkArg(amd64Config, "qemu:///session"); got != "user,model=e1000" {
		t.Fatalf("unexpected session network arg %q", got)
	}

	arm64Config := alchemy_build.VirtualMachineConfig{
		OS:         "ubuntu",
		UbuntuType: "server",
		Arch:       "arm64",
	}
	if got := linuxLibvirtNetworkArg(arm64Config, "qemu:///system"); got != "network=default,model=virtio" {
		t.Fatalf("unexpected arm64 system network arg %q", got)
	}
}

func TestEnsureLinuxLibvirtConfiguredNetworkReadyChecksDefaultNetwork(t *testing.T) {
	previousRunCombined := runLinuxLibvirtCommandWithCombinedOut
	dirs := alchemy_build.GetDirectoriesInstance()
	previousProjectDir := dirs.ProjectDir
	t.Cleanup(func() {
		runLinuxLibvirtCommandWithCombinedOut = previousRunCombined
		dirs.ProjectDir = previousProjectDir
	})

	dirs.ProjectDir = t.TempDir()
	runLinuxLibvirtCommandWithCombinedOut = func(projectDir string, _ time.Duration, executable string, args []string) (string, error) {
		if projectDir != dirs.ProjectDir {
			t.Fatalf("expected project dir %q, got %q", dirs.ProjectDir, projectDir)
		}
		if executable != "virsh" {
			t.Fatalf("expected virsh, got %q", executable)
		}
		joined := strings.Join(args, " ")
		if joined != "--connect qemu:///system net-info default" {
			t.Fatalf("unexpected virsh args %q", joined)
		}
		return "Name: default\nActive: yes\nAutostart: yes\n", nil
	}

	err := ensureLinuxLibvirtConfiguredNetworkReady(alchemy_build.VirtualMachineConfig{
		OS:         "ubuntu",
		UbuntuType: "server",
		Arch:       "amd64",
	}, "qemu:///system")
	if err != nil {
		t.Fatalf("expected active default network to pass preflight: %v", err)
	}
}

func TestEnsureLinuxLibvirtConfiguredNetworkReadySkipsSessionUserNetwork(t *testing.T) {
	previousRunCombined := runLinuxLibvirtCommandWithCombinedOut
	t.Cleanup(func() {
		runLinuxLibvirtCommandWithCombinedOut = previousRunCombined
	})

	runLinuxLibvirtCommandWithCombinedOut = func(_ string, _ time.Duration, executable string, args []string) (string, error) {
		t.Fatalf("did not expect network preflight for %s %q", executable, strings.Join(args, " "))
		return "", nil
	}

	err := ensureLinuxLibvirtConfiguredNetworkReady(alchemy_build.VirtualMachineConfig{
		OS:         "ubuntu",
		UbuntuType: "server",
		Arch:       "amd64",
	}, "qemu:///session")
	if err != nil {
		t.Fatalf("expected session user network to skip preflight: %v", err)
	}
}

func TestEnsureLinuxLibvirtNetworkReadyUsesConfiguredSessionURI(t *testing.T) {
	previousRunCombined := runLinuxLibvirtCommandWithCombinedOut
	t.Cleanup(func() {
		runLinuxLibvirtCommandWithCombinedOut = previousRunCombined
	})

	runLinuxLibvirtCommandWithCombinedOut = func(_ string, _ time.Duration, executable string, args []string) (string, error) {
		if executable != "virsh" {
			t.Fatalf("expected virsh, got %q", executable)
		}
		joined := strings.Join(args, " ")
		if joined != "--connect qemu:///session net-info default" {
			t.Fatalf("unexpected virsh args %q", joined)
		}
		return "Name: default\nActive: yes\n", nil
	}

	if err := ensureLinuxLibvirtNetworkReady("qemu:///session", "default"); err != nil {
		t.Fatalf("expected session default network check to use configured URI: %v", err)
	}
}

func TestEnsureLinuxLibvirtNetworkReadyFailsWhenInactive(t *testing.T) {
	previousRunCombined := runLinuxLibvirtCommandWithCombinedOut
	t.Cleanup(func() {
		runLinuxLibvirtCommandWithCombinedOut = previousRunCombined
	})

	runLinuxLibvirtCommandWithCombinedOut = func(_ string, _ time.Duration, _ string, _ []string) (string, error) {
		return "Name: default\nActive: no\n", nil
	}

	err := ensureLinuxLibvirtNetworkReady("qemu:///system", "default")
	if err == nil {
		t.Fatal("expected inactive network error")
	}
	for _, want := range []string{
		`libvirt network "default" on qemu:///system is inactive`,
		"sudo virsh --connect qemu:///system net-start default && sudo virsh --connect qemu:///system net-autostart default",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got %v", want, err)
		}
	}
}

func TestEnsureLinuxLibvirtNetworkReadyReportsMissingNetwork(t *testing.T) {
	previousRunCombined := runLinuxLibvirtCommandWithCombinedOut
	t.Cleanup(func() {
		runLinuxLibvirtCommandWithCombinedOut = previousRunCombined
	})

	runLinuxLibvirtCommandWithCombinedOut = func(_ string, _ time.Duration, _ string, _ []string) (string, error) {
		return "error: Network not found: no network with matching name 'default'\n", errors.New("exit status 1")
	}

	err := ensureLinuxLibvirtNetworkReady("qemu:///session", "default")
	if err == nil {
		t.Fatal("expected missing network error")
	}
	for _, want := range []string{
		`failed to inspect libvirt network "default" on qemu:///session`,
		"Network not found",
		"virsh --connect qemu:///session net-start default && virsh --connect qemu:///session net-autostart default",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got %v", want, err)
		}
	}
}

func TestLinuxLibvirtStateIndicatesRunning(t *testing.T) {
	if linuxLibvirtStateIndicatesRunning("shut off") {
		t.Fatal("expected shut off VM to be treated as stopped")
	}
	if !linuxLibvirtStateIndicatesRunning("running") {
		t.Fatal("expected running VM to be treated as active")
	}
	if !linuxLibvirtStateIndicatesRunning("paused") {
		t.Fatal("expected paused VM to still be treated as active")
	}
}

func TestLinuxLibvirtHostArch(t *testing.T) {
	got, err := linuxLibvirtHostArch()
	if err != nil {
		t.Fatalf("expected supported host arch for test runtime, got error: %v", err)
	}

	switch runtime.GOARCH {
	case "amd64", "arm64":
		if got != runtime.GOARCH {
			t.Fatalf("expected host arch %q, got %q", runtime.GOARCH, got)
		}
	default:
		t.Fatalf("unexpected test runtime architecture %q", runtime.GOARCH)
	}
}

func TestLinuxLibvirtCPUArgUsesVirtInstallCPUForEmulatedArch(t *testing.T) {
	previousLinuxLibvirtRuntimeGOARCH := linuxLibvirtRuntimeGOARCH
	t.Cleanup(func() {
		linuxLibvirtRuntimeGOARCH = previousLinuxLibvirtRuntimeGOARCH
	})

	linuxLibvirtRuntimeGOARCH = func() string {
		return "amd64"
	}

	if got := linuxLibvirtCPUArg(alchemy_build.VirtualMachineConfig{Arch: "amd64"}); got != "host-passthrough" {
		t.Fatalf("expected native amd64 guest to use host-passthrough, got %q", got)
	}
	if got := linuxLibvirtCPUArg(alchemy_build.VirtualMachineConfig{Arch: "arm64"}); got != "cortex-a57" {
		t.Fatalf("expected emulated arm64 guest to use portable cortex-a57 CPU, got %q", got)
	}
	if got := linuxLibvirtCPUArg(alchemy_build.VirtualMachineConfig{OS: "windows11", Arch: "arm64"}); got != "max,sve=off,sme=off,pauth-impdef=on" {
		t.Fatalf("expected emulated Windows arm64 guest to use Windows-compatible max CPU, got %q", got)
	}

	linuxLibvirtRuntimeGOARCH = func() string {
		return "arm64"
	}
	if got := linuxLibvirtCPUArg(alchemy_build.VirtualMachineConfig{Arch: "amd64"}); got != "Skylake-Client" {
		t.Fatalf("expected emulated amd64 guest to use Skylake-Client, got %q", got)
	}
}

func TestLinuxLibvirtVirtInstallArgsIncludeNativeCPUAndSpiceAgentDevices(t *testing.T) {
	previousLinuxLibvirtRuntimeGOARCH := linuxLibvirtRuntimeGOARCH
	t.Cleanup(func() {
		linuxLibvirtRuntimeGOARCH = previousLinuxLibvirtRuntimeGOARCH
	})
	linuxLibvirtRuntimeGOARCH = func() string {
		return "amd64"
	}

	args := linuxLibvirtVirtInstallArgs(alchemy_build.VirtualMachineConfig{
		OS:         "ubuntu",
		UbuntuType: "desktop",
		Arch:       "amd64",
		Cpus:       4,
		MemoryMB:   4096,
	}, "qemu:///session", "/tmp/test.qcow2")

	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--cpu host-passthrough",
		"--network user,model=e1000",
		"--graphics spice,clipboard.copypaste=on",
		"--video model.type=virtio",
		"--controller type=usb,model=qemu-xhci",
		"--input tablet,bus=usb",
		"--input keyboard,bus=usb",
		"--channel spicevmc,target.type=virtio,target.name=com.redhat.spice.0",
		"--channel unix,target.type=virtio,name=org.qemu.guest_agent.0",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected virt-install args to contain %q, got %q", want, joined)
		}
	}
	if strings.Contains(joined, "--virt-type qemu") {
		t.Fatalf("did not expect native guest virt-install args to force TCG, got %q", joined)
	}
}

func TestLinuxLibvirtVirtInstallArgsUseEmulatedArm64Settings(t *testing.T) {
	previousLinuxLibvirtRuntimeGOARCH := linuxLibvirtRuntimeGOARCH
	t.Cleanup(func() {
		linuxLibvirtRuntimeGOARCH = previousLinuxLibvirtRuntimeGOARCH
	})
	linuxLibvirtRuntimeGOARCH = func() string {
		return "amd64"
	}

	args := linuxLibvirtVirtInstallArgs(alchemy_build.VirtualMachineConfig{
		OS:         "ubuntu",
		UbuntuType: "server",
		Arch:       "arm64",
		Cpus:       1,
		MemoryMB:   4096,
	}, "qemu:///session", "/tmp/test.qcow2")

	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--cpu cortex-a57",
		"--vcpus 1",
		"--virt-type qemu",
		"--arch aarch64",
		"--machine virt",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected virt-install args to contain %q, got %q", want, joined)
		}
	}
}

func TestLinuxLibvirtVirtInstallArgsUseWindowsAmd64Devices(t *testing.T) {
	previousLinuxLibvirtRuntimeGOARCH := linuxLibvirtRuntimeGOARCH
	t.Cleanup(func() {
		linuxLibvirtRuntimeGOARCH = previousLinuxLibvirtRuntimeGOARCH
	})
	linuxLibvirtRuntimeGOARCH = func() string {
		return "amd64"
	}

	args := linuxLibvirtVirtInstallArgs(alchemy_build.VirtualMachineConfig{
		OS:       "windows11",
		Arch:     "amd64",
		Cpus:     4,
		MemoryMB: 8192,
	}, "qemu:///session", "/tmp/test.qcow2")

	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--disk path=/tmp/test.qcow2,format=qcow2,bus=sata",
		"--network user,model=e1000",
		"--video model.type=qxl",
		"--machine q35",
		"hyperv.relaxed.state=on",
		"hyperv.spinlocks.retries=8191",
		"hyperv.synic.state=on,hyperv.stimer.state=on",
		"--clock hypervclock_present=yes,rtc_tickpolicy=catchup,pit_tickpolicy=delay,hpet_present=no",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected Windows virt-install args to contain %q, got %q", want, joined)
		}
	}
}

func TestLinuxLibvirtVirtInstallArgsOmitHypervWhenNotApplicable(t *testing.T) {
	previousLinuxLibvirtRuntimeGOARCH := linuxLibvirtRuntimeGOARCH
	t.Cleanup(func() {
		linuxLibvirtRuntimeGOARCH = previousLinuxLibvirtRuntimeGOARCH
	})

	for _, tc := range []struct {
		name     string
		hostArch string
		config   alchemy_build.VirtualMachineConfig
	}{
		{"ubuntu guest", "amd64", alchemy_build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "desktop", Arch: "amd64"}},
		{"emulated windows amd64", "arm64", alchemy_build.VirtualMachineConfig{OS: "windows11", Arch: "amd64"}},
		{"windows arm64", "arm64", alchemy_build.VirtualMachineConfig{OS: "windows11", Arch: "arm64"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			linuxLibvirtRuntimeGOARCH = func() string {
				return tc.hostArch
			}
			joined := strings.Join(linuxLibvirtVirtInstallArgs(tc.config, "qemu:///session", "/tmp/test.qcow2"), " ")
			if strings.Contains(joined, "hyperv.") || strings.Contains(joined, "--clock") {
				t.Fatalf("did not expect Hyper-V enlightenments, got %q", joined)
			}
		})
	}
}

func TestRunLinuxQemuDeployIncludesVirtInstallOutputOnXMLFailure(t *testing.T) {
	previousRunStreaming := runLinuxLibvirtCommandWithStreamingLogs
	previousRunCombined := runLinuxLibvirtCommandWithCombinedOut
	previousLookPath := lookPathLinuxLibvirtCommand
	previousLinuxLibvirtRuntimeGOARCH := linuxLibvirtRuntimeGOARCH
	dirs := alchemy_build.GetDirectoriesInstance()
	previousProjectDir := dirs.ProjectDir
	t.Cleanup(func() {
		runLinuxLibvirtCommandWithStreamingLogs = previousRunStreaming
		runLinuxLibvirtCommandWithCombinedOut = previousRunCombined
		lookPathLinuxLibvirtCommand = previousLookPath
		linuxLibvirtRuntimeGOARCH = previousLinuxLibvirtRuntimeGOARCH
		dirs.ProjectDir = previousProjectDir
	})
	linuxLibvirtRuntimeGOARCH = func() string {
		return "amd64"
	}

	tempDir := t.TempDir()
	dirs.ProjectDir = tempDir
	imageDir := filepath.Join(tempDir, "images")
	artifactPath := filepath.Join(tempDir, "artifact.qcow2")
	t.Setenv(linuxLibvirtURIEnvVar, "qemu:///session")
	t.Setenv(linuxLibvirtImageDirEnvVar, imageDir)

	if err := os.WriteFile(artifactPath, []byte("artifact"), 0o644); err != nil {
		t.Fatalf("failed to seed test artifact: %v", err)
	}

	lookPathLinuxLibvirtCommand = func(file string) (string, error) {
		return "/usr/bin/" + file, nil
	}
	runLinuxLibvirtCommandWithStreamingLogs = func(_ string, _ time.Duration, executable string, args []string, _ string) error {
		if executable != "qemu-img" {
			t.Fatalf("expected only qemu-img before XML failure, got %q", executable)
		}
		if len(args) == 0 {
			t.Fatal("expected qemu-img args")
		}
		return os.WriteFile(args[len(args)-1], []byte("disk"), 0o644)
	}
	runLinuxLibvirtCommandWithCombinedOut = func(_ string, _ time.Duration, executable string, args []string) (string, error) {
		if executable != "virt-install" {
			t.Fatalf("expected virt-install, got %q", executable)
		}
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "--virt-type qemu") {
			t.Fatalf("expected emulated arm64 virt-install args to force TCG, got %q", joined)
		}
		return "ERROR    Unknown --cpu options: ['sve']\n", errors.New("exit status 1")
	}

	config := alchemy_build.VirtualMachineConfig{
		OS:                     "ubuntu",
		UbuntuType:             "desktop",
		Arch:                   "arm64",
		HostOs:                 alchemy_build.HostOsLinux,
		VirtualizationEngine:   alchemy_build.VirtualizationEngineQemu,
		ExpectedBuildArtifacts: []string{artifactPath},
	}

	err := RunLinuxQemuDeployOnLinux(config)
	if err == nil {
		t.Fatal("expected deploy failure")
	}
	if !strings.Contains(err.Error(), "Unknown --cpu options") {
		t.Fatalf("expected virt-install output in error, got %v", err)
	}
	if _, statErr := os.Stat(linuxLibvirtDiskPath(config)); !os.IsNotExist(statErr) {
		t.Fatalf("expected failed deploy to remove cloned disk, stat err: %v", statErr)
	}
}

func TestRunLinuxQemuDeployEnsuresDefaultImageDirHasRestrictivePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping POSIX permission assertion on Windows")
	}

	previousRunStreaming := runLinuxLibvirtCommandWithStreamingLogs
	previousRunCombined := runLinuxLibvirtCommandWithCombinedOut
	previousLookPath := lookPathLinuxLibvirtCommand
	dirs := alchemy_build.GetDirectoriesInstance()
	previousProjectDir := dirs.ProjectDir
	previousAppDataDir := dirs.AppDataDir
	t.Cleanup(func() {
		runLinuxLibvirtCommandWithStreamingLogs = previousRunStreaming
		runLinuxLibvirtCommandWithCombinedOut = previousRunCombined
		lookPathLinuxLibvirtCommand = previousLookPath
		dirs.ProjectDir = previousProjectDir
		dirs.AppDataDir = previousAppDataDir
	})

	tempDir := t.TempDir()
	dirs.ProjectDir = filepath.Join(tempDir, "project")
	dirs.AppDataDir = filepath.Join(tempDir, "app-data")
	imageDir := filepath.Join(dirs.AppDataDir, linuxLibvirtManagedDomainDirectory)
	artifactPath := filepath.Join(tempDir, "artifact.qcow2")
	t.Setenv(linuxLibvirtURIEnvVar, "qemu:///session")
	t.Setenv(linuxLibvirtImageDirEnvVar, "")

	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		t.Fatalf("failed to seed permissive image dir: %v", err)
	}
	if err := os.Chmod(imageDir, 0o755); err != nil {
		t.Fatalf("failed to set permissive image dir mode: %v", err)
	}
	if err := os.WriteFile(artifactPath, []byte("artifact"), 0o644); err != nil {
		t.Fatalf("failed to seed test artifact: %v", err)
	}

	lookPathLinuxLibvirtCommand = func(file string) (string, error) {
		return "/usr/bin/" + file, nil
	}
	runLinuxLibvirtCommandWithStreamingLogs = func(_ string, _ time.Duration, executable string, args []string, _ string) error {
		switch executable {
		case "qemu-img":
			if len(args) == 0 {
				t.Fatal("expected qemu-img args")
			}
			return os.WriteFile(args[len(args)-1], []byte("disk"), 0o644)
		case "virsh":
			return nil
		default:
			t.Fatalf("unexpected streaming command %q", executable)
			return nil
		}
	}
	runLinuxLibvirtCommandWithCombinedOut = func(_ string, _ time.Duration, executable string, _ []string) (string, error) {
		if executable != "virt-install" {
			t.Fatalf("expected virt-install, got %q", executable)
		}
		return "<domain type='qemu'></domain>\n", nil
	}

	config := alchemy_build.VirtualMachineConfig{
		OS:                     "ubuntu",
		UbuntuType:             "server",
		Arch:                   "amd64",
		HostOs:                 alchemy_build.HostOsLinux,
		VirtualizationEngine:   alchemy_build.VirtualizationEngineQemu,
		ExpectedBuildArtifacts: []string{artifactPath},
	}

	if err := RunLinuxQemuDeployOnLinux(config); err != nil {
		t.Fatalf("expected deploy to succeed: %v", err)
	}

	info, err := os.Stat(imageDir)
	if err != nil {
		t.Fatalf("expected image dir to exist: %v", err)
	}
	if mode := info.Mode().Perm(); mode != linuxLibvirtImageDirPermission {
		t.Fatalf("expected image dir mode %04o, got %04o", linuxLibvirtImageDirPermission, mode)
	}
}

func TestRunLinuxQemuDeployPreflightsDefaultNetworkBeforeClone(t *testing.T) {
	previousRunStreaming := runLinuxLibvirtCommandWithStreamingLogs
	previousRunCombined := runLinuxLibvirtCommandWithCombinedOut
	previousLookPath := lookPathLinuxLibvirtCommand
	dirs := alchemy_build.GetDirectoriesInstance()
	previousProjectDir := dirs.ProjectDir
	t.Cleanup(func() {
		runLinuxLibvirtCommandWithStreamingLogs = previousRunStreaming
		runLinuxLibvirtCommandWithCombinedOut = previousRunCombined
		lookPathLinuxLibvirtCommand = previousLookPath
		dirs.ProjectDir = previousProjectDir
	})

	tempDir := t.TempDir()
	dirs.ProjectDir = tempDir
	imageDir := filepath.Join(tempDir, "images")
	artifactPath := filepath.Join(tempDir, "artifact.qcow2")
	t.Setenv(linuxLibvirtURIEnvVar, "qemu:///system")
	t.Setenv(linuxLibvirtImageDirEnvVar, imageDir)

	if err := os.WriteFile(artifactPath, []byte("artifact"), 0o644); err != nil {
		t.Fatalf("failed to seed test artifact: %v", err)
	}

	lookPathLinuxLibvirtCommand = func(file string) (string, error) {
		return "/usr/bin/" + file, nil
	}
	runLinuxLibvirtCommandWithStreamingLogs = func(_ string, _ time.Duration, executable string, args []string, _ string) error {
		t.Fatalf("did not expect streaming command before network preflight passes: %s %q", executable, strings.Join(args, " "))
		return nil
	}
	runLinuxLibvirtCommandWithCombinedOut = func(_ string, _ time.Duration, executable string, args []string) (string, error) {
		if executable != "virsh" {
			t.Fatalf("expected virsh network preflight, got %q", executable)
		}
		joined := strings.Join(args, " ")
		if joined != "--connect qemu:///system net-info default" {
			t.Fatalf("unexpected virsh args %q", joined)
		}
		return "Name: default\nActive: no\n", nil
	}

	err := RunLinuxQemuDeployOnLinux(alchemy_build.VirtualMachineConfig{
		OS:                     "ubuntu",
		UbuntuType:             "server",
		Arch:                   "amd64",
		HostOs:                 alchemy_build.HostOsLinux,
		VirtualizationEngine:   alchemy_build.VirtualizationEngineQemu,
		ExpectedBuildArtifacts: []string{artifactPath},
	})
	if err == nil {
		t.Fatal("expected network preflight failure")
	}
	if !strings.Contains(err.Error(), `libvirt network "default" on qemu:///system is inactive`) {
		t.Fatalf("expected inactive network error, got %v", err)
	}
}

func TestRunLinuxQemuStartPreflightsDefaultNetworkBeforeStart(t *testing.T) {
	previousRunCombined := runLinuxLibvirtCommandWithCombinedOut
	previousLookPath := lookPathLinuxLibvirtCommand
	dirs := alchemy_build.GetDirectoriesInstance()
	previousProjectDir := dirs.ProjectDir
	t.Cleanup(func() {
		runLinuxLibvirtCommandWithCombinedOut = previousRunCombined
		lookPathLinuxLibvirtCommand = previousLookPath
		dirs.ProjectDir = previousProjectDir
	})

	dirs.ProjectDir = t.TempDir()
	t.Setenv(linuxLibvirtURIEnvVar, "qemu:///system")
	t.Setenv(linuxLibvirtImageDirEnvVar, "")

	lookPathLinuxLibvirtCommand = func(file string) (string, error) {
		return "/usr/bin/" + file, nil
	}
	runLinuxLibvirtCommandWithCombinedOut = func(_ string, _ time.Duration, executable string, args []string) (string, error) {
		if executable != "virsh" {
			t.Fatalf("expected virsh, got %q", executable)
		}
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, " domstate "):
			return "shut off\n", nil
		case strings.Contains(joined, " net-info default"):
			return "Name: default\nActive: no\n", nil
		case strings.Contains(joined, " start "):
			t.Fatalf("did not expect start before network preflight passes: %q", joined)
			return "", nil
		default:
			t.Fatalf("unexpected virsh args %q", joined)
			return "", nil
		}
	}

	err := RunLinuxQemuStartOnLinux(alchemy_build.VirtualMachineConfig{
		OS:                   "ubuntu",
		UbuntuType:           "server",
		Arch:                 "amd64",
		HostOs:               alchemy_build.HostOsLinux,
		VirtualizationEngine: alchemy_build.VirtualizationEngineQemu,
	})
	if err == nil {
		t.Fatal("expected network preflight failure")
	}
	if !strings.Contains(err.Error(), "sudo virsh --connect qemu:///system net-start default") {
		t.Fatalf("expected remediation command in error, got %v", err)
	}
}

func TestRunLinuxQemuStartSuggestsLibvirtStorageACLRepair(t *testing.T) {
	previousRunCombined := runLinuxLibvirtCommandWithCombinedOut
	previousLookPath := lookPathLinuxLibvirtCommand
	dirs := alchemy_build.GetDirectoriesInstance()
	previousProjectDir := dirs.ProjectDir
	t.Cleanup(func() {
		runLinuxLibvirtCommandWithCombinedOut = previousRunCombined
		lookPathLinuxLibvirtCommand = previousLookPath
		dirs.ProjectDir = previousProjectDir
	})

	dirs.ProjectDir = t.TempDir()
	t.Setenv(linuxLibvirtURIEnvVar, "qemu:///system")
	t.Setenv(linuxLibvirtImageDirEnvVar, "")

	lookPathLinuxLibvirtCommand = func(file string) (string, error) {
		return "/usr/bin/" + file, nil
	}
	runLinuxLibvirtCommandWithCombinedOut = func(_ string, _ time.Duration, executable string, args []string) (string, error) {
		if executable != "virsh" {
			t.Fatalf("expected virsh, got %q", executable)
		}
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, " domstate "):
			return "shut off\n", nil
		case strings.Contains(joined, " net-info default"):
			return "Name: default\nActive: yes\n", nil
		case strings.Contains(joined, " start "):
			return "error: Failed to start domain 'ubuntu-desktop-amd64-dev-alchemy'\n" +
					"error: Cannot access storage file '/var/tmp/dev-alchemy/libvirt/images/ubuntu-desktop-amd64-dev-alchemy.qcow2' (as uid:64055, gid:993): Keine Berechtigung\n",
				errors.New("exit status 1")
		default:
			t.Fatalf("unexpected virsh args %q", joined)
			return "", nil
		}
	}

	err := RunLinuxQemuStartOnLinux(alchemy_build.VirtualMachineConfig{
		OS:                   "ubuntu",
		UbuntuType:           "desktop",
		Arch:                 "amd64",
		HostOs:               alchemy_build.HostOsLinux,
		VirtualizationEngine: alchemy_build.VirtualizationEngineQemu,
	})
	if err == nil {
		t.Fatal("expected start failure")
	}

	errText := err.Error()
	for _, want := range []string{
		"Libvirt cannot access the managed QCOW2 disk as uid:64055, gid:993.",
		"sudo setfacl -m g:993:x /var/tmp/dev-alchemy /var/tmp/dev-alchemy/libvirt",
		"sudo setfacl -R -m g:993:rwX /var/tmp/dev-alchemy/libvirt/images",
		"sudo setfacl -d -m g:993:rwX /var/tmp/dev-alchemy/libvirt/images",
		"sudo apt-get install acl",
	} {
		if !strings.Contains(errText, want) {
			t.Fatalf("expected start error to contain %q, got:\n%s", want, errText)
		}
	}
}

func TestLinuxLibvirtNetworkModel(t *testing.T) {
	if got := linuxLibvirtNetworkModel(alchemy_build.VirtualMachineConfig{Arch: "amd64"}); got != "e1000" {
		t.Fatalf("expected amd64 network model e1000, got %q", got)
	}
	if got := linuxLibvirtNetworkModel(alchemy_build.VirtualMachineConfig{Arch: "arm64"}); got != "virtio" {
		t.Fatalf("expected arm64 network model virtio, got %q", got)
	}
}

func TestLinuxLibvirtDiskBus(t *testing.T) {
	if got := linuxLibvirtDiskBus(alchemy_build.VirtualMachineConfig{OS: "windows11", Arch: "amd64"}); got != "sata" {
		t.Fatalf("expected Windows amd64 guests to use SATA disk bus, got %q", got)
	}
	if got := linuxLibvirtDiskBus(alchemy_build.VirtualMachineConfig{OS: "windows11", Arch: "arm64"}); got != "virtio" {
		t.Fatalf("expected Windows arm64 guests to use virtio disk bus, got %q", got)
	}
	if got := linuxLibvirtDiskBus(alchemy_build.VirtualMachineConfig{OS: "ubuntu", Arch: "amd64"}); got != "virtio" {
		t.Fatalf("expected Ubuntu guests to use virtio disk bus, got %q", got)
	}
}

func TestLinuxLibvirtVideoArg(t *testing.T) {
	if got := linuxLibvirtVideoArg(alchemy_build.VirtualMachineConfig{
		OS:         "ubuntu",
		UbuntuType: "desktop",
		Arch:       "amd64",
	}); got != "model.type=virtio" {
		t.Fatalf("expected amd64 desktop guests to keep virtio video, got %q", got)
	}

	if got := linuxLibvirtVideoArg(alchemy_build.VirtualMachineConfig{
		OS:         "ubuntu",
		UbuntuType: "desktop",
		Arch:       "arm64",
	}); got != "model.type=virtio" {
		t.Fatalf("expected arm64 desktop guests to keep virtio video, got %q", got)
	}

	if got := linuxLibvirtVideoArg(alchemy_build.VirtualMachineConfig{
		OS:         "ubuntu",
		UbuntuType: "server",
		Arch:       "amd64",
	}); got != "model.type=virtio" {
		t.Fatalf("expected server guests to keep virtio video, got %q", got)
	}

	if got := linuxLibvirtVideoArg(alchemy_build.VirtualMachineConfig{
		OS:   "windows11",
		Arch: "amd64",
	}); got != "model.type=qxl,model.ram=65536,model.vram=65536,model.vgamem=65536,model.heads=1" {
		t.Fatalf("expected Windows amd64 guests to use qxl video, got %q", got)
	}
}

package deploy

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

const (
	linuxLibvirtURIEnvVar              = "DEV_ALCHEMY_LIBVIRT_URI"
	linuxLibvirtImageDirEnvVar         = "DEV_ALCHEMY_LIBVIRT_IMAGE_DIR"
	linuxLibvirtDefaultURI             = "qemu:///system"
	linuxLibvirtSystemImageDir         = "/var/tmp/dev-alchemy/libvirt/images"
	linuxLibvirtCreateTimeout          = 20 * time.Minute
	linuxLibvirtCommandTimeout         = 2 * time.Minute
	linuxLibvirtStopSettleTimeout      = 45 * time.Second
	linuxLibvirtStopPollInterval       = 2 * time.Second
	linuxLibvirtDiskCloneTimeout       = 30 * time.Minute
	linuxLibvirtImageDirPermission     = 0o750
	linuxLibvirtManagedDomainDirectory = "libvirt/images"
)

var (
	runLinuxLibvirtCommandWithStreamingLogs = runCommandWithStreamingLogs
	runLinuxLibvirtCommandWithCombinedOut   = runCommandWithCombinedOutput
	linuxLibvirtStopTimeout                 = linuxLibvirtStopSettleTimeout
	linuxLibvirtStopPollEvery               = linuxLibvirtStopPollInterval
	lookPathLinuxLibvirtCommand             = exec.LookPath
	linuxLibvirtRuntimeGOARCH               = func() string { return runtime.GOARCH }
	linuxLibvirtStorageAccessErrorPattern   = regexp.MustCompile(`(?i)cannot access storage file '([^']+)' \(as uid:\s*([0-9]+),\s*gid:\s*([0-9]+)\)`)
	linuxLibvirtShellSafePattern            = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)
)

func isLinuxLibvirtTarget(config alchemy_build.VirtualMachineConfig) bool {
	if config.HostOs != alchemy_build.HostOsLinux ||
		config.VirtualizationEngine != alchemy_build.VirtualizationEngineQemu ||
		(config.Arch != "amd64" && config.Arch != "arm64") {
		return false
	}

	switch config.OS {
	case "ubuntu":
		return config.UbuntuType == "server" || config.UbuntuType == "desktop"
	case "windows11":
		return config.UbuntuType == ""
	default:
		return false
	}
}

func RunLinuxQemuDeployOnLinux(config alchemy_build.VirtualMachineConfig) error {
	if !isLinuxLibvirtTarget(config) {
		return fmt.Errorf("linux libvirt deploy is not implemented for OS=%s type=%s arch=%s", config.OS, config.UbuntuType, config.Arch)
	}
	if err := ensureLinuxLibvirtCommandsAvailable("qemu-img", "virt-install", "virsh"); err != nil {
		return err
	}

	uri := linuxLibvirtURI()
	projectDir := alchemy_build.GetDirectoriesInstance().ProjectDir
	artifactPath := linuxQemuArtifactPath(config)
	if _, err := os.Stat(artifactPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("required QCOW2 build artifact is missing at %q; run `sailwright build %s` first", artifactPath, startCommandArguments(config))
		}
		return fmt.Errorf("failed to inspect QCOW2 build artifact %q: %w", artifactPath, err)
	}

	imageDir := linuxLibvirtImageDir()
	if err := ensureLinuxLibvirtImageDir(imageDir); err != nil {
		return fmt.Errorf(
			"failed to prepare libvirt image directory %q: %w; override with %s or use a writable libvirt URI via %s",
			imageDir,
			err,
			linuxLibvirtImageDirEnvVar,
			linuxLibvirtURIEnvVar,
		)
	}

	diskPath := linuxLibvirtDiskPath(config)
	if _, err := os.Stat(diskPath); err == nil {
		return fmt.Errorf("managed libvirt disk %q already exists; destroy the existing VM first", diskPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to inspect managed libvirt disk %q: %w", diskPath, err)
	}

	if err := ensureLinuxLibvirtConfiguredNetworkReady(config, uri); err != nil {
		return err
	}

	if err := runLinuxLibvirtCommandWithStreamingLogs(
		projectDir,
		linuxLibvirtDiskCloneTimeout,
		"qemu-img",
		[]string{"convert", "-p", "-f", "qcow2", "-O", "qcow2", artifactPath, diskPath},
		fmt.Sprintf("%s:%s:%s:qemu-img-convert", config.OS, config.UbuntuType, config.Arch),
	); err != nil {
		return fmt.Errorf("failed to clone QCOW2 artifact into managed libvirt disk %q: %w", diskPath, err)
	}

	xml, err := runLinuxLibvirtCommandWithCombinedOut(
		projectDir,
		linuxLibvirtCommandTimeout,
		"virt-install",
		linuxLibvirtVirtInstallArgs(config, uri, diskPath),
	)
	if err != nil {
		_ = os.Remove(diskPath)
		if trimmedOutput := strings.TrimSpace(xml); trimmedOutput != "" {
			return fmt.Errorf("failed to generate libvirt domain XML for %s: %w; output: %s", linuxLibvirtDomainName(config), err, trimmedOutput)
		}
		return fmt.Errorf("failed to generate libvirt domain XML for %s: %w", linuxLibvirtDomainName(config), err)
	}
	if strings.TrimSpace(xml) == "" {
		_ = os.Remove(diskPath)
		return fmt.Errorf("virt-install generated empty libvirt domain XML for %s", linuxLibvirtDomainName(config))
	}

	xmlFile, err := os.CreateTemp("", "dev-alchemy-libvirt-*.xml")
	if err != nil {
		_ = os.Remove(diskPath)
		return fmt.Errorf("failed to create temporary libvirt XML file: %w", err)
	}
	xmlPath := xmlFile.Name()
	defer os.Remove(xmlPath)

	if _, err := xmlFile.WriteString(xml); err != nil {
		_ = xmlFile.Close()
		_ = os.Remove(diskPath)
		return fmt.Errorf("failed to write libvirt domain XML to %q: %w", xmlPath, err)
	}
	if err := xmlFile.Close(); err != nil {
		_ = os.Remove(diskPath)
		return fmt.Errorf("failed to close libvirt domain XML file %q: %w", xmlPath, err)
	}

	if err := runLinuxLibvirtCommandWithStreamingLogs(
		projectDir,
		linuxLibvirtCreateTimeout,
		"virsh",
		[]string{"--connect", uri, "define", xmlPath},
		fmt.Sprintf("%s:%s:%s:virsh-define", config.OS, config.UbuntuType, config.Arch),
	); err != nil {
		_ = os.Remove(diskPath)
		return fmt.Errorf("failed to define libvirt domain %q: %w", linuxLibvirtDomainName(config), err)
	}

	return nil
}

func RunLinuxQemuStartOnLinux(config alchemy_build.VirtualMachineConfig) error {
	if !isLinuxLibvirtTarget(config) {
		return fmt.Errorf("linux libvirt start is not implemented for OS=%s type=%s arch=%s", config.OS, config.UbuntuType, config.Arch)
	}
	if err := ensureLinuxLibvirtCommandsAvailable("virsh"); err != nil {
		return err
	}

	uri := linuxLibvirtURI()
	state, err := inspectLinuxLibvirtStartTarget(config)
	if err != nil {
		return err
	}
	if !state.Exists {
		return fmt.Errorf("libvirt VM %q does not exist. Run `sailwright create %s` first", linuxLibvirtDomainName(config), startCommandArguments(config))
	}
	if state.Running {
		return nil
	}

	if err := ensureLinuxLibvirtConfiguredNetworkReady(config, uri); err != nil {
		return err
	}

	output, err := runLinuxLibvirtCommandWithCombinedOut(
		alchemy_build.GetDirectoriesInstance().ProjectDir,
		linuxLibvirtCommandTimeout,
		"virsh",
		[]string{"--connect", uri, "start", linuxLibvirtDomainName(config)},
	)
	if err != nil {
		if trimmedOutput := strings.TrimSpace(output); trimmedOutput != "" {
			return fmt.Errorf("failed to start libvirt VM %q: %w; output: %s%s", linuxLibvirtDomainName(config), err, trimmedOutput, linuxLibvirtStorageAccessRepairHint(output))
		}
		return fmt.Errorf("failed to start libvirt VM %q: %w", linuxLibvirtDomainName(config), err)
	}

	return nil
}

func RunLinuxQemuStopOnLinux(config alchemy_build.VirtualMachineConfig) error {
	if !isLinuxLibvirtTarget(config) {
		return fmt.Errorf("linux libvirt stop is not implemented for OS=%s type=%s arch=%s", config.OS, config.UbuntuType, config.Arch)
	}
	if err := ensureLinuxLibvirtCommandsAvailable("virsh"); err != nil {
		return err
	}

	state, err := inspectLinuxLibvirtStartTarget(config)
	if err != nil {
		return err
	}
	if !state.Exists || !state.Running {
		return nil
	}

	projectDir := alchemy_build.GetDirectoriesInstance().ProjectDir
	domainName := linuxLibvirtDomainName(config)
	if err := runLinuxLibvirtCommandWithStreamingLogs(
		projectDir,
		linuxLibvirtCommandTimeout,
		"virsh",
		[]string{"--connect", linuxLibvirtURI(), "shutdown", domainName},
		fmt.Sprintf("%s:%s:%s:virsh-shutdown", config.OS, config.UbuntuType, config.Arch),
	); err == nil {
		stopped, waitErr := waitForLinuxLibvirtStop(config, linuxLibvirtStopTimeout)
		if waitErr == nil && stopped {
			return nil
		}
	}

	if err := runLinuxLibvirtCommandWithStreamingLogs(
		projectDir,
		linuxLibvirtCommandTimeout,
		"virsh",
		[]string{"--connect", linuxLibvirtURI(), "destroy", domainName},
		fmt.Sprintf("%s:%s:%s:virsh-destroy", config.OS, config.UbuntuType, config.Arch),
	); err != nil {
		return fmt.Errorf("failed to force stop libvirt VM %q after graceful shutdown attempt: %w", domainName, err)
	}

	stopped, err := waitForLinuxLibvirtStop(config, linuxLibvirtStopTimeout)
	if err != nil {
		return fmt.Errorf("failed verifying stopped state for libvirt VM %q: %w", domainName, err)
	}
	if !stopped {
		return fmt.Errorf("libvirt VM %q is still running after force stop", domainName)
	}

	return nil
}

func RunLinuxQemuDestroyOnLinux(config alchemy_build.VirtualMachineConfig) error {
	if !isLinuxLibvirtTarget(config) {
		return fmt.Errorf("linux libvirt destroy is not implemented for OS=%s type=%s arch=%s", config.OS, config.UbuntuType, config.Arch)
	}
	if err := ensureLinuxLibvirtCommandsAvailable("virsh"); err != nil {
		return err
	}

	state, err := inspectLinuxLibvirtStartTarget(config)
	if err != nil {
		return err
	}
	if state.Exists {
		if state.Running {
			if err := RunLinuxQemuStopOnLinux(config); err != nil {
				return err
			}
		}

		projectDir := alchemy_build.GetDirectoriesInstance().ProjectDir
		domainName := linuxLibvirtDomainName(config)
		err = runLinuxLibvirtCommandWithStreamingLogs(
			projectDir,
			linuxLibvirtCommandTimeout,
			"virsh",
			[]string{"--connect", linuxLibvirtURI(), "undefine", domainName, "--nvram", "--managed-save", "--snapshots-metadata", "--checkpoints-metadata"},
			fmt.Sprintf("%s:%s:%s:virsh-undefine", config.OS, config.UbuntuType, config.Arch),
		)
		if err != nil {
			err = runLinuxLibvirtCommandWithStreamingLogs(
				projectDir,
				linuxLibvirtCommandTimeout,
				"virsh",
				[]string{"--connect", linuxLibvirtURI(), "undefine", domainName},
				fmt.Sprintf("%s:%s:%s:virsh-undefine", config.OS, config.UbuntuType, config.Arch),
			)
			if err != nil {
				return fmt.Errorf("failed to undefine libvirt VM %q: %w", domainName, err)
			}
		}
	}

	diskPath := linuxLibvirtDiskPath(config)
	if err := os.Remove(diskPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove managed libvirt disk %q: %w", diskPath, err)
	}

	return nil
}

func inspectLinuxLibvirtStartTarget(config alchemy_build.VirtualMachineConfig) (StartTargetState, error) {
	if err := ensureLinuxLibvirtCommandsAvailable("virsh"); err != nil {
		return StartTargetState{}, err
	}
	state, err := linuxLibvirtDomainState(config)
	if err != nil {
		return StartTargetState{}, err
	}
	if state == "missing" {
		return StartTargetState{State: "missing"}, nil
	}

	return StartTargetState{
		Exists:  true,
		Running: linuxLibvirtStateIndicatesRunning(state),
		State:   state,
	}, nil
}

func linuxLibvirtDomainState(config alchemy_build.VirtualMachineConfig) (string, error) {
	output, err := runLinuxLibvirtCommandWithCombinedOut(
		alchemy_build.GetDirectoriesInstance().ProjectDir,
		linuxLibvirtCommandTimeout,
		"virsh",
		[]string{"--connect", linuxLibvirtURI(), "domstate", linuxLibvirtDomainName(config)},
	)
	if err != nil {
		if linuxLibvirtOutputIndicatesMissingDomain(output) {
			return "missing", nil
		}
		return "", fmt.Errorf("failed to inspect libvirt VM %q state: %w; output: %s", linuxLibvirtDomainName(config), err, strings.TrimSpace(output))
	}

	normalized := normalizeLinuxLibvirtState(output)
	if normalized == "" {
		return "unknown", nil
	}
	return normalized, nil
}

func waitForLinuxLibvirtStop(config alchemy_build.VirtualMachineConfig, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state, err := inspectLinuxLibvirtStartTarget(config)
		if err != nil {
			return false, err
		}
		if !state.Exists || !state.Running {
			return true, nil
		}
		time.Sleep(linuxLibvirtStopPollEvery)
	}

	state, err := inspectLinuxLibvirtStartTarget(config)
	if err != nil {
		return false, err
	}
	return !state.Exists || !state.Running, nil
}

func linuxLibvirtDomainName(config alchemy_build.VirtualMachineConfig) string {
	return fmt.Sprintf("%s-%s-dev-alchemy", alchemy_build.GetVirtualMachineNameWithType(config), config.Arch)
}

// LinuxLibvirtDomainName returns the managed libvirt domain name for a VM config.
func LinuxLibvirtDomainName(config alchemy_build.VirtualMachineConfig) string {
	return linuxLibvirtDomainName(config)
}

func linuxQemuArtifactPath(config alchemy_build.VirtualMachineConfig) string {
	if len(config.ExpectedBuildArtifacts) > 0 && strings.TrimSpace(config.ExpectedBuildArtifacts[0]) != "" {
		return config.ExpectedBuildArtifacts[0]
	}

	if config.OS == "windows11" {
		return alchemy_build.GetDirectoriesInstance().CachePath(
			"windows11",
			fmt.Sprintf("qemu-windows11-%s.qcow2", config.Arch),
		)
	}

	return alchemy_build.GetDirectoriesInstance().CachePath(
		"ubuntu",
		fmt.Sprintf("qemu-ubuntu-%s-packer-%s.qcow2", config.UbuntuType, config.Arch),
	)
}

func linuxLibvirtDiskPath(config alchemy_build.VirtualMachineConfig) string {
	return filepath.Join(linuxLibvirtImageDir(), linuxLibvirtDomainName(config)+".qcow2")
}

func linuxLibvirtURI() string {
	if override := strings.TrimSpace(os.Getenv(linuxLibvirtURIEnvVar)); override != "" {
		return override
	}
	return linuxLibvirtDefaultURI
}

// LinuxLibvirtURI returns the configured libvirt connection URI.
func LinuxLibvirtURI() string {
	return linuxLibvirtURI()
}

func linuxLibvirtImageDir() string {
	if override := linuxLibvirtImageDirOverride(); override != "" {
		return filepath.Clean(override)
	}
	if linuxLibvirtUsesSystemConnection(linuxLibvirtURI()) {
		return linuxLibvirtSystemImageDir
	}
	return filepath.Join(alchemy_build.GetDirectoriesInstance().AppDataDir, linuxLibvirtManagedDomainDirectory)
}

func linuxLibvirtImageDirOverride() string {
	return strings.TrimSpace(os.Getenv(linuxLibvirtImageDirEnvVar))
}

func ensureLinuxLibvirtImageDir(imageDir string) error {
	if err := os.MkdirAll(imageDir, linuxLibvirtImageDirPermission); err != nil {
		return err
	}
	if linuxLibvirtImageDirOverride() != "" {
		return nil
	}
	// #nosec G302 -- managed image directories intentionally allow libvirt group read/traversal (0750).
	if err := os.Chmod(imageDir, linuxLibvirtImageDirPermission); err != nil {
		return fmt.Errorf("failed to set managed libvirt image directory permissions to %04o: %w", linuxLibvirtImageDirPermission, err)
	}
	return nil
}

type linuxLibvirtStorageAccessError struct {
	storagePath string
	uid         string
	gid         string
}

func linuxLibvirtStorageAccessRepairHint(output string) string {
	accessErr, ok := parseLinuxLibvirtStorageAccessError(output)
	if !ok {
		return ""
	}

	imageDir := path.Dir(accessErr.storagePath)
	traversalDirs := linuxLibvirtManagedImageTraversalDirs(imageDir)
	quotedImageDir := linuxLibvirtShellQuote(imageDir)

	lines := []string{
		"",
		"",
		fmt.Sprintf("Libvirt cannot access the managed QCOW2 disk as uid:%s, gid:%s.", accessErr.uid, accessErr.gid),
		"Grant that libvirt group access without making the disk directory world-readable:",
		"",
	}
	if len(traversalDirs) > 0 {
		quotedTraversalDirs := make([]string, 0, len(traversalDirs))
		for _, dir := range traversalDirs {
			quotedTraversalDirs = append(quotedTraversalDirs, linuxLibvirtShellQuote(dir))
		}
		lines = append(lines, fmt.Sprintf("  sudo setfacl -m g:%s:x %s", accessErr.gid, strings.Join(quotedTraversalDirs, " ")))
	}
	lines = append(lines,
		fmt.Sprintf("  sudo setfacl -R -m g:%s:rwX %s", accessErr.gid, quotedImageDir),
		fmt.Sprintf("  sudo setfacl -d -m g:%s:rwX %s", accessErr.gid, quotedImageDir),
		"",
		"If setfacl is not installed:",
		"",
		"  sudo apt-get install acl",
	)

	return strings.Join(lines, "\n")
}

func parseLinuxLibvirtStorageAccessError(output string) (linuxLibvirtStorageAccessError, bool) {
	matches := linuxLibvirtStorageAccessErrorPattern.FindStringSubmatch(output)
	if len(matches) != 4 {
		return linuxLibvirtStorageAccessError{}, false
	}
	return linuxLibvirtStorageAccessError{
		storagePath: matches[1],
		uid:         matches[2],
		gid:         matches[3],
	}, true
}

func linuxLibvirtManagedImageTraversalDirs(imageDir string) []string {
	baseDir, ok := linuxLibvirtManagedImageBaseDir(imageDir)
	if !ok {
		return nil
	}
	return []string{
		baseDir,
		path.Join(baseDir, "libvirt"),
	}
}

func linuxLibvirtManagedImageBaseDir(imageDir string) (string, bool) {
	cleanImageDir := path.Clean(imageDir)
	managedSuffix := "/" + linuxLibvirtManagedDomainDirectory
	if !strings.HasSuffix(cleanImageDir, managedSuffix) {
		return "", false
	}

	baseDir := strings.TrimSuffix(cleanImageDir, managedSuffix)
	if baseDir == "" {
		return "", false
	}
	return baseDir, true
}

func linuxLibvirtShellQuote(value string) string {
	if linuxLibvirtShellSafePattern.MatchString(value) {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func linuxLibvirtUsesSystemConnection(uri string) bool {
	return strings.EqualFold(strings.TrimSpace(uri), "qemu:///system")
}

func ensureLinuxLibvirtCommandsAvailable(commands ...string) error {
	for _, command := range commands {
		if _, err := lookPathLinuxLibvirtCommand(command); err != nil {
			return fmt.Errorf(
				"required Linux libvirt command %q was not found in PATH; run `sailwright install` first or install the libvirt/QEMU host packages manually",
				command,
			)
		}
	}
	return nil
}

func linuxLibvirtVirtInstallArgs(config alchemy_build.VirtualMachineConfig, uri string, diskPath string) []string {
	args := []string{
		"--connect", uri,
		"--name", linuxLibvirtDomainName(config),
		"--memory", fmt.Sprintf("%d", alchemy_build.GetVmMemoryMB(config)),
		"--vcpus", fmt.Sprintf("%d", alchemy_build.GetVmCpuCount(config)),
		"--cpu", linuxLibvirtCPUArg(config),
		"--import",
		"--disk", fmt.Sprintf("path=%s,format=qcow2,bus=%s", diskPath, linuxLibvirtDiskBus(config)),
		"--network", linuxLibvirtNetworkArg(config, uri),
		"--graphics", "spice,clipboard.copypaste=on",
		"--video", linuxLibvirtVideoArg(config),
		"--controller", "type=usb,model=qemu-xhci",
		"--input", "tablet,bus=usb",
		"--input", "keyboard,bus=usb",
		"--channel", "unix,target.type=virtio,name=org.qemu.guest_agent.0",
		"--channel", "spicevmc,target.type=virtio,target.name=com.redhat.spice.0",
		"--rng", "/dev/urandom",
		"--os-variant", "generic",
		"--noautoconsole",
		"--print-xml",
	}

	if !linuxLibvirtUsesNativeArch(config) {
		args = append(args, "--virt-type", "qemu")
	}

	args = append(args, linuxLibvirtWindowsHypervArgs(config)...)

	switch config.Arch {
	case "arm64":
		args = append(args,
			"--arch", "aarch64",
			"--machine", "virt",
			"--boot", "uefi,menu=on",
		)
	default:
		args = append(args,
			"--arch", "x86_64",
			"--machine", "q35",
			"--boot", "hd,menu=on",
		)
	}

	return args
}

func linuxLibvirtNetworkArg(config alchemy_build.VirtualMachineConfig, uri string) string {
	model := linuxLibvirtNetworkModel(config)
	if linuxLibvirtUsesSystemConnection(uri) {
		return fmt.Sprintf("network=default,model=%s", model)
	}
	return fmt.Sprintf("user,model=%s", model)
}

func ensureLinuxLibvirtConfiguredNetworkReady(config alchemy_build.VirtualMachineConfig, uri string) error {
	networkName, ok := linuxLibvirtNamedNetwork(config, uri)
	if !ok {
		return nil
	}
	return ensureLinuxLibvirtNetworkReady(uri, networkName)
}

func linuxLibvirtNamedNetwork(config alchemy_build.VirtualMachineConfig, uri string) (string, bool) {
	networkArg := linuxLibvirtNetworkArg(config, uri)
	for _, part := range strings.Split(networkArg, ",") {
		name, ok := strings.CutPrefix(strings.TrimSpace(part), "network=")
		if ok && strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name), true
		}
	}
	return "", false
}

func ensureLinuxLibvirtNetworkReady(uri string, networkName string) error {
	output, err := runLinuxLibvirtCommandWithCombinedOut(
		alchemy_build.GetDirectoriesInstance().ProjectDir,
		linuxLibvirtCommandTimeout,
		"virsh",
		[]string{"--connect", uri, "net-info", networkName},
	)
	if err != nil {
		return fmt.Errorf(
			"failed to inspect libvirt network %q on %s: %w; output: %s; ensure the network exists, then enable it with `%s`",
			networkName,
			uri,
			err,
			strings.TrimSpace(output),
			linuxLibvirtNetworkStartCommand(uri, networkName),
		)
	}
	if !linuxLibvirtNetworkInfoIndicatesActive(output) {
		return fmt.Errorf(
			"libvirt network %q on %s is inactive; enable it with `%s`",
			networkName,
			uri,
			linuxLibvirtNetworkStartCommand(uri, networkName),
		)
	}
	return nil
}

func linuxLibvirtNetworkInfoIndicatesActive(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(key), "Active") {
			return strings.EqualFold(strings.TrimSpace(value), "yes")
		}
	}
	return false
}

func linuxLibvirtNetworkStartCommand(uri string, networkName string) string {
	virsh := "virsh"
	if linuxLibvirtUsesSystemConnection(uri) {
		virsh = "sudo virsh"
	}
	quotedURI := linuxLibvirtShellQuote(uri)
	quotedNetworkName := linuxLibvirtShellQuote(networkName)
	return fmt.Sprintf(
		"%s --connect %s net-start %s && %s --connect %s net-autostart %s",
		virsh,
		quotedURI,
		quotedNetworkName,
		virsh,
		quotedURI,
		quotedNetworkName,
	)
}

func linuxLibvirtNetworkModel(config alchemy_build.VirtualMachineConfig) string {
	if config.Arch == "amd64" {
		return "e1000"
	}
	return "virtio"
}

func linuxLibvirtDiskBus(config alchemy_build.VirtualMachineConfig) string {
	if config.OS == "windows11" && config.Arch == "amd64" {
		return "sata"
	}
	return "virtio"
}

func linuxLibvirtVideoArg(config alchemy_build.VirtualMachineConfig) string {
	if config.OS == "windows11" && config.Arch == "amd64" {
		return "model.type=qxl,model.ram=65536,model.vram=65536,model.vgamem=65536,model.heads=1"
	}
	return "model.type=virtio"
}

// linuxLibvirtWindowsHypervArgs enables Hyper-V enlightenments for KVM-accelerated
// Windows amd64 guests. Without them Windows falls back to slow spinlocks, APIC and
// timer emulation, which makes the desktop noticeably laggy.
//
// virt-install's --features only knows relaxed, vapic, spinlocks and synic on
// older releases (e.g. Ubuntu 24.04), so the remaining enlightenments are set
// through the generic --xml XPath option, which every virt-install >= 3.0 accepts.
func linuxLibvirtWindowsHypervArgs(config alchemy_build.VirtualMachineConfig) []string {
	if config.OS != "windows11" || config.Arch != "amd64" || !linuxLibvirtUsesNativeArch(config) {
		return nil
	}
	args := []string{
		"--features", strings.Join([]string{
			"acpi=on",
			"apic=on",
			"hyperv.relaxed.state=on",
			"hyperv.vapic.state=on",
			"hyperv.spinlocks.state=on",
			"hyperv.spinlocks.retries=8191",
			"hyperv.synic.state=on",
		}, ","),
	}
	for _, enlightenment := range []string{"vpindex", "runtime", "stimer", "frequencies", "tlbflush", "ipi"} {
		args = append(args, "--xml", fmt.Sprintf("./features/hyperv/%s/@state=on", enlightenment))
	}
	return append(args,
		"--clock", "hypervclock_present=yes,rtc_tickpolicy=catchup,pit_tickpolicy=delay,hpet_present=no",
	)
}

func linuxLibvirtCPUArg(config alchemy_build.VirtualMachineConfig) string {
	if linuxLibvirtUsesNativeArch(config) {
		return "host-passthrough"
	}

	switch config.Arch {
	case "amd64":
		return "Skylake-Client"
	case "arm64":
		if config.OS == "windows11" {
			return "max,sve=off,sme=off,pauth-impdef=on"
		}
		return "cortex-a57"
	default:
		return "max"
	}
}

func linuxLibvirtUsesNativeArch(config alchemy_build.VirtualMachineConfig) bool {
	hostArch, err := linuxLibvirtHostArch()
	if err != nil {
		return false
	}
	return config.Arch == hostArch
}

func linuxLibvirtHostArch() (string, error) {
	hostArch := linuxLibvirtRuntimeGOARCH()
	switch hostArch {
	case "amd64":
		return "amd64", nil
	case "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("linux libvirt deploy does not support host architecture %q", hostArch)
	}
}

func linuxLibvirtStateIndicatesRunning(state string) bool {
	switch normalizeLinuxLibvirtState(state) {
	case "", "missing", "shut off", "shutoff", "no state":
		return false
	default:
		return true
	}
}

func normalizeLinuxLibvirtState(state string) string {
	return strings.ToLower(strings.TrimSpace(state))
}

func linuxLibvirtOutputIndicatesMissingDomain(output string) bool {
	normalized := strings.ToLower(output)
	return strings.Contains(normalized, "failed to get domain") ||
		strings.Contains(normalized, "domain not found") ||
		strings.Contains(normalized, "failed to get domain '") ||
		strings.Contains(normalized, "domain not found:")
}

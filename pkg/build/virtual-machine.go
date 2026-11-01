package build

import (
	"path"
	"runtime"
	"sort"
	"strings"
)

type HostOsType string

const (
	HostOsLinux   HostOsType = "debian"
	HostOsWindows HostOsType = "windows"
	HostOsDarwin  HostOsType = "darwin"
)

type VirtualizationEngine string

const (
	VirtualizationEngineQemu       VirtualizationEngine = "qemu"
	VirtualizationEngineTart       VirtualizationEngine = "tart"
	VirtualizationEngineUtm        VirtualizationEngine = "utm"
	VirtualizationEngineHyperv     VirtualizationEngine = "hyperv"
	VirtualizationEngineVirtualBox VirtualizationEngine = "virtualbox"
)

type VirtualMachineConfig struct {
	OS                     string
	Arch                   string
	UbuntuType             string
	VncPort                int
	Slug                   string
	ExpectedBuildArtifacts []string
	StagedBuildArtifacts   []string
	NoCache                bool
	HostOs                 HostOsType
	VirtualizationEngine   VirtualizationEngine
	Cpus                   int
	// MemoryMB is the desired VM memory in megabytes.
	// When 0 (the default), memory is calculated automatically:
	// max(4096, totalSystemMemoryMB - 4096).
	MemoryMB int
	Headless bool
	Verbose  bool
}

func AvailableVirtualMachineConfigs() []VirtualMachineConfig {
	return []VirtualMachineConfig{
		{
			OS:                   "macos",
			Arch:                 "arm64",
			HostOs:               HostOsDarwin,
			VirtualizationEngine: VirtualizationEngineTart,
			Cpus:                 4,
			MemoryMB:             8192,
		},
		{
			OS:         "ubuntu",
			Arch:       "arm64",
			UbuntuType: "server",
			VncPort:    5901,
			ExpectedBuildArtifacts: []string{
				path.Join(GetDirectoriesInstance().CacheDir, "ubuntu/qemu-ubuntu-server-packer-arm64.qcow2"),
			},
			HostOs:               HostOsDarwin,
			VirtualizationEngine: VirtualizationEngineUtm,
			Cpus:                 8,
		},
		{
			OS:         "ubuntu",
			Arch:       "amd64",
			UbuntuType: "server",
			VncPort:    5902,
			ExpectedBuildArtifacts: []string{
				path.Join(GetDirectoriesInstance().CacheDir, "ubuntu/qemu-ubuntu-server-packer-amd64.qcow2"),
			},
			HostOs:               HostOsDarwin,
			VirtualizationEngine: VirtualizationEngineUtm,
			Cpus:                 4,
		},
		{
			OS:         "ubuntu",
			Arch:       "arm64",
			UbuntuType: "desktop",
			VncPort:    5903,
			ExpectedBuildArtifacts: []string{
				path.Join(GetDirectoriesInstance().CacheDir, "ubuntu/qemu-ubuntu-desktop-packer-arm64.qcow2"),
			},
			HostOs:               HostOsDarwin,
			VirtualizationEngine: VirtualizationEngineUtm,
			Cpus:                 8,
		},
		{
			OS:         "ubuntu",
			Arch:       "amd64",
			UbuntuType: "desktop",
			VncPort:    5904,
			ExpectedBuildArtifacts: []string{
				path.Join(GetDirectoriesInstance().CacheDir, "ubuntu/qemu-ubuntu-desktop-packer-amd64.qcow2"),
			},
			HostOs:               HostOsDarwin,
			VirtualizationEngine: VirtualizationEngineUtm,
			Cpus:                 4,
		},
		{
			OS:      "windows11",
			Arch:    "arm64",
			VncPort: 5911,
			ExpectedBuildArtifacts: []string{
				path.Join(GetDirectoriesInstance().CacheDir, "windows11/qemu-windows11-arm64.qcow2"),
			},
			HostOs:               HostOsDarwin,
			VirtualizationEngine: VirtualizationEngineUtm,
			Cpus:                 8,
		},
		{
			OS:      "windows11",
			Arch:    "amd64",
			VncPort: 5912,
			ExpectedBuildArtifacts: []string{
				path.Join(GetDirectoriesInstance().CacheDir, "windows11/qemu-windows11-amd64.qcow2"),
			},
			HostOs:               HostOsDarwin,
			VirtualizationEngine: VirtualizationEngineUtm,
			Cpus:                 4,
		},
		// Host OS Linux builds
		{
			OS:         "ubuntu",
			Arch:       "arm64",
			UbuntuType: "server",
			VncPort:    5921,
			ExpectedBuildArtifacts: []string{
				path.Join(GetDirectoriesInstance().CacheDir, "ubuntu/qemu-ubuntu-server-packer-arm64.qcow2"),
			},
			HostOs:               HostOsLinux,
			VirtualizationEngine: VirtualizationEngineQemu,
			Cpus:                 4,
			MemoryMB:             8192,
		},
		{
			OS:         "ubuntu",
			Arch:       "amd64",
			UbuntuType: "server",
			VncPort:    5922,
			ExpectedBuildArtifacts: []string{
				path.Join(GetDirectoriesInstance().CacheDir, "ubuntu/qemu-ubuntu-server-packer-amd64.qcow2"),
			},
			HostOs:               HostOsLinux,
			VirtualizationEngine: VirtualizationEngineQemu,
			Cpus:                 4,
			MemoryMB:             8192,
		},
		{
			OS:         "ubuntu",
			Arch:       "arm64",
			UbuntuType: "desktop",
			VncPort:    5923,
			ExpectedBuildArtifacts: []string{
				path.Join(GetDirectoriesInstance().CacheDir, "ubuntu/qemu-ubuntu-desktop-packer-arm64.qcow2"),
			},
			HostOs:               HostOsLinux,
			VirtualizationEngine: VirtualizationEngineQemu,
			Cpus:                 4,
			MemoryMB:             8192,
		},
		{
			OS:         "ubuntu",
			Arch:       "amd64",
			UbuntuType: "desktop",
			VncPort:    5924,
			ExpectedBuildArtifacts: []string{
				path.Join(GetDirectoriesInstance().CacheDir, "ubuntu/qemu-ubuntu-desktop-packer-amd64.qcow2"),
			},
			HostOs:               HostOsLinux,
			VirtualizationEngine: VirtualizationEngineQemu,
			Cpus:                 4,
			MemoryMB:             8192,
		},
		{
			OS:      "windows11",
			Arch:    "arm64",
			VncPort: 5931,
			ExpectedBuildArtifacts: []string{
				path.Join(GetDirectoriesInstance().CacheDir, "windows11/qemu-windows11-arm64.qcow2"),
			},
			HostOs:               HostOsLinux,
			VirtualizationEngine: VirtualizationEngineQemu,
			Cpus:                 4,
			MemoryMB:             8192,
		},
		{
			OS:      "windows11",
			Arch:    "amd64",
			VncPort: 5932,
			ExpectedBuildArtifacts: []string{
				path.Join(GetDirectoriesInstance().CacheDir, "windows11/qemu-windows11-amd64.qcow2"),
			},
			HostOs:               HostOsLinux,
			VirtualizationEngine: VirtualizationEngineQemu,
			Cpus:                 4,
			MemoryMB:             8192,
		},
		// Host OS Windows builds
		{
			OS:      "windows11",
			Arch:    "amd64",
			VncPort: 5912,

			ExpectedBuildArtifacts: []string{
				path.Join(GetDirectoriesInstance().CacheDir, "windows11/hyperv-windows11-amd64.box"),
			},
			HostOs:               HostOsWindows,
			VirtualizationEngine: VirtualizationEngineHyperv,
			Cpus:                 4,
			MemoryMB:             8192,
		},
		{
			OS:         "ubuntu",
			Arch:       "amd64",
			UbuntuType: "server",
			VncPort:    5914,
			ExpectedBuildArtifacts: []string{
				path.Join(GetDirectoriesInstance().CacheDir, "ubuntu/hyperv-ubuntu-server-amd64.box"),
			},
			HostOs:               HostOsWindows,
			VirtualizationEngine: VirtualizationEngineHyperv,
			Cpus:                 4,
			MemoryMB:             8192,
		},
		{
			OS:         "ubuntu",
			Arch:       "amd64",
			UbuntuType: "desktop",
			VncPort:    5915,
			ExpectedBuildArtifacts: []string{
				path.Join(GetDirectoriesInstance().CacheDir, "ubuntu/hyperv-ubuntu-desktop-amd64.box"),
			},
			HostOs:               HostOsWindows,
			VirtualizationEngine: VirtualizationEngineHyperv,
			Cpus:                 4,
			MemoryMB:             8192,
		},
		{
			OS:      "windows11",
			Arch:    "amd64",
			VncPort: 5913,

			ExpectedBuildArtifacts: []string{
				path.Join(GetDirectoriesInstance().CacheDir, "windows11/virtualbox-windows11-amd64.box"),
			},
			HostOs:               HostOsWindows,
			VirtualizationEngine: VirtualizationEngineVirtualBox,
			Cpus:                 4,
			MemoryMB:             8192,
		},
	}
}

func GetCurrentHostOs() HostOsType {
	switch runtime.GOOS {
	case "linux":
		return HostOsLinux
	case "windows":
		return HostOsWindows
	case "darwin":
		return HostOsDarwin
	default:
		return HostOsLinux
	}
}

func AvailableVirtualMachineConfigsForCurrentHostOS() []VirtualMachineConfig {
	return AvailableVirtualMachineConfigsForHostOS(GetCurrentHostOs())
}

func AvailableVirtualMachineConfigsForHostOS(hostOs HostOsType) []VirtualMachineConfig {
	var configs []VirtualMachineConfig
	for _, config := range AvailableVirtualMachineConfigs() {
		if config.HostOs == hostOs {
			configs = append(configs, config)
		}
	}
	return configs
}

func AvailableVirtualMachineConfigsForCurrentHostOSByVirtualizationEngine() map[VirtualizationEngine][]VirtualMachineConfig {
	return GroupVirtualMachineConfigsByVirtualizationEngine(AvailableVirtualMachineConfigsForCurrentHostOS())
}

func CurrentHostVirtualizationEngines() []VirtualizationEngine {
	return VirtualizationEnginesForVirtualMachineConfigs(AvailableVirtualMachineConfigsForCurrentHostOS())
}

func GroupVirtualMachineConfigsByVirtualizationEngine(configs []VirtualMachineConfig) map[VirtualizationEngine][]VirtualMachineConfig {
	grouped := make(map[VirtualizationEngine][]VirtualMachineConfig)
	for _, config := range configs {
		grouped[config.VirtualizationEngine] = append(grouped[config.VirtualizationEngine], config)
	}
	return grouped
}

func VirtualizationEnginesForVirtualMachineConfigs(configs []VirtualMachineConfig) []VirtualizationEngine {
	engineSet := make(map[VirtualizationEngine]struct{})
	for _, config := range configs {
		engineSet[config.VirtualizationEngine] = struct{}{}
	}

	engines := make([]VirtualizationEngine, 0, len(engineSet))
	for engine := range engineSet {
		engines = append(engines, engine)
	}
	sort.Slice(engines, func(i, j int) bool {
		return engines[i] < engines[j]
	})
	return engines
}

func IsVirtualizationEngineUnstable(engine VirtualizationEngine) bool {
	switch engine {
	case VirtualizationEngineVirtualBox:
		return true
	default:
		return false
	}
}

func DisplayVirtualizationEngine(engine VirtualizationEngine) string {
	if IsVirtualizationEngineUnstable(engine) {
		return string(engine) + " (unstable)"
	}
	return string(engine)
}

func GenerateVirtualMachineSlug(config *VirtualMachineConfig) string {
	if config.Slug != "" {
		return config.Slug
	}

	slug := strings.ToLower(config.OS)
	if config.UbuntuType != "" {
		slug += "-" + strings.ToLower(config.UbuntuType)
	}
	slug += "-" + strings.ToLower(config.Arch)
	config.Slug = slug
	return slug
}

func GetVirtualMachineNameWithType(config VirtualMachineConfig) string {
	switch config.OS {
	case "ubuntu":
		if config.UbuntuType != "" {
			return config.OS + "-" + config.UbuntuType
		}
		return config.OS
	case "windows11":
		return config.OS
	case "macos":
		return config.OS
	default:
		return "Unknown OS"
	}
}

// OSHasTypes reports whether osName has typed targets (such as ubuntu server and
// desktop). It is derived from the VM configurations, so commands, locks and the
// gateway agree on which targets carry a type.
func OSHasTypes(osName string) bool {
	for _, config := range AvailableVirtualMachineConfigs() {
		if config.OS == osName && config.UbuntuType != "" {
			return true
		}
	}
	return false
}

// TargetType returns the type a command uses for osName: requested for OSes with
// typed targets and empty for all others, including "all" and "local".
func TargetType(osName, requested string) string {
	if !OSHasTypes(osName) {
		return ""
	}
	return requested
}

package build

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

const (
	minVmMemoryMB       = 4096 // 4 GB minimum
	osHeadroomDarwinMB  = 4096 // 4 GB reserved for the host OS
	osHeadroomWindowsMB = 8192 // 8 GB reserved for the host OS
)

// getVmCpuCountInt determines the CPU count to allocate to the VM.
//
// Logic:
//  1. If config.Cpus is non-zero it is used, capped at the host's logical CPU count.
//  2. Otherwise all available host CPUs are used (auto mode).
func getVmCpuCountInt(config VirtualMachineConfig) int {
	system_cpus := runtime.NumCPU()
	if config.Cpus == 0 {
		return system_cpus
	}
	if config.Cpus > system_cpus {
		return system_cpus
	}
	return config.Cpus
}

func getVmCpuCountString(config VirtualMachineConfig) string {
	return strconv.Itoa(getVmCpuCountInt(config))
}

// GetVmCpuCount returns the resolved CPU count for the given VM config.
func GetVmCpuCount(config VirtualMachineConfig) int {
	return getVmCpuCountInt(config)
}

// getVmMemoryMB determines the memory to allocate to the VM in megabytes.
//
// Logic:
//  1. If config.MemoryMB is non-zero it is used as-is (explicit override).
//  2. Otherwise the total physical host memory is queried and the VM receives
//     all memory minus osHeadroomMB (4 GB) reserved for the host OS.
//  3. The result is always at least minVmMemoryMB (4 GB).
func getVmMemoryMB(config VirtualMachineConfig) int {
	if config.MemoryMB > 0 {
		return config.MemoryMB
	}

	totalMB, err := getSystemTotalMemoryMB()
	if err != nil || totalMB == 0 {
		return minVmMemoryMB
	}

	osHeadroomMB := osHeadroomDarwinMB
	if config.HostOs == HostOsWindows {
		osHeadroomMB = osHeadroomWindowsMB
	}

	vmMemory := int(totalMB) - osHeadroomMB // #nosec G115 -- Windows hosts cannot report physical memory large enough to overflow int on supported targets.
	if vmMemory < minVmMemoryMB {
		vmMemory = minVmMemoryMB
	}

	return vmMemory
}

// GetVmMemoryMB returns the resolved memory allocation in megabytes for the given VM config.
func GetVmMemoryMB(config VirtualMachineConfig) int {
	return getVmMemoryMB(config)
}

func getTempDiskPathForHypervBuild() string {
	// we can use the temp disk like (D:\) if available to speed up the build process
	// determine an env variable to check for the temp disk path, default to empty string if not set
	temp_disk_path := ""
	if tempPath := os.Getenv("PACKER_TEMP_PATH"); tempPath != "" {
		temp_disk_path = tempPath
	}
	return temp_disk_path
}

// createHypervTempDir ensures the temp directory for the Hyper-V packer build exists.
// It mirrors the same logic used in the packer locals block for temp_dir.
func createHypervTempDir(dirs *Directories) error {
	tempPath := getTempDiskPathForHypervBuild()
	if tempPath == "" {
		tempPath = filepath.Join(dirs.CacheDir, "windows11", "hyperv-temp")
	}
	// #nosec G301 -- this cache directory must remain traversable for non-root CI steps after sudo-created builds.
	return os.MkdirAll(tempPath, 0755)
}

// getQemuBuildOutputDir is Packer's scratch directory. It lives in the cache so it is
// covered by the same disk and cleanup rules as the artifacts; the build scripts keep
// the QMP socket on a short path under /tmp instead and remove the directory after a
// successful build.
func getQemuBuildOutputDir(config VirtualMachineConfig) string {
	return GetDirectoriesInstance().CachePath(config.OS, "qemu-out-"+GenerateVirtualMachineSlug(&config))
}

func getDarwinQemuBuildOutputDir(config VirtualMachineConfig) string {
	return getQemuBuildOutputDir(config)
}

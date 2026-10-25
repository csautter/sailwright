package build

import (
	"fmt"
	"log"
	"strings"
	"time"
)

const (
	packerExecutable     = "packer"
	hypervPackerFile     = "build/packer/windows/windows11-on-windows-hyperv.pkr.hcl"
	virtualBoxPackerFile = "build/packer/windows/windows11-on-windows-virtualbox.pkr.hcl"
)

// RunHypervWindowsBuildOnWindows builds a Windows 11 VM using Hyper-V on Windows.
// It retries early failures to handle the transient "No ip address" race condition in the
// Packer HyperV plugin: StepRun calls GetHostAdapterIpAddressForSwitch with no retry, and
// attaching a new VM to the Default Switch can briefly disrupt the host adapter's IPv4 address.
// This failure always occurs within the first ~30 seconds; real build failures take 30+ minutes.
// Retries are skipped for long-running failures to avoid wasting CI time on genuine errors.
func RunHypervWindowsBuildOnWindows(config VirtualMachineConfig) error {
	const maxRetries = 3
	const retryDelay = 30 * time.Second
	// Failures under this threshold are likely the transient IP detection race.
	// Real errors (WinRM timeout, provisioner failure, etc.) occur after many minutes.
	const earlyFailureThreshold = 2 * time.Minute

	if err := createHypervTempDir(GetDirectoriesInstance()); err != nil {
		return fmt.Errorf("failed to create hyperv temp directory: %w", err)
	}

	// Prepare once outside the retry loop: packer init failures are not the IP race and
	// must not burn build attempts. initializePacker retries network errors on its own.
	config, err := prepareWindowsBuild(config, hypervPackerFile)
	if err != nil {
		return err
	}

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		captureHypervDiagnostics(config, fmt.Sprintf("attempt-%d-preflight", attempt), nil)

		start := time.Now()
		lastErr = runPackerBuild(config, hypervPackerFile)
		if lastErr == nil {
			return nil
		}

		captureHypervDiagnostics(config, fmt.Sprintf("attempt-%d-failure", attempt), lastErr)

		elapsed := time.Since(start)
		if elapsed >= earlyFailureThreshold {
			// Long-running failure — not the IP race condition, don't retry.
			return fmt.Errorf("HyperV build failed after %.0fs (not retrying): %w", elapsed.Seconds(), lastErr)
		}
		if attempt < maxRetries {
			log.Printf("HyperV build attempt %d/%d failed after %.0fs (likely transient IP detection race): %v. Retrying in %s...",
				attempt, maxRetries, elapsed.Seconds(), lastErr, retryDelay)
			time.Sleep(retryDelay)
		}
	}
	return fmt.Errorf("HyperV build failed after %d attempts: %w", maxRetries, lastErr)
}

// RunVirtualBoxWindowsBuildOnWindows builds a Windows 11 VM using VirtualBox on Windows.
func RunVirtualBoxWindowsBuildOnWindows(config VirtualMachineConfig) error {
	return runWindowsBuild(config, virtualBoxPackerFile)
}

// runWindowsBuild executes the Packer build process for Windows VMs.
func runWindowsBuild(config VirtualMachineConfig, packerFile string) error {
	config, err := prepareWindowsBuild(config, packerFile)
	if err != nil {
		return err
	}
	return runPackerBuild(config, packerFile)
}

// prepareWindowsBuild stages no-cache artifacts and initializes Packer plugins.
func prepareWindowsBuild(config VirtualMachineConfig, packerFile string) (VirtualMachineConfig, error) {
	config, err := withStagedBuildArtifactsForNoCache(config)
	if err != nil {
		return config, err
	}
	if err := initializePacker(config, packerFile); err != nil {
		return config, fmt.Errorf("failed to initialize packer: %w", err)
	}
	return config, nil
}

// runPackerBuild runs packer build for an already initialized packer file.
func runPackerBuild(config VirtualMachineConfig, packerFile string) error {
	return RunBuildScript(config, packerExecutable, buildPackerArgs(config, packerFile))
}

// packerInitRetryDelays are the waits between packer init attempts after a network error.
var packerInitRetryDelays = []time.Duration{10 * time.Second, 30 * time.Second}

// runPackerInitCommand is a seam for tests.
var runPackerInitCommand = RunCliCommandWithEnv

// packerInitNetworkErrorMarkers identify packer init output caused by network problems
// while downloading plugins, which are worth retrying.
var packerInitNetworkErrorMarkers = []string{
	"forcibly closed by the remote host",
	"connection reset by peer",
	"connection refused",
	"i/o timeout",
	"tls handshake timeout",
	"unexpected eof",
	"no such host",
	"network is unreachable",
	"server misbehaving",
	"502 bad gateway",
	"503 service unavailable",
	"504 gateway timeout",
}

// packerInitReleasesFallbackMarker appears when packer could not reach releases.hashicorp.com
// and fell back to GitHub, where official HashiCorp plugins may not publish release assets.
const packerInitReleasesFallbackMarker = "could not get sha256 checksum file"

// initializePacker runs the packer init command for the given file, retrying network errors.
func initializePacker(config VirtualMachineConfig, packerFile string) error {
	env := GetDirectoriesInstance().ManagedEnv()
	if config.Verbose {
		env = append(env, "PACKER_LOG=1")
	}

	maxAttempts := len(packerInitRetryDelays) + 1
	for attempt := 1; ; attempt++ {
		output, err := runPackerInitCommand(GetDirectoriesInstance().ProjectDir, packerExecutable, []string{"init", packerFile}, env)
		if err == nil {
			return nil
		}

		retryable := isRetryablePackerInitOutput(string(output))
		if !retryable || attempt >= maxAttempts {
			err = fmt.Errorf("packer init failed for %s: %w", packerFile, err)
			if strings.Contains(strings.ToLower(string(output)), packerInitReleasesFallbackMarker) {
				err = fmt.Errorf("%w (packer likely could not reach releases.hashicorp.com and fell back to GitHub release assets; check HTTPS/IPv6 connectivity to releases.hashicorp.com)", err)
			}
			return err
		}

		delay := packerInitRetryDelays[attempt-1]
		log.Printf("packer init attempt %d/%d failed with a network error: %v. Retrying in %s...", attempt, maxAttempts, err, delay)
		time.Sleep(delay)
	}
}

func isRetryablePackerInitOutput(output string) bool {
	output = strings.ToLower(output)
	if strings.Contains(output, packerInitReleasesFallbackMarker) {
		return true
	}
	for _, marker := range packerInitNetworkErrorMarkers {
		if strings.Contains(output, marker) {
			return true
		}
	}
	return false
}

// buildPackerArgs constructs the command-line arguments for the Packer build command.
func buildPackerArgs(config VirtualMachineConfig, packerFile string) []string {
	args := []string{"build"}

	// Add temp disk path if configured
	if tempDiskPath := getTempDiskPathForHypervBuild(); tempDiskPath != "" {
		args = append(args, "-var", fmt.Sprintf("temp_disk_path=%s", tempDiskPath))
	}

	// Add ISO URL, CPU count, memory, and Packer file
	args = append(args,
		"-var", fmt.Sprintf("iso_url=%s", windows11ISOPath("amd64")),
		"-var", fmt.Sprintf("cache_dir=%s", GetDirectoriesInstance().CacheDir),
		"-var", fmt.Sprintf("cpus=%s", getVmCpuCountString(config)),
		"-var", fmt.Sprintf("memory=%d", getVmMemoryMB(config)),
	)
	if stagedArtifact, ok := firstStagedBuildArtifact(config); ok {
		args = append(args, "-var", fmt.Sprintf("artifact_output_path=%s", stagedArtifact))
	}
	args = append(args, packerFile)

	return args
}

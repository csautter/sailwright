package build

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/csautter/dev-alchemy/internal/winpsenv"
)

const (
	auxiliaryLogSilenceStartMarker = "__DEV_ALCHEMY_SILENT_HELPERS_ON__"
	auxiliaryLogSilenceEndMarker   = "__DEV_ALCHEMY_SILENT_HELPERS_OFF__"
	buildScriptTimeout             = 5 * time.Hour
)

type buildCompletionDecision struct {
	err          error
	runFfmpeg    bool
	buildSuccess bool
	interrupted  bool
}

// buildProcessGracePeriod is how long a build's process group may take to stop after
// SIGTERM before it is killed. It stays well below the 10 s the agent gateway waits.
const buildProcessGracePeriod = 3 * time.Second

// RunBuildScript runs a build script with the managed environment plus extraEnv.
// Cancelling ctx stops dependency downloads and the build's whole process group; the
// staged artifact is then discarded, so the final artifact path never holds a partial
// build.
func RunBuildScript(ctx context.Context, config VirtualMachineConfig, executable string, args []string, extraEnv ...string) (err error) {
	ctx, stopInterruptHandling := ensureInterruptHandling(ctx)
	defer stopInterruptHandling()
	if err := buildContextError(ctx); err != nil {
		return err
	}

	skipBuild, cleanupArtifacts, err := prepareBuildArtifactsForBuild(config)
	if err != nil {
		return err
	}
	if skipBuild {
		return nil
	}
	artifactsSettled := false
	settleArtifacts := func(success bool) error {
		if artifactsSettled {
			return nil
		}
		artifactsSettled = true
		return cleanupArtifacts(success)
	}
	defer func() {
		if cleanupErr := settleArtifacts(false); cleanupErr != nil {
			if err == nil {
				err = cleanupErr
			} else {
				log.Printf("Build artifact cleanup failed after build error: %v", cleanupErr)
			}
		}
	}()
	defer restoreInteractiveTerminal()

	// Ensure all required dependencies are present
	if err := DependencyReconciliation(ctx, config); err != nil {
		return err
	}
	if err := buildContextError(ctx); err != nil {
		return err
	}

	// Check if VNC port is free, if not, increment until a free port is found
	_ = getFreeVncPort(&config)

	timeout := buildScriptTimeout
	buildCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	auxiliaryProcessSilent := &atomic.Bool{}

	printCurrentWorkingDirectory()

	fmt.Printf("Running Build with executable %s and args %v\n", executable, sanitizeCommandArgs(args))
	cmd, terminal := newBuildCommand(executable, args, config.DetachedFromTerminal)
	defer terminal.restore()
	cmd.Dir = GetDirectoriesInstance().GetDirectories().ProjectDir
	cmd.Env = winpsenv.CommandEnv(executable, GetDirectoriesInstance().ManagedEnv())
	cmd.Env = append(cmd.Env, extraEnv...)
	if config.Verbose {
		cmd.Env = append(cmd.Env, "PACKER_LOG=1")
	}

	if err := readAndPrintStdoutStderr(cmd, config, auxiliaryProcessSilent); err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start build command %s: %w", executable, err)
	}
	processGroupID := commandProcessGroupID(cmd)
	untrackProcessGroup := trackProcessGroup(processGroupID)
	defer untrackProcessGroup()

	done := make(chan error, 1)
	go func() {
		log.Printf("Waiting for command to finish...")
		err := cmd.Wait()
		log.Printf("Command finished.")
		done <- err
	}()

	vnc_recording_config := VncRecordingConfig{Password: "packer"}
	openVncViewerOnMacOSHost(buildCtx, config, vnc_recording_config, auxiliaryProcessSilent)

	// Start Screen Capture to record the VM build process
	vnc_snapshot_done := make(chan struct{})
	vnc_interrupt_retry_chan := make(chan bool)

	startVncScreenCaptureOnSupportedHost(buildCtx, config, timeout, vnc_interrupt_retry_chan, &vnc_recording_config, vnc_snapshot_done, auxiliaryProcessSilent)

	var waitErr error
	select {
	case waitErr = <-done:
	case <-buildCtx.Done():
		log.Printf("Stopping build process group: %v", context.Cause(buildCtx))
		terminateProcessGroup(processGroupID, buildProcessGracePeriod)
		waitErr = <-done
	}
	// Take the terminal back right away, so that Ctrl+C reaches this process again
	// during cleanup and post-processing.
	terminal.restore()
	if terminal.attached && isTerminalInterruptExit(waitErr) {
		// Ctrl+C went to the build that owned the terminal; handle it like our own.
		reportInterrupt(ctx)
	}
	if buildCtx.Err() != nil {
		// Members of the group that survived the leader, e.g. a stopped QEMU.
		terminateProcessGroup(processGroupID, buildProcessGracePeriod)
	}

	stopVncScreenCaptureOnSupportedHost(vnc_interrupt_retry_chan)
	decision := determineBuildCompletionDecision(waitErr, buildCtx.Err(), Interrupted(ctx))

	// Settle the artifacts before post-processing: a supervisor that kills this process
	// during the VNC video post-processing must not find a promoted partial artifact.
	if cleanupErr := settleArtifacts(decision.buildSuccess); cleanupErr != nil {
		if decision.err == nil {
			decision.err = cleanupErr
		} else {
			log.Printf("Build artifact cleanup failed after build error: %v", cleanupErr)
		}
	}

	if decision.runFfmpeg {
		if decision.interrupted {
			log.Printf("Build interrupted; generating VNC video before exit. Press Ctrl+C again to skip remaining post-processing.")
		}
		runFfmpegOnSupportedHost(postProcessingContext(ctx), vnc_snapshot_done, config, &vnc_recording_config)
	}

	switch {
	case decision.err == nil:
		log.Printf("Script finished successfully.")
	case decision.interrupted:
		log.Printf("Script terminated due to interruption: %v", decision.err)
	case errors.Is(decision.err, context.DeadlineExceeded):
		log.Printf("Script terminated due to timeout: %v", decision.err)
	default:
		log.Printf("Script failed: %v", decision.err)
	}
	return decision.err
}

// buildContextError returns the error for a build that must not start or continue.
func buildContextError(ctx context.Context) error {
	if ctx.Err() == nil {
		return nil
	}
	if Interrupted(ctx) {
		return fmt.Errorf("build %w", ErrInterrupted)
	}
	return ctx.Err()
}

func determineBuildCompletionDecision(waitErr error, ctxErr error, interrupted bool) buildCompletionDecision {
	if interrupted {
		// Keep the VNC video of an interrupted build; a second Ctrl+C skips it.
		return buildCompletionDecision{
			err:         fmt.Errorf("build %w", ErrInterrupted),
			runFfmpeg:   true,
			interrupted: true,
		}
	}
	if ctxErr != nil {
		return buildCompletionDecision{
			err:       ctxErr,
			runFfmpeg: false,
		}
	}
	if waitErr != nil {
		return buildCompletionDecision{
			err:       waitErr,
			runFfmpeg: true,
		}
	}
	return buildCompletionDecision{
		runFfmpeg:    true,
		buildSuccess: true,
	}
}

func resolveExpectedBuildArtifacts(config VirtualMachineConfig) ([]string, error) {
	if len(config.ExpectedBuildArtifacts) > 0 {
		return config.ExpectedBuildArtifacts, nil
	}

	for _, vm := range AvailableVirtualMachineConfigs() {
		if string(vm.HostOs) == string(config.HostOs) && vm.OS == config.OS && vm.UbuntuType == config.UbuntuType && vm.Arch == config.Arch && string(vm.VirtualizationEngine) == string(config.VirtualizationEngine) {
			return vm.ExpectedBuildArtifacts, nil
		}
	}

	return nil, errors.New("no build artifacts defined for the given configuration")
}

func hostSupportsVncRecording(goos string) bool {
	return goos != "windows"
}

func hostSupportsVncViewer(goos string) bool {
	return goos == "darwin"
}

func stopVncScreenCaptureOnSupportedHost(vnc_interrupt_retry_chan chan bool) {
	if !hostSupportsVncRecording(runtime.GOOS) {
		return
	}
	log.Printf("stopping VNC snapshot...")
	select {
	case vnc_interrupt_retry_chan <- true:
	default:
		// VNC goroutine already exited (e.g. vncsnapshot finished successfully),
		// so there is no receiver on the channel. Skip the send to avoid deadlock.
		log.Printf("VNC snapshot already stopped, skipping interrupt signal.")
	}
	log.Printf("VNC snapshot stopped.")
}

// FFmpeg integration:
// - FFmpeg is useful for generating a video from the VNC recording, allowing playback and sharing of the build process.
// postCtx is cancelled by a second interrupt, which skips the remaining post-processing.
func runFfmpegOnSupportedHost(postCtx context.Context, vnc_snapshot_done chan struct{}, config VirtualMachineConfig, vnc_recording_config *VncRecordingConfig) {
	if !hostSupportsVncRecording(runtime.GOOS) {
		return
	}
	select {
	case <-vnc_snapshot_done:
	case <-postCtx.Done():
		log.Printf("Skipping VNC video post-processing: %v", context.Cause(postCtx))
		return
	}
	// Always run ffmpeg after vnc_snapshot is done
	timeout := 10 * time.Minute
	ctx, cancel := context.WithTimeout(postCtx, timeout)
	defer cancel()
	RunFfmpegVideoGenerationProcess(config, ctx, RunProcessConfig{Timeout: timeout}, vnc_recording_config)
}

func startVncScreenCaptureOnSupportedHost(ctx context.Context, config VirtualMachineConfig, timeout time.Duration, vnc_interrupt_retry_chan chan bool, vnc_recording_config *VncRecordingConfig, vnc_snapshot_done chan struct{}, auxiliaryProcessSilent *atomic.Bool) {
	if !hostSupportsVncRecording(runtime.GOOS) {
		return
	}
	go func() {
		vnc_snapshot_ctx := RunVncSnapshotProcess(config, ctx, RunProcessConfig{Timeout: timeout, Retries: 30, InterruptRetryChan: vnc_interrupt_retry_chan, RetryInterval: 10 * time.Second, Silent: auxiliaryProcessSilent}, vnc_recording_config)
		if vnc_snapshot_ctx != nil {
			<-vnc_snapshot_ctx.Done()
		}
		close(vnc_snapshot_done)
	}()
}

// VNC integration:
// - Opening a VNC viewer (Screen Sharing) is useful for observing the VM build process in real time.
// - VNC recording enables capturing the build process for later review or debugging.
func openVncViewerOnMacOSHost(ctx context.Context, config VirtualMachineConfig, vnc_recording_config VncRecordingConfig, auxiliaryProcessSilent *atomic.Bool) {
	if !hostSupportsVncViewer(runtime.GOOS) {
		return
	}
	go func() {
		config := RunProcessConfig{
			ExecutablePath:   "open",
			Args:             []string{"-a", "Screen Sharing", fmt.Sprintf("vnc://:%s@localhost:%d", vnc_recording_config.Password, config.VncPort)},
			Timeout:          5 * time.Minute,
			WorkingDir:       "",
			Context:          ctx,
			FailOnError:      false,
			Retries:          5,
			RetryInterval:    time.Minute,
			DelayBeforeStart: time.Minute,
			Silent:           auxiliaryProcessSilent,
		}
		RunExternalProcessWithRetries(config)
	}()
}

func readAndPrintStdoutStderr(cmd *exec.Cmd, config VirtualMachineConfig, auxiliaryProcessSilent *atomic.Bool) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout of build command: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr of build command: %w", err)
	}

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			logBuildOutputLine(scanner.Text(), "stdout", config, auxiliaryProcessSilent)
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			logBuildOutputLine(scanner.Text(), "stderr", config, auxiliaryProcessSilent)
		}
	}()
	return nil
}

func logBuildOutputLine(line string, streamName string, config VirtualMachineConfig, auxiliaryProcessSilent *atomic.Bool) {
	switch line {
	case auxiliaryLogSilenceStartMarker:
		if auxiliaryProcessSilent != nil {
			auxiliaryProcessSilent.Store(true)
		}
		return
	case auxiliaryLogSilenceEndMarker:
		if auxiliaryProcessSilent != nil {
			auxiliaryProcessSilent.Store(false)
		}
		return
	}

	log.Printf("%s:%s:%s %s:  %s", config.OS, config.UbuntuType, config.Arch, streamName, sanitizeSensitiveText(line))
}

func printCurrentWorkingDirectory() {
	cwd, err := os.Getwd()
	if err != nil {
		log.Printf("Failed to get current working directory: %v", err)
		return
	}
	log.Printf("Current working directory: %s", cwd)
}

func getFreeVncPort(config *VirtualMachineConfig) int {
	port := config.VncPort
	for {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			if closeErr := ln.Close(); closeErr != nil {
				log.Printf("Failed to release test listener on %s: %v", addr, closeErr)
			}
			break
		}
		port++
	}
	config.VncPort = port
	log.Printf("Using VNC port: %d", config.VncPort)
	return port
}

func checkIfBuildArtifactsExist(config VirtualMachineConfig) (bool, error) {
	return BuildArtifactsExist(config)
}

type buildArtifactBackup struct {
	originalPath string
	backupPath   string
}

func deferBuildArtifactCleanup(cleanup func(bool) error, buildSucceeded *bool) func() {
	return func() {
		if err := cleanup(*buildSucceeded); err != nil {
			log.Printf("Build artifact cleanup failed: %v", err)
		}
	}
}

func prepareBuildArtifactsForBuild(config VirtualMachineConfig) (bool, func(bool) error, error) {
	if !config.NoCache {
		buildArtifactExists, err := checkIfBuildArtifactsExist(config)
		if err != nil {
			return false, nil, err
		}
		if buildArtifactExists {
			return true, func(bool) error { return nil }, nil
		}
	}

	artifacts, err := resolveExpectedBuildArtifacts(config)
	if err != nil {
		return false, nil, err
	}
	if err := removeBuildArtifactLeftovers(artifacts); err != nil {
		return false, nil, err
	}

	if len(config.StagedBuildArtifacts) > 0 {
		stagedArtifacts, err := validateStagedBuildArtifacts(artifacts, config.StagedBuildArtifacts)
		if err != nil {
			return false, nil, err
		}
		if err := removeBuildArtifacts(stagedArtifacts); err != nil {
			return false, nil, err
		}
		cleanup := func(success bool) error {
			if success {
				log.Printf("Promoting staged build artifact(s) after successful build: %v -> %v", stagedArtifacts, artifacts)
				if err := promoteStagedBuildArtifacts(artifacts, stagedArtifacts); err != nil {
					return err
				}
				return MarkBuildArtifactsComplete(artifacts)
			}
			log.Printf("Removing staged build artifact(s) after failed or interrupted build: %v", stagedArtifacts)
			return removeBuildArtifacts(stagedArtifacts)
		}

		return false, cleanup, nil
	}

	if err := RemoveBuildArtifactCompletionMarkers(artifacts); err != nil {
		return false, nil, err
	}
	backups, err := backupBuildArtifacts(artifacts)
	if err != nil {
		return false, nil, err
	}

	cleanup := func(success bool) error {
		if success {
			return errors.Join(MarkBuildArtifactsComplete(artifacts), removeBackedUpArtifacts(backups))
		}
		return errors.Join(removeBuildArtifacts(artifacts), restoreBackedUpArtifacts(backups))
	}

	return false, cleanup, nil
}

// withStagedBuildArtifacts makes the build write its artifacts to staging paths next
// to the final ones. They are renamed to the final paths only after a successful build,
// so an interrupted or failed build never leaves a partial artifact at the final path.
func withStagedBuildArtifacts(config VirtualMachineConfig) (VirtualMachineConfig, error) {
	if len(config.StagedBuildArtifacts) > 0 {
		return config, nil
	}

	artifacts, err := resolveExpectedBuildArtifacts(config)
	if err != nil {
		return config, err
	}

	stagedArtifacts := make([]string, 0, len(artifacts))
	uniqueSuffix := time.Now().UnixNano()
	for _, artifact := range artifacts {
		stagedArtifacts = append(stagedArtifacts, stagedBuildArtifactPath(artifact, uniqueSuffix))
	}
	config.StagedBuildArtifacts = stagedArtifacts
	return config, nil
}

func stagedBuildArtifactPath(artifact string, uniqueSuffix int64) string {
	dir := filepath.Dir(artifact)
	base := filepath.Base(artifact)
	ext := filepath.Ext(base)
	stem := base[:len(base)-len(ext)]
	return filepath.Join(dir, fmt.Sprintf("%s%s%d%s", stem, stagedBuildArtifactInfix, uniqueSuffix, ext))
}

func firstStagedBuildArtifact(config VirtualMachineConfig) (string, bool) {
	if len(config.StagedBuildArtifacts) == 0 {
		return "", false
	}
	return config.StagedBuildArtifacts[0], true
}

func validateStagedBuildArtifacts(finalArtifacts []string, stagedArtifacts []string) ([]string, error) {
	if len(stagedArtifacts) != len(finalArtifacts) {
		return nil, fmt.Errorf("staged build artifact count %d does not match expected artifact count %d", len(stagedArtifacts), len(finalArtifacts))
	}
	for i, artifact := range stagedArtifacts {
		if artifact == "" {
			return nil, fmt.Errorf("staged build artifact %d is empty", i)
		}
		if artifact == finalArtifacts[i] {
			return nil, fmt.Errorf("staged build artifact %q must differ from final artifact path", artifact)
		}
	}
	return stagedArtifacts, nil
}

func BuildArtifactsExist(config VirtualMachineConfig) (bool, error) {
	return buildArtifactsExist(config, true)
}

func BuildArtifactsExistQuiet(config VirtualMachineConfig) (bool, error) {
	return buildArtifactsExist(config, false)
}

func buildArtifactsExist(config VirtualMachineConfig, verbose bool) (bool, error) {
	artifacts, err := resolveExpectedBuildArtifacts(config)
	if err != nil {
		if verbose {
			log.Printf("No build artifacts defined. Aborting build.")
		}
		return false, err
	}

	if verbose {
		log.Printf("Checking artifact(s): %v", artifacts)
	}
	state, err := artifactsState(artifacts)
	if err != nil {
		return false, err
	}
	if verbose {
		switch state {
		case ArtifactStateExists:
			log.Printf("Build artifacts already exist, skipping build: %v", artifacts)
		case ArtifactStateUnverified:
			log.Printf("Build artifacts exist without a completion marker (built by an older sailwright version?), skipping build: %v. Rebuild with --no-cache if they are broken.", artifacts)
		case ArtifactStateIncomplete:
			log.Printf("Build artifacts are incomplete (interrupted or failed build), rebuilding: %v", artifacts)
		default:
			log.Printf("Expected build artifacts do not exist, proceeding with build: %v", artifacts)
		}
	}
	return state.Usable(), nil
}

func RemoveBuildArtifacts(artifacts []string) error {
	return errors.Join(removeBuildArtifacts(artifacts), RemoveBuildArtifactCompletionMarkers(artifacts))
}

func removeBuildArtifacts(artifacts []string) error {
	var errs []error
	for _, artifact := range artifacts {
		if _, err := os.Stat(artifact); err == nil {
			log.Printf("Removing existing build artifact: %s", artifact)
			if err := os.RemoveAll(artifact); err != nil {
				errs = append(errs, fmt.Errorf("failed to remove build artifact %s: %w", artifact, err))
			}
		} else if err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("failed to inspect build artifact %s: %w", artifact, err))
		}
	}
	return errors.Join(errs...)
}

func RemoveBuildArtifactsForConfig(config VirtualMachineConfig) {
	artifacts, err := resolveExpectedBuildArtifacts(config)
	if err != nil {
		log.Printf("Failed to resolve build artifacts for cleanup: %v", err)
		return
	}
	if err := RemoveBuildArtifacts(artifacts); err != nil {
		log.Printf("Failed to remove build artifacts: %v", err)
	}
}

func backupBuildArtifacts(artifacts []string) ([]buildArtifactBackup, error) {
	backups := make([]buildArtifactBackup, 0, len(artifacts))
	for _, artifact := range artifacts {
		if _, err := os.Stat(artifact); os.IsNotExist(err) {
			continue
		} else if err != nil {
			_ = restoreBackedUpArtifacts(backups)
			return nil, err
		}

		backupPath := fmt.Sprintf("%s%s%d", artifact, buildArtifactBackupInfix, time.Now().UnixNano())
		if err := os.Rename(artifact, backupPath); err != nil {
			_ = restoreBackedUpArtifacts(backups)
			return nil, fmt.Errorf("failed to back up build artifact %s: %w", artifact, err)
		}

		log.Printf("Backed up existing build artifact: %s -> %s", artifact, backupPath)
		backups = append(backups, buildArtifactBackup{
			originalPath: artifact,
			backupPath:   backupPath,
		})
	}

	return backups, nil
}

func restoreBackedUpArtifacts(backups []buildArtifactBackup) error {
	var errs []error
	for _, backup := range backups {
		if _, err := os.Stat(backup.backupPath); os.IsNotExist(err) {
			continue
		} else if err != nil {
			errs = append(errs, fmt.Errorf("failed to stat backup artifact %s: %w", backup.backupPath, err))
			continue
		}

		// #nosec G301 -- restored cache artifact directories must remain traversable for non-root CI steps after sudo-created builds.
		if err := os.MkdirAll(filepath.Dir(backup.originalPath), 0755); err != nil {
			errs = append(errs, fmt.Errorf("failed to recreate artifact directory for %s: %w", backup.originalPath, err))
			continue
		}

		if err := os.Rename(backup.backupPath, backup.originalPath); err != nil {
			errs = append(errs, fmt.Errorf("failed to restore backup artifact %s: %w", backup.originalPath, err))
			continue
		}
		log.Printf("Restored build artifact backup: %s", backup.originalPath)
	}
	return errors.Join(errs...)
}

func removeBackedUpArtifacts(backups []buildArtifactBackup) error {
	var errs []error
	for _, backup := range backups {
		if _, err := os.Stat(backup.backupPath); os.IsNotExist(err) {
			continue
		} else if err != nil {
			errs = append(errs, fmt.Errorf("failed to stat backup artifact %s: %w", backup.backupPath, err))
			continue
		}

		if err := os.RemoveAll(backup.backupPath); err != nil {
			errs = append(errs, fmt.Errorf("failed to remove backup artifact %s: %w", backup.backupPath, err))
			continue
		}
		log.Printf("Removed build artifact backup: %s", backup.backupPath)
	}
	return errors.Join(errs...)
}

func promoteStagedBuildArtifacts(finalArtifacts []string, stagedArtifacts []string) error {
	var errs []error
	for i, finalArtifact := range finalArtifacts {
		stagedArtifact := stagedArtifacts[i]
		if _, err := os.Stat(stagedArtifact); err != nil {
			if os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("staged build artifact is missing: %s", stagedArtifact))
				continue
			}
			errs = append(errs, fmt.Errorf("failed to inspect staged build artifact %s: %w", stagedArtifact, err))
			continue
		}

		if err := RemoveBuildArtifactCompletionMarkers([]string{finalArtifact}); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := replaceBuildArtifact(finalArtifact, stagedArtifact); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func replaceBuildArtifact(finalArtifact string, stagedArtifact string) error {
	// #nosec G301 -- cache artifact directories must remain traversable for non-root CI steps after sudo-created builds.
	if err := os.MkdirAll(filepath.Dir(finalArtifact), 0755); err != nil {
		return fmt.Errorf("failed to create artifact directory for %s: %w", finalArtifact, err)
	}

	backupPath := fmt.Sprintf("%s%s%d", finalArtifact, replaceArtifactBackupInfix, time.Now().UnixNano())
	finalExists := false
	if _, err := os.Stat(finalArtifact); err == nil {
		finalExists = true
		if err := os.Rename(finalArtifact, backupPath); err != nil {
			return fmt.Errorf("failed to prepare existing artifact %s for replacement: %w", finalArtifact, err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to inspect existing artifact %s: %w", finalArtifact, err)
	}

	if err := os.Rename(stagedArtifact, finalArtifact); err != nil {
		if finalExists {
			if restoreErr := os.Rename(backupPath, finalArtifact); restoreErr != nil {
				return fmt.Errorf("failed to promote staged artifact %s to %s: %w; also failed to restore previous artifact: %v", stagedArtifact, finalArtifact, err, restoreErr)
			}
		}
		return fmt.Errorf("failed to promote staged artifact %s to %s: %w", stagedArtifact, finalArtifact, err)
	}

	if finalExists {
		if err := os.RemoveAll(backupPath); err != nil {
			return fmt.Errorf("promoted staged artifact but failed to remove replacement backup %s: %w", backupPath, err)
		}
	}
	log.Printf("Promoted staged build artifact: %s -> %s", stagedArtifact, finalArtifact)
	return nil
}

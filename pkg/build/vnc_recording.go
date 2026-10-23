package build

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/KarpelesLab/vncpasswd"
)

const (
	vncRecordingFfmpegExecutable   = "ffmpeg"
	vncRecordingSnapshotExecutable = "vncsnapshot"
)

type VncRecordingConfig struct {
	OutputFile      string
	OutputFolder    string
	OutputVideoFile string
	Password        string
	StreamedToVideo bool
}

// VncSnapshotDir is where the VNC snapshot process stores console frames for a VM.
// sailwright-gateway mirrors this layout without importing this package; a test in
// internal/gateway keeps both in sync.
func VncSnapshotDir(cacheDir string, vm_config VirtualMachineConfig) string {
	return filepath.Join(cacheDir, vm_config.OS, "qemu-out-"+GenerateVirtualMachineSlug(&vm_config)+"-vncsnapshot")
}

func RunVncSnapshotProcess(vm_config VirtualMachineConfig, ctx context.Context, process_config RunProcessConfig, recording_config *VncRecordingConfig) context.Context {
	// if running on windows, skip vnc snapshot
	if runtime.GOOS == "windows" {
		log.Printf("Skipping vnc snapshot on Windows host.")
		return ctx
	}
	vnc_display := strconv.Itoa(vm_config.VncPort - 5900)

	snapshot_dir := VncSnapshotDir(GetDirectoriesInstance().GetDirectories().CacheDir, vm_config)
	recording_config.OutputFolder = snapshot_dir

	if err := os.RemoveAll(snapshot_dir); err != nil {
		log.Fatalf("Failed to remove snapshot directory: %v", err)
	}

	if err := os.MkdirAll(snapshot_dir, 0700); err != nil {
		log.Fatalf("Failed to create snapshot directory: %v", err)
	}
	snapshot_file := filepath.Join(snapshot_dir, "qemu.vnc.jpg")
	recording_config.OutputFile = snapshot_file
	video_file := filepath.Join(snapshot_dir, "qemu.vnc.mp4")
	recording_config.OutputVideoFile = video_file
	recording_config.StreamedToVideo = true

	vnc_passwd_file := filepath.Join(snapshot_dir, ".packer-qemu.vnc.pass")
	if _, err := os.Stat(vnc_passwd_file); err == nil {
		if err := os.Remove(vnc_passwd_file); err != nil {
			log.Fatalf("Failed to remove existing VNC password file: %v", err)
		}
	}

	config := RunProcessConfig{
		Args:             ffmpegImagePipeArgs(video_file),
		WorkingDir:       GetDirectoriesInstance().GetDirectories().ProjectDir,
		Timeout:          10 * time.Minute,
		Context:          ctx,
		Retries:          5,
		RetryInterval:    time.Minute,
		DelayBeforeStart: time.Minute,
	}

	// VNC streaming depends on fixed tool argv; overrides only tune runtime behavior.
	if process_config.WorkingDir != "" {
		config.WorkingDir = process_config.WorkingDir
	}
	if process_config.Timeout != 0 {
		config.Timeout = process_config.Timeout
	}
	if process_config.Context != nil {
		config.Context = process_config.Context
	}
	if process_config.FailOnError {
		config.FailOnError = process_config.FailOnError
	}
	if process_config.Retries != 0 {
		config.Retries = process_config.Retries
	}
	if process_config.RetryInterval != 0 {
		config.RetryInterval = process_config.RetryInterval
	}
	if process_config.InterruptRetryChan != nil {
		config.InterruptRetryChan = process_config.InterruptRetryChan
	}
	if process_config.DelayBeforeStart != 0 {
		config.DelayBeforeStart = process_config.DelayBeforeStart
	}
	if process_config.Silent != nil {
		config.Silent = process_config.Silent
	}

	// Write VNC password file.
	vnc_password := "packer"
	if recording_config.Password != "" {
		vnc_password = recording_config.Password
	}

	encrypted := vncpasswd.Crypt(vnc_password)
	if err := os.WriteFile(vnc_passwd_file, encrypted, 0600); err != nil {
		log.Fatalf("Failed to write VNC password file: %v", err)
	}

	recordingCtx := ctx
	if config.Context != nil {
		recordingCtx = config.Context
	}
	ctx = streamVncSnapshotsToFfmpeg(recordingCtx, config, vnc_passwd_file, "localhost:"+vnc_display, snapshot_file)

	// Remove VNC password file
	if err := os.Remove(vnc_passwd_file); err != nil {
		log.Printf("Failed to remove VNC password file: %v", err)
	} else {
		// #nosec G302 -- this relaxes a directory, not a secret file, after the password file has been removed.
		if err := os.Chmod(snapshot_dir, 0755); err != nil {
			log.Printf("Failed to relax VNC snapshot directory permissions after password removal: %v", err)
		}
	}

	return ctx
}

func ffmpegImagePipeArgs(outputFile string) []string {
	return []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-y",
		"-f", "image2pipe",
		"-framerate", "1",
		"-vcodec", "mjpeg",
		"-i", "pipe:0",
		"-an",
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", "28",
		"-pix_fmt", "yuv420p",
		"-movflags", "+faststart",
		outputFile,
	}
}

func streamVncSnapshotsToFfmpeg(parentCtx context.Context, config RunProcessConfig, vncPasswdFile string, vncTarget string, snapshotFile string) context.Context {
	ctx := parentCtx
	if ctx == nil {
		ctx = context.Background()
	}
	if config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, config.Timeout)
		defer cancel()
	}

	if config.DelayBeforeStart > 0 {
		if !isSilent(config.Silent) {
			log.Printf("Delaying start of VNC video recording by %s", config.DelayBeforeStart)
		}
		if interrupted := waitForVncRecordingInterval(ctx, config.InterruptRetryChan, config.DelayBeforeStart); interrupted {
			if !isSilent(config.Silent) {
				log.Printf("VNC video recording start interrupted")
			}
			return cancelledContext()
		}
	}

	if len(config.Args) == 0 {
		log.Printf("Skipping VNC video recording because no ffmpeg arguments were configured")
		return cancelledContext()
	}
	if !isSilent(config.Silent) {
		log.Printf("Starting VNC video recording: vncsnapshot %s -> ffmpeg %s", vncTarget, config.Args[len(config.Args)-1])
	}

	// #nosec G204 -- executable is fixed to ffmpeg and argv is built internally without shell interpretation.
	ffmpegCmd := exec.Command(vncRecordingFfmpegExecutable, config.Args...)
	configureCommandForCleanup(ffmpegCmd)
	ffmpegCmd.Dir = config.WorkingDir
	if len(config.Env) > 0 {
		ffmpegCmd.Env = append(os.Environ(), config.Env...)
	}

	ffmpegStdin, err := ffmpegCmd.StdinPipe()
	if err != nil {
		log.Printf("Failed to create ffmpeg stdin pipe: %v", err)
		return cancelledContext()
	}
	ffmpegStdout, err := ffmpegCmd.StdoutPipe()
	if err != nil {
		log.Printf("Failed to get ffmpeg stdout: %v", err)
		return cancelledContext()
	}
	ffmpegStderr, err := ffmpegCmd.StderrPipe()
	if err != nil {
		log.Printf("Failed to get ffmpeg stderr: %v", err)
		return cancelledContext()
	}

	if err := ffmpegCmd.Start(); err != nil {
		log.Printf("Failed to start ffmpeg for VNC video recording: %v", err)
		return cancelledContext()
	}
	processGroupID := commandProcessGroupID(ffmpegCmd)
	go streamProcessOutput(ffmpegStdout, vncRecordingFfmpegExecutable, "stdout", config.Silent)
	go streamProcessOutput(ffmpegStderr, vncRecordingFfmpegExecutable, "stderr", config.Silent)

	ffmpegDone := make(chan error, 1)
	go func() {
		ffmpegDone <- ffmpegCmd.Wait()
	}()

	vncDone, vncProcessGroupID, err := startVncSnapshotCapture(ctx, config, vncPasswdFile, vncTarget, snapshotFile)
	if err != nil {
		log.Printf("Failed to start vncsnapshot for VNC video recording: %v", err)
		return finishFfmpegVncRecording(ffmpegStdin, ffmpegDone, processGroupID, 0, config.Silent)
	}

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	attempt := 0
	frameCount := 0
	for {
		select {
		case <-ctx.Done():
			terminateProcessGroup(vncProcessGroupID, processCleanupGracePeriod)
			frameCount += feedRemainingVncSnapshotFrames(snapshotFile, ffmpegStdin, config.Silent)
			return finishFfmpegVncRecording(ffmpegStdin, ffmpegDone, processGroupID, frameCount, config.Silent)
		case <-config.InterruptRetryChan:
			terminateProcessGroup(vncProcessGroupID, processCleanupGracePeriod)
			frameCount += feedRemainingVncSnapshotFrames(snapshotFile, ffmpegStdin, config.Silent)
			return finishFfmpegVncRecording(ffmpegStdin, ffmpegDone, processGroupID, frameCount, config.Silent)
		case err := <-ffmpegDone:
			terminateProcessGroup(vncProcessGroupID, processCleanupGracePeriod)
			if err != nil && !isSilent(config.Silent) {
				log.Printf("ffmpeg VNC video recording exited after %d frame(s): %v", frameCount, err)
			}
			return cancelledContext()
		case err := <-vncDone:
			written, feedErr := feedAvailableVncSnapshotFrames(snapshotFile, ffmpegStdin, true)
			frameCount += written
			if feedErr != nil {
				log.Printf("Failed to stream final VNC snapshot frame(s) to ffmpeg: %v", feedErr)
				return finishFfmpegVncRecording(ffmpegStdin, ffmpegDone, processGroupID, frameCount, config.Silent)
			}

			if err != nil && ctx.Err() == nil && attempt < config.Retries {
				attempt++
				log.Printf("vncsnapshot exited before recording completed, retrying attempt %d/%d: %v", attempt, config.Retries, err)
				if interrupted := waitForVncRecordingInterval(ctx, config.InterruptRetryChan, config.RetryInterval); interrupted {
					return finishFfmpegVncRecording(ffmpegStdin, ffmpegDone, processGroupID, frameCount, config.Silent)
				}
				vncDone, vncProcessGroupID, err = startVncSnapshotCapture(ctx, config, vncPasswdFile, vncTarget, snapshotFile)
				if err != nil {
					log.Printf("Failed to restart vncsnapshot for VNC video recording: %v", err)
					return finishFfmpegVncRecording(ffmpegStdin, ffmpegDone, processGroupID, frameCount, config.Silent)
				}
				continue
			}

			if err != nil && !isSilent(config.Silent) {
				log.Printf("vncsnapshot VNC recording exited after %d frame(s): %v", frameCount, err)
			}
			return finishFfmpegVncRecording(ffmpegStdin, ffmpegDone, processGroupID, frameCount, config.Silent)
		case <-ticker.C:
			written, err := feedAvailableVncSnapshotFrames(snapshotFile, ffmpegStdin, false)
			frameCount += written
			if err != nil {
				log.Printf("Failed to stream VNC snapshot frame(s) to ffmpeg: %v", err)
				terminateProcessGroup(vncProcessGroupID, processCleanupGracePeriod)
				return finishFfmpegVncRecording(ffmpegStdin, ffmpegDone, processGroupID, frameCount, config.Silent)
			}
		}
	}
}

func startVncSnapshotCapture(ctx context.Context, config RunProcessConfig, vncPasswdFile string, vncTarget string, snapshotFile string) (<-chan error, int, error) {
	args := []string{"-quiet", "-passwd", vncPasswdFile, "-compresslevel", "9", "-count", "21600", "-fps", "1", vncTarget, snapshotFile}
	// #nosec G204 -- executable is fixed to vncsnapshot and argv is derived from the managed VNC snapshot directory.
	cmd := exec.CommandContext(ctx, vncRecordingSnapshotExecutable, args...)
	configureCommandForCleanup(cmd)
	cmd.Dir = config.WorkingDir
	if len(config.Env) > 0 {
		cmd.Env = append(os.Environ(), config.Env...)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, 0, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, 0, err
	}
	if err := cmd.Start(); err != nil {
		return nil, 0, err
	}

	go streamProcessOutput(stdout, vncRecordingSnapshotExecutable, "stdout", config.Silent)
	go streamProcessOutput(stderr, vncRecordingSnapshotExecutable, "stderr", config.Silent)

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	return done, commandProcessGroupID(cmd), nil
}

func feedRemainingVncSnapshotFrames(snapshotFile string, writer io.Writer, silent *atomic.Bool) int {
	written, err := feedAvailableVncSnapshotFrames(snapshotFile, writer, true)
	if err != nil && !isSilent(silent) {
		log.Printf("Failed to stream remaining VNC snapshot frame(s) to ffmpeg: %v", err)
	}
	return written
}

func feedAvailableVncSnapshotFrames(snapshotFile string, writer io.Writer, includeNewest bool) (int, error) {
	frames, err := filepath.Glob(vncSnapshotFramePattern(snapshotFile))
	if err != nil {
		return 0, err
	}
	sort.Strings(frames)
	if !includeNewest && len(frames) > 0 {
		frames = frames[:len(frames)-1]
	}

	written := 0
	for _, framePath := range frames {
		validatedFramePath, err := validateVncSnapshotFramePath(snapshotFile, framePath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return written, err
		}
		// #nosec G304 -- frame paths are globbed from and validated against the managed snapshot directory before opening.
		frame, err := os.Open(validatedFramePath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return written, err
		}
		_, copyErr := io.Copy(writer, frame)
		closeErr := frame.Close()
		if copyErr != nil {
			return written, copyErr
		}
		if closeErr != nil {
			return written, closeErr
		}
		if err := os.Remove(framePath); err != nil && !os.IsNotExist(err) {
			return written, err
		}
		written++
	}
	return written, nil
}

func validateVncSnapshotFramePath(snapshotFile string, framePath string) (string, error) {
	snapshotAbs, err := filepath.Abs(snapshotFile)
	if err != nil {
		return "", fmt.Errorf("resolve snapshot file path: %w", err)
	}
	frameAbs, err := filepath.Abs(framePath)
	if err != nil {
		return "", fmt.Errorf("resolve snapshot frame path: %w", err)
	}

	snapshotDir := filepath.Clean(filepath.Dir(snapshotAbs))
	frameDir := filepath.Clean(filepath.Dir(frameAbs))
	if frameDir != snapshotDir {
		return "", fmt.Errorf("refusing VNC snapshot frame outside snapshot directory: %s", framePath)
	}

	snapshotBase := filepath.Base(snapshotAbs)
	frameBase := filepath.Base(frameAbs)
	ext := filepath.Ext(snapshotBase)
	prefix := strings.TrimSuffix(snapshotBase, ext)
	if !strings.HasPrefix(frameBase, prefix) || (ext != "" && filepath.Ext(frameBase) != ext) {
		return "", fmt.Errorf("refusing unexpected VNC snapshot frame name: %s", framePath)
	}

	info, err := os.Lstat(frameAbs)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("refusing symlink VNC snapshot frame: %s", framePath)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("refusing non-regular VNC snapshot frame: %s", framePath)
	}

	return frameAbs, nil
}

func vncSnapshotFramePattern(snapshotFile string) string {
	ext := filepath.Ext(snapshotFile)
	if ext == "" {
		return snapshotFile + "*"
	}
	return snapshotFile[:len(snapshotFile)-len(ext)] + "*" + ext
}

func waitForVncRecordingInterval(ctx context.Context, interrupt <-chan bool, interval time.Duration) bool {
	if interval <= 0 {
		interval = time.Second
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return true
	case <-interrupt:
		return true
	case <-timer.C:
		return false
	}
}

func finishFfmpegVncRecording(stdin io.Closer, ffmpegDone <-chan error, processGroupID int, frameCount int, silent *atomic.Bool) context.Context {
	if err := stdin.Close(); err != nil && !isSilent(silent) {
		log.Printf("Failed to close ffmpeg stdin for VNC recording: %v", err)
	}

	select {
	case err := <-ffmpegDone:
		if err != nil && !isSilent(silent) {
			if frameCount == 0 {
				log.Printf("ffmpeg VNC video recording stopped before any frames were captured: %v", err)
			} else {
				log.Printf("ffmpeg VNC video recording stopped after %d frame(s): %v", frameCount, err)
			}
		}
	case <-time.After(10 * time.Second):
		log.Printf("ffmpeg VNC video recording did not stop gracefully; terminating process group")
		terminateProcessGroup(processGroupID, processCleanupGracePeriod)
	}

	return cancelledContext()
}

func RunFfmpegVideoGenerationProcess(vm_config VirtualMachineConfig, ctx context.Context, process_config RunProcessConfig, recording_config *VncRecordingConfig) context.Context {
	// if running on windows, skip ffmpeg video generation
	if runtime.GOOS == "windows" {
		log.Printf("Skipping ffmpeg video generation on Windows host.")
		return ctx
	}
	if recording_config.StreamedToVideo {
		if _, err := os.Stat(recording_config.OutputVideoFile); err == nil {
			log.Printf("VNC video was streamed directly to %s; skipping frame post-processing.", recording_config.OutputVideoFile)
			return ctx
		}
		log.Printf("VNC video was configured for direct streaming, but no video file was found at %s.", recording_config.OutputVideoFile)
		return ctx
	}
	// Modify the OutputFile to use ffmpeg's sequence pattern (e.g., qemu.vnc%05d.jpg)
	inputPattern := recording_config.OutputFile
	if len(inputPattern) > 4 && inputPattern[len(inputPattern)-4:] == ".jpg" {
		inputPattern = inputPattern[:len(inputPattern)-4] + "%05d.jpg"
	}
	framePattern := recording_config.OutputFile
	if len(framePattern) > 4 && framePattern[len(framePattern)-4:] == ".jpg" {
		framePattern = framePattern[:len(framePattern)-4] + "*.jpg"
	}
	frames, err := filepath.Glob(framePattern)
	if err != nil {
		log.Fatalf("Failed to glob VNC snapshot frames: %v", err)
	}
	if len(frames) == 0 {
		log.Printf("Skipping ffmpeg video generation because no VNC snapshot frames were captured.")
		return ctx
	}

	config := RunProcessConfig{
		ExecutablePath: "ffmpeg",
		Args: []string{
			"-framerate", "1",
			"-i", inputPattern, // e.g., qemu.vnc%05d.jpg
			"-c:v", "libx264",
			"-pix_fmt", "yuv420p",
			recording_config.OutputFolder + "/qemu.vnc.mp4",
		},
		WorkingDir:    GetDirectoriesInstance().GetDirectories().ProjectDir,
		Timeout:       10 * time.Minute,
		Context:       ctx,
		Retries:       3,
		RetryInterval: 1 * time.Minute,
	}

	// Overwrite config fields with process_config if set (non-zero values)
	if process_config.ExecutablePath != "" {
		config.ExecutablePath = process_config.ExecutablePath
	}
	if len(process_config.Args) > 0 {
		config.Args = process_config.Args
	}
	if process_config.WorkingDir != "" {
		config.WorkingDir = process_config.WorkingDir
	}
	if process_config.Timeout != 0 {
		config.Timeout = process_config.Timeout
	}
	if process_config.Context != nil {
		config.Context = process_config.Context
	}
	if process_config.FailOnError {
		config.FailOnError = process_config.FailOnError
	}
	if process_config.Retries != 0 {
		config.Retries = process_config.Retries
	}
	if process_config.RetryInterval != 0 {
		config.RetryInterval = process_config.RetryInterval
	}
	if process_config.Silent != nil {
		config.Silent = process_config.Silent
	}

	processCtx, convErr := RunExternalProcess(config)
	if convErr != nil {
		if processCtx != nil {
			ctx = processCtx
		}
		log.Printf("Video conversion for %s failed: %v", recording_config.OutputFile, convErr)
	}

	// remove the snapshot images after video generation
	removePattern := framePattern

	log.Printf("Removing vncsnapshot images with pattern: %s", removePattern)

	matches, err := filepath.Glob(removePattern)
	if err != nil {
		log.Fatalf("Failed to glob snapshot images for removal: %v", err)
	}
	log.Printf("Removing %d vncsnapshot image(s)...", len(matches))
	for _, path := range matches {
		if ctx.Err() != nil {
			log.Printf("Stopping vncsnapshot image cleanup early due to interruption: %v", ctx.Err())
			return ctx
		}
		err := os.RemoveAll(path)
		if err != nil {
			log.Printf("Failed to remove snapshot image %v: %v", path, err)
		}
	}

	return ctx
}

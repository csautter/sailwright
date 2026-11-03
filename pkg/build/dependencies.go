package build

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-getter"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"golang.org/x/term"
)

// ProgressBarListener implements getter.ProgressListener using an mpb container
// so that concurrent downloads render their bars cleanly on separate lines.
type ProgressBarListener struct {
	progress *mpb.Progress
	bar      *mpb.Bar
}

func (p *ProgressBarListener) TrackProgress(src string, current, total int64, r io.ReadCloser) io.ReadCloser {
	if p == nil || p.progress == nil {
		return r
	}

	name := filepath.Base(src)
	log.Printf("Downloading %s (%s)", name, formatByteSize(total))
	p.bar = p.progress.AddBar(total,
		mpb.PrependDecorators(
			decor.Name(fmt.Sprintf("%-45s", "downloading "+name)),
		),
		mpb.AppendDecorators(
			decor.CountersKibiByte("% .2f / % .2f"),
			decor.Name(" | "),
			decor.EwmaSpeed(decor.SizeB1024(0), "% .2f", 30),
			decor.Name(" | "),
			decor.EwmaETA(decor.ET_STYLE_GO, 30),
		),
	)
	reader := &progressReader{
		reader:   r,
		bar:      p.bar,
		lastRead: time.Now(),
	}
	if !stdoutIsTerminal() {
		// The progress bar only renders on a terminal; log plain progress lines instead
		// so CI, pipes and the agent gateway can follow long downloads.
		reader.plain = &plainProgress{name: name, current: current, total: total, lastLog: time.Now(), interval: plainProgressInterval, logf: log.Printf}
	}
	return reader
}

const plainProgressInterval = 10 * time.Second

var stdoutIsTerminal = func() bool {
	return term.IsTerminal(int(os.Stdout.Fd())) // #nosec G115 -- file descriptors fit in int.
}

// plainProgress logs download progress at a fixed interval.
type plainProgress struct {
	name     string
	current  int64
	total    int64
	lastLog  time.Time
	interval time.Duration
	logf     func(format string, args ...any)
}

func (p *plainProgress) add(n int, now time.Time) {
	p.current += int64(n)
	if now.Sub(p.lastLog) < p.interval {
		return
	}
	p.lastLog = now
	if p.total > 0 {
		p.logf("Downloading %s: %d%% (%s / %s)", p.name, p.current*100/p.total, formatByteSize(p.current), formatByteSize(p.total))
		return
	}
	p.logf("Downloading %s: %s", p.name, formatByteSize(p.current))
}

func formatByteSize(size int64) string {
	const unit = 1024
	if size < 0 {
		return "unknown size"
	}
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	value, exponent := float64(size), 0
	for value >= unit && exponent < 4 {
		value /= unit
		exponent++
	}
	return fmt.Sprintf("%.2f %ciB", value, "KMGT"[exponent-1])
}

type progressReader struct {
	reader   io.ReadCloser
	bar      *mpb.Bar
	lastRead time.Time
	plain    *plainProgress
}

func (pr *progressReader) Read(p []byte) (int, error) {
	now := time.Now()
	n, err := pr.reader.Read(p)
	if n > 0 && pr.bar != nil {
		elapsed := now.Sub(pr.lastRead)
		if pr.lastRead.IsZero() || elapsed <= 0 {
			elapsed = time.Millisecond
		}
		pr.bar.EwmaIncrBy(n, elapsed)
		pr.lastRead = now
	}
	if n > 0 && pr.plain != nil {
		pr.plain.add(n, now)
	}
	return n, err
}

func (pr *progressReader) Close() error {
	return pr.reader.Close()
}

type WebFileDependency struct {
	LocalPath        string
	Checksum         string
	Source           string
	FallbackSources  []string
	RelatedVmConfigs []VirtualMachineConfig
	// BeforeHook is a function that is called before downloading the dependency. It can be used to modify the Source URL dynamically.
	BeforeHook func(ctx context.Context) (string, error)
	// PrepareHook runs during dependency reconciliation before the existence check, whether or not a
	// download follows. It can migrate or clean up related files in the cache.
	PrepareHook func()
	// FailureHint is appended to the error when every source fails.
	FailureHint string
}

const (
	// renovate: datasource=custom.ubuntu-live-server-amd64 depName=ubuntu-live-server-amd64 versioning=loose
	ubuntuLiveServerAMD64Version = "24.04.5"
	ubuntuLiveServerAMD64SHA256  = "97f3d7ffb032c3eb3b23d2c8be9cc76e60c2c1f2c0146ba5ba9fe01cafae0fd8"

	// renovate: datasource=custom.ubuntu-live-server-arm64 depName=ubuntu-live-server-arm64 versioning=loose
	ubuntuLiveServerArm64Version = "24.04.5"
	ubuntuLiveServerArm64SHA256  = "f12484464c7d4d73e2167e0a7dbda1e669f8fae9bcf778dc0944937034b320cb"

	// renovate: datasource=custom.virtio-win depName=virtio-win versioning=loose
	virtioWinVersion = "0.1.302-1"

	// virtioWinSHA256 is not managed by Renovate: no datasource publishes ISO digests.
	// The "Sync virtio-win checksum" workflow updates it via scripts/update-virtio-win-checksum.sh.
	virtioWinSHA256 = "303f7ae40dad495d6ae474fdc571df58958a4dbc5c37a522d80f9a203867949d"

	// renovate: datasource=deb depName=qemu-efi-aarch64 versioning=deb registryUrl=https://deb.debian.org/debian?suite=trixie&components=main&binaryArch=all
	qemuEfiAarch64Version = "2025.02-8+deb13u1"

	// qemuEfiAarch64SHA256 and qemuEfiAarch64SnapshotTimestamp are not managed by Renovate:
	// the deb datasource publishes no digests. Update both together with qemuEfiAarch64Version;
	// a stale checksum fails the download instead of silently changing the ARM64 firmware.
	// Keep this comment directly below the Renovate-managed version: it also stops the
	// shared Renovate regex from capturing the checksum as a digest of the version.
	qemuEfiAarch64SHA256 = "a00b2411a79c8aeafd95a7c868ac3cd1aab592f1af9fff965f02d81a625276ed"
	// A snapshot.debian.org timestamp at which qemuEfiAarch64Version was in the archive; use
	// the file's first_seen. Earlier timestamps return 404, and so can timestamps after the
	// version was removed. deb.debian.org drops superseded versions at point releases.
	qemuEfiAarch64SnapshotTimestamp = "20260104T203950Z"
	// The qemuEfiAarch64Version that qemuEfiAarch64SHA256 and qemuEfiAarch64SnapshotTimestamp
	// were taken for. Renovate does not touch it, so a version-only bump fails
	// TestQemuEfiAarch64PinIsConsistent instead of passing CI with a cached deb.
	qemuEfiAarch64PinnedFor = "2025.02-8+deb13u1"

	virtioWinFedoraArchiveURL = "https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/archive-virtio"
	virtioWinMirrorArchiveURL = "https://fedora-virt.repo.nfrance.com/virtio-win/direct-downloads/archive-virtio"
	virtioWinProbeBytes       = 2 * 1024 * 1024
	virtioWinProbeTimeout     = 20 * time.Second
)

func ubuntuLiveServerISOName(arch, version string) string {
	return fmt.Sprintf("ubuntu-%s-live-server-%s.iso", version, arch)
}

func ubuntuLiveServerISOPath(arch, version string) string {
	return GetDirectoriesInstance().CachePath("linux", ubuntuLiveServerISOName(arch, version))
}

func ubuntuLiveServerISOURL(arch, version string) string {
	name := ubuntuLiveServerISOName(arch, version)
	if arch == "amd64" {
		return fmt.Sprintf("https://releases.ubuntu.com/%s/%s", version, name)
	}
	return fmt.Sprintf("https://cdimage.ubuntu.com/ubuntu/releases/%s/release/%s", version, name)
}

func virtioWinISOFileVersion(version string) string {
	fileVersion := version
	if idx := strings.LastIndex(version, "-"); idx > 0 {
		fileVersion = version[:idx]
	}
	return fileVersion
}

func virtioWinISOFileName(version string) string {
	return fmt.Sprintf("virtio-win-%s.iso", virtioWinISOFileVersion(version))
}

// virtioWinISOCacheFileName is the local cache file name of the virtio-win ISO. It carries the
// full release version, so a version bump changes the path and the CI build-cache key instead of
// restoring a stale ISO that fails the checksum. Renovate updates the same name in the build
// workflows' cache_files.
func virtioWinISOCacheFileName(version string) string {
	return fmt.Sprintf("virtio-win-%s.iso", version)
}

func virtioWinISOCachePath(version string) string {
	return GetDirectoriesInstance().CachePath("windows", virtioWinISOCacheFileName(version))
}

// virtioWinLegacyISOFileName is the unversioned cache file name used before the version was part
// of the name.
const virtioWinLegacyISOFileName = "virtio-win.iso"

// prepareVirtioWinISOCache removes every virtio-win ISO in the directory of destination except
// destination itself, so outdated versions do not pile up after a version bump. An ISO cached under
// the unversioned legacy name is moved to destination instead when destination is missing and the
// ISO matches checksum. scripts/macos/download-virtio-win-iso.sh does the same for the shell build
// scripts.
func prepareVirtioWinISOCache(destination, checksum string) {
	dir := filepath.Dir(destination)
	paths, err := filepath.Glob(filepath.Join(dir, "virtio-win*.iso"))
	if err != nil {
		log.Printf("Warning: could not list cached virtio-win ISOs in %s: %v", dir, err)
		return
	}
	legacyPath := filepath.Join(dir, virtioWinLegacyISOFileName)
	for _, path := range paths {
		if path == destination {
			continue
		}
		if path == legacyPath && migrateLegacyVirtioWinISO(legacyPath, destination, checksum) {
			continue
		}
		if err := os.Remove(path); err != nil {
			log.Printf("Warning: could not remove outdated virtio-win ISO %s: %v", path, err)
			continue
		}
		log.Printf("Removed outdated virtio-win ISO %s", path)
	}
}

// migrateLegacyVirtioWinISO moves the ISO at legacyPath to destination when destination is missing
// and the ISO matches checksum. It reports whether legacyPath must be kept: true when it was moved
// or when the move failed, so verified bytes are not thrown away; false when it is outdated or
// destination already exists.
func migrateLegacyVirtioWinISO(legacyPath, destination, checksum string) bool {
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		return false
	}
	if !checkIfWebFileDependencyExists(WebFileDependency{LocalPath: legacyPath, Checksum: checksum}) {
		return false
	}
	if err := os.Rename(legacyPath, destination); err != nil {
		log.Printf("Warning: could not move cached virtio-win ISO %s to %s: %v", legacyPath, destination, err)
		return true
	}
	log.Printf("Moved cached virtio-win ISO %s to %s", legacyPath, destination)
	return true
}

func virtioWinISOURLFromArchive(archiveURL, version string) string {
	return fmt.Sprintf(
		"%s/virtio-win-%s/%s",
		archiveURL,
		version,
		virtioWinISOFileName(version),
	)
}

func virtioWinISOURLs(version string) []string {
	return []string{
		virtioWinISOURLFromArchive(virtioWinFedoraArchiveURL, version),
		virtioWinISOURLFromArchive(virtioWinMirrorArchiveURL, version),
	}
}

func selectFastestVirtioWinISOURL(ctx context.Context, version string) (string, error) {
	selected := selectFastestDownloadURLOrDefaultContext(ctx, virtioWinISOURLs(version), virtioWinProbeBytes, virtioWinProbeTimeout)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	log.Printf("Selected virtio-win download URL: %s", selected)
	return selected, nil
}

// selectFastestDownloadURLOrDefault returns the fastest URL, or the first one when every probe
// fails: a slow or briefly unreachable mirror must not abort the build, because the download
// itself still tries all sources in order.
func selectFastestDownloadURLOrDefault(urls []string, probeBytes int64, timeout time.Duration) string {
	return selectFastestDownloadURLOrDefaultContext(context.Background(), urls, probeBytes, timeout)
}

func selectFastestDownloadURLOrDefaultContext(ctx context.Context, urls []string, probeBytes int64, timeout time.Duration) string {
	selected, err := selectFastestDownloadURLContext(ctx, urls, probeBytes, timeout)
	if err != nil && len(urls) > 0 {
		log.Printf("Could not select fastest download URL, using default source order: %v", err)
		return urls[0]
	}
	return selected
}

type downloadProbeResult struct {
	URL      string
	Bytes    int64
	Duration time.Duration
	Err      error
}

func selectFastestDownloadURL(urls []string, probeBytes int64, timeout time.Duration) (string, error) {
	return selectFastestDownloadURLContext(context.Background(), urls, probeBytes, timeout)
}

func selectFastestDownloadURLContext(parent context.Context, urls []string, probeBytes int64, timeout time.Duration) (string, error) {
	if len(urls) == 0 {
		return "", fmt.Errorf("no download URLs to probe")
	}

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	results := make(chan downloadProbeResult, len(urls))
	client := &http.Client{}

	for _, candidate := range urls {
		candidate := candidate
		go func() {
			results <- probeDownloadURL(ctx, client, candidate, probeBytes)
		}()
	}

	var best *downloadProbeResult
	var failures []string
	for range urls {
		result := <-results
		if result.Err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", result.URL, result.Err))
			log.Printf("Download probe failed for %s: %v", result.URL, result.Err)
			continue
		}

		speed := float64(result.Bytes) / result.Duration.Seconds()
		log.Printf("Download probe for %s read %d bytes in %s (%.2f MiB/s)", result.URL, result.Bytes, result.Duration, speed/1024/1024)

		if best == nil {
			resultCopy := result
			best = &resultCopy
			continue
		}
		bestSpeed := float64(best.Bytes) / best.Duration.Seconds()
		if speed > bestSpeed {
			resultCopy := result
			best = &resultCopy
		}
	}

	if best == nil {
		return "", fmt.Errorf("all download URL probes failed: %s", strings.Join(failures, "; "))
	}

	return best.URL, nil
}

func probeDownloadURL(ctx context.Context, client *http.Client, candidate string, probeBytes int64) downloadProbeResult {
	if probeBytes <= 0 {
		probeBytes = 1024 * 1024
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, candidate, nil)
	if err != nil {
		return downloadProbeResult{URL: candidate, Err: err}
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", probeBytes-1))

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return downloadProbeResult{URL: candidate, Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return downloadProbeResult{URL: candidate, Duration: time.Since(start), Err: fmt.Errorf("unexpected HTTP status %d", resp.StatusCode)}
	}

	bytesRead, err := io.Copy(io.Discard, io.LimitReader(resp.Body, probeBytes))
	duration := time.Since(start)
	if err != nil && bytesRead > 0 && ctx.Err() != nil {
		// The probe window ran out mid-body; the bytes read so far still measure the mirror's speed.
		err = nil
	}
	if err != nil {
		return downloadProbeResult{URL: candidate, Bytes: bytesRead, Duration: duration, Err: err}
	}
	if bytesRead == 0 {
		return downloadProbeResult{URL: candidate, Duration: duration, Err: fmt.Errorf("probe read no bytes")}
	}
	if duration <= 0 {
		duration = time.Nanosecond
	}

	return downloadProbeResult{URL: candidate, Bytes: bytesRead, Duration: duration}
}

// qemuEfiAarch64URLs returns the pinned Debian qemu-efi-aarch64 package on the
// Debian mirror first and on snapshot.debian.org as a fallback.
func qemuEfiAarch64URLs() []string {
	poolPath := "pool/main/e/edk2/qemu-efi-aarch64_" + qemuEfiAarch64Version + "_all.deb"
	return []string{
		"https://deb.debian.org/debian/" + poolPath,
		"https://snapshot.debian.org/archive/debian/" + qemuEfiAarch64SnapshotTimestamp + "/" + poolPath,
	}
}

// qemuEfiAarch64Dependency returns the pinned ARM64 UEFI firmware package for the given VM configs.
func qemuEfiAarch64Dependency(related ...VirtualMachineConfig) WebFileDependency {
	urls := qemuEfiAarch64URLs()
	return WebFileDependency{
		LocalPath:        GetDirectoriesInstance().CachePath("qemu-efi-aarch64_all.deb"),
		Checksum:         "sha256:" + qemuEfiAarch64SHA256,
		Source:           urls[0],
		FallbackSources:  urls[1:],
		RelatedVmConfigs: related,
		FailureHint: fmt.Sprintf("if every source failed the checksum, the qemu-efi-aarch64 %s pin is probably stale (see build/packer/linux/ubuntu/README.md)",
			qemuEfiAarch64Version),
	}
}

// DependencyReconciliation downloads the missing web file dependencies of vmconfig.
// Cancelling ctx (e.g. Ctrl+C) aborts the running download.
func DependencyReconciliation(ctx context.Context, vmconfig VirtualMachineConfig) error {
	ctx, stopInterruptHandling := ensureInterruptHandling(ctx)
	defer stopInterruptHandling()

	p := mpb.New(mpb.WithWidth(80))
	defer p.Wait()

	for _, dep := range webFileDependenciesForVMConfig(vmconfig) {
		if err := buildContextError(ctx); err != nil {
			return err
		}
		if err := reconcileWebFileDependency(ctx, p, dep); err != nil {
			return err
		}
	}
	return nil
}

// reconcileWebFileDependency downloads dep unless it exists. Parallel builds share
// dependencies such as ISOs, so only one of them downloads a given file at a time:
// the others wait and then find it in place. Otherwise one download would delete or
// promote the partial file of another.
func reconcileWebFileDependency(ctx context.Context, p *mpb.Progress, dep WebFileDependency) error {
	unlock, err := lockWebFileDependency(ctx, dep.LocalPath)
	if err != nil {
		return fmt.Errorf("download of %s stopped: %w", dep.LocalPath, err)
	}
	defer unlock()
	if dep.PrepareHook != nil {
		dep.PrepareHook()
	}
	if checkIfWebFileDependencyExists(dep) {
		return nil
	}
	removeStaleWebFileDependency(dep)
	if err := downloadWebFileDependency(ctx, p, dep); err != nil {
		if ctxErr := buildContextError(ctx); ctxErr != nil {
			return fmt.Errorf("download of %s stopped: %w", dep.LocalPath, ctxErr)
		}
		return fmt.Errorf("failed to download web file dependency: %w", err)
	}
	return nil
}

var (
	webFileDependencyLocksMu sync.Mutex
	webFileDependencyLocks   = map[string]chan struct{}{}
)

// lockWebFileDependency serialises the downloads of one local path within this
// process. Waiting for the lock stops when ctx is cancelled.
func lockWebFileDependency(ctx context.Context, localPath string) (func(), error) {
	webFileDependencyLocksMu.Lock()
	lock, ok := webFileDependencyLocks[localPath]
	if !ok {
		lock = make(chan struct{}, 1)
		webFileDependencyLocks[localPath] = lock
	}
	webFileDependencyLocksMu.Unlock()

	select {
	case lock <- struct{}{}:
		return func() { <-lock }, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

// removeStaleWebFileDependency deletes a local file that failed the existence check, typically
// an outdated file restored from a cache. go-getter's HTTP getter resumes onto an existing file
// with a Range request: a stale file at least as large as the remote one skips the transfer, a
// smaller one gets the remote tail appended. Either way the attempt fails the checksum instead of
// downloading a fresh copy.
func removeStaleWebFileDependency(dep WebFileDependency) {
	if _, err := os.Stat(dep.LocalPath); err != nil {
		return
	}
	if err := os.Remove(dep.LocalPath); err != nil {
		log.Printf("Warning: could not remove %s, which does not match its checksum: %v", dep.LocalPath, err)
		return
	}
	log.Printf("Removed %s because it does not match its checksum", dep.LocalPath)
}

func webFileDependenciesForVMConfig(vmconfig VirtualMachineConfig) []WebFileDependency {
	var deps []WebFileDependency
	for _, dep := range getWebFileDependencies() {
		if webFileDependencyMatchesVMConfig(dep, vmconfig) {
			deps = append(deps, dep)
		}
	}
	return deps
}

func webFileDependencyMatchesVMConfig(dep WebFileDependency, vmconfig VirtualMachineConfig) bool {
	for _, relatedConfig := range dep.RelatedVmConfigs {
		if string(relatedConfig.HostOs) == string(vmconfig.HostOs) &&
			relatedConfig.OS == vmconfig.OS &&
			relatedConfig.Arch == vmconfig.Arch &&
			relatedConfig.UbuntuType == vmconfig.UbuntuType &&
			string(relatedConfig.VirtualizationEngine) == string(vmconfig.VirtualizationEngine) {
			return true
		}
	}
	return false
}

type pythonVenvCommand struct {
	executable string
	argsPrefix []string
}

func (command pythonVenvCommand) args(args ...string) []string {
	merged := make([]string, 0, len(command.argsPrefix)+len(args))
	merged = append(merged, command.argsPrefix...)
	merged = append(merged, args...)
	return merged
}

func (command pythonVenvCommand) displayName() string {
	if len(command.argsPrefix) == 0 {
		return command.executable
	}
	return command.executable + " " + strings.Join(command.argsPrefix, " ")
}

// minPythonMinor is the oldest Python 3 minor version the Windows 11 download
// script supports. Playwright 1.61 dropped Python 3.9, which macOS still ships
// as /usr/bin/python3.
const minPythonMinor = 10

const pythonVersionProbeTimeout = 10 * time.Second

// pythonVersionProbe reports the major and minor version of a Python command.
type pythonVersionProbe func(command pythonVenvCommand) (major, minor int, err error)

func resolvePythonVenvCommand() (pythonVenvCommand, error) {
	return resolvePythonVenvCommandForOS(runtime.GOOS, exec.LookPath, probePythonVersion)
}

func resolvePythonVenvCommandForOS(goos string, lookPath func(string) (string, error), probeVersion pythonVersionProbe) (pythonVenvCommand, error) {
	candidates := []pythonVenvCommand{
		{executable: "python3"},
		{executable: "python3.14"},
		{executable: "python3.13"},
		{executable: "python3.12"},
		{executable: "python3.11"},
		{executable: "python3.10"},
		{executable: "python"},
	}
	if goos == "darwin" {
		// /usr/bin often comes before Homebrew on PATH and holds Python 3.9.
		candidates = append(candidates,
			pythonVenvCommand{executable: "/opt/homebrew/bin/python3"},
			pythonVenvCommand{executable: "/usr/local/bin/python3"},
		)
	}
	if goos == "windows" {
		candidates = []pythonVenvCommand{
			{executable: "python"},
			{executable: "py", argsPrefix: []string{"-3"}},
			{executable: "python3"},
		}
	}

	checked := make([]string, 0, len(candidates))
	// python3 and python3.13 are often links to the same interpreter: probe and report it once.
	seen := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		resolvedPath, err := lookPath(candidate.executable)
		if err != nil {
			checked = append(checked, candidate.displayName())
			continue
		}
		target := resolvedPath
		if realPath, err := filepath.EvalSymlinks(resolvedPath); err == nil {
			target = realPath
		}
		if seen[target] {
			continue
		}
		seen[target] = true
		candidate.executable = resolvedPath
		major, minor, err := probeVersion(candidate)
		if err != nil {
			checked = append(checked, fmt.Sprintf("%s (%v)", candidate.displayName(), err))
			continue
		}
		if !isSupportedPythonVersion(major, minor) {
			checked = append(checked, fmt.Sprintf("%s (Python %d.%d)", candidate.displayName(), major, minor))
			continue
		}
		return candidate, nil
	}

	hint := ""
	if goos == "darwin" {
		hint = "; install one with: brew install python@3.13"
	}
	return pythonVenvCommand{}, fmt.Errorf("Python 3.%d or newer not found on PATH; checked %s%s", minPythonMinor, strings.Join(checked, ", "), hint)
}

func isSupportedPythonVersion(major, minor int) bool {
	return major == 3 && minor >= minPythonMinor
}

func probePythonVersion(command pythonVenvCommand) (int, int, error) {
	// A broken interpreter, such as a stale Windows Store alias, must not block the bootstrap.
	ctx, cancel := context.WithTimeout(context.Background(), pythonVersionProbeTimeout)
	defer cancel()
	// #nosec G204 -- the executable is a resolved Python interpreter and the argv is fixed.
	output, err := exec.CommandContext(ctx, command.executable, command.args("-c", "import sys; print('%d.%d' % sys.version_info[:2])")...).Output()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to query Python version: %w", err)
	}
	return parsePythonVersion(string(output))
}

func parsePythonVersion(output string) (int, int, error) {
	majorText, minorText, ok := strings.Cut(strings.TrimSpace(output), ".")
	if !ok {
		return 0, 0, fmt.Errorf("unexpected Python version %q", strings.TrimSpace(output))
	}
	major, majorErr := strconv.Atoi(majorText)
	minor, minorErr := strconv.Atoi(minorText)
	if majorErr != nil || minorErr != nil {
		return 0, 0, fmt.Errorf("unexpected Python version %q", strings.TrimSpace(output))
	}
	return major, minor, nil
}

// bootstrapPythonEnv ensures the Python virtual environment at workdir/.venv exists
// and has playwright and playwright-stealth installed, then installs the Chromium browser.
// pythonExe is the system Python executable used to create or repair the venv.
func bootstrapPythonEnv(workdir, pythonExe string) error {
	return bootstrapPythonEnvWithCommand(workdir, pythonVenvCommand{executable: pythonExe})
}

func bootstrapPythonEnvWithCommand(workdir string, pythonCommand pythonVenvCommand) error {
	if err := ensurePythonVenvWithCommand(workdir, pythonCommand); err != nil {
		return err
	}

	venvPython, _ := pythonVenvExecutablePaths(workdir)

	// Run pip through the venv interpreter: the pip.exe launcher hardcodes the interpreter
	// path from creation time and can exit 1 without output once that path is stale.
	log.Printf("Installing required Python packages for Windows 11 download script")
	if _, err := RunCliCommand(workdir, venvPython, []string{"-m", "pip", "install", "-r", "requirements.txt"}); err != nil {
		return fmt.Errorf("failed to install Windows 11 download script requirements: %w", err)
	}

	log.Printf("Installing Playwright browsers for Windows 11 download script")
	if _, err := RunCliCommand(workdir, venvPython, []string{"-m", "playwright", "install", "chromium"}); err != nil {
		return fmt.Errorf("failed to install Playwright Chromium browser: %w", err)
	}

	return nil
}

func ensurePythonVenv(workdir, pythonExe string) error {
	return ensurePythonVenvWithCommand(workdir, pythonVenvCommand{executable: pythonExe})
}

func ensurePythonVenvWithCommand(workdir string, pythonCommand pythonVenvCommand) error {
	venvDir := filepath.Join(workdir, ".venv")
	if _, err := os.Stat(venvDir); os.IsNotExist(err) {
		log.Printf("Creating Python virtual environment for Windows 11 download script")
		if _, err = RunCliCommand(workdir, pythonCommand.executable, pythonCommand.args("-m", "venv", ".venv")); err != nil {
			return fmt.Errorf("failed to create Python venv: %w", err)
		}
	} else if err != nil {
		return err
	} else {
		log.Printf("Python virtual environment for Windows 11 download script already exists")
		missing, err := missingPythonVenvExecutables(workdir)
		if err != nil {
			return err
		}
		reason := ""
		if len(missing) > 0 {
			reason = fmt.Sprintf("is incomplete; missing %s", strings.Join(missing, ", "))
		} else if err := checkPythonVenvUsable(workdir); err != nil {
			// The base interpreter may have been upgraded, moved or removed since creation.
			reason = fmt.Sprintf("is not usable (%v)", err)
		}
		if reason != "" {
			log.Printf("Python virtual environment for Windows 11 download script %s; recreating", reason)
			if err := os.RemoveAll(venvDir); err != nil {
				return fmt.Errorf("failed to remove broken Python venv: %w", err)
			}
			if _, err := RunCliCommand(workdir, pythonCommand.executable, pythonCommand.args("-m", "venv", ".venv")); err != nil {
				return fmt.Errorf("failed to recreate Python venv: %w", err)
			}
		}
	}

	missing, err := missingPythonVenvExecutables(workdir)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		return fmt.Errorf("Python venv is missing required executables after creation: %s", strings.Join(missing, ", "))
	}

	return nil
}

// checkPythonVenvUsable verifies that the venv interpreter starts, can import pip
// and is new enough for the pinned requirements.
func checkPythonVenvUsable(workdir string) error {
	venvPython, _ := pythonVenvExecutablePaths(workdir)
	if _, err := RunCliCommand(workdir, venvPython, []string{"-m", "pip", "--version"}); err != nil {
		return err
	}
	major, minor, err := probePythonVersion(pythonVenvCommand{executable: venvPython})
	if err != nil {
		return err
	}
	if !isSupportedPythonVersion(major, minor) {
		return fmt.Errorf("Python %d.%d is older than 3.%d", major, minor, minPythonMinor)
	}
	return nil
}

func pythonVenvExecutablePaths(workdir string) (venvPython, pipPath string) {
	pipPath = filepath.Join(workdir, ".venv", "Scripts", "pip.exe")
	if runtime.GOOS != "windows" {
		pipPath = filepath.Join(workdir, ".venv", "bin", "pip")
	}
	venvPython = filepath.Join(workdir, ".venv", "Scripts", "python.exe")
	if runtime.GOOS != "windows" {
		venvPython = filepath.Join(workdir, ".venv", "bin", "python3")
	}

	return venvPython, pipPath
}

func missingPythonVenvExecutables(workdir string) ([]string, error) {
	venvPython, pipPath := pythonVenvExecutablePaths(workdir)
	var missing []string
	for _, path := range []string{venvPython, pipPath} {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			missing = append(missing, path)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("failed to inspect Python venv executable %s: %w", path, err)
		}
		if info.IsDir() {
			missing = append(missing, path)
		}
	}
	return missing, nil
}

func getWindows11Download(arch string, savePath string, download bool) (string, error) {
	return getWindows11DownloadContext(context.Background(), arch, savePath, download)
}

func getWindows11DownloadContext(ctx context.Context, arch string, savePath string, download bool) (string, error) {

	_, err := os.Stat(savePath)
	if err != nil && os.IsNotExist(err) {
		if Win11ISOOffline() {
			return "", win11OfflineError(savePath)
		}
		log.Printf("Windows 11 ISO not found at %s, will attempt to get download url", savePath)
	} else {
		log.Printf("File already exists at %s, skipping evaluation of download url", savePath)
		return "", nil
	}

	var url_file string
	if arch == "amd64" {
		url_file = "win11_amd64_iso_url.txt"
	}
	if arch == "arm64" {
		url_file = "win11_arm64_iso_url.txt"
	}

	workdir := filepath.Join(GetDirectoriesInstance().ProjectDir, "./scripts/macos")
	pythonCommand, err := resolvePythonVenvCommand()
	if err != nil {
		return "", fmt.Errorf("dependency bootstrap failed: %w", err)
	}
	if err := bootstrapPythonEnvWithCommand(workdir, pythonCommand); err != nil {
		return "", fmt.Errorf("dependency bootstrap failed: %w", err)
	}

	venvPython := filepath.Join(workdir, ".venv", "Scripts", "python.exe")
	if runtime.GOOS != "windows" {
		venvPython = filepath.Join(workdir, ".venv", "bin", "python3")
	}

	args := []string{"playwright_win11_iso.py", "--save-path", savePath}
	// if arch is arm64, add --arm flag
	if arch == "arm64" {
		args = append(args, "--arm")
	}
	if download {
		args = append(args, "--download")
	}
	config := RunProcessConfig{
		WorkingDir:     workdir,
		ExecutablePath: venvPython,
		Args:           args,
		Env:            GetDirectoriesInstance().ManagedEnv(),
		Timeout:        10 * time.Minute,
		Context:        ctx,
	}
	if _, err := RunExternalProcess(config); err != nil {
		if isWin11RefusedExit(err) {
			return "", fmt.Errorf("%w: %v", ErrMicrosoftRefused, err)
		}
		return "", fmt.Errorf("playwright script failed: %w", err)
	}

	if download {
		return "", nil
	}

	// #nosec G304 -- url_file is selected from a fixed arch allowlist and resolved under the managed cache directory.
	content, err := os.ReadFile(GetDirectoriesInstance().CachePath("windows", url_file))
	if err != nil {
		return "", err
	}
	url := string(content)

	return url, nil
}

func getWebFileDependencies() []WebFileDependency {
	return []WebFileDependency{
		{
			LocalPath: GetDirectoriesInstance().CachePath("utm", "utm-guest-tools-latest.iso"),
			Checksum:  "",
			Source:    "https://getutm.app/downloads/utm-guest-tools-latest.iso",
			RelatedVmConfigs: []VirtualMachineConfig{
				{
					OS:                   "windows11",
					Arch:                 "amd64",
					HostOs:               HostOsDarwin,
					VirtualizationEngine: VirtualizationEngineUtm,
				},
				{
					OS:                   "windows11",
					Arch:                 "amd64",
					HostOs:               HostOsLinux,
					VirtualizationEngine: VirtualizationEngineQemu,
				},
				{
					OS:                   "windows11",
					Arch:                 "arm64",
					HostOs:               HostOsDarwin,
					VirtualizationEngine: VirtualizationEngineUtm,
				},
				{
					OS:                   "windows11",
					Arch:                 "arm64",
					HostOs:               HostOsLinux,
					VirtualizationEngine: VirtualizationEngineQemu,
				},
			},
		},
		{
			LocalPath: windows11ISOPath("amd64"),
			Checksum:  "",
			BeforeHook: func(ctx context.Context) (string, error) {
				if err := migrateLegacyWindows11ISO("amd64"); err != nil {
					return "", err
				}
				return getWindows11DownloadContext(ctx, "amd64", windows11ISOPath("amd64"), false)
			},
			RelatedVmConfigs: []VirtualMachineConfig{
				{
					OS:                   "windows11",
					Arch:                 "amd64",
					HostOs:               HostOsDarwin,
					VirtualizationEngine: VirtualizationEngineUtm,
				},
				{
					OS:                   "windows11",
					Arch:                 "amd64",
					HostOs:               HostOsWindows,
					VirtualizationEngine: VirtualizationEngineUtm,
				},
				{
					OS:                   "windows11",
					Arch:                 "amd64",
					HostOs:               HostOsWindows,
					VirtualizationEngine: VirtualizationEngineHyperv,
				},
				{
					OS:                   "windows11",
					Arch:                 "amd64",
					HostOs:               HostOsWindows,
					VirtualizationEngine: VirtualizationEngineVirtualBox,
				},
				{
					OS:                   "windows11",
					Arch:                 "amd64",
					HostOs:               HostOsLinux,
					VirtualizationEngine: VirtualizationEngineQemu,
				},
			},
		},
		{
			LocalPath: windows11ISOPath("arm64"),
			Checksum:  "",
			BeforeHook: func(ctx context.Context) (string, error) {
				if err := migrateLegacyWindows11ISO("arm64"); err != nil {
					return "", err
				}
				return getWindows11DownloadContext(ctx, "arm64", windows11ISOPath("arm64"), false)
			},
			RelatedVmConfigs: []VirtualMachineConfig{
				{
					OS:                   "windows11",
					Arch:                 "arm64",
					HostOs:               HostOsDarwin,
					VirtualizationEngine: VirtualizationEngineUtm,
				},
				{
					OS:                   "windows11",
					Arch:                 "arm64",
					HostOs:               HostOsLinux,
					VirtualizationEngine: VirtualizationEngineQemu,
				},
			},
		},
		qemuEfiAarch64Dependency(
			VirtualMachineConfig{
				OS:                   "windows11",
				Arch:                 "arm64",
				HostOs:               HostOsDarwin,
				VirtualizationEngine: VirtualizationEngineUtm,
			},
			VirtualMachineConfig{
				OS:                   "windows11",
				Arch:                 "arm64",
				HostOs:               HostOsLinux,
				VirtualizationEngine: VirtualizationEngineQemu,
			},
		),
		{
			LocalPath:       virtioWinISOCachePath(virtioWinVersion),
			Checksum:        "sha256:" + virtioWinSHA256,
			Source:          virtioWinISOURLFromArchive(virtioWinFedoraArchiveURL, virtioWinVersion),
			FallbackSources: virtioWinISOURLs(virtioWinVersion),
			BeforeHook: func(ctx context.Context) (string, error) {
				return selectFastestVirtioWinISOURL(ctx, virtioWinVersion)
			},
			PrepareHook: func() {
				prepareVirtioWinISOCache(virtioWinISOCachePath(virtioWinVersion), "sha256:"+virtioWinSHA256)
			},
			RelatedVmConfigs: []VirtualMachineConfig{
				{
					OS:                   "windows11",
					Arch:                 "amd64",
					HostOs:               HostOsDarwin,
					VirtualizationEngine: VirtualizationEngineUtm,
				},
				{
					OS:                   "windows11",
					Arch:                 "amd64",
					HostOs:               HostOsLinux,
					VirtualizationEngine: VirtualizationEngineQemu,
				},
				{
					OS:                   "windows11",
					Arch:                 "arm64",
					HostOs:               HostOsDarwin,
					VirtualizationEngine: VirtualizationEngineUtm,
				},
				{
					OS:                   "windows11",
					Arch:                 "arm64",
					HostOs:               HostOsLinux,
					VirtualizationEngine: VirtualizationEngineQemu,
				},
			},
		},
		{
			LocalPath: ubuntuLiveServerISOPath("arm64", ubuntuLiveServerArm64Version),
			Checksum:  "sha256:" + ubuntuLiveServerArm64SHA256,
			Source:    ubuntuLiveServerISOURL("arm64", ubuntuLiveServerArm64Version),
			RelatedVmConfigs: []VirtualMachineConfig{
				{
					OS:                   "ubuntu",
					UbuntuType:           "server",
					Arch:                 "arm64",
					HostOs:               HostOsDarwin,
					VirtualizationEngine: VirtualizationEngineUtm,
				},
				{
					OS:                   "ubuntu",
					UbuntuType:           "server",
					Arch:                 "arm64",
					HostOs:               HostOsLinux,
					VirtualizationEngine: VirtualizationEngineQemu,
				},
				{
					OS:                   "ubuntu",
					UbuntuType:           "desktop",
					Arch:                 "arm64",
					HostOs:               HostOsDarwin,
					VirtualizationEngine: VirtualizationEngineUtm,
				},
				{
					OS:                   "ubuntu",
					UbuntuType:           "desktop",
					Arch:                 "arm64",
					HostOs:               HostOsLinux,
					VirtualizationEngine: VirtualizationEngineQemu,
				},
			},
		},
		{
			LocalPath: ubuntuLiveServerISOPath("amd64", ubuntuLiveServerAMD64Version),
			Checksum:  "sha256:" + ubuntuLiveServerAMD64SHA256,
			Source:    ubuntuLiveServerISOURL("amd64", ubuntuLiveServerAMD64Version),
			RelatedVmConfigs: []VirtualMachineConfig{
				{
					OS:                   "ubuntu",
					UbuntuType:           "server",
					Arch:                 "amd64",
					HostOs:               HostOsDarwin,
					VirtualizationEngine: VirtualizationEngineUtm,
				},
				{
					OS:                   "ubuntu",
					UbuntuType:           "server",
					Arch:                 "amd64",
					HostOs:               HostOsWindows,
					VirtualizationEngine: VirtualizationEngineHyperv,
				},
				{
					OS:                   "ubuntu",
					UbuntuType:           "server",
					Arch:                 "amd64",
					HostOs:               HostOsLinux,
					VirtualizationEngine: VirtualizationEngineQemu,
				},
				{
					OS:                   "ubuntu",
					UbuntuType:           "desktop",
					Arch:                 "amd64",
					HostOs:               HostOsDarwin,
					VirtualizationEngine: VirtualizationEngineUtm,
				},
				{
					OS:                   "ubuntu",
					UbuntuType:           "desktop",
					Arch:                 "amd64",
					HostOs:               HostOsWindows,
					VirtualizationEngine: VirtualizationEngineHyperv,
				},
				{
					OS:                   "ubuntu",
					UbuntuType:           "desktop",
					Arch:                 "amd64",
					HostOs:               HostOsLinux,
					VirtualizationEngine: VirtualizationEngineQemu,
				},
			},
		},
		qemuEfiAarch64Dependency(
			VirtualMachineConfig{
				OS:                   "ubuntu",
				UbuntuType:           "server",
				Arch:                 "arm64",
				HostOs:               HostOsDarwin,
				VirtualizationEngine: VirtualizationEngineUtm,
			},
			VirtualMachineConfig{
				OS:                   "ubuntu",
				UbuntuType:           "desktop",
				Arch:                 "arm64",
				HostOs:               HostOsDarwin,
				VirtualizationEngine: VirtualizationEngineUtm,
			},
			VirtualMachineConfig{
				OS:                   "ubuntu",
				UbuntuType:           "server",
				Arch:                 "arm64",
				HostOs:               HostOsLinux,
				VirtualizationEngine: VirtualizationEngineQemu,
			},
			VirtualMachineConfig{
				OS:                   "ubuntu",
				UbuntuType:           "desktop",
				Arch:                 "arm64",
				HostOs:               HostOsLinux,
				VirtualizationEngine: VirtualizationEngineQemu,
			},
		),
	}
}

const webFileChecksumAttempts = 3

var webFileRetryDelay = 5 * time.Second

func downloadWebFileDependency(ctx context.Context, p *mpb.Progress, dep WebFileDependency) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if dep.BeforeHook != nil {
		newSource, err := dep.BeforeHook(ctx)
		if err != nil {
			return err
		}
		// some before hooks may also download the file themselves, so if the new source is empty, we can assume the file has been downloaded and skip the download step
		if newSource == "" {
			log.Printf("BeforeHook for %s returned empty source, assuming file has been downloaded", dep.LocalPath)
			return nil
		}
		dep.Source = newSource
	}

	sources := downloadSources(dep.Source, dep.FallbackSources)
	if len(sources) == 0 {
		return fmt.Errorf("no download source configured for %s", dep.LocalPath)
	}

	var failures []string
	for index, source := range sources {
		err := downloadWebFileDependencyFromSource(ctx, p, dep, source)
		// Mirrors occasionally serve a truncated or corrupted body; a fresh request usually succeeds.
		for attempt := 2; attempt <= webFileChecksumAttempts && err != nil && isChecksumMismatch(err); attempt++ {
			log.Printf("Checksum mismatch downloading %s from %s; retrying (attempt %d/%d)", dep.LocalPath, source, attempt, webFileChecksumAttempts)
			if sleepErr := sleepContext(ctx, webFileRetryDelay); sleepErr != nil {
				return sleepErr
			}
			err = downloadWebFileDependencyFromSource(ctx, p, dep, source)
		}
		if err == nil {
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("download of %s cancelled: %w", dep.LocalPath, ctxErr)
		}

		failures = append(failures, fmt.Sprintf("%s: %v", source, err))
		if index == len(sources)-1 {
			break
		}

		if isChecksumMismatch(err) {
			log.Printf("Checksum mismatch downloading %s from %s; trying fallback source", dep.LocalPath, source)
		} else {
			log.Printf("Failed to download %s from %s; trying fallback source: %v", dep.LocalPath, source, err)
		}
	}

	err := fmt.Errorf("failed to download web file dependency from all sources: %s", strings.Join(failures, "; "))
	if dep.FailureHint != "" {
		return fmt.Errorf("%w; %s", err, dep.FailureHint)
	}
	return err
}

// partialDownloadSuffix names the file a download is written to before it is complete.
const partialDownloadSuffix = ".part"

// downloadWebFileDependencyFromSource downloads into a partial file next to
// dep.LocalPath and renames it only after the download (and its checksum, if any)
// completed. An interrupted download therefore never looks like a complete file, which
// matters for dependencies without a checksum.
func downloadWebFileDependencyFromSource(ctx context.Context, p *mpb.Progress, dep WebFileDependency, source string) error {
	src := sourceWithChecksum(source, dep.Checksum)
	partialPath := dep.LocalPath + partialDownloadSuffix
	// Never resume a leftover partial file: without a checksum a stale prefix of an
	// updated file could not be detected.
	if err := os.Remove(partialPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove partial download %s: %w", partialPath, err)
	}
	listener := &ProgressBarListener{progress: p}
	client := &getter.Client{
		Ctx:              ctx,
		Src:              src,
		Dst:              partialPath,
		Mode:             getter.ClientModeFile,
		ProgressListener: listener,
	}
	err := client.Get()
	if err == nil {
		if renameErr := os.Rename(partialPath, dep.LocalPath); renameErr != nil {
			err = fmt.Errorf("move completed download %s to %s: %w", partialPath, dep.LocalPath, renameErr)
		}
	}
	if err != nil {
		_ = os.Remove(partialPath)
		if listener.bar != nil {
			listener.bar.Abort(false)
		}
		log.Printf("Failed to download web file dependency from %s to %s: %v", source, dep.LocalPath, err)
		return err
	}
	if listener.bar != nil {
		// Mark bar complete; mpb renders it as done and removes it from the live display.
		listener.bar.SetTotal(listener.bar.Current(), true)
	}
	log.Printf("Successfully downloaded web file dependency from %s to %s", source, dep.LocalPath)
	return nil
}

func downloadSources(primary string, fallbacks []string) []string {
	seen := map[string]bool{}
	sources := make([]string, 0, 1+len(fallbacks))
	for _, source := range append([]string{primary}, fallbacks...) {
		if source == "" || seen[source] {
			continue
		}
		seen[source] = true
		sources = append(sources, source)
	}
	return sources
}

func sourceWithChecksum(source, checksum string) string {
	if checksum == "" {
		return source
	}
	parsed, err := url.Parse(source)
	if err == nil && parsed.Scheme != "" {
		query := parsed.Query()
		query.Set("checksum", checksum)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}

	separator := "?"
	if strings.Contains(source, "?") {
		separator = "&"
	}
	return source + separator + "checksum=" + url.QueryEscape(checksum)
}

func isChecksumMismatch(err error) bool {
	var checksumErr *getter.ChecksumError
	return errors.As(err, &checksumErr)
}

func checkIfWebFileDependencyExists(dep WebFileDependency) bool {
	_, err := filepath.Abs(dep.LocalPath)
	if err != nil {
		return false
	}
	_, err = os.Stat(dep.LocalPath)
	if os.IsNotExist(err) {
		return false
	}

	// check sha256 checksums if dep.Checksum begins with "sha256:"
	if dep.Checksum != "" && len(dep.Checksum) > 7 && dep.Checksum[:7] == "sha256:" {
		file, err := os.Open(dep.LocalPath)
		if err != nil {
			return false
		}
		defer file.Close()

		hasher := sha256.New()
		if _, err := io.Copy(hasher, file); err != nil {
			return false
		}
		computedChecksum := fmt.Sprintf("sha256:%x", hasher.Sum(nil))

		if computedChecksum != dep.Checksum {
			return false
		}
	}

	log.Printf("File exists and checksum matches for %s", dep.LocalPath)
	return err == nil
}

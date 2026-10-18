package gateway

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Build screenshots.
//
// During QEMU builds the sailwright CLI records the VM console with vncsnapshot at one
// frame per second into <cache>/<os>/qemu-out-<slug>-vncsnapshot/qemu.vncNNNNN.jpg and
// streams the frames into ffmpeg, which deletes every frame except the newest. The
// gateway samples that newest frame for running build jobs, keeps a short ring of
// copies per job and compares frames by visual similarity, so agents and the human on
// the host can tell a busy installer from a stuck one.

const (
	screenSampleInterval = 10 * time.Second
	screenRingSize       = 20
	maxScreenFrameBytes  = 4 << 20
	// Frames are compared on a coarse grayscale grid. A cell counts as changed when its
	// mean luminance moved by more than screenCellThreshold (0-255); the screen counts
	// as changed when at least screenMinChangedCells cells did. A blinking cursor or a
	// spinner touches one or two cells, a progress step, new text or a dialog more.
	screenGridWidth       = 32
	screenGridHeight      = 24
	screenCellThreshold   = 10
	screenMinChangedCells = 3
	// screenFrameSlack tolerates coarse file system timestamps when frames written
	// right after the job start are matched against it.
	screenFrameSlack        = 2 * time.Second
	screenReadAttempts      = 3
	defaultScreenStuckAfter = 15 * time.Minute
)

// vncFramePattern matches the frame files vncsnapshot writes; the VNC password file in
// the same directory never matches.
var vncFramePattern = regexp.MustCompile(`^qemu\.vnc[0-9]*\.jpg$`)

// JobTarget is the VM a job operates on, taken from the validated Operation.
type JobTarget struct {
	Kind OpKind
	OS   string
	Type string
	Arch string
}

// vncSnapshotDir mirrors the CLI's recording directory (pkg/build/vnc_recording.go
// with GenerateVirtualMachineSlug: os[-type]-arch). The gateway does not import CLI
// internals, so a change there must be repeated here.
func vncSnapshotDir(cacheDir string, target JobTarget) string {
	slug := strings.ToLower(target.OS)
	if target.Type != "" {
		slug += "-" + strings.ToLower(target.Type)
	}
	slug += "-" + strings.ToLower(target.Arch)
	return filepath.Join(cacheDir, target.OS, "qemu-out-"+slug+"-vncsnapshot")
}

// ScreenFrame is one sampled console frame kept for a job.
type ScreenFrame struct {
	Seq        int
	Path       string
	CapturedAt time.Time
	Size       int
}

// ScreenStatus summarizes the console frames of a job.
type ScreenStatus struct {
	Frames         int
	LastFrameAt    time.Time
	ChangedAt      time.Time
	ChangedCells   int
	StuckSuspected bool
}

type frameKey struct {
	name    string
	modTime time.Time
	size    int64
}

// screenRecorder samples the newest VNC frame of one build job.
type screenRecorder struct {
	source     string
	dir        string
	stuckAfter time.Duration
	now        func() time.Time
	lastOutput func() time.Time
	onStuck    func(idle time.Duration)

	mu            sync.Mutex
	since         time.Time
	running       bool
	frames        []ScreenFrame
	seq           int
	last          frameKey
	baseline      []uint8
	changedAt     time.Time
	changedCells  int
	stuckReported bool
}

func newScreenRecorder(source string, dir string, stuckAfter time.Duration, lastOutput func() time.Time, onStuck func(time.Duration)) *screenRecorder {
	return &screenRecorder{
		source:     source,
		dir:        dir,
		stuckAfter: stuckAfter,
		now:        time.Now,
		lastOutput: lastOutput,
		onStuck:    onStuck,
	}
}

// Start begins sampling every interval. The returned function stops sampling after a
// final sample, so the last frame of a build is kept.
func (r *screenRecorder) Start(interval time.Duration) func() {
	r.mu.Lock()
	r.since = r.now()
	r.running = true
	r.mu.Unlock()

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				r.Sample()
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			<-done
			r.Sample()
			r.mu.Lock()
			r.running = false
			r.mu.Unlock()
		})
	}
}

// Sample reads the newest complete frame, if there is a new one, and updates the
// similarity state. It is safe to call concurrently with the sampling loop.
func (r *screenRecorder) Sample() {
	r.mu.Lock()
	since := r.since
	r.mu.Unlock()

	data, key, err := readNewestVncFrame(r.source, since.Add(-screenFrameSlack))
	if err == nil && data != nil {
		r.store(data, key)
	}
	r.checkStuck()
}

func (r *screenRecorder) store(data []byte, key frameKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if key == r.last {
		return
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return
	}
	thumb := screenThumbnail(img)
	now := r.now()

	if err := ensurePrivateDir(r.dir); err != nil {
		return
	}
	r.seq++
	path := filepath.Join(r.dir, fmt.Sprintf("frame-%06d.jpg", r.seq))
	if err := os.WriteFile(path, data, privateFilePermission); err != nil {
		return
	}
	r.last = key

	if r.baseline == nil {
		r.baseline, r.changedAt, r.changedCells = thumb, now, len(thumb)
	} else {
		r.changedCells = changedScreenCells(r.baseline, thumb)
		// Comparing with the last significant frame, not the previous sample, lets a
		// slow progress bar add up until it counts.
		if r.changedCells >= screenMinChangedCells {
			r.baseline, r.changedAt, r.stuckReported = thumb, now, false
		}
	}

	r.frames = append(r.frames, ScreenFrame{Seq: r.seq, Path: path, CapturedAt: key.modTime.UTC(), Size: len(data)})
	for len(r.frames) > screenRingSize {
		_ = os.Remove(r.frames[0].Path)
		r.frames = r.frames[1:]
	}
}

func (r *screenRecorder) checkStuck() {
	r.mu.Lock()
	now := r.now()
	stuck := r.stuckLocked(now)
	report := stuck && !r.stuckReported
	if report {
		r.stuckReported = true
	}
	idle := now.Sub(r.changedAt)
	r.mu.Unlock()
	if report && r.onStuck != nil {
		r.onStuck(idle.Round(time.Second))
	}
}

// stuckLocked reports a suspected hang: neither the screen nor the output really
// changed for stuckAfter. Without frames there is nothing to judge.
func (r *screenRecorder) stuckLocked(now time.Time) bool {
	if r.stuckAfter <= 0 || !r.running || r.changedAt.IsZero() {
		return false
	}
	lastOutput := r.since
	if r.lastOutput != nil {
		if at := r.lastOutput(); at.After(lastOutput) {
			lastOutput = at
		}
	}
	return now.Sub(r.changedAt) >= r.stuckAfter && now.Sub(lastOutput) >= r.stuckAfter
}

// Status returns the current frame summary.
func (r *screenRecorder) Status() ScreenStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	status := ScreenStatus{
		Frames:         len(r.frames),
		ChangedAt:      r.changedAt.UTC(),
		ChangedCells:   r.changedCells,
		StuckSuspected: r.stuckLocked(r.now()),
	}
	if len(r.frames) > 0 {
		status.LastFrameAt = r.frames[len(r.frames)-1].CapturedAt
	}
	return status
}

// Frame returns a kept frame and its content; index 0 is the newest.
func (r *screenRecorder) Frame(index int) (ScreenFrame, []byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if index < 0 || index >= len(r.frames) {
		if len(r.frames) == 0 {
			return ScreenFrame{}, nil, errNoScreenFrame
		}
		return ScreenFrame{}, nil, fmt.Errorf("index %d is out of range, %d frame(s) are kept", index, len(r.frames))
	}
	frame := r.frames[len(r.frames)-1-index]
	// Read under the lock so the ring cannot drop the file meanwhile.
	// #nosec G304 -- frame paths are created by the recorder inside the gateway state dir.
	data, err := os.ReadFile(frame.Path)
	if err != nil {
		return ScreenFrame{}, nil, err
	}
	return frame, data, nil
}

// Remove deletes the kept frames of this job.
func (r *screenRecorder) Remove() {
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = os.RemoveAll(r.dir)
	r.frames = nil
}

var errNoScreenFrame = errors.New("no frame yet")

// readNewestVncFrame returns the newest complete frame in dir modified after since. A
// missing dir is not an error: recording starts about a minute into a build.
func readNewestVncFrame(dir string, since time.Time) ([]byte, frameKey, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, frameKey{}, nil
		}
		return nil, frameKey{}, err
	}
	type candidate struct {
		path string
		info os.FileInfo
	}
	var candidates []candidate
	for _, entry := range entries {
		if !vncFramePattern.MatchString(entry.Name()) {
			continue
		}
		// DirEntry.Info does not follow symlinks.
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > maxScreenFrameBytes {
			continue
		}
		if info.ModTime().Before(since) {
			continue
		}
		candidates = append(candidates, candidate{path: filepath.Join(dir, entry.Name()), info: info})
	}
	// Newest first by time: vncsnapshot restarts numbering after a retry.
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i].info, candidates[j].info
		if !a.ModTime().Equal(b.ModTime()) {
			return a.ModTime().After(b.ModTime())
		}
		return a.Name() > b.Name()
	})
	for i, c := range candidates {
		if i == screenReadAttempts {
			break
		}
		// ffmpeg may delete a frame while it is read and vncsnapshot may still be
		// writing the newest one; both fall through to the next candidate.
		if data, err := readVncFrame(c.path, c.info); err == nil {
			return data, frameKey{name: c.info.Name(), modTime: c.info.ModTime(), size: c.info.Size()}, nil
		}
	}
	return nil, frameKey{}, nil
}

func readVncFrame(path string, listed os.FileInfo) ([]byte, error) {
	// #nosec G304 -- path is a pattern-checked regular file in the gateway-managed cache.
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(listed, opened) || !opened.Mode().IsRegular() {
		return nil, fmt.Errorf("%s changed while it was opened", path)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxScreenFrameBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxScreenFrameBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, maxScreenFrameBytes)
	}
	if !completeJPEG(data) {
		return nil, fmt.Errorf("%s is not a complete JPEG", path)
	}
	return data, nil
}

// completeJPEG checks the start and end markers; a frame that is still being written
// lacks the end marker.
func completeJPEG(data []byte) bool {
	n := len(data)
	return n >= 4 && data[0] == 0xFF && data[1] == 0xD8 && data[n-2] == 0xFF && data[n-1] == 0xD9
}

// screenThumbnail reduces an image to a screenGridWidth x screenGridHeight grid of
// mean luminance values.
func screenThumbnail(img image.Image) []uint8 {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil
	}
	sums := make([]uint64, screenGridWidth*screenGridHeight)
	counts := make([]uint64, len(sums))
	luma := func(x, y int) uint8 {
		return color.GrayModel.Convert(img.At(x, y)).(color.Gray).Y
	}
	switch src := img.(type) {
	case *image.YCbCr:
		luma = func(x, y int) uint8 { return src.Y[src.YOffset(x, y)] }
	case *image.Gray:
		luma = func(x, y int) uint8 { return src.GrayAt(x, y).Y }
	}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		row := (y - bounds.Min.Y) * screenGridHeight / height * screenGridWidth
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			cell := row + (x-bounds.Min.X)*screenGridWidth/width
			sums[cell] += uint64(luma(x, y))
			counts[cell]++
		}
	}
	thumb := make([]uint8, len(sums))
	for i := range sums {
		if counts[i] > 0 {
			thumb[i] = uint8(sums[i] / counts[i]) // #nosec G115 -- a mean of uint8 values fits in uint8.
		}
	}
	return thumb
}

// changedScreenCells counts the grid cells whose luminance differs by more than the
// cell threshold. Thumbnails of different sizes count as fully changed.
func changedScreenCells(a []uint8, b []uint8) int {
	if len(a) != len(b) {
		return max(len(a), len(b))
	}
	changed := 0
	for i := range a {
		diff := int(a[i]) - int(b[i])
		if diff < 0 {
			diff = -diff
		}
		if diff > screenCellThreshold {
			changed++
		}
	}
	return changed
}

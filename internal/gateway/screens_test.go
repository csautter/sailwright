package gateway

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

const (
	testScreenWidth  = 1024
	testScreenHeight = 768
)

// testScreen returns a dark console with bright rectangles.
func testScreen(rects ...image.Rectangle) *image.Gray {
	img := image.NewGray(image.Rect(0, 0, testScreenWidth, testScreenHeight))
	for i := range img.Pix {
		img.Pix[i] = 20
	}
	for _, rect := range rects {
		for y := rect.Min.Y; y < rect.Max.Y; y++ {
			for x := rect.Min.X; x < rect.Max.X; x++ {
				img.SetGray(x, y, color.Gray{Y: 230})
			}
		}
	}
	return img
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeFrame(t *testing.T, dir string, name string, data []byte, modTime time.Time) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeClock is a settable clock for the recorder.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type recorderFixture struct {
	t        *testing.T
	source   string
	clock    *fakeClock
	recorder *screenRecorder
	frame    int
	output   time.Time
	stuck    []time.Duration
	stop     func()
	mu       sync.Mutex
}

func newRecorderFixture(t *testing.T, stuckAfter time.Duration) *recorderFixture {
	t.Helper()
	f := &recorderFixture{t: t, source: filepath.Join(t.TempDir(), "vnc"), clock: &fakeClock{now: time.Now().Truncate(time.Second)}}
	f.recorder = newScreenRecorder(f.source, filepath.Join(t.TempDir(), "screens"), stuckAfter, f.lastOutput, func(idle time.Duration) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.stuck = append(f.stuck, idle)
	})
	f.recorder.now = f.clock.Now
	// The loop never ticks in tests; samples are taken explicitly.
	f.stop = f.recorder.Start(time.Hour)
	t.Cleanup(func() { f.stop() })
	return f
}

func (f *recorderFixture) lastOutput() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.output
}

func (f *recorderFixture) setOutput(at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.output = at
}

func (f *recorderFixture) stuckReports() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.stuck)
}

// show writes the next frame like vncsnapshot does and samples it.
func (f *recorderFixture) show(img image.Image) ScreenStatus {
	f.t.Helper()
	f.frame++
	// Real file times, so frames are newer than the recorder start.
	writeFrame(f.t, f.source, fmt.Sprintf("qemu.vnc%05d.jpg", f.frame), encodeJPEG(f.t, img), time.Now().Add(time.Duration(f.frame)*time.Second))
	f.recorder.Sample()
	return f.recorder.Status()
}

func TestVncSnapshotDirMirrorsCLISlug(t *testing.T) {
	cache := filepath.Join("cache")
	cases := map[JobTarget]string{
		{Kind: OpBuild, OS: "ubuntu", Type: "server", Arch: "arm64"}: filepath.Join(cache, "ubuntu", "qemu-out-ubuntu-server-arm64-vncsnapshot"),
		{Kind: OpBuild, OS: "windows11", Arch: "amd64"}:              filepath.Join(cache, "windows11", "qemu-out-windows11-amd64-vncsnapshot"),
	}
	for target, want := range cases {
		if got := vncSnapshotDir(cache, target); got != want {
			t.Errorf("vncSnapshotDir(%+v) = %s, want %s", target, got, want)
		}
		// The CLI helper is the source of truth for the layout.
		cli := alchemy_build.VirtualMachineConfig{OS: target.OS, UbuntuType: target.Type, Arch: target.Arch}
		if got := alchemy_build.VncSnapshotDir(cache, cli); got != want {
			t.Errorf("pkg/build VncSnapshotDir(%+v) = %s, want %s; update vncSnapshotDir", target, got, want)
		}
	}
}

func TestReadNewestVncFramePicksNewestCompleteFrame(t *testing.T) {
	dir := t.TempDir()
	start := time.Now().Add(-time.Minute)
	older := encodeJPEG(t, testScreen())
	newer := encodeJPEG(t, testScreen(image.Rect(100, 100, 300, 300)))

	if data, _, err := readNewestVncFrame(filepath.Join(dir, "missing"), start); err != nil || data != nil {
		t.Fatalf("missing dir: data=%v err=%v", data != nil, err)
	}

	// vncsnapshot restarts numbering after a retry, so time decides, not the name.
	writeFrame(t, dir, "qemu.vnc00500.jpg", older, start.Add(time.Second))
	writeFrame(t, dir, "qemu.vnc00001.jpg", newer, start.Add(2*time.Second))
	writeFrame(t, dir, ".packer-qemu.vnc.pass", []byte("secret"), start.Add(time.Hour))
	writeFrame(t, dir, "notes.jpg", older, start.Add(time.Hour))
	writeFrame(t, dir, "qemu.vnc00002.jpg", older, start.Add(-time.Hour)) // before the job

	data, key, err := readNewestVncFrame(dir, start)
	if err != nil || !bytes.Equal(data, newer) || key.name != "qemu.vnc00001.jpg" {
		t.Fatalf("got key %+v err %v, want qemu.vnc00001.jpg", key, err)
	}

	// A frame still being written lacks the JPEG end marker; fall back to the next one.
	writeFrame(t, dir, "qemu.vnc00003.jpg", newer[:len(newer)/2], start.Add(3*time.Second))
	if _, key, _ = readNewestVncFrame(dir, start); key.name != "qemu.vnc00001.jpg" {
		t.Fatalf("truncated frame not skipped: %+v", key)
	}

	// Oversized files are never read.
	big := append(append([]byte{0xFF, 0xD8}, make([]byte, maxScreenFrameBytes)...), 0xFF, 0xD9)
	writeFrame(t, dir, "qemu.vnc00004.jpg", big, start.Add(4*time.Second))
	if _, key, _ = readNewestVncFrame(dir, start); key.name != "qemu.vnc00001.jpg" {
		t.Fatalf("oversized frame not skipped: %+v", key)
	}

	if runtime.GOOS != "windows" {
		target := writeFrame(t, t.TempDir(), "outside.jpg", newer, start.Add(5*time.Second))
		if err := os.Symlink(target, filepath.Join(dir, "qemu.vnc00005.jpg")); err != nil {
			t.Fatal(err)
		}
		if _, key, _ = readNewestVncFrame(dir, start); key.name != "qemu.vnc00001.jpg" {
			t.Fatalf("symlinked frame not skipped: %+v", key)
		}
	}
}

func TestScreenRecorderIgnoresCursorBlinkAndCountsDialogs(t *testing.T) {
	f := newRecorderFixture(t, 0)
	first := f.show(testScreen())
	if first.Frames != 1 || first.ChangedAt.IsZero() {
		t.Fatalf("first frame should set the baseline: %+v", first)
	}
	baseline := first.ChangedAt

	cursor := image.Rect(400, 500, 408, 516)
	for i := 0; i < 6; i++ {
		f.clock.Advance(10 * time.Second)
		img := testScreen()
		if i%2 == 0 {
			img = testScreen(cursor)
		}
		if status := f.show(img); !status.ChangedAt.Equal(baseline) {
			t.Fatalf("cursor blink %d counted as a change (%d cells)", i, status.ChangedCells)
		}
	}

	f.clock.Advance(10 * time.Second)
	status := f.show(testScreen(image.Rect(256, 192, 768, 576)))
	if !status.ChangedAt.After(baseline) || status.ChangedCells < screenMinChangedCells {
		t.Fatalf("dialog not counted as a change: %+v", status)
	}
}

func TestScreenRecorderAddsUpSlowProgress(t *testing.T) {
	f := newRecorderFixture(t, 0)
	baseline := f.show(testScreen(image.Rect(200, 600, 206, 620))).ChangedAt

	// A 600 px progress bar that grows by 1 % per sample.
	changedAfter := 0
	for step := 2; step <= 40; step++ {
		f.clock.Advance(10 * time.Second)
		status := f.show(testScreen(image.Rect(200, 600, 200+6*step, 620)))
		if status.ChangedAt.After(baseline) {
			changedAfter = step
			break
		}
	}
	if changedAfter == 0 {
		t.Fatal("slow progress never counted as a change")
	}
	if changedAfter == 2 {
		t.Fatal("a single progress step should not count on its own")
	}
	t.Logf("progress counted as a change after %d steps", changedAfter)
}

func TestScreenRecorderKeepsRingAndServesFramesByIndex(t *testing.T) {
	f := newRecorderFixture(t, 0)
	if _, _, err := f.recorder.Frame(0); !errors.Is(err, errNoScreenFrame) {
		t.Fatalf("expected errNoScreenFrame, got %v", err)
	}
	var last []byte
	for i := 0; i < screenRingSize+5; i++ {
		img := testScreen(image.Rect(i*10, 0, i*10+40, 40))
		last = encodeJPEG(t, img)
		f.frame++
		writeFrame(t, f.source, fmt.Sprintf("qemu.vnc%05d.jpg", f.frame), last, time.Now().Add(time.Duration(f.frame)*time.Second))
		f.recorder.Sample()
	}
	// Sampling again without a new frame keeps the ring as is.
	f.recorder.Sample()

	status := f.recorder.Status()
	if status.Frames != screenRingSize {
		t.Fatalf("ring holds %d frames, want %d", status.Frames, screenRingSize)
	}
	entries, err := os.ReadDir(f.recorder.dir)
	if err != nil || len(entries) != screenRingSize {
		t.Fatalf("ring dir holds %d files (%v), want %d", len(entries), err, screenRingSize)
	}
	frame, data, err := f.recorder.Frame(0)
	if err != nil || !bytes.Equal(data, last) || frame.Seq != screenRingSize+5 {
		t.Fatalf("newest frame: seq %d err %v", frame.Seq, err)
	}
	if older, _, err := f.recorder.Frame(screenRingSize - 1); err != nil || older.Seq != 6 {
		t.Fatalf("oldest kept frame: seq %d err %v", older.Seq, err)
	}
	if _, _, err := f.recorder.Frame(screenRingSize); err == nil {
		t.Fatal("expected an out of range error")
	}

	f.recorder.Remove()
	if _, err := os.Stat(f.recorder.dir); !os.IsNotExist(err) {
		t.Fatalf("frames not removed: %v", err)
	}
}

func TestScreenRecorderSuspectsStuckOnlyWhenScreenAndOutputIdle(t *testing.T) {
	const stuckAfter = 15 * time.Minute
	f := newRecorderFixture(t, stuckAfter)
	start := f.clock.Now()

	// Without frames there is nothing to judge.
	f.clock.Advance(stuckAfter + time.Minute)
	f.recorder.Sample()
	if f.recorder.Status().StuckSuspected {
		t.Fatal("stuck suspected without any frame")
	}

	f.show(testScreen())
	f.setOutput(start.Add(stuckAfter + 2*time.Minute))

	// The screen is idle long enough, but output arrived a minute ago.
	f.clock.Advance(stuckAfter)
	f.recorder.Sample()
	if f.recorder.Status().StuckSuspected {
		t.Fatal("stuck suspected although output is recent")
	}

	// Both idle: reported once.
	f.clock.Advance(stuckAfter)
	f.recorder.Sample()
	f.recorder.Sample()
	if !f.recorder.Status().StuckSuspected || f.stuckReports() != 1 {
		t.Fatalf("expected one stuck report, got %d (status %+v)", f.stuckReports(), f.recorder.Status())
	}

	// New output clears the hint.
	f.setOutput(f.clock.Now())
	if f.recorder.Status().StuckSuspected {
		t.Fatal("new output should clear the hint")
	}

	// A real change clears it as well and allows a new report later.
	f.setOutput(start)
	f.show(testScreen(image.Rect(256, 192, 768, 576)))
	if f.recorder.Status().StuckSuspected {
		t.Fatal("a real screen change should clear the hint")
	}
	f.clock.Advance(stuckAfter)
	f.recorder.Sample()
	if f.stuckReports() != 2 {
		t.Fatalf("expected a second stuck report, got %d", f.stuckReports())
	}

	// A finished job is never stuck.
	f.stop()
	if f.recorder.Status().StuckSuspected {
		t.Fatal("stopped recorder reports stuck")
	}
}

func TestScreenRecorderStuckDisabled(t *testing.T) {
	f := newRecorderFixture(t, 0)
	f.show(testScreen())
	f.clock.Advance(24 * time.Hour)
	f.recorder.Sample()
	if f.recorder.Status().StuckSuspected || f.stuckReports() != 0 {
		t.Fatal("stuck hint should be disabled")
	}
}

func TestChangedScreenCells(t *testing.T) {
	a := screenThumbnail(testScreen())
	if len(a) != screenGridWidth*screenGridHeight {
		t.Fatalf("thumbnail has %d cells", len(a))
	}
	if got := changedScreenCells(a, a); got != 0 {
		t.Fatalf("identical thumbnails differ in %d cells", got)
	}
	if got := changedScreenCells(a, a[:10]); got != len(a) {
		t.Fatalf("size mismatch should count as fully changed, got %d", got)
	}
	if screenThumbnail(image.NewGray(image.Rect(0, 0, 0, 0))) != nil {
		t.Fatal("empty image should have no thumbnail")
	}
}

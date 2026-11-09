package gateway

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestLiveConsole(lines, width int) (*LiveConsole, *syncBuffer, *time.Time) {
	return newSizedTestLiveConsole(lines, width, 0)
}

func newSizedTestLiveConsole(lines, width, height int) (*LiveConsole, *syncBuffer, *time.Time) {
	out := &syncBuffer{}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	c := NewLiveConsole(out, lines, func() (int, int) { return width, height })
	c.now = func() time.Time { return now }
	return c, out, &now
}

func (b *syncBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

// region returns the rows of the last drawn region without styling.
func region(out string) []string {
	if idx := strings.LastIndex(out, "\x1b[J"); idx >= 0 {
		out = out[idx+len("\x1b[J"):]
	}
	out = strings.NewReplacer("\x1b[2m", "", "\x1b[0m", "").Replace(out)
	return strings.Split(strings.TrimSuffix(out, "\n"), "\n")
}

func TestLiveConsoleKeepsTheNewestLines(t *testing.T) {
	c, out, _ := newTestLiveConsole(3, 80)
	w := c.Begin("job1", "/logs/job1.log")
	_, _ = w.Write([]byte("one\ntwo\nthree\nfour\nfi"))
	_, _ = w.Write([]byte("ve\n"))
	c.redraw(true)
	c.End(false)

	before := out.String()[:strings.LastIndex(out.String(), "\r")]
	got := region(before)
	want := []string{"── job job1 · 0s · full log: /logs/job1.log ──", "three", "four", "five"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("region = %q, want %q", got, want)
	}
	if !strings.HasSuffix(out.String(), "\r\x1b[4A\x1b[J") {
		t.Fatalf("End must clear the region:\n%q", out.String())
	}
}

func TestLiveConsoleShowsOnlyTheTextAfterACarriageReturn(t *testing.T) {
	c, _, _ := newTestLiveConsole(5, 80)
	defer c.Close()
	w := c.Begin("job1", "")
	_, _ = w.Write([]byte("10%\r50%\r\n80%\r90%"))
	c.mu.Lock()
	defer c.mu.Unlock()
	if got := strings.Join(c.rowsLocked(), "|"); got != "50%|90%" {
		t.Fatalf("rows = %q", got)
	}
}

func TestLiveConsoleCutsRowsToTheTerminalWidth(t *testing.T) {
	c, out, _ := newTestLiveConsole(2, 10)
	w := c.Begin("job1", "/a/very/long/log/path.log")
	_, _ = w.Write([]byte("0123456789abcdef\n"))
	c.redraw(true)
	rows := region(out.String())
	c.Close()
	for _, row := range rows {
		if n := len([]rune(row)); n > 9 {
			t.Errorf("row %q has %d columns, want at most 9", row, n)
		}
	}
	if rows[len(rows)-1] != "01234567…" {
		t.Errorf("last row = %q", rows[len(rows)-1])
	}
}

func TestLiveConsolePrintsConsoleOutputAboveTheRegion(t *testing.T) {
	c, out, _ := newTestLiveConsole(2, 80)
	defer c.Close()
	w := c.Begin("job1", "")
	_, _ = w.Write([]byte("working\n"))
	c.redraw(true)
	out.Reset()

	_, _ = c.Write([]byte("approval needed\n"))
	got := out.String()
	clear := strings.Index(got, "\r\x1b[2A\x1b[J")
	text := strings.Index(got, "approval needed\n")
	redraw := strings.LastIndex(got, "working")
	if clear != 0 || text < clear || redraw < text {
		t.Fatalf("want clear, then text, then redraw; got %q", got)
	}
}

func TestLiveConsoleHoldHidesTheRegion(t *testing.T) {
	c, out, _ := newTestLiveConsole(2, 80)
	defer c.Close()
	w := c.Begin("job1", "")
	c.Hold(true)
	out.Reset()
	_, _ = w.Write([]byte("hidden\n"))
	_, _ = c.Write([]byte("Type approve: "))
	c.redraw(true)
	if strings.Contains(out.String(), "hidden") {
		t.Fatalf("held region was drawn: %q", out.String())
	}
	_, _ = c.Write([]byte("\n"))
	c.Hold(false)
	if !strings.Contains(out.String(), "hidden") {
		t.Fatalf("region not drawn after release: %q", out.String())
	}
}

func TestLiveConsoleSkipsTheRegionAfterAPrompt(t *testing.T) {
	c, out, _ := newTestLiveConsole(2, 80)
	defer c.Close()
	_ = c.Begin("job1", "")
	_, _ = c.Write([]byte("sure? [y/N] "))
	if strings.HasSuffix(out.String(), "──\x1b[0m\n") {
		t.Fatalf("region drawn after a prompt: %q", out.String())
	}
}

func TestLiveConsoleKeepsTheTailOfAFailedJob(t *testing.T) {
	c, out, _ := newTestLiveConsole(2, 80)
	w := c.Begin("job1", "")
	_, _ = w.Write([]byte("a\nfatal: boom\n"))
	c.End(true)
	if !strings.HasSuffix(out.String(), "last output of job job1:\n  a\n  fatal: boom\n") {
		t.Fatalf("failed job tail not kept:\n%q", out.String())
	}
	c, out, _ = newTestLiveConsole(2, 80)
	w = c.Begin("job2", "")
	_, _ = w.Write([]byte("ok\n"))
	c.End(false)
	if strings.Contains(out.String(), "last output") {
		t.Fatalf("succeeded job printed its tail:\n%q", out.String())
	}
}

func TestLiveConsoleRedrawsWhenTheElapsedTimeChanges(t *testing.T) {
	c, out, now := newTestLiveConsole(2, 80)
	defer c.Close()
	_ = c.Begin("job1", "")
	c.mu.Lock()
	*now = now.Add(3 * time.Second)
	c.mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := out.String()
		if strings.Contains(got, "job job1 · 3s") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("elapsed time not redrawn: %q", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestLiveConsoleStripsControlCharacters(t *testing.T) {
	c, _, _ := newTestLiveConsole(2, 80)
	defer c.Close()
	w := c.Begin("job1", "")
	_, _ = w.Write([]byte("a\x07b\tc\n"))
	c.mu.Lock()
	defer c.mu.Unlock()
	if got := c.rowsLocked()[0]; got != "a?b c" {
		t.Fatalf("row = %q", got)
	}
}

func TestLiveConsoleReturnsAfterAPromptWithoutOutput(t *testing.T) {
	c, out, _ := newTestLiveConsole(2, 80)
	defer c.Close()
	w := c.Begin("job1", "")
	c.Hold(true)
	_, _ = c.Write([]byte("Continue? [y/N] "))
	_, _ = w.Write([]byte("later\n"))
	out.Reset()
	c.Hold(false)
	if !strings.Contains(out.String(), "later") {
		t.Fatalf("region not drawn after a prompt: %q", out.String())
	}
}

func TestLiveConsoleBeginReplacesAnActiveTail(t *testing.T) {
	c, out, _ := newTestLiveConsole(2, 80)
	defer c.Close()
	first := c.Begin("job1", "")
	_, _ = first.Write([]byte("one\n"))
	second := c.Begin("job2", "")
	_, _ = second.Write([]byte("two\n"))
	c.redraw(true)
	if !strings.Contains(out.String(), "job job2") {
		t.Fatalf("second job not shown: %q", out.String())
	}
}

// blockingWriter stands in for a terminal that does not accept writes, e.g. a
// Windows console in QuickEdit selection or a terminal stopped with Ctrl-S.
type blockingWriter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *blockingWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return len(p), nil
}

func TestLiveConsoleJobOutputDoesNotWaitForABlockedTerminal(t *testing.T) {
	c, _, _ := newTestLiveConsole(2, 80)
	tail := c.Begin("job1", "")
	blocked := &blockingWriter{entered: make(chan struct{}), release: make(chan struct{})}
	c.termMu.Lock()
	c.out = blocked
	c.termMu.Unlock()
	defer func() {
		close(blocked.release)
		c.Close()
	}()

	go func() { _, _ = c.Write([]byte("console message\n")) }()
	<-blocked.entered

	written := make(chan struct{})
	go func() {
		_, _ = tail.Write([]byte("job output\n"))
		close(written)
	}()
	select {
	case <-written:
	case <-time.After(2 * time.Second):
		t.Fatal("job output waited for the blocked terminal")
	}
}

func TestLiveConsoleFitsTheRegionIntoTheTerminalHeight(t *testing.T) {
	c, out, _ := newSizedTestLiveConsole(10, 80, 5)
	w := c.Begin("job1", "")
	_, _ = w.Write([]byte("1\n2\n3\n4\n5\n6\n"))
	c.redraw(true)
	rows := region(out.String())
	c.Close()
	// Header plus three rows leaves the cursor row on a five-row screen.
	if got := strings.Join(rows[1:], "|"); len(rows) != 4 || got != "4|5|6" {
		t.Fatalf("region = %q, want the header and the newest three lines", rows)
	}
	if !strings.HasSuffix(out.String(), "\r\x1b[4A\x1b[J") {
		t.Fatalf("End must clear exactly the drawn rows:\n%q", out.String())
	}
}

// Once the region has its full height, redraws rewrite it in place and restore the
// cursor, so text the operator types on the row below stays visible.
func TestLiveConsoleRedrawsInPlaceAndKeepsTheCursorRow(t *testing.T) {
	c, out, _ := newTestLiveConsole(2, 80)
	defer c.Close()
	w := c.Begin("job1", "")
	_, _ = w.Write([]byte("one\ntwo\n"))
	c.redraw(true)
	_, _ = w.Write([]byte("three\n"))
	out.Reset()
	c.redraw(false)
	got := out.String()
	if !strings.HasPrefix(got, "\x1b7\x1b[3A\r") || !strings.HasSuffix(got, "\x1b8") {
		t.Fatalf("want an in-place redraw with saved cursor, got %q", got)
	}
	if strings.Contains(got, "\x1b[J") {
		t.Fatalf("in-place redraw must not clear the cursor row: %q", got)
	}
	if !strings.Contains(got, "two") || !strings.Contains(got, "three") || strings.Contains(got, "one") {
		t.Fatalf("redraw does not show the newest lines: %q", got)
	}
}

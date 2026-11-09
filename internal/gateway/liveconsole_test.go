package gateway

import (
	"strings"
	"testing"
	"time"
)

func newTestLiveConsole(lines, width int) (*LiveConsole, *strings.Builder, *time.Time) {
	var out strings.Builder
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	c := NewLiveConsole(&out, lines, func() int { return width })
	c.now = func() time.Time { return now }
	return c, &out, &now
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
	c.mu.Lock()
	c.flushLocked()
	c.mu.Unlock()
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
	c.mu.Lock()
	c.flushLocked()
	c.mu.Unlock()
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
	c.mu.Lock()
	c.flushLocked()
	c.mu.Unlock()
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
	c.mu.Lock()
	c.flushLocked()
	c.mu.Unlock()
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
		c.mu.Lock()
		got := out.String()
		c.mu.Unlock()
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

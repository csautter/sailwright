package gateway

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

const (
	liveRedrawInterval = 250 * time.Millisecond
	liveDefaultWidth   = 80
	// liveMaxLine limits the bytes kept per line; rows show only the start of it.
	liveMaxLine = 4 << 10
)

// LiveTail shows the newest output of the running job on the gateway terminal.
type LiveTail interface {
	// Begin starts showing a job; output written to the returned writer appears in the tail.
	Begin(id, logPath string) io.Writer
	// End removes the tail. A failed job leaves its last lines in the scrollback.
	End(failed bool)
}

// LiveConsole is the gateway terminal with a small region at the bottom that shows
// the last lines of the running job. It redraws that region in place with VT
// sequences. Normal console output is written above it and stays in the
// scrollback. Every terminal write, on stdout and stderr, must go through the same
// LiveConsole, or the cursor math breaks.
type LiveConsole struct {
	out   io.Writer
	lines int
	size  func() (width, height int)
	now   func() time.Time

	// termMu serializes terminal writes and guards the cursor state below. It is
	// taken before mu. mu is never held while writing to out, so job output never
	// waits for a terminal that does not accept writes (a paused console, XOFF, a
	// stuck SSH session).
	termMu sync.Mutex
	// drawn is the number of rows of the region on screen; the cursor is at the
	// start of the row below them.
	drawn int
	holds int
	// atLineStart is false after console output without a final newline (a prompt),
	// so the region is not drawn after it.
	atLineStart bool
	lastElapsed time.Duration
	stop        chan struct{}
	done        chan struct{}

	// mu guards the job state, which the job's output writer updates.
	mu      sync.Mutex
	active  bool
	id      string
	logPath string
	started time.Time
	tail    []string
	partial string
	dirty   bool
}

// liveFrame is a copy of the job state for one redraw.
type liveFrame struct {
	active  bool
	id      string
	logPath string
	elapsed time.Duration
	rows    []string
	dirty   bool
}

// NewLiveConsole wraps out. The region shows up to lines job lines; size reports the
// terminal size (0 or less means unknown).
func NewLiveConsole(out io.Writer, lines int, size func() (width, height int)) *LiveConsole {
	if size == nil {
		size = func() (int, int) { return 0, 0 }
	}
	return &LiveConsole{out: out, lines: lines, size: size, now: time.Now, atLineStart: true}
}

// TerminalSize reports the size of the terminal on fd.
func TerminalSize(fd int) func() (width, height int) {
	return func() (int, int) {
		width, height, err := term.GetSize(fd)
		if err != nil {
			return 0, 0
		}
		return width, height
	}
}

// Write prints normal console output above the region.
func (c *LiveConsole) Write(p []byte) (int, error) {
	c.termMu.Lock()
	defer c.termMu.Unlock()
	var b strings.Builder
	c.clearLocked(&b)
	b.Write(p)
	if len(p) > 0 {
		c.atLineStart = p[len(p)-1] == '\n'
	}
	c.drawLocked(&b, c.frame())
	if _, err := io.WriteString(c.out, b.String()); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Begin starts the tail for a job and replaces a tail that is still shown.
func (c *LiveConsole) Begin(id, logPath string) io.Writer {
	c.End(false)
	c.termMu.Lock()
	c.mu.Lock()
	c.active, c.id, c.logPath, c.started = true, id, logPath, c.now()
	c.tail, c.partial, c.dirty = nil, "", false
	c.mu.Unlock()
	c.lastElapsed = 0
	c.stop, c.done = make(chan struct{}), make(chan struct{})
	go c.redrawLoop(c.stop, c.done)
	c.termMu.Unlock()
	c.redraw(true)
	return liveTailWriter{c: c}
}

// End removes the region. After a failure the last lines are printed as normal text.
func (c *LiveConsole) End(failed bool) {
	c.termMu.Lock()
	c.mu.Lock()
	if !c.active {
		c.mu.Unlock()
		c.termMu.Unlock()
		return
	}
	c.active = false
	id, lines := c.id, c.rowsLocked()
	c.mu.Unlock()

	var b strings.Builder
	c.clearLocked(&b)
	if failed && len(lines) > 0 {
		if !c.atLineStart {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "last output of job %s:\n", id)
		for _, line := range lines {
			b.WriteString("  " + line + "\n")
		}
		c.atLineStart = true
	}
	_, _ = io.WriteString(c.out, b.String())
	stop, done := c.stop, c.done
	c.termMu.Unlock()
	close(stop)
	<-done
}

// Close removes the region; call it before the process exits.
func (c *LiveConsole) Close() { c.End(false) }

// Hold hides the region while the operator types an answer, so redraws do not
// overwrite the input. Holds nest.
func (c *LiveConsole) Hold(on bool) {
	c.termMu.Lock()
	defer c.termMu.Unlock()
	var b strings.Builder
	if on {
		c.holds++
		c.clearLocked(&b)
	} else if c.holds > 0 {
		c.holds--
		if c.holds == 0 {
			// The operator's Enter moved the cursor to a fresh line.
			c.atLineStart = true
		}
		c.drawLocked(&b, c.frame())
	}
	_, _ = io.WriteString(c.out, b.String())
}

func (c *LiveConsole) redrawLoop(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(liveRedrawInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			c.redraw(false)
		}
	}
}

// redraw draws the region again in one write when the output or the elapsed time
// changed, or always when force is set.
func (c *LiveConsole) redraw(force bool) {
	c.termMu.Lock()
	defer c.termMu.Unlock()
	f := c.frame()
	if !force && !f.dirty && f.elapsed == c.lastElapsed {
		return
	}
	var b strings.Builder
	if rows := c.renderLocked(f); c.drawn > 0 && len(rows) == c.drawn {
		// Rewrite the rows in place and put the cursor back. Clearing to the end
		// of the screen would also erase what the operator is typing on the
		// cursor row below the region.
		fmt.Fprintf(&b, "\x1b7\x1b[%dA\r", c.drawn)
		for _, row := range rows {
			fmt.Fprintf(&b, "\x1b[2K\x1b[2m%s\x1b[0m\r\n", row)
		}
		b.WriteString("\x1b8")
		c.lastElapsed = f.elapsed
	} else {
		c.clearLocked(&b)
		c.writeRowsLocked(&b, rows, f.elapsed)
	}
	_, _ = io.WriteString(c.out, b.String())
}

// frame copies the job state and marks it as drawn.
func (c *LiveConsole) frame() liveFrame {
	c.mu.Lock()
	defer c.mu.Unlock()
	f := liveFrame{active: c.active, id: c.id, logPath: c.logPath, dirty: c.dirty}
	if c.active {
		f.elapsed = c.now().Sub(c.started).Truncate(time.Second)
		f.rows = c.rowsLocked()
	}
	c.dirty = false
	return f
}

func (c *LiveConsole) clearLocked(b *strings.Builder) {
	if c.drawn > 0 {
		fmt.Fprintf(b, "\r\x1b[%dA\x1b[J", c.drawn)
		c.drawn = 0
	}
}

// drawLocked draws the region below the cursor unless it is already shown.
func (c *LiveConsole) drawLocked(b *strings.Builder, f liveFrame) {
	if c.drawn > 0 {
		return
	}
	c.writeRowsLocked(b, c.renderLocked(f), f.elapsed)
}

func (c *LiveConsole) writeRowsLocked(b *strings.Builder, rows []string, elapsed time.Duration) {
	for _, row := range rows {
		fmt.Fprintf(b, "\x1b[2m%s\x1b[0m\n", row)
	}
	c.drawn = len(rows)
	if len(rows) > 0 {
		c.lastElapsed = elapsed
	}
}

// renderLocked returns the rows of the region, cut to the terminal size, or nil
// when the region must stay hidden.
func (c *LiveConsole) renderLocked(f liveFrame) []string {
	if !f.active || c.holds > 0 || !c.atLineStart {
		return nil
	}
	width, height := c.size()
	if width <= 0 {
		width = liveDefaultWidth
	}
	maxWidth := width - 1 // a full-width row wraps on some terminals
	lines := f.rows
	if height > 0 {
		// The header and the cursor row need one row each. Rows above the top of
		// the screen cannot be cleared again.
		fit := height - 2
		if fit < 0 {
			return nil
		}
		if len(lines) > fit {
			lines = lines[len(lines)-fit:]
		}
	}
	header := fmt.Sprintf("── job %s · %s", f.id, f.elapsed)
	if f.logPath != "" {
		header += " · full log: " + sanitizeForTerminal(f.logPath)
	}
	header += " ──"
	rows := make([]string, 0, 1+len(lines))
	rows = append(rows, runewidth.Truncate(header, maxWidth, "…"))
	for _, line := range lines {
		rows = append(rows, runewidth.Truncate(line, maxWidth, "…"))
	}
	return rows
}

// rowsLocked returns the newest lines, including an unfinished last line.
func (c *LiveConsole) rowsLocked() []string {
	rows := append([]string(nil), c.tail...)
	if c.partial != "" {
		rows = append(rows, sanitizeForTerminal(c.partial))
	}
	if len(rows) > c.lines {
		rows = rows[len(rows)-c.lines:]
	}
	return rows
}

// addOutput records job output. It only takes mu, never termMu.
func (c *LiveConsole) addOutput(p []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.active {
		return
	}
	text := c.partial + string(p)
	for {
		idx := strings.IndexByte(text, '\n')
		if idx < 0 {
			break
		}
		c.tail = append(c.tail, sanitizeForTerminal(cutLine(lastSegment(text[:idx]))))
		text = text[idx+1:]
	}
	if surplus := len(c.tail) - c.lines; surplus > 0 {
		c.tail = append(c.tail[:0], c.tail[surplus:]...)
	}
	// A carriage return redraws the line in place (progress bars), so only the
	// text after it is shown.
	c.partial = cutLine(lastSegment(text))
	c.dirty = true
}

// cutLine keeps the first liveMaxLine bytes of line without splitting a character.
func cutLine(line string) string {
	if len(line) <= liveMaxLine {
		return line
	}
	cut := liveMaxLine
	for cut > 0 && !utf8.RuneStart(line[cut]) {
		cut--
	}
	return line[:cut]
}

// lastSegment returns the text after the last carriage return of a line.
func lastSegment(line string) string {
	line = strings.TrimRight(line, "\r")
	if idx := strings.LastIndexByte(line, '\r'); idx >= 0 {
		return line[idx+1:]
	}
	return line
}

type liveTailWriter struct{ c *LiveConsole }

func (w liveTailWriter) Write(p []byte) (int, error) {
	w.c.addOutput(p)
	return len(p), nil
}

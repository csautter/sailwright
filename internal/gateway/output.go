package gateway

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// OutputChunk is a slice of job output.
type OutputChunk struct {
	Data            string `json:"data"`
	Offset          int64  `json:"offset"`
	NextOffset      int64  `json:"next_offset"`
	TruncatedBefore bool   `json:"truncated_before"`
}

// outputBuffer keeps the most recent output up to a limit, with absolute offsets.
type outputBuffer struct {
	mu        sync.Mutex
	data      []byte
	base      int64
	limit     int
	lastWrite time.Time
}

func newOutputBuffer(limit int) *outputBuffer {
	return &outputBuffer{limit: limit}
}

func (b *outputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(p) > 0 {
		b.lastWrite = time.Now().UTC()
	}
	b.data = append(b.data, p...)
	// Compact only once a quarter of the limit is surplus, so a full buffer is not
	// copied on every write. Read clamps to the newest limit bytes in between.
	if len(b.data)-b.limit > b.limit/4 {
		drop := len(b.data) - b.limit
		b.data = append(b.data[:0], b.data[drop:]...)
		b.base += int64(drop)
	}
	return len(p), nil
}

// LastWrite returns when output was last written; zero before the first write.
func (b *outputBuffer) LastWrite() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastWrite
}

// Len returns the total number of bytes ever written.
func (b *outputBuffer) Len() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.base + int64(len(b.data))
}

// Read returns up to maxBytes starting at an absolute offset.
func (b *outputBuffer) Read(offset int64, maxBytes int) OutputChunk {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, base := b.data, b.base
	if surplus := len(data) - b.limit; surplus > 0 {
		data, base = data[surplus:], base+int64(surplus)
	}
	truncated := false
	if offset < base {
		offset = base
		truncated = true
	}
	start := offset - base
	if start > int64(len(data)) {
		start = int64(len(data))
	}
	end := start + int64(maxBytes)
	if end > int64(len(data)) {
		end = int64(len(data))
	}
	return OutputChunk{
		Data:            string(data[start:end]),
		Offset:          base + start,
		NextOffset:      base + end,
		TruncatedBefore: truncated,
	}
}

const (
	// maxJobLogBytes caps one job's log file; the in-memory buffer has its own limit.
	maxJobLogBytes = 64 << 20
	maxJobLogAge   = 30 * 24 * time.Hour
)

// cappedWriter forwards at most limit bytes, then writes a marker once and drops the
// rest. It always reports success so the job's other sinks keep working.
type cappedWriter struct {
	w       io.Writer
	limit   int64
	written int64
	capped  bool
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	if c.capped {
		return len(p), nil
	}
	if remaining := c.limit - c.written; int64(len(p)) > remaining {
		if remaining > 0 {
			_, _ = c.w.Write(p[:remaining])
		}
		_, _ = io.WriteString(c.w, "\n[log truncated: size limit reached]\n")
		c.capped = true
		return len(p), nil
	}
	n, err := c.w.Write(p)
	c.written += int64(n)
	return n, err
}

// pruneOldLogs deletes job logs that were last written before maxAge ago.
func pruneOldLogs(dir string, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		if info, err := entry.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

// prefixWriter prefixes each write; it receives whole lines from lineRedactingWriter.
type prefixWriter struct {
	prefix string
	next   io.Writer
}

func (w *prefixWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(w.next, w.prefix+string(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

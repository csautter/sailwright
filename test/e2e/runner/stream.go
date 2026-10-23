package runner

import (
	"bytes"
	"io"
	"sync"
)

// PrefixWriter prefixes every line written to it, so live command output stands out
// from the test log. Partial lines are held until they are complete.
type PrefixWriter struct {
	mu      sync.Mutex
	next    io.Writer
	prefix  []byte
	pending []byte
}

// NewPrefixWriter returns a PrefixWriter writing to next.
func NewPrefixWriter(next io.Writer, prefix string) *PrefixWriter {
	return &PrefixWriter{next: next, prefix: []byte(prefix)}
}

// Write implements io.Writer.
func (w *PrefixWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, p...)
	for {
		idx := bytes.IndexByte(w.pending, '\n')
		if idx < 0 {
			return len(p), nil
		}
		line := append(append([]byte{}, w.prefix...), w.pending[:idx+1]...)
		if _, err := w.next.Write(line); err != nil {
			return 0, err
		}
		w.pending = w.pending[idx+1:]
	}
}

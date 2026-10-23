package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	redactedMarker     = "***"
	minRedactedSecret  = 4
	maxPendingLineSize = 64 << 10
)

// ansiPattern matches terminal escape sequences (CSI, OSC and single-character escapes).
var ansiPattern = regexp.MustCompile(`\x1b(\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)|[@-Z\\-_])`)

// Redactor replaces known secret values with a marker.
type Redactor struct {
	secrets []string
}

// NewRedactor builds a redactor; very short values are ignored to avoid mangling output.
func NewRedactor(secrets ...string) *Redactor {
	return newRedactor(minRedactedSecret, secrets)
}

func newRedactor(minLen int, secrets []string) *Redactor {
	var kept []string
	for _, secret := range secrets {
		if secret == "" || len(secret) < minLen {
			continue
		}
		kept = append(kept, secret)
		// Also catch the JSON-escaped form used in structured logs and tool results.
		if escaped, err := json.Marshal(secret); err == nil {
			if inner := string(escaped[1 : len(escaped)-1]); inner != secret {
				kept = append(kept, inner)
			}
		}
	}
	// Longest first so a secret that contains another one is fully replaced.
	sort.Slice(kept, func(i, j int) bool { return len(kept[i]) > len(kept[j]) })
	return &Redactor{secrets: kept}
}

// maxSecretLen is the length of the longest secret that is redacted.
func (r *Redactor) maxSecretLen() int {
	if r == nil || len(r.secrets) == 0 {
		return 0
	}
	return len(r.secrets[0]) // sorted longest first
}

// safeCut moves cut back to the start of any secret occurrence that straddles it, so
// a complete secret is never split between two emitted chunks.
func (r *Redactor) safeCut(buf []byte, cut int) int {
	if r == nil {
		return cut
	}
	for moved := true; moved; {
		moved = false
		for _, secret := range r.secrets {
			from := max(0, cut-len(secret)+1)
			if i := bytes.Index(buf[from:], []byte(secret)); i >= 0 && from+i < cut && from+i+len(secret) > cut {
				cut, moved = from+i, true
			}
		}
	}
	return cut
}

// With returns a redactor that also hides the given secrets.
func (r *Redactor) With(secrets ...string) *Redactor {
	var all []string
	if r != nil {
		all = append(all, r.secrets...)
	}
	return NewRedactor(append(all, secrets...)...)
}

// WithRequired is like With but also hides short values. Use it for secrets that must
// never appear, such as a sudo password, accepting that output may be mangled.
func (r *Redactor) WithRequired(secrets ...string) *Redactor {
	var all []string
	if r != nil {
		all = append(all, r.secrets...)
	}
	return newRedactor(1, append(all, secrets...))
}

// String redacts a string. A nil redactor returns the value unchanged.
func (r *Redactor) String(value string) string {
	if r == nil {
		return value
	}
	for _, secret := range r.secrets {
		value = strings.ReplaceAll(value, secret, redactedMarker)
	}
	return value
}

// Strings redacts every element.
func (r *Redactor) Strings(values []string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = r.String(value)
	}
	return out
}

// lineRedactingWriter redacts complete lines so secrets split across writes are caught.
// It also strips terminal escape sequences, which only add noise for agents.
type lineRedactingWriter struct {
	mu       sync.Mutex
	redactor *Redactor
	next     io.Writer
	pending  []byte
}

func newLineRedactingWriter(redactor *Redactor, next io.Writer) *lineRedactingWriter {
	return &lineRedactingWriter{redactor: redactor, next: next}
}

func (w *lineRedactingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, p...)
	for {
		idx := bytes.IndexByte(w.pending, '\n')
		if idx < 0 {
			break
		}
		if err := w.emit(w.pending[:idx+1]); err != nil {
			return 0, err
		}
		w.pending = w.pending[idx+1:]
	}
	if len(w.pending) > maxPendingLineSize {
		// Hold back the tail so a secret straddling the cut is still matched once the
		// rest of it arrives, and never split a multi-byte rune.
		cut := len(w.pending)
		if keep := w.redactor.maxSecretLen() - 1; keep > 0 {
			cut -= keep
			for cut > 0 && !utf8.RuneStart(w.pending[cut]) {
				cut--
			}
		}
		cut = w.redactor.safeCut(w.pending, cut)
		if cut > 0 {
			if err := w.emit(w.pending[:cut]); err != nil {
				return 0, err
			}
			w.pending = append([]byte(nil), w.pending[cut:]...)
		}
	}
	return len(p), nil
}

// Flush writes any partial last line.
func (w *lineRedactingWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) == 0 {
		return nil
	}
	err := w.emit(w.pending)
	w.pending = nil
	return err
}

func (w *lineRedactingWriter) emit(line []byte) error {
	clean := ansiPattern.ReplaceAllString(string(line), "")
	_, err := io.WriteString(w.next, w.redactor.String(clean))
	return err
}

// sanitizeForTerminal removes escape sequences and control characters from text that
// is shown on the operator's terminal, so agent-controlled strings cannot rewrite the
// approval prompt.
func sanitizeForTerminal(value string) string {
	value = ansiPattern.ReplaceAllString(value, "")
	var b strings.Builder
	for _, r := range value {
		switch {
		case r == '\t':
			b.WriteRune(' ')
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			b.WriteRune('?')
		case r >= '\u202a' && r <= '\u202e', r >= '\u2066' && r <= '\u2069', r == '\u200b', r == '\u200e', r == '\u200f':
			// Bidirectional controls and invisible marks can visually disguise the command.
			b.WriteRune('?')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

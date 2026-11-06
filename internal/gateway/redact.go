package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

const (
	redactedMarker     = "***"
	minRedactedSecret  = 4
	maxPendingLineSize = 64 << 10

	// defaultGuestPassword is the documented password of disposable sailwright guests
	// (pkg/provision). It is redacted before any guest has been resolved.
	defaultGuestPassword = "P@ssw0rd!" // #nosec G101 -- documented default for disposable guests, redacted, never used to authenticate.
)

// ansiPattern matches terminal escape sequences (CSI, OSC and single-character escapes).
var ansiPattern = regexp.MustCompile(`\x1b(\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)|[@-Z\\-_])`)

// Redactor replaces known secret values with a marker. A redactor derived with With
// or WithRequired also applies its parent's current secrets, so secrets the parent
// learns later (AddRequired) are hidden in derived output too.
type Redactor struct {
	mu      sync.RWMutex
	secrets []string
	minLen  int // shortest optional value With accepts; accepted secrets are always kept
	parent  *Redactor

	// merged caches all() for derived redactors, valid while redactorGeneration
	// still equals mergedGen.
	cacheMu   sync.Mutex
	merged    []string
	mergedGen uint64
	hasMerged bool
}

// redactorGeneration changes whenever any redactor learns a secret (AddRequired), so
// derived redactors know their merged list may be stale. Secrets are added rarely and
// output is redacted per chunk, so one global counter is enough.
var redactorGeneration atomic.Uint64

// all returns the secrets of r and its ancestors, longest first.
func (r *Redactor) all() []string {
	if r.parent == nil {
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.secrets
	}
	// Read the generation before the secrets: a concurrent AddRequired bumps it after
	// its update, so a list merged from older secrets is never cached as current.
	gen := redactorGeneration.Load()
	r.cacheMu.Lock()
	if r.hasMerged && r.mergedGen == gen {
		merged := r.merged
		r.cacheMu.Unlock()
		return merged
	}
	r.cacheMu.Unlock()
	r.mu.RLock()
	own := r.secrets
	r.mu.RUnlock()
	merged := mergeSecrets(r.parent.all(), own)
	r.cacheMu.Lock()
	r.merged, r.mergedGen, r.hasMerged = merged, gen, true
	r.cacheMu.Unlock()
	return merged
}

// mergeSecrets combines accepted secret lists without re-escaping them.
func mergeSecrets(lists ...[]string) []string {
	seen := map[string]bool{}
	var merged []string
	for _, list := range lists {
		for _, secret := range list {
			if !seen[secret] {
				seen[secret] = true
				merged = append(merged, secret)
			}
		}
	}
	sort.Slice(merged, func(i, j int) bool { return len(merged[i]) > len(merged[j]) })
	return merged
}

// NewRedactor builds a redactor; very short values are ignored to avoid mangling output.
func NewRedactor(secrets ...string) *Redactor {
	return newRedactor(minRedactedSecret, nil, minRedactedSecret, secrets)
}

// newRedactor keeps every existing (already accepted) secret and adds the new values
// that are at least addMinLen long. minLen is the threshold later With calls apply.
func newRedactor(minLen int, existing []string, addMinLen int, added []string) *Redactor {
	var kept []string
	seen := map[string]bool{}
	keep := func(secret string) {
		if secret == "" || seen[secret] {
			return
		}
		kept = append(kept, secret)
		seen[secret] = true
	}
	for _, secret := range existing {
		keep(secret)
	}
	for _, secret := range added {
		if len(secret) < addMinLen {
			continue
		}
		keep(secret)
		// Also catch the JSON-escaped form used in structured logs and tool results.
		if escaped, err := json.Marshal(secret); err == nil {
			keep(string(escaped[1 : len(escaped)-1]))
		}
	}
	// Longest first so a secret that contains another one is fully replaced.
	sort.Slice(kept, func(i, j int) bool { return len(kept[i]) > len(kept[j]) })
	return &Redactor{secrets: kept, minLen: minLen}
}

// maxSecretLen is the length of the longest secret that is redacted.
func (r *Redactor) maxSecretLen() int {
	if r == nil {
		return 0
	}
	secrets := r.all()
	if len(secrets) == 0 {
		return 0
	}
	return len(secrets[0]) // sorted longest first
}

// safeCut moves cut back to the start of any secret occurrence that straddles it, so
// a complete secret is never split between two emitted chunks.
func (r *Redactor) safeCut(buf []byte, cut int) int {
	if r == nil {
		return cut
	}
	secrets := r.all()
	for moved := true; moved; {
		moved = false
		for _, secret := range secrets {
			from := max(0, cut-len(secret)+1)
			if i := bytes.Index(buf[from:], []byte(secret)); i >= 0 && from+i < cut && from+i+len(secret) > cut {
				cut, moved = from+i, true
			}
		}
	}
	return cut
}

// With returns a redactor that also hides the given secrets, keeping r's minimum length.
func (r *Redactor) With(secrets ...string) *Redactor {
	if r == nil {
		return NewRedactor(secrets...)
	}
	derived := newRedactor(r.minLen, nil, r.minLen, secrets)
	derived.parent = r
	return derived
}

// WithRequired is like With but also hides short values. Use it for secrets that must
// never appear, such as a sudo password, accepting that output may be mangled.
func (r *Redactor) WithRequired(secrets ...string) *Redactor {
	if r == nil {
		return newRedactor(minRedactedSecret, nil, 1, secrets)
	}
	derived := newRedactor(r.minLen, nil, 1, secrets)
	derived.parent = r
	return derived
}

// String redacts a string. A nil redactor returns the value unchanged.
func (r *Redactor) String(value string) string {
	if r == nil {
		return value
	}
	for _, secret := range r.all() {
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

// AddRequired registers newly resolved guest secrets for subsequent audit and job output.
func (r *Redactor) AddRequired(secrets ...string) {
	r.mu.Lock()
	// Short guest passwords are still hidden, but the threshold for later optional
	// secrets (With) stays unchanged.
	before := len(r.secrets)
	r.secrets = newRedactor(r.minLen, r.secrets, 1, secrets).secrets
	learned := len(r.secrets) != before // existing secrets are kept and deduplicated
	r.mu.Unlock()
	if learned {
		redactorGeneration.Add(1)
	}
}

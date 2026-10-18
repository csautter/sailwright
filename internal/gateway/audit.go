package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// AuditEvent is one line of the JSONL audit log.
type AuditEvent struct {
	Time         time.Time      `json:"time"`
	Event        string         `json:"event"`
	Peer         string         `json:"peer,omitempty"`
	Tool         string         `json:"tool,omitempty"`
	JobID        string         `json:"job_id,omitempty"`
	Tier         string         `json:"tier,omitempty"`
	Argv         []string       `json:"argv,omitempty"`
	Params       map[string]any `json:"params,omitempty"`
	BinarySHA256 string         `json:"binary_sha256,omitempty"`
	Snapshot     *Snapshot      `json:"snapshot,omitempty"`
	Risk         []string       `json:"risk,omitempty"`
	Decision     string         `json:"decision,omitempty"`
	ExitCode     *int           `json:"exit_code,omitempty"`
	DurationMS   int64          `json:"duration_ms,omitempty"`
	Error        string         `json:"error,omitempty"`
	HTTPStatus   int            `json:"http_status,omitempty"`
}

// AuditLog appends events to a JSONL file.
type AuditLog struct {
	mu       sync.Mutex
	file     *os.File
	redactor *Redactor
	now      func() time.Time
}

// OpenAuditLog opens (or creates) the audit log with owner-only permissions.
func OpenAuditLog(path string, redactor *Redactor) (*AuditLog, error) {
	// #nosec G304 -- fixed file inside the operator's state directory.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, privateFilePermission)
	if err != nil {
		return nil, fmt.Errorf("open audit log: %w", err)
	}
	return &AuditLog{file: file, redactor: redactor, now: time.Now}, nil
}

// Record writes one event. Errors are returned so callers can fail closed.
func (a *AuditLog) Record(event AuditEvent) error {
	if a == nil {
		return nil
	}
	event.Time = a.now().UTC()
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	// Redact the serialized line so secrets in params, argv and errors are all caught.
	line := []byte(a.redactor.String(string(encoded)))
	a.mu.Lock()
	defer a.mu.Unlock()
	_, err = a.file.Write(append(line, '\n'))
	return err
}

// Close closes the log file.
func (a *AuditLog) Close() error {
	if a == nil {
		return nil
	}
	return a.file.Close()
}

// Package operationlock coordinates CLI operations across processes.
package operationlock

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrBusy reports that another process holds a conflicting lock.
var ErrBusy = errors.New("lock held")

const (
	globalLockName = "operations.lock"
	maxHolderBytes = 4 << 10
	// maxReportedHolders bounds the per-target holders listed for an all-targets request.
	maxReportedHolders = 3
)

// Holder describes the process holding an exclusive lock. It is written into the
// lock file so a refused process can say who is busy. Locks cover a byte range far
// beyond the content (see lockFile), so the content stays readable on Windows too.
type Holder struct {
	PID       int       `json:"pid"`
	Program   string    `json:"program"`
	Operation string    `json:"operation,omitempty"`
	Target    string    `json:"target,omitempty"`
	Since     time.Time `json:"since"`
}

func (h Holder) String() string {
	what := h.Program
	if h.Operation != "" {
		what += " " + h.Operation
	}
	if h.Target != "" {
		what += " " + h.Target
	}
	return fmt.Sprintf("pid %d (%s) since %s", h.PID, what, h.Since.Local().Format("15:04:05"))
}

// busyError is returned by Acquire for a held lock file; AcquireWithin turns it into
// a message that names the holders.
type busyError struct {
	dir, file, target string
	exclusive         bool
}

func (e *busyError) Error() string {
	what := e.target
	if what == "" {
		what = "all targets"
	}
	return fmt.Sprintf("resource busy (%s): another sailwright or gateway operation is running: %s", what, ErrBusy)
}

func (e *busyError) Unwrap() error { return ErrBusy }

// AcquireWithin is Acquire, retrying with backoff while the lock is busy for up to
// timeout. A zero timeout tries exactly once. When it gives up, the error names the
// processes holding the conflicting locks where they are known.
func AcquireWithin(appData, target, operation string, timeout time.Duration) (func(), error) {
	return acquireWithin(appData, target, operation, timeout, false)
}

// AcquireSharedWithin waits for an image read lock, honoring the CLI timeout.
func AcquireSharedWithin(appData, target, operation string, timeout time.Duration) (func(), error) {
	return acquireWithin(appData, target, operation, timeout, true)
}

func acquireWithin(appData, target, operation string, timeout time.Duration, shared bool) (func(), error) {
	deadline := time.Now().Add(timeout)
	delay := 100 * time.Millisecond
	for {
		release, err := acquire(appData, target, operation, shared)
		if err == nil || !errors.Is(err, ErrBusy) {
			return release, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, describeBusy(err, timeout)
		}
		time.Sleep(min(delay, remaining))
		delay = min(2*delay, time.Second)
	}
}

// Acquire locks a target, or all targets when target is empty. operation names the
// command for the holder description. Lock files remain in place so another process
// cannot lock a replacement inode during release.
func Acquire(appData, target, operation string) (func(), error) {
	return acquire(appData, target, operation, false)
}

// AcquireShared holds an image read lock while independent instances clone it.
func AcquireShared(appData, target, operation string) (func(), error) {
	return acquire(appData, target, operation, true)
}

func acquire(appData, target, operation string, sharedTarget bool) (func(), error) {
	dir := filepath.Join(appData, "locks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	holder := Holder{PID: os.Getpid(), Program: filepath.Base(os.Args[0]), Operation: operation, Target: target, Since: time.Now().UTC()}
	var files []*os.File
	var written []*os.File
	release := func() {
		// Clear the holder first so a stale description is never read as current.
		for _, f := range written {
			_ = f.Truncate(0)
		}
		for i := len(files) - 1; i >= 0; i-- {
			unlockFile(files[i])
			_ = files[i].Close()
		}
	}
	take := func(name string, shared bool) error {
		// #nosec G304 -- operator-owned app data; names are fixed or SHA-256 hashes of target keys.
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return err
		}
		if err := lockFile(f, shared); err != nil {
			_ = f.Close()
			if errors.Is(err, ErrBusy) {
				return &busyError{dir: dir, file: name, target: target, exclusive: !shared}
			}
			return err
		}
		files = append(files, f)
		if !shared {
			// Diagnostics only: a failed write leaves the holder unknown.
			if data, err := json.Marshal(holder); err == nil && f.Truncate(0) == nil {
				if _, err := f.WriteAt(data, 0); err == nil {
					written = append(written, f)
				}
			}
		}
		return nil
	}
	if err := take(globalLockName, target != ""); err != nil {
		release()
		return nil, err
	}
	if target != "" {
		if err := take(targetLockName(target), sharedTarget); err != nil {
			release()
			return nil, err
		}
	}
	return release, nil
}

func targetLockName(target string) string {
	return fmt.Sprintf("%x.lock", sha256.Sum256([]byte(target)))
}

// describeBusy adds the known holders to a busy error.
func describeBusy(err error, waited time.Duration) error {
	var busy *busyError
	suffix := ""
	if waited > 0 {
		suffix = fmt.Sprintf(" (waited %s)", waited)
	}
	if !errors.As(err, &busy) {
		return fmt.Errorf("%w%s", err, suffix)
	}
	holders := busy.holders()
	if len(holders) == 0 {
		return fmt.Errorf("%w%s", err, suffix)
	}
	names := make([]string, len(holders))
	for i, holder := range holders {
		names[i] = holder.String()
	}
	return fmt.Errorf("%w%s; held by %s", err, suffix, strings.Join(names, ", "))
}

// holders reads who holds the lock that refused us. An exclusive request for all
// targets is refused by shared holders of the global lock, which write nothing; the
// single-target operations behind them hold their target locks exclusively, so
// those are probed instead.
func (e *busyError) holders() []Holder {
	if holder, ok := readHolder(filepath.Join(e.dir, e.file)); ok {
		return []Holder{holder}
	}
	if e.file != globalLockName || !e.exclusive {
		return nil
	}
	entries, err := os.ReadDir(e.dir)
	if err != nil {
		return nil
	}
	var holders []Holder
	for _, entry := range entries {
		name := entry.Name()
		if name == globalLockName || !strings.HasSuffix(name, ".lock") {
			continue
		}
		if holder, ok := heldTargetLock(filepath.Join(e.dir, name)); ok {
			holders = append(holders, holder)
			if len(holders) == maxReportedHolders {
				break
			}
		}
	}
	return holders
}

// heldTargetLock reports the holder of a target lock that is currently held. The
// probe takes a shared lock, which conflicts only with an exclusive holder.
func heldTargetLock(path string) (Holder, bool) {
	// #nosec G304 -- lock files inside the operator-owned locks directory.
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return Holder{}, false
	}
	defer f.Close()
	if err := lockFile(f, true); err == nil {
		unlockFile(f)
		return Holder{}, false
	} else if !errors.Is(err, ErrBusy) {
		return Holder{}, false
	}
	return readHolder(path)
}

func readHolder(path string) (Holder, bool) {
	// #nosec G304 -- lock files inside the operator-owned locks directory.
	f, err := os.Open(path)
	if err != nil {
		return Holder{}, false
	}
	defer f.Close()
	data := make([]byte, maxHolderBytes)
	n, _ := f.ReadAt(data, 0)
	data = bytes.TrimSpace(data[:n])
	var holder Holder
	if len(data) == 0 || json.Unmarshal(data, &holder) != nil || holder.PID <= 0 {
		return Holder{}, false
	}
	return holder, true
}

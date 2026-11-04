package gateway

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type denyingApprover struct{}

func (denyingApprover) Enabled() bool { return true }

func (denyingApprover) Request(context.Context, ApprovalRequest) Approval {
	return Approval{Decision: DecisionDenied}
}

// A job must release its slot and snapshot before it is reported done, so an agent
// that polls job_status and submits right away is never told the gateway is busy.
func TestJobReleasesSlotAndSnapshotBeforeReportingDone(t *testing.T) {
	m := NewJobManager(JobManagerOptions{Approver: denyingApprover{}, Redactor: NewRedactor()})

	var mu sync.Mutex
	var seenDone []bool
	var seenActive []string
	m.removeSnapshot = func(dir string) error {
		// Observe what clients see while the (possibly slow) removal runs.
		mu.Lock()
		for _, view := range m.List() {
			seenDone = append(seenDone, view.Done())
		}
		seenActive = append(seenActive, m.Active())
		mu.Unlock()
		return RemoveSnapshot(dir)
	}

	snapshotDir := filepath.Join(t.TempDir(), "bundle")
	if err := os.MkdirAll(snapshotDir, 0o700); err != nil {
		t.Fatal(err)
	}
	spec := JobSpec{Tool: "run_cli", NeedsApproval: true, Snapshot: &Snapshot{Dir: snapshotDir}, Timeout: time.Minute}
	view, err := m.Submit(spec)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	final, err := m.Wait(ctx, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.State != JobDenied {
		t.Fatalf("state = %s, want %s", final.State, JobDenied)
	}

	mu.Lock()
	if len(seenDone) != 1 || seenDone[0] {
		t.Errorf("job was reported done while its snapshot was still being removed: %v", seenDone)
	}
	if len(seenActive) != 1 || seenActive[0] != view.ID {
		t.Errorf("active job during snapshot removal = %v, want [%s]", seenActive, view.ID)
	}
	mu.Unlock()

	if got := m.Active(); got != "" {
		t.Errorf("Active() = %q after the job was reported done", got)
	}
	if _, err := os.Stat(snapshotDir); !os.IsNotExist(err) {
		t.Errorf("snapshot must be removed once the job is reported done: %v", err)
	}
	if _, err := m.Submit(JobSpec{Tool: "run_cli", NeedsApproval: true, Timeout: time.Minute}); errors.Is(err, ErrBusy) {
		t.Fatalf("submit after done: %v", err)
	}
	m.WaitAll()
}

// Polling Get until Done must never observe the slot still taken.
func TestJobDoneImpliesSlotReleased(t *testing.T) {
	m := NewJobManager(JobManagerOptions{Approver: denyingApprover{}, Redactor: NewRedactor()})
	for range 50 {
		view, err := m.Submit(JobSpec{Tool: "run_cli", NeedsApproval: true, Snapshot: &Snapshot{Dir: t.TempDir()}, Timeout: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			got, _ := m.Get(view.ID)
			if got.Done() {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("job %s not done after 5s: %s", view.ID, got.State)
			}
			runtime.Gosched()
		}
		// Reading the raw slot, not Active(), which already filters finished jobs.
		m.mu.Lock()
		raw := m.active
		m.mu.Unlock()
		if raw == view.ID {
			t.Fatalf("job %s reported done while still holding the active slot", view.ID)
		}
		if _, err := os.Stat(view.Snapshot.Dir); !os.IsNotExist(err) {
			t.Fatalf("job %s reported done before its snapshot was removed: %v", view.ID, err)
		}
	}
	m.WaitAll()
}

// The slot is freed in the same step that reports the job done, so the gateway never
// looks idle while a job is still unfinished (which would let a second job start).
func TestJobSlotNotFreedBeforeReportingDone(t *testing.T) {
	m := NewJobManager(JobManagerOptions{Approver: denyingApprover{}, Redactor: NewRedactor()})
	for range 50 {
		stop := make(chan struct{})
		violations := make(chan string, 1)
		var watcher sync.WaitGroup
		watcher.Add(1)
		go func() {
			defer watcher.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				// Read the slot and the job states in one critical section, as Submit does.
				m.mu.Lock()
				raw := m.active
				var unfinished []string
				for _, id := range m.order {
					if !m.jobs[id].snapshotView().Done() {
						unfinished = append(unfinished, id)
					}
				}
				m.mu.Unlock()
				if raw == "" && len(unfinished) > 0 {
					select {
					case violations <- strings.Join(unfinished, ","):
					default:
					}
					return
				}
				runtime.Gosched()
			}
		}()

		view, err := m.Submit(JobSpec{Tool: "run_cli", NeedsApproval: true, Snapshot: &Snapshot{Dir: t.TempDir()}, Timeout: time.Minute})
		if err != nil {
			close(stop)
			watcher.Wait()
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err = m.Wait(ctx, view.ID)
		cancel()
		close(stop)
		watcher.Wait()
		if err != nil {
			t.Fatal(err)
		}
		select {
		case ids := <-violations:
			t.Fatalf("slot was free while job(s) %s were still unfinished", ids)
		default:
		}
	}
	m.WaitAll()
}

// A failed snapshot removal is reported but must not keep the slot taken.
func TestJobReleasesSlotWhenSnapshotRemovalFails(t *testing.T) {
	var console strings.Builder
	var consoleMu sync.Mutex
	m := NewJobManager(JobManagerOptions{Approver: denyingApprover{}, Redactor: NewRedactor(), Console: lockedWriter{mu: &consoleMu, w: &console}})
	m.removeSnapshot = func(string) error { return errors.New("permission denied") }

	view, err := m.Submit(JobSpec{Tool: "run_cli", NeedsApproval: true, Snapshot: &Snapshot{Dir: filepath.Join(t.TempDir(), "bundle")}, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := m.Wait(ctx, view.ID); err != nil {
		t.Fatal(err)
	}
	if got := m.Active(); got != "" {
		t.Errorf("Active() = %q after a failed snapshot removal", got)
	}
	m.mu.Lock()
	raw := m.active
	m.mu.Unlock()
	if raw != "" {
		t.Errorf("active slot = %q after a failed snapshot removal", raw)
	}
	consoleMu.Lock()
	out := console.String()
	consoleMu.Unlock()
	if !strings.Contains(out, "could not remove snapshot") {
		t.Errorf("console does not report the failed removal:\n%s", out)
	}
	if _, err := m.Submit(JobSpec{Tool: "run_cli", NeedsApproval: true, Timeout: time.Minute}); errors.Is(err, ErrBusy) {
		t.Fatalf("submit after failed removal: %v", err)
	}
	m.WaitAll()
}

type lockedWriter struct {
	mu *sync.Mutex
	w  *strings.Builder
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

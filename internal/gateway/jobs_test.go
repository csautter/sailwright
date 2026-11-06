package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
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

// Block the finish audit write to exercise the window where a new submission
// used to overtake it. The current job must remain busy until the write completes.
func TestJobFinishAuditPrecedesNextSubmission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	audit, err := OpenAuditLog(path, NewRedactor())
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()
	writingFinish := make(chan struct{})
	resume := make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(resume) })
	var records atomic.Int32
	audit.now = func() time.Time {
		// Submission and approval are the first two events.
		if records.Add(1) == 3 {
			close(writingFinish)
			<-resume
		}
		return time.Now()
	}
	m := NewJobManager(JobManagerOptions{Approver: denyingApprover{}, Redactor: NewRedactor(), Audit: audit})
	spec := JobSpec{Tool: "run_cli", NeedsApproval: true, Timeout: time.Minute}
	first, err := m.Submit(spec)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-writingFinish:
	case <-time.After(10 * time.Second):
		t.Fatal("job did not reach its finish audit write")
	}
	if got := m.Active(); got != first.ID {
		t.Errorf("active during finish audit write = %q, want %q", got, first.ID)
	}
	if _, err := m.Submit(spec); !errors.Is(err, ErrBusy) {
		t.Errorf("submit during finish audit write = %v, want ErrBusy", err)
	}
	unblock.Do(func() { close(resume) })
	m.WaitAll()
	second, err := m.Submit(spec)
	if err != nil {
		t.Fatal(err)
	}
	m.WaitAll()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lifecycle []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var event AuditEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.Event == "job_submitted" || event.Event == "job_finished" {
			lifecycle = append(lifecycle, event.Event+":"+event.JobID)
		}
	}
	want := []string{"job_submitted:" + first.ID, "job_finished:" + first.ID, "job_submitted:" + second.ID, "job_finished:" + second.ID}
	if !reflect.DeepEqual(lifecycle, want) {
		t.Fatalf("audit lifecycle = %v, want %v", lifecycle, want)
	}
}

type sudoApprover struct{}

func (sudoApprover) Enabled() bool { return true }
func (sudoApprover) Request(context.Context, ApprovalRequest) Approval {
	return Approval{Decision: DecisionApproved, BecomePassword: "test-password"}
}

// Snapshot cleanup precedes publishing completion. Check that the password is
// already gone at this boundary, even when the runner fails before starting.
func TestJobRemovesSudoPasswordBeforeReportingDone(t *testing.T) {
	staging := t.TempDir()
	m := NewJobManager(JobManagerOptions{
		Approver: sudoApprover{}, Redactor: NewRedactor(), StagingDir: staging,
		Runner: &Runner{}, // An unpinned binary fails verification before execution.
	})
	checked := false
	m.removeSnapshot = func(dir string) error {
		checked = true
		entries, err := os.ReadDir(staging)
		if err != nil || len(entries) != 0 {
			t.Errorf("password staging directory during snapshot cleanup = %v, error %v", entries, err)
		}
		return RemoveSnapshot(dir)
	}
	view, err := m.Submit(JobSpec{
		Tool: "provision_local", NeedsApproval: true, Become: true,
		Snapshot: &Snapshot{Dir: t.TempDir()}, Timeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	m.WaitAll()
	final, _ := m.Get(view.ID)
	if !checked || !final.SudoPassword || final.State != JobFailed {
		t.Fatalf("expected cleanup of an approved sudo job after runner failure: checked=%v, job=%+v", checked, final)
	}
}

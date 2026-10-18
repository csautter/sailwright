package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// JobState is the lifecycle state of a job.
type JobState string

const (
	JobPendingApproval JobState = "pending_approval"
	JobRunning         JobState = "running"
	JobSucceeded       JobState = "succeeded"
	JobFailed          JobState = "failed"
	JobCancelled       JobState = "cancelled"
	JobDenied          JobState = "denied"
	JobExpired         JobState = "expired"
)

const (
	maxFinishedJobs = 100
	diskBytesPerGB  = 1 << 30
)

// ErrBusy is returned when another job is active; jobs run one at a time.
var ErrBusy = errors.New("another job is active; wait for it or cancel it")

// JobSpec describes a job to run.
type JobSpec struct {
	Tool             string
	Tier             string
	Argv             []string
	Dir              string
	Timeout          time.Duration
	Params           map[string]any
	Peer             string
	Risk             []string
	Snapshot         *Snapshot
	NeedsApproval    bool
	NotSafe          string
	AgentNote        string
	RequiresFreeDisk bool
	// Become lets the operator approve with a sudo password for Ansible become tasks.
	Become bool
}

// JobView is the agent-visible state of a job.
type JobView struct {
	ID        string    `json:"job_id"`
	Tool      string    `json:"tool"`
	Tier      string    `json:"tier"`
	State     JobState  `json:"state"`
	Argv      []string  `json:"argv"`
	Risk      []string  `json:"risk,omitempty"`
	ExitCode  *int      `json:"exit_code,omitempty"`
	Error     string    `json:"error,omitempty"`
	Created   time.Time `json:"created"`
	Started   time.Time `json:"started,omitzero"`
	Finished  time.Time `json:"finished,omitzero"`
	Snapshot  *Snapshot `json:"snapshot,omitempty"`
	OutputLen int64     `json:"output_bytes"`
	// SudoPassword reports that the operator supplied a sudo password for this run.
	// The password itself is never exposed.
	SudoPassword bool `json:"sudo_password,omitempty"`
}

// Done reports whether the job reached a final state.
func (v JobView) Done() bool {
	switch v.State {
	case JobPendingApproval, JobRunning:
		return false
	}
	return true
}

type job struct {
	mu       sync.Mutex
	view     JobView
	output   *outputBuffer
	cancel   context.CancelFunc
	finished chan struct{}
}

func (j *job) snapshotView() JobView {
	j.mu.Lock()
	defer j.mu.Unlock()
	view := j.view
	view.Argv = append([]string(nil), view.Argv...)
	view.OutputLen = j.output.Len()
	return view
}

func (j *job) update(fn func(*JobView)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	fn(&j.view)
}

// JobManager runs at most one job at a time and keeps a short history.
type JobManager struct {
	runner      *Runner
	approver    Approver
	audit       *AuditLog
	redactor    *Redactor
	logsDir     string
	stagingDir  string
	dataDir     string
	minFreeGB   int
	outputLimit int
	binarySHA   string
	onFinish    func()
	console     io.Writer
	follow      io.Writer

	running sync.WaitGroup

	mu     sync.Mutex
	jobs   map[string]*job
	order  []string
	active string
}

// JobManagerOptions configures a JobManager.
type JobManagerOptions struct {
	Runner      *Runner
	Approver    Approver
	Audit       *AuditLog
	Redactor    *Redactor
	LogsDir     string
	StagingDir  string
	DataDir     string
	MinFreeGB   int
	OutputLimit int
	BinarySHA   string
	OnFinish    func()
	Console     io.Writer
	// Follow mirrors job output (redacted) to this writer, e.g. the gateway terminal.
	Follow io.Writer
}

// NewJobManager creates a job manager.
func NewJobManager(opts JobManagerOptions) *JobManager {
	console := opts.Console
	if console == nil {
		console = io.Discard
	}
	return &JobManager{
		runner: opts.Runner, approver: opts.Approver, audit: opts.Audit, redactor: opts.Redactor,
		logsDir: opts.LogsDir, stagingDir: opts.StagingDir, dataDir: opts.DataDir, minFreeGB: opts.MinFreeGB, outputLimit: opts.OutputLimit,
		binarySHA: opts.BinarySHA, onFinish: opts.OnFinish, console: console, follow: opts.Follow,
		jobs: map[string]*job{},
	}
}

// Submit starts a job unless another one is active.
func (m *JobManager) Submit(spec JobSpec) (JobView, error) {
	if spec.NeedsApproval && !m.approver.Enabled() {
		return JobView{}, errors.New("this request needs host approval, but the gateway has no approval console (start it in a foreground terminal)")
	}

	m.mu.Lock()
	if m.active != "" {
		if active, ok := m.jobs[m.active]; ok && !active.snapshotView().Done() {
			m.mu.Unlock()
			return JobView{}, fmt.Errorf("%w (active job %s)", ErrBusy, m.active)
		}
	}
	id, err := newJobID()
	if err != nil {
		m.mu.Unlock()
		return JobView{}, err
	}
	state := JobRunning
	if spec.NeedsApproval {
		state = JobPendingApproval
	}
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{
		view: JobView{
			ID: id, Tool: spec.Tool, Tier: spec.Tier, State: state, Argv: m.redactor.Strings(spec.Argv),
			Risk: spec.Risk, Created: time.Now().UTC(), Snapshot: spec.Snapshot,
		},
		output:   newOutputBuffer(m.outputLimit),
		cancel:   cancel,
		finished: make(chan struct{}),
	}
	m.jobs[id] = j
	m.order = append(m.order, id)
	m.active = id
	m.trimHistoryLocked()
	m.mu.Unlock()

	m.record(AuditEvent{Event: "job_submitted", Peer: spec.Peer, Tool: spec.Tool, JobID: id, Tier: spec.Tier, Argv: spec.Argv, Params: spec.Params, Risk: spec.Risk, Snapshot: spec.Snapshot})
	m.running.Add(1)
	go func() {
		defer m.running.Done()
		m.execute(ctx, j, spec)
	}()
	return j.snapshotView(), nil
}

func (m *JobManager) execute(ctx context.Context, j *job, spec JobSpec) {
	id := j.snapshotView().ID
	defer func() {
		if spec.Snapshot != nil {
			_ = RemoveSnapshot(spec.Snapshot.Dir)
		}
		m.mu.Lock()
		if m.active == id {
			m.active = ""
		}
		m.mu.Unlock()
		close(j.finished)
		if m.onFinish != nil {
			m.onFinish()
		}
	}()

	argv := spec.Argv
	redactor := m.redactor
	if spec.NeedsApproval {
		approval := m.approver.Request(ctx, ApprovalRequest{
			ID: id, Tool: spec.Tool, Argv: m.redactor.Strings(spec.Argv), Dir: spec.Dir, Risk: spec.Risk,
			Snapshot: spec.Snapshot, NotSafe: spec.NotSafe, AgentNote: spec.AgentNote, BinarySHA: m.binarySHA,
			Become: spec.Become,
		})
		decision := approval.Decision
		m.record(AuditEvent{Event: "approval_decision", Tool: spec.Tool, JobID: id, Decision: string(decision), Argv: spec.Argv, Snapshot: spec.Snapshot, SudoPassword: approval.BecomePassword != ""})
		if decision == DecisionApproved && approval.BecomePassword != "" {
			if !spec.Become {
				m.finish(j, JobFailed, nil, "a sudo password was supplied for a request that does not support it", time.Time{})
				return
			}
			path, cleanup, err := writeBecomePasswordFile(m.stagingDir, approval.BecomePassword)
			if err != nil {
				m.finish(j, JobFailed, nil, "could not pass the sudo password: "+err.Error(), time.Time{})
				return
			}
			defer cleanup()
			argv = WithBecomePasswordFile(argv, path)
			redactor = m.redactor.With(approval.BecomePassword)
			j.update(func(v *JobView) {
				v.SudoPassword = true
				v.Argv = redactor.Strings(argv)
			})
		}
		if decision != DecisionApproved {
			final := JobDenied
			switch decision {
			case DecisionExpired:
				final = JobExpired
			case DecisionCancelled:
				final = JobCancelled
			}
			m.finish(j, final, nil, "approval "+string(decision), time.Time{})
			return
		}
	}

	if spec.RequiresFreeDisk && m.minFreeGB > 0 { // a negative value disables the check
		if free, err := freeDiskBytes(m.dataDir); err == nil && free < uint64(m.minFreeGB)*diskBytesPerGB { // #nosec G115 -- validated positive config value.
			m.finish(j, JobFailed, nil, fmt.Sprintf("only %d GB free in %s, at least %d GB required", free/diskBytesPerGB, m.dataDir, m.minFreeGB), time.Time{})
			return
		}
	}

	started := time.Now().UTC()
	j.update(func(v *JobView) {
		v.State = JobRunning
		v.Started = started
	})
	fmt.Fprintf(m.console, "job %s started: sailwright %s\n", id, quoteArgv(redactor.Strings(argv)))

	var sinks []io.Writer
	sinks = append(sinks, j.output)
	if m.logsDir != "" {
		logPath := filepath.Join(m.logsDir, id+".log")
		// #nosec G304 -- fixed file name inside the gateway log directory.
		if logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, privateFilePermission); err == nil {
			defer logFile.Close()
			sinks = append(sinks, logFile)
			if m.follow == nil {
				fmt.Fprintf(m.console, "  live output: tail -f %s\n", logPath)
			}
		}
	}
	if m.follow != nil {
		// Output is already redacted and stripped of escape sequences at this point.
		sinks = append(sinks, &prefixWriter{prefix: "[" + id + "] ", next: m.follow})
	}
	writer := newLineRedactingWriter(redactor, io.MultiWriter(sinks...))

	runCtx, cancelTimeout := context.WithTimeout(ctx, spec.Timeout)
	defer cancelTimeout()
	code, err := m.runner.Run(runCtx, argv, spec.Dir, writer)
	_ = writer.Flush()

	switch {
	case errors.Is(err, context.DeadlineExceeded):
		m.finish(j, JobFailed, &code, fmt.Sprintf("timed out after %s", spec.Timeout), started)
	case errors.Is(err, context.Canceled):
		m.finish(j, JobCancelled, &code, "cancelled", started)
	case err != nil:
		m.finish(j, JobFailed, &code, err.Error(), started)
	case code == 0:
		m.finish(j, JobSucceeded, &code, "", started)
	default:
		m.finish(j, JobFailed, &code, fmt.Sprintf("exit code %d", code), started)
	}
}

func (m *JobManager) finish(j *job, state JobState, code *int, message string, started time.Time) {
	finished := time.Now().UTC()
	j.update(func(v *JobView) {
		v.State = state
		v.ExitCode = code
		v.Error = m.redactor.String(message)
		v.Finished = finished
	})
	view := j.snapshotView()
	var duration int64
	if !started.IsZero() {
		duration = finished.Sub(started).Milliseconds()
	}
	m.record(AuditEvent{Event: "job_finished", Tool: view.Tool, JobID: view.ID, ExitCode: code, DurationMS: duration, Error: message, Decision: string(state)})
	fmt.Fprintf(m.console, "job %s %s\n", view.ID, state)
}

// Get returns a job view.
func (m *JobManager) Get(id string) (JobView, bool) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return JobView{}, false
	}
	return j.snapshotView(), true
}

// Output returns a chunk of a job's output.
func (m *JobManager) Output(id string, offset int64, maxBytes int) (OutputChunk, bool) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return OutputChunk{}, false
	}
	return j.output.Read(offset, maxBytes), true
}

// Cancel cancels a pending or running job.
func (m *JobManager) Cancel(id string) (JobView, bool) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return JobView{}, false
	}
	j.cancel()
	return j.snapshotView(), true
}

// Wait blocks until the job is finished or ctx is done (used by tests and shutdown).
func (m *JobManager) Wait(ctx context.Context, id string) (JobView, error) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return JobView{}, fmt.Errorf("unknown job %s", id)
	}
	select {
	case <-j.finished:
		return j.snapshotView(), nil
	case <-ctx.Done():
		return j.snapshotView(), ctx.Err()
	}
}

// List returns all known jobs, oldest first.
func (m *JobManager) List() []JobView {
	m.mu.Lock()
	defer m.mu.Unlock()
	views := make([]JobView, 0, len(m.order))
	for _, id := range m.order {
		views = append(views, m.jobs[id].snapshotView())
	}
	return views
}

// Active returns the active job id, if any.
func (m *JobManager) Active() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active
}

// CancelAll cancels every unfinished job (shutdown).
func (m *JobManager) CancelAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		j.cancel()
	}
}

// WaitAll blocks until every job goroutine has finished (shutdown).
func (m *JobManager) WaitAll() {
	m.running.Wait()
}

func (m *JobManager) trimHistoryLocked() {
	for len(m.order) > maxFinishedJobs {
		oldest := m.order[0]
		if oldest == m.active {
			return
		}
		delete(m.jobs, oldest)
		m.order = m.order[1:]
	}
}

func (m *JobManager) record(event AuditEvent) {
	event.BinarySHA256 = m.binarySHA
	if err := m.audit.Record(event); err != nil {
		fmt.Fprintf(m.console, "audit log write failed: %v\n", err)
	}
}

func newJobID() (string, error) {
	raw := make([]byte, 4)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// OutputChunk is a slice of job output.
type OutputChunk struct {
	Data            string `json:"data"`
	Offset          int64  `json:"offset"`
	NextOffset      int64  `json:"next_offset"`
	TruncatedBefore bool   `json:"truncated_before"`
}

// outputBuffer keeps the most recent output up to a limit, with absolute offsets.
type outputBuffer struct {
	mu    sync.Mutex
	data  []byte
	base  int64
	limit int
}

func newOutputBuffer(limit int) *outputBuffer {
	return &outputBuffer{limit: limit}
}

func (b *outputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if over := len(b.data) - b.limit; over > 0 {
		b.data = append([]byte(nil), b.data[over:]...)
		b.base += int64(over)
	}
	return len(p), nil
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
	truncated := false
	if offset < b.base {
		offset = b.base
		truncated = true
	}
	start := offset - b.base
	if start > int64(len(b.data)) {
		start = int64(len(b.data))
	}
	end := start + int64(maxBytes)
	if end > int64(len(b.data)) {
		end = int64(len(b.data))
	}
	return OutputChunk{
		Data:            string(b.data[start:end]),
		Offset:          b.base + start,
		NextOffset:      b.base + end,
		TruncatedBefore: truncated,
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

// WithBecomePasswordFile passes an Ansible become password file to `sailwright
// provision` as a passthrough argument.
func WithBecomePasswordFile(argv []string, path string) []string {
	out := append([]string(nil), argv...)
	if !containsString(out, "--") {
		out = append(out, "--")
	}
	return append(out, "--become-password-file", path)
}

// writeBecomePasswordFile stores the password in an owner-only file inside the
// gateway staging dir; the returned cleanup removes it.
func writeBecomePasswordFile(dir string, password string) (string, func(), error) {
	if err := ensurePrivateDir(dir); err != nil {
		return "", nil, err
	}
	file, err := os.CreateTemp(dir, "become-*")
	if err != nil {
		return "", nil, err
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if err := file.Chmod(privateFilePermission); err != nil && runtime.GOOS != "windows" {
		_ = file.Close()
		cleanup()
		return "", nil, err
	}
	if _, err := file.WriteString(password); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

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
	"strings"
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
	// BeforeRun reserves resources after approval and releases them on every exit.
	BeforeRun        func(context.Context) (func(), error)
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
	// BecomeWarning is shown with approve-sudo when the args can redirect the play.
	BecomeWarning string
	// Target is the VM of a safe-set operation; build jobs record console frames for it.
	Target *JobTarget
	// Env adds NAME=value entries to this job's child environment.
	Env []string
	// OnFinished runs after the command ran, before the job is reported done.
	// succeeded is true only for exit code 0.
	OnFinished func(jobID string, succeeded bool)
	// Recheck runs in the job right before the command starts, under the job's
	// context, for jobs that do not need approval yet. A non-nil result makes the
	// job wait for operator approval after all. Slow checks (hypervisor queries)
	// belong here rather than in the tool call, which must return quickly, and
	// running them last narrows the gap between check and use.
	Recheck func(ctx context.Context) *Escalation
}

// Escalation is why a job that looked safe needs operator approval.
type Escalation struct {
	Risk    []string
	NotSafe string
}

const errNoApprovalConsole = "this request needs host approval, but the gateway has no approval console (start it in a foreground terminal)"

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
	// LastOutputAt is when the job last wrote output; builds can be silent for long.
	LastOutputAt time.Time `json:"last_output_at,omitzero"`
	// ScreenshotAvailable reports that job_screenshot has a console frame.
	ScreenshotAvailable bool `json:"screenshot_available,omitempty"`
	// ScreenChangedAt is when the VM console last changed visibly (cursor blinks and
	// spinners do not count).
	ScreenChangedAt time.Time `json:"screen_changed_at,omitzero"`
	// StuckSuspected is a hint that neither the console nor the output changed for a
	// long time. The gateway never cancels a job because of it.
	StuckSuspected bool `json:"stuck_suspected,omitempty"`
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
	// screens is set for build jobs.
	screens *screenRecorder
	// redactor adds per-job secrets (a sudo password); nil means the manager's. Only
	// the job's execute goroutine reads or writes it.
	redactor *Redactor
	// cleanupBecomePassword is also deferred as a fallback; removal is idempotent.
	cleanupBecomePassword func()
	// snapshotDir is the job's bundle snapshot, removed before the job is reported done.
	// Only the job's execute goroutine reads or writes it after Submit.
	snapshotDir string
}

func (j *job) snapshotView() JobView {
	j.mu.Lock()
	defer j.mu.Unlock()
	view := j.view
	view.Argv = append([]string(nil), view.Argv...)
	view.OutputLen = j.output.Len()
	view.LastOutputAt = j.output.LastWrite()
	if j.screens != nil {
		status := j.screens.Status()
		view.ScreenshotAvailable = status.Frames > 0
		view.ScreenChangedAt = status.ChangedAt
		view.StuckSuspected = status.StuckSuspected
	}
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
	// removeSnapshot deletes a snapshot directory; tests replace it to observe ordering.
	removeSnapshot func(dir string) error
	console        io.Writer
	follow         io.Writer
	live           LiveTail
	cacheDir       string
	screensDir     string
	stuckAfter     time.Duration

	running sync.WaitGroup

	mu      sync.Mutex
	jobs    map[string]*job
	order   []string
	active  string
	viewURL string
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
	// Live shows the newest lines of the running job on the gateway terminal. Follow
	// takes precedence.
	Live LiveTail
	// CacheDir is the sailwright cache dir, where builds record console frames.
	CacheDir string
	// ScreensDir keeps the sampled frames per job; empty disables screenshots.
	ScreensDir string
	// StuckAfter is the idle time after which a build is suspected stuck; 0 disables.
	StuckAfter time.Duration
}

// NewJobManager creates a job manager.
func NewJobManager(opts JobManagerOptions) *JobManager {
	console := opts.Console
	if console == nil {
		console = io.Discard
	}
	if opts.LogsDir != "" {
		pruneOldLogs(opts.LogsDir, maxJobLogAge)
	}
	return &JobManager{
		runner: opts.Runner, approver: opts.Approver, audit: opts.Audit, redactor: opts.Redactor,
		logsDir: opts.LogsDir, stagingDir: opts.StagingDir, dataDir: opts.DataDir, minFreeGB: opts.MinFreeGB, outputLimit: opts.OutputLimit,
		binarySHA: opts.BinarySHA, onFinish: opts.OnFinish, console: console, follow: opts.Follow, live: opts.Live,
		cacheDir: opts.CacheDir, screensDir: opts.ScreensDir, stuckAfter: opts.StuckAfter,
		removeSnapshot: RemoveSnapshot,
		jobs:           map[string]*job{},
	}
}

// SetViewURL sets the live view address printed when a build starts.
func (m *JobManager) SetViewURL(url string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.viewURL = url
}

func (m *JobManager) liveViewURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.viewURL
}

// Submit starts a job unless another one is active.
func (m *JobManager) Submit(spec JobSpec) (JobView, error) {
	if spec.NeedsApproval && !m.approver.Enabled() {
		return JobView{}, errors.New(errNoApprovalConsole)
	}

	m.mu.Lock()
	if active := m.activeLocked(); active != "" {
		m.mu.Unlock()
		return JobView{}, fmt.Errorf("%w (active job %s)", ErrBusy, active)
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
	if spec.Snapshot != nil {
		j.snapshotDir = spec.Snapshot.Dir
	}
	if spec.Target != nil && spec.Target.Kind == OpBuild && m.screensDir != "" {
		j.screens = newScreenRecorder(vncSnapshotDir(m.cacheDir, *spec.Target), filepath.Join(m.screensDir, id), m.stuckAfter, j.output.LastWrite, func(idle time.Duration) {
			m.record(AuditEvent{Event: "job_stuck_suspected", Tool: spec.Tool, JobID: id, DurationMS: idle.Milliseconds()})
			fmt.Fprintf(m.console, "job %s: no visible console change and no output for %s; the build may be stuck%s\n", id, idle, m.liveViewHint())
		})
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
		// Defensive: every path calls finish today, which already released the job.
		// This keeps the slot from leaking if a future early return skips finish.
		m.removeJobSnapshot(j)
		m.mu.Lock()
		m.releaseSlotLocked(id)
		m.mu.Unlock()
		close(j.finished)
		if m.onFinish != nil {
			m.onFinish()
		}
	}()

	argv := spec.Argv
	redactor := m.redactor
	if !spec.NeedsApproval && spec.Recheck != nil {
		escalation := spec.Recheck(ctx)
		if ctx.Err() != nil {
			m.finish(j, JobCancelled, nil, "cancelled", time.Time{})
			return
		}
		if escalation != nil {
			if !m.approver.Enabled() {
				m.finish(j, JobFailed, nil, errNoApprovalConsole, time.Time{})
				return
			}
			spec.Tier, spec.NeedsApproval, spec.Risk, spec.NotSafe = tierApproval, true, escalation.Risk, escalation.NotSafe
			j.update(func(v *JobView) {
				v.Tier = tierApproval
				v.State = JobPendingApproval
				v.Risk = escalation.Risk
			})
			m.record(AuditEvent{Event: "job_escalated", Tool: spec.Tool, JobID: id, Tier: tierApproval, Risk: escalation.Risk})
		}
	}
	if spec.NeedsApproval {
		approval := m.approver.Request(ctx, ApprovalRequest{
			ID: id, Tool: spec.Tool, Argv: m.redactor.Strings(spec.Argv), Dir: spec.Dir, Risk: spec.Risk,
			Snapshot: spec.Snapshot, NotSafe: spec.NotSafe, AgentNote: spec.AgentNote, BinarySHA: m.binarySHA,
			Become: spec.Become, BecomeWarning: spec.BecomeWarning,
		})
		decision := approval.Decision
		m.record(AuditEvent{Event: "approval_decision", Tool: spec.Tool, JobID: id, Decision: string(decision), Argv: spec.Argv, Snapshot: spec.Snapshot, SudoPassword: approval.BecomePassword != "", AutoApproved: approval.Auto})
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
			j.cleanupBecomePassword = cleanup
			defer cleanup()
			argv = WithBecomePasswordFile(argv, path)
			redactor = m.redactor.WithRequired(approval.BecomePassword)
			j.redactor = redactor
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

	if spec.BeforeRun != nil {
		release, err := spec.BeforeRun(ctx)
		if err != nil {
			m.finish(j, JobFailed, nil, err.Error(), time.Time{})
			return
		}
		defer release()
	}
	started := time.Now().UTC()
	j.update(func(v *JobView) {
		v.State = JobRunning
		v.Started = started
	})
	fmt.Fprintf(m.console, "job %s started: sailwright %s\n", id, quoteArgv(redactor.Strings(argv)))
	stopScreens := func() {}
	if j.screens != nil {
		stopScreens = j.screens.Start(screenSampleInterval)
		defer stopScreens()
		if url := m.liveViewURL(); url != "" {
			fmt.Fprintf(m.console, "  live view:   %s\n", url)
		}
	}

	var sinks []io.Writer
	sinks = append(sinks, j.output)
	logPath := ""
	if m.logsDir != "" {
		path := filepath.Join(m.logsDir, id+".log")
		// #nosec G304 -- fixed file name inside the gateway log directory.
		if logFile, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, privateFilePermission); err == nil {
			defer logFile.Close()
			sinks = append(sinks, &cappedWriter{w: logFile, limit: maxJobLogBytes})
			logPath = path
			if m.follow == nil && m.live == nil {
				fmt.Fprintf(m.console, "  live output: %s\n", liveOutputCommand(logPath))
			}
		}
	}
	var live LiveTail
	liveEnded := false
	switch {
	case m.follow != nil:
		// Output is already redacted and stripped of escape sequences at this point.
		sinks = append(sinks, &prefixWriter{prefix: "[" + id + "] ", next: m.follow})
	case m.live != nil:
		live = m.live
		sinks = append(sinks, live.Begin(id, logPath))
		// Removes the tail if the job panics before the regular End below.
		defer func() {
			if !liveEnded {
				live.End(true)
			}
		}()
	}
	writer := newLineRedactingWriter(redactor, io.MultiWriter(sinks...))

	runCtx, cancelTimeout := context.WithTimeout(ctx, spec.Timeout)
	defer cancelTimeout()
	runner := m.runner
	if len(spec.Env) > 0 {
		runner = runner.WithEnv(spec.Env...)
	}
	code, err := runner.Run(runCtx, argv, spec.Dir, writer)
	_ = writer.Flush()
	if live != nil {
		// Remove the tail before the job is reported as finished.
		liveEnded = true
		live.End(err != nil || code != 0)
	}
	// Keep the final frame before the job is reported as finished.
	stopScreens()
	if spec.OnFinished != nil {
		spec.OnFinished(id, err == nil && code == 0)
	}

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
	redactor := m.redactor
	if j.redactor != nil {
		redactor = j.redactor
	}
	message = redactor.String(message)
	if j.cleanupBecomePassword != nil {
		j.cleanupBecomePassword()
	}
	// A job reported as done must have released its snapshot and slot: agents poll
	// job_status and submit the next job right away. The (possibly slow) removal runs
	// first; the terminal state and the free slot are then published together, so no
	// one sees a done job holding the slot or a free slot next to an unfinished job.
	m.removeJobSnapshot(j)
	// Record completion while the job still blocks submissions, so the next
	// job_submitted event cannot overtake this job_finished event.
	view := j.snapshotView()
	var duration int64
	if !started.IsZero() {
		duration = finished.Sub(started).Milliseconds()
	}
	m.record(AuditEvent{Event: "job_finished", Tool: view.Tool, JobID: view.ID, ExitCode: code, DurationMS: duration, Error: message, Decision: string(state)})
	m.mu.Lock()
	j.update(func(v *JobView) {
		v.State = state
		v.ExitCode = code
		v.Error = message
		v.Finished = finished
	})
	m.releaseSlotLocked(j.snapshotView().ID)
	m.mu.Unlock()
	fmt.Fprintf(m.console, "job %s %s\n", view.ID, state)
}

// removeJobSnapshot deletes the job's bundle snapshot once. A failed removal is
// reported; the leftover is cleaned up when the gateway starts next.
func (m *JobManager) removeJobSnapshot(j *job) {
	dir := j.snapshotDir
	if dir == "" {
		return
	}
	j.snapshotDir = ""
	if err := m.removeSnapshot(dir); err != nil {
		id := j.snapshotView().ID
		fmt.Fprintf(m.console, "job %s: could not remove snapshot %s: %v\n", id, dir, err)
		m.record(AuditEvent{Event: "snapshot_remove_failed", JobID: id, Error: err.Error()})
	}
}

// releaseSlotLocked frees the active slot if the job holds it. m.mu must be held.
func (m *JobManager) releaseSlotLocked(id string) {
	if m.active == id {
		m.active = ""
	}
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

// Active returns the id of the job that blocks new submissions, if any.
func (m *JobManager) Active() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeLocked()
}

// activeLocked is the single busy check for Submit and callers that pre-check
// before doing expensive work; a finished job never counts as active.
func (m *JobManager) activeLocked() string {
	if m.active == "" {
		return ""
	}
	if active, ok := m.jobs[m.active]; ok && !active.snapshotView().Done() {
		return m.active
	}
	return ""
}

// ScreenshotResult is a console frame of a build job, or the reason there is none.
type ScreenshotResult struct {
	View   JobView
	Status ScreenStatus
	Frame  ScreenFrame
	Data   []byte
	Reason string
}

// Screenshot returns a kept console frame of a job; index 0 is the newest. A running
// job is sampled first so the frame is current.
func (m *JobManager) Screenshot(id string, index int) (ScreenshotResult, error) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return ScreenshotResult{}, fmt.Errorf("unknown job %q", id)
	}
	if index < 0 {
		return ScreenshotResult{}, errors.New("index must not be negative")
	}
	if j.screens == nil {
		return ScreenshotResult{View: j.snapshotView(), Reason: "not a build job; only builds record the VM console"}, nil
	}
	if view := j.snapshotView(); view.State == JobRunning && index == 0 {
		j.screens.Sample()
	}
	frame, data, err := j.screens.Frame(index)
	result := ScreenshotResult{View: j.snapshotView(), Status: j.screens.Status(), Frame: frame, Data: data}
	switch {
	case errors.Is(err, errNoScreenFrame):
		result.Reason = noScreenFrameReason(result.View)
	case err != nil:
		return ScreenshotResult{}, err
	}
	return result, nil
}

func noScreenFrameReason(view JobView) string {
	switch {
	case runtime.GOOS == "windows":
		return "this host does not record the VM console (Windows hosts have no VNC capture)"
	case view.State == JobPendingApproval:
		return "the job has not started yet"
	case view.State == JobRunning:
		return "no frame yet: console capture starts about a minute after the build starts, and only for QEMU builds"
	default:
		return "the build recorded no console frames (only QEMU builds on Linux and macOS hosts do)"
	}
}

// LatestScreenJob returns the active build job, or else the most recent one.
func (m *JobManager) LatestScreenJob() (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if active, ok := m.jobs[m.active]; ok && active.screens != nil {
		return m.active, true
	}
	for i := len(m.order) - 1; i >= 0; i-- {
		if m.jobs[m.order[i]].screens != nil {
			return m.order[i], true
		}
	}
	return "", false
}

func (m *JobManager) liveViewHint() string {
	if url := m.liveViewURL(); url != "" {
		return " (live view: " + url + ")"
	}
	return ""
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
		if screens := m.jobs[oldest].screens; screens != nil {
			screens.Remove()
		}
		if m.logsDir != "" {
			_ = os.Remove(filepath.Join(m.logsDir, oldest+".log"))
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

// liveOutputCommand returns a shell command that follows the job log on the
// gateway host. Windows hosts have no tail, so PowerShell is used instead.
func liveOutputCommand(logPath string) string {
	if runtime.GOOS == "windows" {
		return "Get-Content -Wait -Tail 50 -LiteralPath '" + strings.ReplaceAll(logPath, "'", "''") + "'"
	}
	return "tail -f " + logPath
}

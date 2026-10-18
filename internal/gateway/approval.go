package gateway

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Decision is the outcome of an approval request.
type Decision string

const (
	DecisionApproved  Decision = "approved"
	DecisionDenied    Decision = "denied"
	DecisionExpired   Decision = "expired"
	DecisionCancelled Decision = "cancelled"
	DecisionBusy      Decision = "busy"
)

// ApprovalRequest is what the human sees before an out-of-safe-set command runs.
type ApprovalRequest struct {
	ID        string
	Tool      string
	Argv      []string
	Dir       string
	Risk      []string
	Snapshot  *Snapshot
	NotSafe   string
	AgentNote string
	BinarySHA string
	// Become offers "approve-sudo": the operator also enters the sudo password that
	// Ansible needs for become tasks on this host.
	Become bool
}

// Approval is the operator's answer. BecomePassword is only set for approve-sudo and
// must never be shown, logged or returned to the agent.
type Approval struct {
	Decision       Decision
	BecomePassword string
}

// Approver decides approval requests through a channel the agent cannot reach.
type Approver interface {
	Enabled() bool
	Request(ctx context.Context, req ApprovalRequest) Approval
}

// InputHider turns off terminal echo and returns a function that restores it.
type InputHider func() (restore func(), err error)

// DisabledApprover rejects every request; used when the gateway has no terminal.
type DisabledApprover struct{}

// Enabled reports false.
func (DisabledApprover) Enabled() bool { return false }

// Request always denies.
func (DisabledApprover) Request(context.Context, ApprovalRequest) Approval {
	return Approval{Decision: DecisionDenied}
}

// ConsoleApprover asks the operator on the gateway's own foreground terminal. It has
// no network surface, so an agent cannot approve its own requests. At most one request
// is pending; each decision is single-use and expires.
type ConsoleApprover struct {
	out io.Writer
	ttl time.Duration
	now func() time.Time

	hideInput InputHider

	mu      sync.Mutex
	pending *pendingApproval
	closed  bool
	// awaiting is the pending request whose sudo password is typed next.
	awaiting    *pendingApproval
	restoreEcho func()
}

type pendingApproval struct {
	id       string
	become   bool
	decision chan Approval
}

// NewConsoleApprover starts reading operator commands from in.
func NewConsoleApprover(in *bufio.Reader, out io.Writer, ttl time.Duration) *ConsoleApprover {
	approver := &ConsoleApprover{out: out, ttl: ttl, now: time.Now}
	go approver.readCommands(in)
	return approver
}

// HideInputWith sets how terminal echo is turned off while a password is typed.
// Without it, approve-sudo is refused so a password is never echoed.
func (a *ConsoleApprover) HideInputWith(hide InputHider) *ConsoleApprover {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.hideInput = hide
	return a
}

// Enabled reports whether the console is still readable.
func (a *ConsoleApprover) Enabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return !a.closed
}

// Request prints the request and blocks until the operator decides, the request
// expires or ctx is cancelled.
func (a *ConsoleApprover) Request(ctx context.Context, req ApprovalRequest) Approval {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return Approval{Decision: DecisionDenied}
	}
	if a.pending != nil {
		a.mu.Unlock()
		return Approval{Decision: DecisionBusy}
	}
	pending := &pendingApproval{id: req.ID, become: req.Become, decision: make(chan Approval, 1)}
	a.pending = pending
	expires := a.now().Add(a.ttl)
	a.printRequest(req, expires)
	a.mu.Unlock()

	timer := time.NewTimer(a.ttl)
	defer timer.Stop()

	var approval Approval
	select {
	case approval = <-pending.decision:
	case <-timer.C:
		approval = Approval{Decision: DecisionExpired}
	case <-ctx.Done():
		approval = Approval{Decision: DecisionCancelled}
	}

	a.mu.Lock()
	if a.pending == pending {
		a.pending = nil
	}
	if a.awaiting == pending {
		a.stopAwaitingLocked()
	}
	note := ""
	if approval.BecomePassword != "" {
		note = " (with sudo password)"
	}
	fmt.Fprintf(a.out, "approval %s: %s%s\n", req.ID, approval.Decision, note)
	a.mu.Unlock()
	return approval
}

func (a *ConsoleApprover) printRequest(req ApprovalRequest, expires time.Time) {
	var b strings.Builder
	b.WriteString("\n================ APPROVAL REQUIRED ================\n")
	fmt.Fprintf(&b, "id:        %s\n", req.ID)
	fmt.Fprintf(&b, "tool:      %s\n", sanitizeForTerminal(req.Tool))
	fmt.Fprintf(&b, "command:   sailwright %s\n", quoteArgv(req.Argv))
	fmt.Fprintf(&b, "workdir:   %s\n", sanitizeForTerminal(req.Dir))
	fmt.Fprintf(&b, "binary:    sha256 %s\n", req.BinarySHA)
	if req.Snapshot != nil {
		fmt.Fprintf(&b, "bundle:    %s (host %s)\n", sanitizeForTerminal(req.Snapshot.AgentRoot), sanitizeForTerminal(req.Snapshot.HostRoot))
		fmt.Fprintf(&b, "snapshot:  %d files, %d bytes, tree sha256 %s\n", req.Snapshot.Files, req.Snapshot.Bytes, req.Snapshot.TreeHash)
		fmt.Fprintf(&b, "           review: %s\n", sanitizeForTerminal(req.Snapshot.Dir))
	}
	risk := "none detected"
	if len(req.Risk) > 0 {
		risk = strings.Join(req.Risk, ", ")
	}
	fmt.Fprintf(&b, "risk:      %s\n", risk)
	if req.NotSafe != "" {
		fmt.Fprintf(&b, "not safe:  %s\n", sanitizeForTerminal(req.NotSafe))
	}
	if req.AgentNote != "" {
		fmt.Fprintf(&b, "agent note (untrusted): %s\n", sanitizeForTerminal(truncate(req.AgentNote, 500)))
	}
	fmt.Fprintf(&b, "expires:   %s\n", expires.Format("15:04:05"))
	if req.Become {
		fmt.Fprintf(&b, "sudo:      roles with become need your sudo password; \"approve-sudo %s\" asks for it\n", req.ID)
		fmt.Fprintf(&b, "           (hidden input, used only for this run, never shown to the agent)\n")
		fmt.Fprintf(&b, "Type \"approve-sudo %s\", \"approve %s\" or \"deny %s\" and press Enter.\n", req.ID, req.ID, req.ID)
	} else {
		fmt.Fprintf(&b, "Type \"approve %s\" or \"deny %s\" and press Enter.\n", req.ID, req.ID)
	}
	b.WriteString("===================================================\n")
	_, _ = io.WriteString(a.out, b.String())
}

func (a *ConsoleApprover) readCommands(in *bufio.Reader) {
	for {
		line, err := in.ReadString('\n')
		if !a.consumePassword(line) && strings.TrimSpace(line) != "" {
			a.handleCommand(strings.Fields(line))
		}
		if err != nil {
			a.mu.Lock()
			a.stopAwaitingLocked()
			a.closed = true
			if !errors.Is(err, io.EOF) {
				fmt.Fprintf(a.out, "approval console closed: %v\n", err)
			}
			a.mu.Unlock()
			return
		}
	}
}

func (a *ConsoleApprover) handleCommand(fields []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(fields) == 0 {
		return
	}
	switch fields[0] {
	case "approve-sudo":
		if len(fields) != 2 {
			fmt.Fprintln(a.out, "usage: approve-sudo <id>")
			return
		}
		if a.pending == nil || a.pending.id != fields[1] {
			fmt.Fprintf(a.out, "no pending approval with id %q\n", sanitizeForTerminal(fields[1]))
			return
		}
		if !a.pending.become {
			fmt.Fprintln(a.out, "this request does not run Ansible on this host; use approve")
			return
		}
		if a.hideInput == nil {
			fmt.Fprintln(a.out, "cannot hide input on this terminal; approve-sudo is not available")
			return
		}
		restore, err := a.hideInput()
		if err != nil {
			fmt.Fprintf(a.out, "cannot hide input (%v); approve-sudo is not available\n", err)
			return
		}
		a.awaiting, a.restoreEcho = a.pending, restore
		fmt.Fprintf(a.out, "sudo password for job %s (hidden, press Enter): ", a.pending.id)
	case "approve", "deny":
		if len(fields) != 2 {
			fmt.Fprintf(a.out, "usage: %s <id>\n", fields[0])
			return
		}
		if a.pending == nil || a.pending.id != fields[1] {
			fmt.Fprintf(a.out, "no pending approval with id %q\n", sanitizeForTerminal(fields[1]))
			return
		}
		decision := DecisionDenied
		if fields[0] == "approve" {
			decision = DecisionApproved
		}
		select {
		case a.pending.decision <- Approval{Decision: decision}:
		default:
		}
	case "pending":
		if a.pending == nil {
			fmt.Fprintln(a.out, "no pending approval")
			return
		}
		fmt.Fprintf(a.out, "pending approval: %s\n", a.pending.id)
	default:
		fmt.Fprintln(a.out, `commands: approve <id> | approve-sudo <id> | deny <id> | pending`)
	}
}

// consumePassword treats line as the sudo password if approve-sudo is waiting for one.
func (a *ConsoleApprover) consumePassword(line string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.awaiting == nil {
		return false
	}
	awaiting := a.awaiting
	a.stopAwaitingLocked()
	fmt.Fprintln(a.out)

	password := strings.TrimRight(line, "\r\n")
	switch {
	case a.pending != awaiting:
		fmt.Fprintln(a.out, "the approval request is no longer pending; the password was discarded")
	case password == "":
		fmt.Fprintf(a.out, "empty password; nothing approved. Type \"approve-sudo %s\" again.\n", awaiting.id)
	default:
		select {
		case awaiting.decision <- Approval{Decision: DecisionApproved, BecomePassword: password}:
		default:
		}
	}
	return true
}

func (a *ConsoleApprover) stopAwaitingLocked() {
	if a.restoreEcho != nil {
		a.restoreEcho()
	}
	a.awaiting, a.restoreEcho = nil, nil
}

// quoteArgv renders argv unambiguously for review.
func quoteArgv(argv []string) string {
	parts := make([]string, len(argv))
	for i, arg := range argv {
		clean := sanitizeForTerminal(arg)
		if clean == "" || strings.ContainsAny(clean, " \"'\\$`;&|<>()*?[]{}!#~") || clean != arg {
			clean = strconv.Quote(arg)
		}
		parts[i] = clean
	}
	return strings.Join(parts, " ")
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}

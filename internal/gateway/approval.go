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
}

// Approver decides approval requests through a channel the agent cannot reach.
type Approver interface {
	Enabled() bool
	Request(ctx context.Context, req ApprovalRequest) Decision
}

// DisabledApprover rejects every request; used when the gateway has no terminal.
type DisabledApprover struct{}

// Enabled reports false.
func (DisabledApprover) Enabled() bool { return false }

// Request always denies.
func (DisabledApprover) Request(context.Context, ApprovalRequest) Decision { return DecisionDenied }

// ConsoleApprover asks the operator on the gateway's own foreground terminal. It has
// no network surface, so an agent cannot approve its own requests. At most one request
// is pending; each decision is single-use and expires.
type ConsoleApprover struct {
	out io.Writer
	ttl time.Duration
	now func() time.Time

	mu      sync.Mutex
	pending *pendingApproval
	closed  bool
}

type pendingApproval struct {
	id       string
	decision chan Decision
}

// NewConsoleApprover starts reading operator commands from in.
func NewConsoleApprover(in *bufio.Reader, out io.Writer, ttl time.Duration) *ConsoleApprover {
	approver := &ConsoleApprover{out: out, ttl: ttl, now: time.Now}
	go approver.readCommands(in)
	return approver
}

// Enabled reports whether the console is still readable.
func (a *ConsoleApprover) Enabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return !a.closed
}

// Request prints the request and blocks until the operator decides, the request
// expires or ctx is cancelled.
func (a *ConsoleApprover) Request(ctx context.Context, req ApprovalRequest) Decision {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return DecisionDenied
	}
	if a.pending != nil {
		a.mu.Unlock()
		return DecisionBusy
	}
	pending := &pendingApproval{id: req.ID, decision: make(chan Decision, 1)}
	a.pending = pending
	expires := a.now().Add(a.ttl)
	a.printRequest(req, expires)
	a.mu.Unlock()

	timer := time.NewTimer(a.ttl)
	defer timer.Stop()

	var decision Decision
	select {
	case decision = <-pending.decision:
	case <-timer.C:
		decision = DecisionExpired
	case <-ctx.Done():
		decision = DecisionCancelled
	}

	a.mu.Lock()
	if a.pending == pending {
		a.pending = nil
	}
	fmt.Fprintf(a.out, "approval %s: %s\n", req.ID, decision)
	a.mu.Unlock()
	return decision
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
	fmt.Fprintf(&b, "Type \"approve %s\" or \"deny %s\" and press Enter.\n", req.ID, req.ID)
	b.WriteString("===================================================\n")
	_, _ = io.WriteString(a.out, b.String())
}

func (a *ConsoleApprover) readCommands(in *bufio.Reader) {
	for {
		line, err := in.ReadString('\n')
		if strings.TrimSpace(line) != "" {
			a.handleCommand(strings.Fields(line))
		}
		if err != nil {
			a.mu.Lock()
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
		case a.pending.decision <- decision:
		default:
		}
	case "pending":
		if a.pending == nil {
			fmt.Fprintln(a.out, "no pending approval")
			return
		}
		fmt.Fprintf(a.out, "pending approval: %s\n", a.pending.id)
	default:
		fmt.Fprintln(a.out, `commands: approve <id> | deny <id> | pending`)
	}
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

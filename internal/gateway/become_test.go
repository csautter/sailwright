package gateway

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestApproveSudoReadsHiddenPassword(t *testing.T) {
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	out := &syncBuffer{}
	var hidden, restored atomic.Int32
	approver := NewConsoleApprover(bufio.NewReader(reader), out, time.Minute).HideInputWith(func() (func(), error) {
		hidden.Add(1)
		return func() { restored.Add(1) }, nil
	})

	results := make(chan Approval, 1)
	request := func(id string, become bool) {
		go func() { results <- approver.Request(context.Background(), ApprovalRequest{ID: id, Become: become}) }()
		waitFor(t, func() bool { return strings.Contains(out.String(), "approve "+id) })
	}

	request("job1", true)
	if !strings.Contains(out.String(), `"approve-sudo job1"`) {
		t.Fatalf("the prompt must offer approve-sudo:\n%s", out.String())
	}
	_, _ = io.WriteString(writer, "approve-sudo job1\n")
	waitFor(t, func() bool { return strings.Contains(out.String(), "sudo password for job job1") })
	_, _ = io.WriteString(writer, "\n") // empty password: nothing approved, still pending
	waitFor(t, func() bool { return strings.Contains(out.String(), "empty password") })
	_, _ = io.WriteString(writer, "approve-sudo job1\n")
	_, _ = io.WriteString(writer, "s3cret pw\n")
	got := <-results
	if got.Decision != DecisionApproved || got.BecomePassword != "s3cret pw" {
		t.Fatalf("unexpected approval %+v", got)
	}
	if hidden.Load() != 2 || restored.Load() != 2 {
		t.Fatalf("echo must be hidden and restored for each prompt: %d/%d", hidden.Load(), restored.Load())
	}
	if strings.Contains(out.String(), "s3cret") {
		t.Fatal("the password must never be printed")
	}

	request("job2", false)
	_, _ = io.WriteString(writer, "approve-sudo job2\n")
	waitFor(t, func() bool { return strings.Contains(out.String(), "use approve") })
	_, _ = io.WriteString(writer, "approve job2\n")
	if got := <-results; got.Decision != DecisionApproved || got.BecomePassword != "" {
		t.Fatalf("plain approve must not carry a password: %+v", got)
	}

	failing := NewConsoleApprover(bufio.NewReader(strings.NewReader("approve-sudo job4\n")), out, 200*time.Millisecond).HideInputWith(func() (func(), error) {
		return nil, errors.New("no tty")
	})
	if got := failing.Request(context.Background(), ApprovalRequest{ID: "job4", Become: true}); got.Decision == DecisionApproved || got.BecomePassword != "" {
		t.Fatalf("approve-sudo must be refused when input cannot be hidden: %+v", got)
	}

	noHider := NewConsoleApprover(bufio.NewReader(strings.NewReader("approve-sudo job3\n")), out, 200*time.Millisecond)
	if got := noHider.Request(context.Background(), ApprovalRequest{ID: "job3", Become: true}); got.Decision == DecisionApproved || got.BecomePassword != "" {
		t.Fatalf("approve-sudo without a way to hide input must be refused: %+v", got)
	}
}

func TestWithBecomePasswordFile(t *testing.T) {
	if got := WithBecomePasswordFile([]string{"provision", "local", "--check"}, "/s/f"); !reflect.DeepEqual(got, []string{"provision", "local", "--check", "--", "--become-password-file", "/s/f"}) {
		t.Fatalf("got %q", got)
	}
	if got := WithBecomePasswordFile([]string{"provision", "local", "--", "--diff"}, "/s/f"); !reflect.DeepEqual(got, []string{"provision", "local", "--", "--diff", "--become-password-file", "/s/f"}) {
		t.Fatalf("got %q", got)
	}
}

func TestBecomeSupported(t *testing.T) {
	want := runtime.GOOS != "windows"
	cases := map[string]bool{
		"provision local --check":                 want,
		"provision --yes local":                   want,
		"provision -K local":                      want,
		"provision --ask-become-pass local":       want,
		"provision ubuntu --check":                false,
		"install":                                 false,
		"provision -- local":                      false,
		"provision --playbook /x/site.yml local":  want,
		"provision --inventory-path=/x/inv local": want,
		"provision --inventory-path /x/inv local": want,
		"provision local -- -i/x/inv":             want,
		"provision local -- --tags base":          want,
		"provision --playbook /x/local ubuntu":    false,
		"build local":                             false,
		"provision":                               false,
	}
	for args, expected := range cases {
		if got := becomeSupported(strings.Fields(args)); got != expected {
			t.Fatalf("becomeSupported(%q) = %v, want %v", args, got, expected)
		}
	}
}

func TestBecomeRedirectWarning(t *testing.T) {
	cases := map[string]string{
		"provision local --check":                                                "",
		"provision --playbook /x/site.yml local":                                 "",
		"provision --inventory-path=/x/inv local":                                "--inventory-path can",
		"provision --inventory-path /x/inv local":                                "--inventory-path can",
		"provision local -- -i/x/inv":                                            "Ansible args after -- can",
		"provision local -- --inventory-file /x/inv":                             "Ansible args after -- can",
		"provision local -- -e ansible_host=evil.example ansible_connection=ssh": "Ansible args after -- can",
		"provision local --inventory-path /x/inv -- --tags base":                 "--inventory-path and Ansible args after -- can",
	}
	for args, want := range cases {
		got := becomeRedirectWarning(strings.Fields(args))
		if (want == "") != (got == "") || !strings.HasPrefix(got, want) {
			t.Fatalf("becomeRedirectWarning(%q) = %q, want prefix %q", args, got, want)
		}
	}
}

func TestApprovalPromptShowsBecomeWarning(t *testing.T) {
	out := &syncBuffer{}
	approver := NewConsoleApprover(bufio.NewReader(strings.NewReader("deny job1\n")), out, time.Minute)
	approver.Request(context.Background(), ApprovalRequest{ID: "job1", Become: true, BecomeWarning: "Ansible args after -- can point the play at another host"})
	if !strings.Contains(out.String(), "WARNING:   Ansible args after -- can point the play at another host") || !strings.Contains(out.String(), `"approve-sudo job1"`) {
		t.Fatalf("the prompt must offer approve-sudo with the warning:\n%s", out.String())
	}
}

func TestWriteBecomePasswordFile(t *testing.T) {
	path, cleanup, err := writeBecomePasswordFile(t.TempDir(), "pw")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(data) != "pw" || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("unexpected file %q mode %v", data, info.Mode())
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("cleanup must remove the password file")
	}
}

func TestRejectAskBecomePass(t *testing.T) {
	cases := map[string]bool{
		"provision local -K":                     true,
		"provision --ask-become-pass local":      true,
		"provision local --ask-become-pass=true": true,
		"provision local --check":                false,
		"provision local -- --ask-become-pass":   false,
	}
	for args, rejected := range cases {
		if err := rejectAskBecomePass(strings.Fields(args)); (err != nil) != rejected {
			t.Fatalf("rejectAskBecomePass(%q) = %v, want rejected=%v", args, err, rejected)
		}
	}
}

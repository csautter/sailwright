package gateway

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeSizedFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCleanCacheKeepsVMStateAndConfig(t *testing.T) {
	cfg := Config{StateDir: t.TempDir(), DataDir: t.TempDir()}
	dirs := NewManagedDirs(cfg)
	writeSizedFile(t, filepath.Join(dirs.Cache, "windows11", "iso", "win.iso"), 100)
	writeSizedFile(t, filepath.Join(dirs.Cache, "ubuntu", "qemu-ubuntu.qcow2"), 50)
	writeSizedFile(t, filepath.Join(dirs.PackerCache, "download.iso"), 25)
	kept := []string{
		filepath.Join(dirs.Vagrant, "machines", "state"),
		filepath.Join(dirs.AppData, "libvirt", "images", "vm.qcow2"),
		filepath.Join(dirs.Config, "settings.yaml"),
	}
	for _, path := range kept {
		writeSizedFile(t, path, 10)
	}

	usage, err := MeasureCache(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Total != 175 || len(usage.Dirs) != 2 {
		t.Fatalf("usage = %+v, want 175 bytes in cache and packer cache", usage)
	}

	if err := ClearCache(cfg); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{dirs.Cache, dirs.PackerCache} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("%s must still exist: %v", dir, err)
		}
		if len(entries) != 0 {
			t.Fatalf("%s not emptied: %v", dir, entries)
		}
	}
	for _, path := range kept {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s must be kept: %v", path, err)
		}
	}
}

func TestCleanableDirsSkipsSharedCache(t *testing.T) {
	shared := t.TempDir()
	cfg := Config{StateDir: t.TempDir(), DataDir: t.TempDir(), SharedCacheDir: shared}
	writeSizedFile(t, filepath.Join(shared, "win.iso"), 100)

	for _, dir := range CleanableDirs(cfg) {
		if dir == shared {
			t.Fatal("the shared cache dir must never be cleaned")
		}
	}
	if err := ClearCache(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(shared, "win.iso")); err != nil {
		t.Fatalf("shared cache was touched: %v", err)
	}
}

func TestCleanPromptThreshold(t *testing.T) {
	if got := (Config{CleanPromptMinGB: -1}).CleanPromptThreshold(); got != 0 {
		t.Fatalf("-1 must disable the prompt, got %d", got)
	}
	if got := (Config{CleanPromptMinGB: 2}).CleanPromptThreshold(); got != 2*diskBytesPerGB {
		t.Fatalf("got %d", got)
	}
}

func TestConsoleApproverConfirm(t *testing.T) {
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	out := &syncBuffer{}
	approver := NewConsoleApprover(bufio.NewReader(reader), out, time.Minute)

	answers := make(chan bool, 1)
	ask := func() {
		go func() { answers <- approver.Confirm(context.Background(), "Delete it?") }()
		waitFor(t, func() bool { return strings.HasSuffix(out.String(), "Delete it? [y/N] ") })
	}

	ask()
	_, _ = io.WriteString(writer, "yes\n")
	if !<-answers {
		t.Fatal("yes must confirm")
	}

	out.mu.Lock()
	out.buf.Reset()
	out.mu.Unlock()
	ask()
	// The answer is not taken as a console command.
	_, _ = io.WriteString(writer, "approve job1\n")
	if <-answers {
		t.Fatal("anything but y/yes must decline")
	}
	if strings.Contains(out.String(), "no pending approval") {
		t.Fatal("the answer was handled as a command")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { answers <- approver.Confirm(ctx, "Again?") }()
	waitFor(t, func() bool { return strings.Contains(out.String(), "Again? [y/N] ") })
	cancel()
	if <-answers {
		t.Fatal("a cancelled question must decline")
	}

	// An approval request must not start while a question is open.
	go func() { answers <- approver.Confirm(context.Background(), "Open?") }()
	waitFor(t, func() bool { return strings.Contains(out.String(), "Open? [y/N] ") })
	if got := approver.Request(context.Background(), ApprovalRequest{ID: "job2"}); got.Decision != DecisionBusy {
		t.Fatalf("request during a question: got %v, want busy", got.Decision)
	}
	_, _ = io.WriteString(writer, "n\n")
	<-answers

	closed := NewConsoleApprover(bufio.NewReader(strings.NewReader("")), out, time.Minute)
	waitFor(t, func() bool { return !closed.Enabled() })
	if closed.Confirm(context.Background(), "Closed?") {
		t.Fatal("a closed console must decline")
	}
}

func TestLineConfirmer(t *testing.T) {
	out := &syncBuffer{}
	if !(LineConfirmer{In: bufio.NewReader(strings.NewReader("Y\n")), Out: out}).Confirm(context.Background(), "Delete?") {
		t.Fatal("Y must confirm")
	}
	if (LineConfirmer{In: bufio.NewReader(strings.NewReader("\n")), Out: out}).Confirm(context.Background(), "Delete?") {
		t.Fatal("Enter must decline")
	}
}

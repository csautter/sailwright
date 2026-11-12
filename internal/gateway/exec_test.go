package gateway

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

const (
	treeHelperEnv  = "GATEWAY_TREE_HELPER"
	treeBeatEnvKey = "GATEWAY_TREE_BEAT"
)

// TestProcessTreeHelper is not a test: Runner re-executes the test binary to get a
// parent that starts a long-lived grandchild.
func TestProcessTreeHelper(t *testing.T) {
	switch os.Getenv(treeHelperEnv) {
	case "parent":
		child := exec.Command(os.Args[0], "-test.run=^TestProcessTreeHelper$")
		child.Env = append(os.Environ(), treeHelperEnv+"=child")
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		// Outlive the startup budget, so the parent cannot exit on its own (and
		// end Run without a cancel) while the test still waits for the first beat.
		time.Sleep(2 * treeHelperStartup)
	case "child":
		beat := os.Getenv(treeBeatEnvKey)
		// Stop when the test is gone (its temp dir is removed) or after 10 s, so a failed
		// assertion cannot leave a writer behind.
		for i := 0; i < 200; i++ {
			if _, err := os.Stat(filepath.Dir(beat)); err != nil {
				return
			}
			_ = os.WriteFile(beat, []byte(strconv.Itoa(i)), 0o600)
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// treeHelperStartup bounds how long the helper tree may take to write its first beat.
const treeHelperStartup = 60 * time.Second

func readBeat(path string) string {
	data, _ := os.ReadFile(path)
	return string(data)
}

// TestRunnerCancelTerminatesDescendants covers processTree on every OS (process group
// on Unix, job object on Windows): cancelling must stop grandchildren too.
func TestRunnerCancelTerminatesDescendants(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := PinBinary(exe, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	beat := filepath.Join(t.TempDir(), "beat")
	runner := &Runner{Binary: pinned, Env: append(os.Environ(), treeHelperEnv+"=parent", treeBeatEnvKey+"="+beat)}

	ctx, cancel := context.WithCancel(context.Background())
	// Kill the helper tree when an assertion fails early, so it does not keep
	// running or holding the pinned binary open during temp dir cleanup.
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(ctx, []string{"-test.run=^TestProcessTreeHelper$"}, t.TempDir(), nopWriter{})
		done <- err
	}()

	// Starting the pinned test binary twice (parent, then child) is the slow part
	// under a full parallel `go test ./...` run, and on Windows each fresh copy is
	// scanned before it runs. Only the startup gets the long wait; the assertions
	// after cancel stay strict.
	startup := time.Now().Add(treeHelperStartup)
	for readBeat(beat) == "" {
		if time.Now().After(startup) {
			t.Fatal("process tree helper did not start writing beats")
		}
		time.Sleep(10 * time.Millisecond)
	}
	before := readBeat(beat)
	waitFor(t, func() bool { return readBeat(beat) != before })

	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	time.Sleep(300 * time.Millisecond)
	stopped := readBeat(beat)
	time.Sleep(400 * time.Millisecond)
	if after := readBeat(beat); after != stopped {
		t.Fatalf("grandchild kept running after cancel (%s -> %s)", stopped, after)
	}
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

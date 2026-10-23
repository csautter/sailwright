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
		time.Sleep(time.Minute)
	case "child":
		beat := os.Getenv(treeBeatEnvKey)
		for i := 0; i < 1200; i++ {
			_ = os.WriteFile(beat, []byte(strconv.Itoa(i)), 0o600)
			time.Sleep(50 * time.Millisecond)
		}
	}
}

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
	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(ctx, []string{"-test.run=^TestProcessTreeHelper$"}, t.TempDir(), nopWriter{})
		done <- err
	}()

	waitFor(t, func() bool { return readBeat(beat) != "" })
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

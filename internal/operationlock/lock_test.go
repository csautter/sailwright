package operationlock

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestProcessContention(t *testing.T) {
	if os.Getenv("SAILWRIGHT_LOCK_CHILD") == "1" {
		release, err := Acquire(os.Getenv("SAILWRIGHT_LOCK_ROOT"), os.Getenv("SAILWRIGHT_LOCK_TARGET"), "child")
		if err != nil {
			os.Exit(7)
		}
		release()
		return
	}
	root := t.TempDir()
	child := func(target string, busy bool) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestProcessContention$")
		cmd.Env = append(os.Environ(), "SAILWRIGHT_LOCK_CHILD=1", "SAILWRIGHT_LOCK_ROOT="+root, "SAILWRIGHT_LOCK_TARGET="+target)
		err := cmd.Run()
		if busy {
			if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != 7 {
				t.Fatalf("expected contention, got %v", err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
	release, err := Acquire(root, "ubuntu/server/amd64", "test")
	if err != nil {
		t.Fatal(err)
	}
	child("ubuntu/server/amd64", true)
	child("ubuntu/desktop/amd64", false)
	child("", true)
	release()
	child("ubuntu/server/amd64", false)
	releaseAll, err := Acquire(root, "", "test")
	if err != nil {
		t.Fatal(err)
	}
	child("ubuntu/server/amd64", true)
	releaseAll()
	child("", false)
}

func TestAcquireWithinWaitsForRelease(t *testing.T) {
	root := t.TempDir()
	release, err := Acquire(root, "ubuntu/server/amd64", "test")
	if err != nil {
		t.Fatal(err)
	}
	// flock and LockFileEx locks are per open file, so a second Acquire in the same
	// process contends like another process would.
	if _, err := AcquireWithin(root, "ubuntu/server/amd64", "test", 0); !errors.Is(err, ErrBusy) {
		t.Fatalf("expected busy, got %v", err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		release()
	}()
	second, err := AcquireWithin(root, "ubuntu/server/amd64", "test", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	second()
}

func TestBusyErrorNamesHolder(t *testing.T) {
	root := t.TempDir()
	release, err := Acquire(root, "ubuntu/server/amd64", "create")
	if err != nil {
		t.Fatal(err)
	}
	pid := "pid " + strconv.Itoa(os.Getpid())
	for _, target := range []string{"ubuntu/server/amd64", ""} {
		_, err := AcquireWithin(root, target, "start", 0)
		if !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), pid) || !strings.Contains(err.Error(), "create ubuntu/server/amd64") {
			t.Fatalf("target %q: %v", target, err)
		}
	}
	release()

	releaseAll, err := Acquire(root, "", "destroy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireWithin(root, "windows11//amd64", "start", 0); err == nil || !strings.Contains(err.Error(), "destroy") {
		t.Fatalf("all-targets holder not named: %v", err)
	}
	releaseAll()

	// A released lock leaves no stale holder behind.
	if holder, ok := readHolder(filepath.Join(root, "locks", targetLockName("ubuntu/server/amd64"))); ok {
		t.Fatalf("stale holder %v", holder)
	}
}

func TestImageReadLocksAllowClonesAndExcludeBuilds(t *testing.T) {
	dir := t.TempDir()
	first, err := AcquireShared(dir, "ubuntu/server/amd64", "clone a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := AcquireShared(dir, "ubuntu/server/amd64", "clone b")
	if err != nil {
		t.Fatal(err)
	}
	if release, err := Acquire(dir, "ubuntu/server/amd64", "build"); err == nil {
		release()
		t.Fatal("build raced with image clones")
	}
	if release, err := Acquire(dir, "", "build all"); err == nil {
		release()
		t.Fatal("bulk build raced with clones")
	}
	first()
	second()
	release, err := Acquire(dir, "ubuntu/server/amd64", "build")
	if err != nil {
		t.Fatal(err)
	}
	release()
}

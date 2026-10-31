package operationlock

import (
	"os"
	"os/exec"
	"testing"
)

func TestProcessContention(t *testing.T) {
	if os.Getenv("SAILWRIGHT_LOCK_CHILD") == "1" {
		release, err := Acquire(os.Getenv("SAILWRIGHT_LOCK_ROOT"), os.Getenv("SAILWRIGHT_LOCK_TARGET"))
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
	release, err := Acquire(root, "ubuntu/server/amd64")
	if err != nil {
		t.Fatal(err)
	}
	child("ubuntu/server/amd64", true)
	child("ubuntu/desktop/amd64", false)
	child("", true)
	release()
	child("ubuntu/server/amd64", false)
	releaseAll, err := Acquire(root, "")
	if err != nil {
		t.Fatal(err)
	}
	child("ubuntu/server/amd64", true)
	releaseAll()
	child("", false)
}

package main

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"testing"
)

func TestReadStateFileRetriesTransientErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"access denied", os.ErrPermission},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases, struct {
			name string
			err  error
		}{"sharing violation", syscall.Errno(32)})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			data, err := readStateFile("vm-state", func(path string) ([]byte, error) {
				calls++
				if calls == 1 {
					return nil, &os.PathError{Op: "open", Path: path, Err: tc.err}
				}
				return []byte("running"), nil
			})
			if err != nil || string(data) != "running" || calls != 2 {
				t.Fatalf("read state: data=%q err=%v calls=%d", data, err, calls)
			}
		})
	}
}

func TestReadStateFileReturnsMissingAndUnexpectedErrors(t *testing.T) {
	for _, want := range []error{os.ErrNotExist, errors.New("unexpected read failure")} {
		calls := 0
		_, err := readStateFile("vm-state", func(path string) ([]byte, error) {
			calls++
			return nil, &os.PathError{Op: "open", Path: path, Err: want}
		})
		if !errors.Is(err, want) || calls != 1 {
			t.Fatalf("read state: err=%v calls=%d; want %v without retry", err, calls, want)
		}
	}
}

// Background listings must never mistake a state update for a destroyed VM.
func TestStateUpdatesNeverReportMissingVM(t *testing.T) {
	t.Setenv("FAKE_CLI_STATE", t.TempDir())
	const id = "ubuntu-server-amd64"
	writeState(id, "stopped")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
			writeState(id, "running")
			writeState(id, "stopped")
		}
	}()
	defer func() { <-done }()
	for {
		select {
		case <-done:
			return
		default:
			if state := readState(id); state != "running" && state != "stopped" {
				t.Fatalf("state update reported a missing or partial VM: %q", state)
			}
		}
	}
}

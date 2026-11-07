package main

import "testing"

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

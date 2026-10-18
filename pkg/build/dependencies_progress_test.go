package build

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

func TestPlainProgressLogsAtInterval(t *testing.T) {
	var lines []string
	progress := &plainProgress{name: "ubuntu.iso", total: 1000, lastLog: time.Unix(0, 0), interval: 10 * time.Second,
		logf: func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }}

	progress.add(100, time.Unix(5, 0))
	if len(lines) != 0 {
		t.Fatalf("logged before the interval elapsed: %v", lines)
	}
	progress.add(400, time.Unix(11, 0))
	progress.add(100, time.Unix(12, 0))
	if len(lines) != 1 || lines[0] != "Downloading ubuntu.iso: 50% (500 B / 1000 B)" {
		t.Fatalf("unexpected progress lines %v", lines)
	}
}

func TestTrackProgressUsesPlainProgressWithoutTerminal(t *testing.T) {
	previous := stdoutIsTerminal
	t.Cleanup(func() { stdoutIsTerminal = previous })
	stdoutIsTerminal = func() bool { return false }

	listener := &ProgressBarListener{}
	if reader := listener.TrackProgress("x", 0, 10, io.NopCloser(strings.NewReader(""))); reader == nil {
		t.Fatal("expected the original reader without a progress container")
	}
}

func TestFormatByteSize(t *testing.T) {
	cases := map[int64]string{-1: "unknown size", 512: "512 B", 2048: "2.00 KiB", 3 << 30: "3.00 GiB"}
	for size, want := range cases {
		if got := formatByteSize(size); got != want {
			t.Fatalf("formatByteSize(%d) = %q, want %q", size, got, want)
		}
	}
}

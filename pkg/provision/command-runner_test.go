package provision

import (
	"log"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestSanitizeCommandArgsForLogsRedactsURLCredentials(t *testing.T) {
	args := []string{
		"clone",
		"https://token@example.test/org/private.git",
		"ssh://git:secret@example.test/org/private.git",
		"https://example.test/org/public.git",
	}

	got := sanitizeCommandArgsForLogs(args)
	want := []string{
		"clone",
		"https://***REDACTED***@example.test/org/private.git",
		"ssh://***REDACTED***@example.test/org/private.git",
		"https://example.test/org/public.git",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected sanitized args %v, got %v", want, got)
	}
}

func TestLogStreamLines_TruncatesOverlongLinesAndKeepsReading(t *testing.T) {
	var logOutput strings.Builder
	log.SetOutput(&logOutput)
	defer log.SetOutput(os.Stderr)

	long := strings.Repeat("x", scannerMaxBufferSize*2)
	logStreamLines(strings.NewReader("before\n"+long+"\nafter\n"), "[p]", "stdout")

	out := logOutput.String()
	for _, want := range []string{"[p] stdout: before", "[line truncated]", "[p] stdout: after"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected log output to contain %q", want)
		}
	}
	if strings.Contains(out, "scanner error") || len(out) > scannerMaxBufferSize+1024 {
		t.Fatalf("unexpected log output size %d", len(out))
	}
}

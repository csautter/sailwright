package provision

import (
	"reflect"
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

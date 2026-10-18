package build

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Helpers for tests that check the Packer templates, wrapper scripts and guest
// configuration files this package drives. Paths are relative to the
// repository root.

// readRepoFile returns the file content with line endings normalized to "\n",
// so assertions behave the same on a Windows checkout with CRLF files.
func readRepoFile(t *testing.T, relPath string) string {
	t.Helper()

	content, err := os.ReadFile(repoPath(t, relPath))
	if err != nil {
		t.Fatalf("failed to read %q: %v", relPath, err)
	}
	return normalizeLineEndings(string(content))
}

// assertContainsAll reports every want that is missing from content; what
// names the checked text in failure messages.
func assertContainsAll(t *testing.T, what, content string, wants ...string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(content, want) {
			t.Errorf("expected %s to contain %q", what, want)
		}
	}
}

func containsCollapsedAssignment(content, name, value string) bool {
	for _, line := range strings.Split(content, "\n") {
		if strings.Join(strings.Fields(line), " ") == name+" = "+value {
			return true
		}
	}
	return false
}

func textBetween(content, start, end string) (string, bool) {
	startIndex := strings.Index(content, start)
	if startIndex == -1 {
		return "", false
	}
	startIndex += len(start)
	endIndex := strings.Index(content[startIndex:], end)
	if endIndex == -1 {
		return "", false
	}
	return content[startIndex : startIndex+endIndex], true
}

func normalizeLineEndings(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	return strings.ReplaceAll(content, "\r", "\n")
}

func TestNormalizeLineEndingsConvertsCRLFAndCR(t *testing.T) {
	t.Parallel()

	if got := normalizeLineEndings("a\r\nb\rc\n"); got != "a\nb\nc\n" {
		t.Fatalf("expected CRLF and CR to become LF, got %q", got)
	}
}

func repoPath(t *testing.T, relPath string) string {
	t.Helper()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to determine test file path")
	}

	return filepath.Join(filepath.Dir(currentFile), "..", "..", relPath)
}

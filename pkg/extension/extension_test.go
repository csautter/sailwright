package extension

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDiscoverFindsExecutableExtensionsOnPath(t *testing.T) {
	dir := t.TempDir()
	createTestExecutable(t, filepath.Join(dir, "sailwright-analyzer"))
	createTestExecutable(t, filepath.Join(dir, "sailwright-generate"))
	createTestFile(t, filepath.Join(dir, "sailwright-not-executable"), 0o600)
	createTestExecutable(t, filepath.Join(dir, "other-tool"))

	extensions, err := Discover(DiscoverOptions{PathEnv: dir})
	if err != nil {
		t.Fatalf("expected discovery to succeed, got %v", err)
	}

	if len(extensions) != 2 {
		t.Fatalf("expected 2 extensions, got %d: %v", len(extensions), extensions)
	}
	if extensions[0].Name != "analyzer" {
		t.Fatalf("expected analyzer extension first, got %q", extensions[0].Name)
	}
	if extensions[1].Name != "generate" {
		t.Fatalf("expected generate extension second, got %q", extensions[1].Name)
	}
}

func TestDiscoverKeepsFirstPathMatch(t *testing.T) {
	firstDir := t.TempDir()
	secondDir := t.TempDir()
	firstPath := createTestExecutable(t, filepath.Join(firstDir, "sailwright-analyzer"))
	createTestExecutable(t, filepath.Join(secondDir, "sailwright-analyzer"))

	extensions, err := Discover(DiscoverOptions{PathEnv: strings.Join([]string{firstDir, secondDir}, string(os.PathListSeparator))})
	if err != nil {
		t.Fatalf("expected discovery to succeed, got %v", err)
	}
	if len(extensions) != 1 {
		t.Fatalf("expected 1 extension, got %d: %v", len(extensions), extensions)
	}
	if extensions[0].Path != firstPath {
		t.Fatalf("expected first PATH match %q, got %q", firstPath, extensions[0].Path)
	}
}

func TestDiscoverSkipsRelativePathEntries(t *testing.T) {
	workDir := t.TempDir()
	createTestExecutable(t, filepath.Join(workDir, "sailwright-injected"))
	t.Chdir(workDir)

	extensions, err := Discover(DiscoverOptions{PathEnv: strings.Join([]string{".", "rel"}, string(os.PathListSeparator))})
	if err != nil {
		t.Fatalf("expected discovery to succeed, got %v", err)
	}
	if len(extensions) != 0 {
		t.Fatalf("expected relative PATH entries to be ignored, got %v", extensions)
	}
}

func TestDiscoverPrefersPathextOrderWithinDirectory(t *testing.T) {
	dir := t.TempDir()
	createTestFile(t, filepath.Join(dir, "sailwright-analyzer.cmd"), 0o755)
	exePath := filepath.Join(dir, "sailwright-analyzer.exe")
	createTestFile(t, exePath, 0o755)

	extensions, err := Discover(DiscoverOptions{PathEnv: dir})
	if err != nil {
		t.Fatalf("expected discovery to succeed, got %v", err)
	}
	if len(extensions) != 1 {
		t.Fatalf("expected 1 extension, got %d: %v", len(extensions), extensions)
	}
	if extensions[0].Path != exePath {
		t.Fatalf("expected .exe to win over .cmd, got %q", extensions[0].Path)
	}
}

func TestRunPassesArgumentsEnvironmentAndExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell script as extension")
	}

	dir := t.TempDir()
	script := "#!/bin/sh\necho \"$" + ProtocolEnvVar + " $" + NameEnvVar + " $*\"\nexit 3\n"
	if err := os.WriteFile(filepath.Join(dir, "sailwright-demo"), []byte(script), 0o755); err != nil {
		t.Fatalf("failed to create extension script: %v", err)
	}

	var stdout strings.Builder
	err := Run(context.Background(), RunOptions{
		Name:    "demo",
		Args:    []string{"scan", "--out", "snapshot.json"},
		PathEnv: dir + string(os.PathListSeparator) + os.Getenv("PATH"),
		Stdin:   strings.NewReader(""),
		Stdout:  &stdout,
		Stderr:  io.Discard,
	})

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected wrapped exec.ExitError, got %v", err)
	}
	if exitErr.ExitCode() != 3 {
		t.Fatalf("expected exit code 3, got %d", exitErr.ExitCode())
	}
	if got, want := strings.TrimSpace(stdout.String()), ProtocolVersion+" demo scan --out snapshot.json"; got != want {
		t.Fatalf("expected output %q, got %q", want, got)
	}
}

func TestResolveAcceptsNameWithExecutablePrefix(t *testing.T) {
	dir := t.TempDir()
	executablePath := createTestExecutable(t, filepath.Join(dir, "sailwright-analyzer"))

	resolved, err := Resolve("sailwright-analyzer", DiscoverOptions{PathEnv: dir})
	if err != nil {
		t.Fatalf("expected prefixed extension name to resolve, got %v", err)
	}
	if resolved.Name != "analyzer" {
		t.Fatalf("expected normalized extension name, got %q", resolved.Name)
	}
	if resolved.Path != executablePath {
		t.Fatalf("expected path %q, got %q", executablePath, resolved.Path)
	}
}

func TestResolveRejectsPathLikeNames(t *testing.T) {
	_, err := Resolve("../analyzer", DiscoverOptions{PathEnv: t.TempDir()})
	if err == nil {
		t.Fatal("expected path-like extension name to fail")
	}
	if !strings.Contains(err.Error(), "path separators") {
		t.Fatalf("expected path separator error, got %v", err)
	}
}

func TestNameFromExecutableTrimsKnownExecutableSuffixes(t *testing.T) {
	name, ok := NameFromExecutable("sailwright-analyzer.exe")
	if !ok {
		t.Fatal("expected executable name to be recognized")
	}
	if name != "analyzer" {
		t.Fatalf("expected analyzer name, got %q", name)
	}
}

func TestNameFromExecutableKeepsPowerShellSuffix(t *testing.T) {
	name, ok := NameFromExecutable("sailwright-analyzer.ps1")
	if !ok {
		t.Fatal("expected prefixed file name to be recognized")
	}
	if name != "analyzer.ps1" {
		t.Fatalf("expected .ps1 not to be treated as executable suffix, got %q", name)
	}
}

func TestExtensionEnvironmentSetsProtocolValues(t *testing.T) {
	env := extensionEnvironment(
		[]string{"PATH=/bin", ProtocolEnvVar + "=old"},
		"analyzer",
		"/custom/bin",
		[]string{ProtocolEnvVar + "=older", NameEnvVar + "=old"},
	)

	assertEnvValue(t, env, "PATH", "/custom/bin")
	assertEnvValue(t, env, ProtocolEnvVar, ProtocolVersion)
	assertEnvValue(t, env, NameEnvVar, "analyzer")
	assertSingleEnvValue(t, env, ProtocolEnvVar)
}

func createTestExecutable(t *testing.T, path string) string {
	t.Helper()
	mode := os.FileMode(0o755)
	if runtime.GOOS == "windows" {
		path = path + ".cmd"
		mode = 0o600
	}
	createTestFile(t, path, mode)
	return path
}

func createTestFile(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatalf("failed to create test file %q: %v", path, err)
	}
}

func assertEnvValue(t *testing.T, env []string, key string, want string) {
	t.Helper()
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			if got := strings.TrimPrefix(entry, prefix); got != want {
				t.Fatalf("expected %s=%q, got %q", key, want, got)
			}
			return
		}
	}
	t.Fatalf("expected env to include %s", key)
}

func assertSingleEnvValue(t *testing.T, env []string, key string) {
	t.Helper()
	prefix := key + "="
	count := 0
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one %s env entry, got %d in %v", key, count, env)
	}
}

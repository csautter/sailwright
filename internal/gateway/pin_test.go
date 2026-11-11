package gateway

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// windowsLikeRename refuses to replace an existing target, like Windows does for
// a running executable, but lets the target itself be renamed.
func windowsLikeRename(t *testing.T, target string, lockedMoves *int) func(string, string) error {
	t.Helper()
	return func(from, to string) error {
		if to == target {
			if _, err := os.Stat(target); err == nil {
				return &os.LinkError{Op: "rename", Old: from, New: to, Err: errors.New("Access is denied.")}
			}
		}
		if from == target && *lockedMoves > 0 {
			*lockedMoves--
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: errors.New("The process cannot access the file because it is being used by another process.")}
		}
		return os.Rename(from, to)
	}
}

func stubPinWindows(t *testing.T, rename func(string, string) error, timeout time.Duration) {
	t.Helper()
	oldGOOS, oldRename, oldDelay, oldTimeout := pinGOOS, pinRename, pinRetryDelay, pinReplaceTimeout
	pinGOOS, pinRename, pinRetryDelay, pinReplaceTimeout = "windows", rename, time.Millisecond, timeout
	t.Cleanup(func() {
		pinGOOS, pinRename, pinRetryDelay, pinReplaceTimeout = oldGOOS, oldRename, oldDelay, oldTimeout
	})
}

func TestInstallPinnedBinaryReplacesBusyWindowsBinary(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sailwright.exe")
	tmp := filepath.Join(dir, ".pin-1")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	busy := 2 // a scanner holds the old copy for two attempts
	stubPinWindows(t, windowsLikeRename(t, target, &busy), time.Second)
	if err := installPinnedBinary(tmp, target); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(target); string(data) != "new" {
		t.Fatalf("target holds %q", data)
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if entry.Name() != "sailwright.exe" {
			t.Errorf("left behind: %s", entry.Name())
		}
	}
}

func TestInstallPinnedBinaryGivesUpOnPermanentlyLockedBinary(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sailwright.exe")
	tmp := filepath.Join(dir, ".pin-1")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	busy := 1 << 30
	stubPinWindows(t, windowsLikeRename(t, target, &busy), 20*time.Millisecond)
	err := installPinnedBinary(tmp, target)
	if err == nil || !strings.Contains(err.Error(), "being used by another process") {
		t.Fatalf("expected the lock error, got %v", err)
	}
	if data, _ := os.ReadFile(target); string(data) != "old" {
		t.Fatalf("target changed to %q", data)
	}
}

func TestPinBinaryRemovesReplacedCopies(t *testing.T) {
	stateDir := t.TempDir()
	source := filepath.Join(t.TempDir(), "sailwright")
	if err := os.WriteFile(source, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(stateDir, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(binDir, ".replaced-1-sailwright.exe")
	if err := os.WriteFile(stale, []byte("old"), 0o500); err != nil {
		t.Fatal(err)
	}
	if _, err := PinBinary(source, stateDir, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replaced copy not removed: %v", err)
	}
}

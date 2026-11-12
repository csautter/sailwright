package gateway_test

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csautter/dev-alchemy/internal/gateway"
	"github.com/csautter/dev-alchemy/internal/gateway/gatewaytest"
)

// A gateway restart re-pins into the same state dir while a job of the previous
// gateway may still run the old pinned copy. Windows refuses to replace a running
// executable, so this runs the real replacement path on Windows CI.
func TestPinBinaryReplacesRunningPinnedBinary(t *testing.T) {
	fake := gatewaytest.BuildFakeCLI(t)
	state := t.TempDir()
	first, err := gateway.PinBinary(fake, state, "")
	if err != nil {
		t.Fatal(err)
	}

	// #nosec G204 -- test runs the fake CLI it just pinned.
	cmd := exec.Command(first.Path, "build", "slowos")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Registered after TempDir, so it runs first and the state dir can be removed.
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "sleeping" {
		t.Fatalf("pinned binary did not start: %q, %v", line, err)
	}

	second, err := gateway.PinBinary(fake, state, "")
	if err != nil {
		t.Fatalf("re-pin while the pinned binary runs: %v", err)
	}
	if second.Path != first.Path || second.SHA256 != first.SHA256 {
		t.Fatalf("re-pin = %+v, want path and hash of %+v", second, first)
	}
	if err := second.Verify(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(second.Path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pin-") {
			t.Errorf("temporary pin left behind: %s", entry.Name())
		}
	}
}

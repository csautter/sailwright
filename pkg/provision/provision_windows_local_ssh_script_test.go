package provision

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Start-Process -Credential inherits the caller's working directory. The
// temporary local account cannot enter a checkout inside another user's
// profile, so PowerShell fails with "The directory name is invalid". Every
// credentialed Start-Process in the bootstrap script must set a directory
// that any local account can enter.
func TestWindowsSSHBootstrapCredentialedStartProcessSetsWorkingDirectory(t *testing.T) {
	scriptPath := filepath.Join("..", "..", filepath.FromSlash(localWindowsSSHBootstrapScriptPath))
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read bootstrap script: %v", err)
	}

	startProcess := regexp.MustCompile(`(?i)Start-Process\b[^\r\n]*-Credential\b[^\r\n]*`)
	matches := startProcess.FindAllString(string(data), -1)
	if len(matches) == 0 {
		t.Fatal("expected a Start-Process -Credential call in the bootstrap script")
	}
	for _, line := range matches {
		if !strings.Contains(strings.ToLower(line), "-workingdirectory") {
			t.Errorf("Start-Process -Credential without -WorkingDirectory: %s", strings.TrimSpace(line))
		}
	}
}

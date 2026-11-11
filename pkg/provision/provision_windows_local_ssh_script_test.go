package provision

import (
	"os"
	"os/exec"
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

// An OpenSSH capability install that only completes after a restart leaves no sshd
// service. The bootstrap must name the pending restart rather than a missing service.
func TestWindowsSSHBootstrapNamesPendingRestartWhenSshdIsMissing(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("pwsh not available")
	}
	scriptPath := filepath.Join("..", "..", filepath.FromSlash(localWindowsSSHBootstrapScriptPath))
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read bootstrap script: %v", err)
	}
	function := regexp.MustCompile(`(?ms)^function Get-SshdMissingMessage\b.*?^}`).Find(data)
	if function == nil {
		t.Fatal("bootstrap script defines no Get-SshdMissingMessage")
	}
	script := string(function) + `
Get-SshdMissingMessage @{ State = 'InstallPending'; Pending = $true } $false
Get-SshdMissingMessage @{ State = 'Installed'; Pending = $false } $true
Get-SshdMissingMessage @{ State = 'Installed'; Pending = $false } $false
`
	out, err := exec.Command(pwsh, "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		t.Fatalf("pwsh: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(out), "\r\n", "\n")), "\n")
	if len(lines) != 3 {
		t.Fatalf("unexpected output:\n%s", out)
	}
	for i, want := range []string{`state "InstallPending" and Windows has a restart pending`, `restart pending`, `was not found after enabling OpenSSH Server`} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("case %d: %q does not contain %q", i, lines[i], want)
		}
	}
}

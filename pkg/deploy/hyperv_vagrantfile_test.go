package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// The Hyper-V Ubuntu image has no passwordless sudo, and Vagrant pipes privileged
// scripts (such as "Setting hostname" for named instances) into sudo_command on
// stdin. The command must authenticate without reading stdin, pass the script
// through to the shell and keep its exit status.
func TestHypervUbuntuVagrantfileSudoCommandUsesAskpass(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the sudo command runs in the Linux guest")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "deployments", "vagrant", "linux-ubuntu-hyperv", "Vagrantfile"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^\s*config\.ssh\.sudo_command = %q\{(.*)\}$`).FindSubmatch(data)
	if match == nil {
		t.Fatal("Vagrantfile sets no config.ssh.sudo_command; Vagrant's sudo steps fail without passwordless sudo")
	}
	command := strings.Replace(string(match[1]), "%c", "sh", 1)

	dir := t.TempDir()
	fakeSudo := `#!/bin/sh
[ "$1 $2 $3" = "-A -E -H" ] || { echo "unexpected flags: $*" >&2; exit 90; }
shift 3
[ "$("$SUDO_ASKPASS")" = 'P@ssw0rd!' ] || { echo "askpass returned the wrong password" >&2; exit 91; }
exec "$@"
`
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte(fakeSudo), 0o700); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	cmd := exec.Command(bash, "-c", command)
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "TMPDIR="+tmp)
	cmd.Stdin = strings.NewReader("echo script-ran\nexit 3\n")
	out, err := cmd.CombinedOutput()
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 3 {
		t.Fatalf("exit status of the piped script not kept: %v; output: %s", err, out)
	}
	if !strings.Contains(string(out), "script-ran") {
		t.Fatalf("piped script did not run: %s", out)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Fatalf("askpass helper left behind: %v", left)
	}
}

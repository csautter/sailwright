package deploy

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// fakeSudo stands in for the guest's sudo. It checks the flags Vagrant needs,
// that the askpass helper is private and returns the SSH password, and then
// runs the shell that reads the piped script. FAKE_SUDO_MODE simulates an
// authentication failure or the outer shell being terminated mid-command.
const fakeSudo = `#!/bin/sh
[ "$1 $2 $3" = "-A -E -H" ] || { echo "unexpected flags: $*" >&2; exit 90; }
shift 3
case "$(ls -l "$SUDO_ASKPASS")" in
  -rwx------*) ;;
  *) echo "askpass helper is not private: $(ls -l "$SUDO_ASKPASS")" >&2; exit 92 ;;
esac
[ "$("$SUDO_ASKPASS")" = "$WANT_PASSWORD" ] || { echo "askpass returned the wrong password" >&2; exit 91; }
case "$FAKE_SUDO_MODE" in
  auth-failure) exit 1 ;;
  terminate-parent) kill -TERM "$PPID"; exit 0 ;;
esac
exec "$@"
`

const failingMktemp = `#!/bin/sh
exit 4
`

// hypervUbuntuVagrantfileSSH returns the sudo_command and ssh.password set in the
// Hyper-V Ubuntu Vagrantfile.
func hypervUbuntuVagrantfileSSH(t *testing.T) (sudoCommand, password string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "deployments", "vagrant", "linux-ubuntu-hyperv", "Vagrantfile"))
	if err != nil {
		t.Fatal(err)
	}
	command := regexp.MustCompile(`(?m)^\s*config\.ssh\.sudo_command = %q\{(.*)\}$`).FindSubmatch(data)
	if command == nil {
		t.Fatal("Vagrantfile sets no config.ssh.sudo_command; Vagrant's sudo steps fail without passwordless sudo")
	}
	pass := regexp.MustCompile(`(?m)^\s*config\.ssh\.password = "([^"\\#]*)"$`).FindSubmatch(data)
	if pass == nil {
		t.Fatal("Vagrantfile sets no plain config.ssh.password")
	}
	return string(command[1]), string(pass[1])
}

// The Hyper-V Ubuntu image has no passwordless sudo, and Vagrant 2.4's SSH
// communicator runs sudo_command (with %c replaced by the login shell) and pipes
// privileged scripts such as "Setting hostname" for named instances into it on
// stdin. The command must authenticate without reading stdin, pass the script
// through, keep its exit status and never leave the askpass helper behind.
func TestHypervUbuntuVagrantfileSudoCommandUsesAskpass(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the sudo command runs in the Linux guest")
	}
	sudoCommand, password := hypervUbuntuVagrantfileSSH(t)
	if strings.Count(sudoCommand, "%c") != 1 {
		t.Fatalf("sudo_command must contain exactly one %%c placeholder: %s", sudoCommand)
	}
	command := strings.Replace(sudoCommand, "%c", "sh", 1)

	cases := []struct {
		name          string
		mode          string
		failingMktemp bool
		wantExit      int
		wantScriptRan bool
	}{
		{name: "script exit status is kept", wantExit: 3, wantScriptRan: true},
		{name: "sudo authentication failure", mode: "auth-failure", wantExit: 1},
		{name: "terminated while sudo runs", mode: "terminate-parent", wantExit: 143},
		{name: "mktemp failure", failingMktemp: true, wantExit: 4},
	}
	// The guest runs the command through the login shell (bash); also check a
	// plain POSIX sh so the traps do not rely on bash-only behaviour.
	for _, shell := range []string{"bash", "sh"} {
		shellPath, err := exec.LookPath(shell)
		if err != nil {
			t.Logf("%s not available, skipping", shell)
			continue
		}
		for _, tc := range cases {
			t.Run(shell+"/"+tc.name, func(t *testing.T) {
				bin := t.TempDir()
				if err := os.WriteFile(filepath.Join(bin, "sudo"), []byte(fakeSudo), 0o700); err != nil {
					t.Fatal(err)
				}
				if tc.failingMktemp {
					if err := os.WriteFile(filepath.Join(bin, "mktemp"), []byte(failingMktemp), 0o700); err != nil {
						t.Fatal(err)
					}
				}
				tmp := t.TempDir()
				cmd := exec.Command(shellPath, "-c", command)
				cmd.Env = append(os.Environ(),
					"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
					"TMPDIR="+tmp,
					"WANT_PASSWORD="+password,
					"FAKE_SUDO_MODE="+tc.mode,
				)
				cmd.Stdin = strings.NewReader("echo script-ran\nexit 3\n")
				out, err := cmd.CombinedOutput()
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || exitErr.ExitCode() != tc.wantExit {
					t.Fatalf("want exit status %d, got %v; output: %s", tc.wantExit, err, out)
				}
				if ran := strings.Contains(string(out), "script-ran"); ran != tc.wantScriptRan {
					t.Fatalf("piped script ran = %v, want %v; output: %s", ran, tc.wantScriptRan, out)
				}
				if strings.Contains(string(out), password) {
					t.Fatalf("password leaked into the command output: %s", out)
				}
				if left, _ := os.ReadDir(tmp); len(left) != 0 {
					t.Fatalf("askpass helper left behind: %v", left)
				}
			})
		}
	}
}

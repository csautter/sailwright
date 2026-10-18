//go:build linux

package build

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const ttyScenarioEnv = "SAILWRIGHT_TEST_TTY_SCENARIO"

// TestMain lets the test binary act as the helper processes of the TTY tests.
func TestMain(m *testing.M) {
	switch os.Getenv(ttyScenarioEnv) {
	case "session":
		os.Exit(runTTYSessionHelper())
	case "terminal-handoff":
		os.Exit(runTerminalHandoff())
	}
	os.Exit(m.Run())
}

// runTerminalHandoff does what a build does around Packer: hand the terminal to a
// child, wait for it and take the terminal back.
func runTerminalHandoff() int {
	cmd := exec.Command("true")
	restoreCommandTerminal := attachCommandToInteractiveTerminal(cmd)
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	restoreCommandTerminal()
	restoreInteractiveTerminal()
	return 0
}

// runTTYSessionHelper owns the pseudo terminal (session leader) and runs the
// hand-off either itself (foreground) or in a background process group.
func runTTYSessionHelper() int {
	self, _ := os.Executable()
	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), ttyScenarioEnv+"=terminal-handoff")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if os.Getenv("SAILWRIGHT_TEST_TTY_BACKGROUND") == "1" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	return 0
}

func openPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pseudo terminal support: %v", err)
	}
	unlock := 0
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, master.Fd(), unix.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		t.Skipf("unlock pty: %v", errno)
	}
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Skipf("pty number: %v", err)
	}
	slave, err := os.OpenFile("/dev/pts/"+strconv.Itoa(number), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("open pty slave: %v", err)
	}
	return master, slave
}

// TestTerminalHandoffDoesNotStopTheProcess reproduces a build hang: a sailwright
// process in a background process group took the terminal for Packer and was then
// stopped by SIGTTOU when taking it back, so it never exited.
func TestTerminalHandoffDoesNotStopTheProcess(t *testing.T) {
	for _, background := range []bool{false, true} {
		t.Run(fmt.Sprintf("background=%v", background), func(t *testing.T) {
			master, slave := openPTY(t)
			defer master.Close()
			defer slave.Close()

			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(self)
			cmd.Env = append(os.Environ(), ttyScenarioEnv+"=session")
			if background {
				cmd.Env = append(cmd.Env, "SAILWRIGHT_TEST_TTY_BACKGROUND=1")
			}
			cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			go func() {
				buf := make([]byte, 4096)
				for {
					if _, err := master.Read(buf); err != nil {
						return
					}
				}
			}()

			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("terminal hand-off failed: %v", err)
				}
			case <-time.After(20 * time.Second):
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				t.Fatal("terminal hand-off hung (process stopped by SIGTTOU)")
			}
		})
	}
}

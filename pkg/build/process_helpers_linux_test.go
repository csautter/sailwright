//go:build linux

package build

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
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
	case "terminal-interrupt":
		os.Exit(runTerminalInterrupt())
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

// runTerminalInterrupt runs a build command that owns the terminal and handles SIGINT
// like Packer does (clean up, exit 1), or exits 0 with SAILWRIGHT_TEST_TTY_EXIT=0 like
// a build that finished as Ctrl+C arrived. It exits 0 when the command's exit is
// detected as a Ctrl+C from the terminal and keeps the command's success or failure.
func runTerminalInterrupt() int {
	exitStatus := os.Getenv("SAILWRIGHT_TEST_TTY_EXIT")
	if exitStatus == "" {
		exitStatus = "1"
	}
	cmd, terminal := newBuildCommand("sh", []string{"-c", "trap 'exit " + exitStatus + "' INT; echo ready; while :; do sleep 0.1; done"}, false)
	if !terminal.attached {
		fmt.Fprintln(os.Stderr, "build command did not get the terminal")
		return 5
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	waitErr, interrupted := terminalCommandExit(cmd.Run())
	terminal.restore()
	if !interrupted {
		fmt.Fprintf(os.Stderr, "Ctrl+C was not detected, command ended with %v\n", waitErr)
		return 4
	}
	if (waitErr == nil) != (exitStatus == "0") {
		fmt.Fprintf(os.Stderr, "command with exit status %s ended with %v\n", exitStatus, waitErr)
		return 6
	}
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

// TestTerminalInterruptOfBuildCommandIsDetected covers Ctrl+C while a single build owns
// the terminal: the signal reaches only the build, which exits 1 after cleaning up, and
// sailwright must still recognise it as an interrupt to stop the other builds.
func TestTerminalInterruptOfBuildCommandIsDetected(t *testing.T) {
	for _, exitStatus := range []string{"1", "0"} {
		t.Run("exit="+exitStatus, func(t *testing.T) {
			testTerminalInterruptOfBuildCommand(t, exitStatus)
		})
	}
}

// testTerminalInterruptOfBuildCommand sends Ctrl+C to a build command that exits with
// exitStatus when it gets SIGINT.
func testTerminalInterruptOfBuildCommand(t *testing.T, exitStatus string) {
	master, slave := openPTY(t)
	defer master.Close()
	defer slave.Close()

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), ttyScenarioEnv+"=terminal-interrupt", "SAILWRIGHT_TEST_TTY_EXIT="+exitStatus)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	ready := make(chan struct{})
	output := make(chan string, 1)
	go func() {
		var seen []byte
		signalled := false
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			seen = append(seen, buf[:n]...)
			if !signalled && strings.Contains(string(seen), "ready") {
				signalled = true
				close(ready)
			}
			if err != nil {
				output <- string(seen)
				return
			}
		}
	}()

	select {
	case <-ready:
	case <-time.After(20 * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		t.Fatal("build command did not start")
	}
	if _, err := master.Write([]byte{0x03}); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			_ = slave.Close()
			t.Fatalf("Ctrl+C of the build command was not detected: %v\n%s", err, <-output)
		}
	case <-time.After(20 * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		t.Fatal("build command did not stop after Ctrl+C")
	}
}

// A command that exits 130 or 199 by itself must not be taken for a Ctrl+C, and must
// not count as a success.
func TestTerminalWrapperDoesNotMistakeOwnExitStatusForInterrupt(t *testing.T) {
	for _, status := range []string{"130", "199", "3"} {
		t.Run("exit="+status, func(t *testing.T) {
			cmd := exec.Command("/bin/sh", "-c", terminalInterruptWrapper, "sailwright-build", "sh", "-c", "exit "+status)
			waitErr, interrupted := terminalCommandExit(cmd.Run())
			if interrupted {
				t.Fatalf("exit %s without Ctrl+C was taken for an interrupt", status)
			}
			if waitErr == nil {
				t.Fatalf("exit %s must stay a failure", status)
			}
		})
	}
}

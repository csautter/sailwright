package cmd

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	runtimeassets "github.com/csautter/dev-alchemy"
	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

func TestInstallCommandForHostDarwin(t *testing.T) {
	projectDir := filepath.Join("tmp", "dev-alchemy")

	spec, err := installCommandForHost(alchemy_build.HostOsDarwin, projectDir, installCommandOptions{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedScriptPath := filepath.Join(projectDir, "scripts", "macos", "sailwright-install-dependencies.sh")
	if spec.executable != "bash" {
		t.Fatalf("expected bash, got %q", spec.executable)
	}
	if spec.scriptPath != expectedScriptPath {
		t.Fatalf("expected script path %q, got %q", expectedScriptPath, spec.scriptPath)
	}
	if !reflect.DeepEqual(spec.args, []string{expectedScriptPath}) {
		t.Fatalf("expected args %v, got %v", []string{expectedScriptPath}, spec.args)
	}
}

func TestInstallCommandForHostWindows(t *testing.T) {
	projectDir := filepath.Join("tmp", "dev-alchemy")

	spec, err := installCommandForHost(alchemy_build.HostOsWindows, projectDir, installCommandOptions{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedScriptPath := filepath.Join(projectDir, "scripts", "windows", "sailwright-self-setup.ps1")
	expectedArgs := []string{"-ExecutionPolicy", "Bypass", "-File", expectedScriptPath}
	if spec.executable != "powershell" {
		t.Fatalf("expected powershell, got %q", spec.executable)
	}
	if spec.scriptPath != expectedScriptPath {
		t.Fatalf("expected script path %q, got %q", expectedScriptPath, spec.scriptPath)
	}
	if !reflect.DeepEqual(spec.args, expectedArgs) {
		t.Fatalf("expected args %v, got %v", expectedArgs, spec.args)
	}
}

func TestInstallCommandForHostWindowsWithVirtualBox(t *testing.T) {
	projectDir := filepath.Join("tmp", "dev-alchemy")

	spec, err := installCommandForHost(alchemy_build.HostOsWindows, projectDir, installCommandOptions{virtualBox: true})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedScriptPath := filepath.Join(projectDir, "scripts", "windows", "sailwright-self-setup.ps1")
	expectedArgs := []string{"-ExecutionPolicy", "Bypass", "-File", expectedScriptPath, "-VirtualBox"}
	if spec.executable != "powershell" {
		t.Fatalf("expected powershell, got %q", spec.executable)
	}
	if spec.scriptPath != expectedScriptPath {
		t.Fatalf("expected script path %q, got %q", expectedScriptPath, spec.scriptPath)
	}
	if !reflect.DeepEqual(spec.args, expectedArgs) {
		t.Fatalf("expected args %v, got %v", expectedArgs, spec.args)
	}
}

func TestInstallCommandForHostWindowsWithGo(t *testing.T) {
	projectDir := filepath.Join("tmp", "dev-alchemy")

	spec, err := installCommandForHost(alchemy_build.HostOsWindows, projectDir, installCommandOptions{withGo: true})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedScriptPath := filepath.Join(projectDir, "scripts", "windows", "sailwright-self-setup.ps1")
	expectedArgs := []string{"-ExecutionPolicy", "Bypass", "-File", expectedScriptPath, "-WithGo"}
	if !reflect.DeepEqual(spec.args, expectedArgs) {
		t.Fatalf("expected args %v, got %v", expectedArgs, spec.args)
	}
}

func TestInstallCommandForHostWindowsWithHyperV(t *testing.T) {
	projectDir := filepath.Join("tmp", "dev-alchemy")

	spec, err := installCommandForHost(alchemy_build.HostOsWindows, projectDir, installCommandOptions{virtualBox: true, hyperV: true})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedScriptPath := filepath.Join(projectDir, "scripts", "windows", "sailwright-self-setup.ps1")
	expectedArgs := []string{"-ExecutionPolicy", "Bypass", "-File", expectedScriptPath, "-VirtualBox", "-HyperV"}
	if !reflect.DeepEqual(spec.args, expectedArgs) {
		t.Fatalf("expected args %v, got %v", expectedArgs, spec.args)
	}
}

func TestInstallCommandForHostHyperVUnsupported(t *testing.T) {
	for _, hostOs := range []alchemy_build.HostOsType{alchemy_build.HostOsDarwin, alchemy_build.HostOsLinux} {
		if _, err := installCommandForHost(hostOs, "/tmp/dev-alchemy", installCommandOptions{hyperV: true}); err == nil {
			t.Fatalf("expected unsupported --hyperv error for %s", hostOs)
		}
	}
}

func TestInstallCommandForHostLinux(t *testing.T) {
	projectDir := filepath.Join("tmp", "dev-alchemy")

	spec, err := installCommandForHost(alchemy_build.HostOsLinux, projectDir, installCommandOptions{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedScriptPath := filepath.Join(projectDir, "scripts", "linux", "sailwright-install-dependencies.sh")
	if spec.executable != "bash" {
		t.Fatalf("expected bash, got %q", spec.executable)
	}
	if spec.scriptPath != expectedScriptPath {
		t.Fatalf("expected script path %q, got %q", expectedScriptPath, spec.scriptPath)
	}
	if !reflect.DeepEqual(spec.args, []string{expectedScriptPath}) {
		t.Fatalf("expected args %v, got %v", []string{expectedScriptPath}, spec.args)
	}
}

func TestInstallCommandForHostDarwinWithGo(t *testing.T) {
	projectDir := filepath.Join("tmp", "dev-alchemy")

	spec, err := installCommandForHost(alchemy_build.HostOsDarwin, projectDir, installCommandOptions{withGo: true})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedScriptPath := filepath.Join(projectDir, "scripts", "macos", "sailwright-install-dependencies.sh")
	expectedArgs := []string{expectedScriptPath, "--with-go"}
	if !reflect.DeepEqual(spec.args, expectedArgs) {
		t.Fatalf("expected args %v, got %v", expectedArgs, spec.args)
	}
}

func TestInstallCommandForHostLinuxWithGo(t *testing.T) {
	projectDir := filepath.Join("tmp", "dev-alchemy")

	spec, err := installCommandForHost(alchemy_build.HostOsLinux, projectDir, installCommandOptions{withGo: true})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedScriptPath := filepath.Join(projectDir, "scripts", "linux", "sailwright-install-dependencies.sh")
	expectedArgs := []string{expectedScriptPath, "--with-go"}
	if !reflect.DeepEqual(spec.args, expectedArgs) {
		t.Fatalf("expected args %v, got %v", expectedArgs, spec.args)
	}
}

func TestInstallCommandForHostDarwinWithVirtualBoxUnsupported(t *testing.T) {
	_, err := installCommandForHost(alchemy_build.HostOsDarwin, "/tmp/dev-alchemy", installCommandOptions{virtualBox: true})
	if err == nil {
		t.Fatal("expected unsupported virtualbox error")
	}
}

func TestInstallCommandForUnsupportedHost(t *testing.T) {
	_, err := installCommandForHost(alchemy_build.HostOsType("plan9"), "/tmp/dev-alchemy", installCommandOptions{})
	if err == nil {
		t.Fatalf("expected unsupported host error")
	}
}

// Release binaries run outside a git checkout, so the installer must come
// from the embedded runtime assets rather than the repository on disk.
func TestInstallCommandForHostScriptsAreEmbedded(t *testing.T) {
	for _, hostOs := range []alchemy_build.HostOsType{
		alchemy_build.HostOsDarwin,
		alchemy_build.HostOsLinux,
		alchemy_build.HostOsWindows,
	} {
		t.Run(string(hostOs), func(t *testing.T) {
			spec, err := installCommandForHost(hostOs, "", installCommandOptions{withGo: true})
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if _, err := fs.Stat(runtimeassets.FS(), filepath.ToSlash(spec.scriptPath)); err != nil {
				t.Fatalf("install script %q is not embedded in the release binary (add it to runtimeassets.go): %v", spec.scriptPath, err)
			}
		})
	}
}

func newInstallHyperVTestCommand(t *testing.T, input io.Reader, args ...string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	previous := installHyperV
	t.Cleanup(func() { installHyperV = previous })
	installHyperV = false

	command := &cobra.Command{}
	command.Flags().BoolVar(&installHyperV, "hyperv", false, "")
	if err := command.Flags().Parse(args); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	var output bytes.Buffer
	command.SetIn(input)
	command.SetOut(&output)
	return command, &output
}

func stubInstallHyperVPrompt(t *testing.T, answer bool) *int {
	t.Helper()
	previous := promptForConfirmationFunc
	t.Cleanup(func() { promptForConfirmationFunc = previous })
	calls := 0
	promptForConfirmationFunc = func(_ io.Reader, _ io.Writer, prompt string) (bool, error) {
		calls++
		if !strings.Contains(prompt, "Enable Hyper-V now? [y/N]") || !strings.Contains(prompt, "only needed") {
			t.Fatalf("unexpected Hyper-V prompt %q", prompt)
		}
		return answer, nil
	}
	return &calls
}

func TestResolveInstallHyperVExplicitFlagSkipsPrompt(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"--hyperv"}, true},
		{[]string{"--hyperv=false"}, false},
	} {
		command, _ := newInstallHyperVTestCommand(t, bytes.NewBufferString(""), tc.args...)
		calls := stubInstallHyperVPrompt(t, !tc.want)

		got, err := resolveInstallHyperV(command, alchemy_build.HostOsWindows)
		if err != nil {
			t.Fatalf("%v: unexpected error %v", tc.args, err)
		}
		if got != tc.want || *calls != 0 {
			t.Fatalf("%v: got %v with %d prompts, want %v without prompt", tc.args, got, *calls, tc.want)
		}
	}
}

func TestResolveInstallHyperVAsksInteractiveWindowsUsers(t *testing.T) {
	for _, answer := range []bool{true, false} {
		command, _ := newInstallHyperVTestCommand(t, bytes.NewBufferString(""))
		calls := stubInstallHyperVPrompt(t, answer)

		got, err := resolveInstallHyperV(command, alchemy_build.HostOsWindows)
		if err != nil {
			t.Fatalf("unexpected error %v", err)
		}
		if got != answer || *calls != 1 {
			t.Fatalf("got %v with %d prompts, want %v after one prompt", got, *calls, answer)
		}
	}
}

func TestResolveInstallHyperVDefaultsToNoForEmptyAnswer(t *testing.T) {
	command, output := newInstallHyperVTestCommand(t, bytes.NewBufferString("\n"))

	got, err := resolveInstallHyperV(command, alchemy_build.HostOsWindows)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if got {
		t.Fatal("expected an empty answer to skip Hyper-V")
	}
	if !strings.Contains(output.String(), "sailwright install --hyperv") {
		t.Fatalf("expected a hint how to enable Hyper-V later, got %q", output.String())
	}
}

func TestResolveInstallHyperVSkipsNonInteractiveRuns(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { _ = devNull.Close() })
	command, output := newInstallHyperVTestCommand(t, devNull)
	calls := stubInstallHyperVPrompt(t, true)

	got, err := resolveInstallHyperV(command, alchemy_build.HostOsWindows)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if got || *calls != 0 {
		t.Fatalf("got %v with %d prompts, want false without prompt", got, *calls)
	}
	if !strings.Contains(output.String(), "Skipping Hyper-V setup") || !strings.Contains(output.String(), "--hyperv") {
		t.Fatalf("expected a skip hint, got %q", output.String())
	}
}

func TestResolveInstallHyperVNeverAsksOnOtherHosts(t *testing.T) {
	command, _ := newInstallHyperVTestCommand(t, bytes.NewBufferString(""))
	calls := stubInstallHyperVPrompt(t, true)

	for _, hostOs := range []alchemy_build.HostOsType{alchemy_build.HostOsDarwin, alchemy_build.HostOsLinux} {
		got, err := resolveInstallHyperV(command, hostOs)
		if err != nil || got || *calls != 0 {
			t.Fatalf("%s: got %v, %v with %d prompts", hostOs, got, err, *calls)
		}
	}
}

func TestInstallCommandForHostWindowsWithVagrant(t *testing.T) {
	projectDir := filepath.Join("tmp", "dev-alchemy")

	spec, err := installCommandForHost(alchemy_build.HostOsWindows, projectDir, installCommandOptions{hyperV: true, vagrant: true})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedScriptPath := filepath.Join(projectDir, "scripts", "windows", "sailwright-self-setup.ps1")
	expectedArgs := []string{"-ExecutionPolicy", "Bypass", "-File", expectedScriptPath, "-HyperV", "-Vagrant"}
	if !reflect.DeepEqual(spec.args, expectedArgs) {
		t.Fatalf("expected args %v, got %v", expectedArgs, spec.args)
	}
}

func TestInstallCommandForHostVagrantUnsupported(t *testing.T) {
	for _, hostOs := range []alchemy_build.HostOsType{alchemy_build.HostOsDarwin, alchemy_build.HostOsLinux} {
		if _, err := installCommandForHost(hostOs, "/tmp/dev-alchemy", installCommandOptions{vagrant: true}); err == nil {
			t.Fatalf("expected unsupported --vagrant error for %s", hostOs)
		}
	}
}

func newInstallVagrantTestCommand(t *testing.T, input io.Reader, args ...string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	previous := installVagrant
	t.Cleanup(func() { installVagrant = previous })
	installVagrant = false

	command := &cobra.Command{}
	command.Flags().BoolVar(&installVagrant, "vagrant", false, "")
	if err := command.Flags().Parse(args); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	var output bytes.Buffer
	command.SetIn(input)
	command.SetOut(&output)
	return command, &output
}

func TestResolveInstallVagrantExplicitFlagSkipsPrompt(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"--vagrant"}, true},
		{[]string{"--vagrant=false"}, false},
	} {
		command, _ := newInstallVagrantTestCommand(t, bytes.NewBufferString(""), tc.args...)
		previous := promptForConfirmationFunc
		promptForConfirmationFunc = func(_ io.Reader, _ io.Writer, _ string) (bool, error) {
			t.Fatalf("%v: unexpected prompt", tc.args)
			return false, nil
		}

		got, err := resolveInstallVagrant(command, alchemy_build.HostOsWindows)
		promptForConfirmationFunc = previous
		if err != nil || got != tc.want {
			t.Fatalf("%v: got %v, %v, want %v", tc.args, got, err, tc.want)
		}
	}
}

func TestResolveInstallVagrantAsksInteractiveWindowsUsers(t *testing.T) {
	command, _ := newInstallVagrantTestCommand(t, bytes.NewBufferString(""))
	previous := promptForConfirmationFunc
	t.Cleanup(func() { promptForConfirmationFunc = previous })
	var asked string
	promptForConfirmationFunc = func(_ io.Reader, _ io.Writer, prompt string) (bool, error) {
		asked = prompt
		return true, nil
	}

	got, err := resolveInstallVagrant(command, alchemy_build.HostOsWindows)
	if err != nil || !got {
		t.Fatalf("got %v, %v, want true", got, err)
	}
	if !strings.Contains(asked, "Install Vagrant now? [y/N]") || !strings.Contains(asked, "Local provisioning") {
		t.Fatalf("unexpected Vagrant prompt %q", asked)
	}
}

func TestResolveInstallVagrantDefaultsToNo(t *testing.T) {
	command, output := newInstallVagrantTestCommand(t, bytes.NewBufferString("\n"))

	got, err := resolveInstallVagrant(command, alchemy_build.HostOsWindows)
	if err != nil || got {
		t.Fatalf("got %v, %v, want false for an empty answer", got, err)
	}
	if !strings.Contains(output.String(), "sailwright install --vagrant") {
		t.Fatalf("expected a hint how to add Vagrant later, got %q", output.String())
	}
}

func TestResolveInstallVagrantSkipsNonInteractiveRuns(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { _ = devNull.Close() })
	command, output := newInstallVagrantTestCommand(t, devNull)

	got, err := resolveInstallVagrant(command, alchemy_build.HostOsWindows)
	if err != nil || got {
		t.Fatalf("got %v, %v, want false", got, err)
	}
	if !strings.Contains(output.String(), "Skipping Vagrant installation") || !strings.Contains(output.String(), "create, start, stop or destroy") {
		t.Fatalf("expected a skip hint, got %q", output.String())
	}
}

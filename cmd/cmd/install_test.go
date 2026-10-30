package cmd

import (
	"io/fs"
	"path/filepath"
	"reflect"
	"testing"

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

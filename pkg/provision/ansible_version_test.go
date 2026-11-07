package provision

import (
	"errors"
	"strings"
	"testing"
)

func TestAskBecomePassVersionWarning(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		wantWarning bool
		wantVersion string
	}{
		{name: "ubuntu 24.04 apt", output: "ansible-playbook [core 2.16.3]\n  config file = None\n", wantWarning: true, wantVersion: "2.16.3"},
		{name: "last release without the check", output: "ansible-playbook [core 2.18.12]", wantWarning: true, wantVersion: "2.18.12"},
		{name: "release candidate before the check", output: "ansible-playbook [core 2.18.0rc1]", wantWarning: true, wantVersion: "2.18.0rc1"},
		{name: "first release with the check", output: "ansible-playbook [core 2.19.0]"},
		{name: "current release", output: "ansible-playbook [core 2.21.5]"},
		{name: "next major", output: "ansible-playbook [core 3.0.0]"},
		{name: "unknown output", output: "command not found"},
		{name: "empty output", output: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warning := askBecomePassVersionWarning(tt.output)
			if !tt.wantWarning {
				if warning != "" {
					t.Fatalf("expected no warning, got %q", warning)
				}
				return
			}
			if !strings.Contains(warning, "ansible-core "+tt.wantVersion+" ") {
				t.Fatalf("expected warning to name ansible-core %s, got %q", tt.wantVersion, warning)
			}
			if !strings.Contains(warning, "Ctrl+C") || !strings.Contains(warning, "2.19") {
				t.Fatalf("expected warning to explain the way out and the fixed version, got %q", warning)
			}
		})
	}
}

func TestWarnIfAnsibleCoreHangsOnWrongBecomePasswordIgnoresErrors(t *testing.T) {
	previous := ansiblePlaybookVersionOutputFunc
	t.Cleanup(func() { ansiblePlaybookVersionOutputFunc = previous })

	called := false
	ansiblePlaybookVersionOutputFunc = func() (string, error) {
		called = true
		return "ansible-playbook [core 2.16.3]", errors.New("exit status 1")
	}

	// Must not panic or print for a failed version query; only check it ran.
	warnIfAnsibleCoreHangsOnWrongBecomePassword()
	if !called {
		t.Fatal("expected the ansible-playbook version query to run")
	}
}

func TestCheckInstalledAnsibleCore(t *testing.T) {
	previous := ansiblePlaybookVersionOutputFunc
	t.Cleanup(func() { ansiblePlaybookVersionOutputFunc = previous })
	for _, tt := range []struct {
		name, output string
		queryErr     error
		wantErr      bool
	}{
		{name: "old apt version", output: "ansible-playbook [core 2.16.3]", wantErr: true},
		{name: "last unsupported version", output: "ansible-playbook [core 2.18.12]", wantErr: true},
		{name: "minimum", output: "ansible-playbook [core 2.19.0]"},
		{name: "newer", output: "ansible-playbook [core 2.21.5]"},
		{name: "next major", output: "ansible-playbook [core 3.0.0]"},
		{name: "prerelease", output: "ansible-playbook [core 2.19.0rc1]", wantErr: true},
		{name: "unknown", output: "ansible-playbook 2.9.0", wantErr: true},
		{name: "startup crash", output: "AttributeError: module 'dataclasses' has no attribute '_is_type'", queryErr: errors.New("exit status 1"), wantErr: true},
		{name: "missing executable", queryErr: errors.New("not found"), wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ansiblePlaybookVersionOutputFunc = func() (string, error) { return tt.output, tt.queryErr }
			err := CheckInstalledAnsibleCore()
			if (err != nil) != tt.wantErr {
				t.Fatalf("got %v, want error %t", err, tt.wantErr)
			}
			if err != nil && (!strings.Contains(err.Error(), "sailwright install") || !strings.Contains(err.Error(), "PATH")) {
				t.Fatalf("missing recovery instructions: %v", err)
			}
		})
	}
}

package workflowcheck

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestWindowsAnsiblePython(t *testing.T) {
	j := loadWorkflow(t, "test-ansible-roles.yml").job(t, "test-ansible-roles")
	installIndex := j.stepIndex(func(s step) bool { return s.Name == "Windows install Ansible" })
	cygwinIndex := j.stepIndex(func(s step) bool { return s.Name == "Windows install cygwin" })
	if cygwinIndex == -1 || installIndex <= cygwinIndex {
		t.Fatal("expected Windows Ansible installation after Cygwin installation")
	}
	if err := windowsAnsiblePython(j.Steps[installIndex].Run, j.Steps[cygwinIndex].With["packages"]); err != nil {
		t.Fatal(err)
	}
}

// Require the Cygwin command to select the interpreter whose pip package is installed.
func windowsAnsiblePython(run, packages string) error {
	command := regexp.MustCompile(`-c "python([0-9]+)\.([0-9]+) -m pip install ansible pywinrm"`)
	version := command.FindStringSubmatch(run)
	if version == nil {
		return fmt.Errorf("Windows Ansible installation must use an explicit pythonX.Y -m pip command")
	}
	pipPackage := "python" + version[1] + version[2] + "-pip"
	installed := strings.FieldsFunc(packages, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' })
	if !slices.Contains(installed, pipPackage) {
		return fmt.Errorf("Windows Cygwin packages must include %s", pipPackage)
	}
	return nil
}

func TestWindowsAnsiblePythonContract(t *testing.T) {
	for _, tc := range []struct {
		name     string
		command  string
		packages string
		wantErr  bool
	}{
		{"explicit Python 3.9", "python3.9 -m pip install ansible pywinrm", "python39,python39-pip,python39-cryptography", false},
		{"old workflow", "python -m pip install ansible pywinrm", "python39,python39-pip,python39-cryptography", true},
		{"bare pip", "pip install ansible pywinrm", "python39,python39-pip", true},
		{"missing pip", "python3.9 -m pip install ansible pywinrm", "python39,python39-cryptography", true},
		{"mismatched pip", "python3.9 -m pip install ansible pywinrm", "python39,python312-pip", true},
		{"different explicit version", "python3.12 -m pip install ansible pywinrm", "python312,python312-pip", false},
		{"different version missing pip", "python3.12 -m pip install ansible pywinrm", "python312,python39-pip", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := `& $env:CYGWIN_BASH_PATH -l -c "` + tc.command + `"` + "\n"
			if err := windowsAnsiblePython(run, tc.packages); (err != nil) != tc.wantErr {
				t.Fatalf("windowsAnsiblePython() = %v, want error: %v", err, tc.wantErr)
			}
		})
	}
}

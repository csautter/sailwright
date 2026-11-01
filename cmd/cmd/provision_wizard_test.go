package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	"github.com/csautter/dev-alchemy/pkg/provision"
	"github.com/spf13/cobra"
)

func TestProvisionWithoutTerminal(t *testing.T) {
	input, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	oldInput := provisionCmd.InOrStdin()
	provisionCmd.SetIn(input)
	defer provisionCmd.SetIn(oldInput)
	err = provisionCmd.RunE(provisionCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "provision local --check") || !provisionCmd.SilenceUsage {
		t.Fatalf("unexpected result: %v", err)
	}
}

func TestWizardPreviewApplyAndCommand(t *testing.T) {
	dirs := alchemy_build.GetDirectoriesInstance()
	previousDirs := *dirs
	defer func() { *dirs = previousDirs }()
	root := t.TempDir()
	dirs.ProjectDir = root
	dirs.CacheDir = filepath.Join(root, "cache")
	dirs.ConfigDir = filepath.Join(root, "config")
	for _, dir := range []string{"roles/tool/tasks", "playbooks"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "roles/tool/tasks/main.yml"), []byte("---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEV_ALCHEMY_ROLE_SOURCES_CONFIG", filepath.Join(root, "missing.yml"))
	oldForm, oldRun := runWizardForm, runProvisionFunc
	defer func() { runWizardForm, runProvisionFunc = oldForm, oldRun }()
	answers := []string{"1\n", "1\n", "1\n0\n", "y\n", "y\n"}
	step := 0
	runWizardForm = func(cmd *cobra.Command, fields ...huh.Field) error {
		if step >= len(answers) {
			return errors.New("too many prompts")
		}
		answer := answers[step]
		step++
		return huh.NewForm(huh.NewGroup(fields...)).WithAccessible(true).WithInput(strings.NewReader(answer)).WithOutput(cmd.OutOrStdout()).Run()
	}
	checks := []bool{}
	runProvisionFunc = func(vm alchemy_build.VirtualMachineConfig, opts provision.ProvisionOptions) error {
		checks = append(checks, opts.Check)
		if _, err := os.Stat(opts.PlaybookPath); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	var output bytes.Buffer
	previousOutput, previousInput := provisionCmd.OutOrStdout(), provisionCmd.InOrStdin()
	defer func() { provisionCmd.SetOut(previousOutput); provisionCmd.SetIn(previousInput) }()
	provisionCmd.SetOut(&output)
	provisionCmd.SetIn(strings.NewReader(""))
	if err := provisionWizard(provisionCmd); err != nil {
		t.Fatal(err)
	}
	if len(checks) != 2 || !checks[0] || checks[1] {
		t.Fatalf("runs: %v", checks)
	}
	if !strings.Contains(output.String(), "sailwright provision local --role tool") {
		t.Fatalf("output: %s", output.String())
	}
	files, _ := filepath.Glob(filepath.Join(root, "cache/selected-playbooks/*.yml"))
	if len(files) != 0 {
		t.Fatal("temporary playbook leaked")
	}
}

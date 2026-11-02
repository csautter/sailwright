package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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
	provisionCmd.SetIn(input)
	defer provisionCmd.SetIn(nil)
	err = provisionCmd.RunE(provisionCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "provision local --check") || !provisionCmd.SilenceUsage {
		t.Fatalf("unexpected result: %v", err)
	}
}

func TestWizardPreviewApplyAndCommand(t *testing.T) {
	for _, tc := range []struct {
		name           string
		preview, apply bool
	}{
		{"preview and apply", true, true}, {"preview only", true, false}, {"apply without preview", false, true}, {"decline without preview", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) { testWizardFlow(t, tc.preview, tc.apply) })
	}
}

func testWizardFlow(t *testing.T, preview, apply bool) {
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
	configPath := filepath.Join(root, "sources.yml")
	if err := os.WriteFile(configPath, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEV_ALCHEMY_ROLE_SOURCES_CONFIG", configPath)
	oldForm, oldRun := runWizardForm, runProvisionFunc
	defer func() { runWizardForm, runProvisionFunc = oldForm, oldRun }()
	previewAnswer, applyAnswer := "n\n", "n\n"
	if preview {
		previewAnswer = "y\n"
	}
	if apply {
		applyAnswer = "y\n"
	}
	answers := []string{"1\n", "1\n", "1\n0\n", previewAnswer}
	if runtime.GOOS == "windows" {
		answers = append(answers, "y\n")
	}
	answers = append(answers, applyAnswer)
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
	defer func() { provisionCmd.SetOut(nil); provisionCmd.SetIn(nil) }()
	provisionCmd.SetOut(&output)
	provisionCmd.SetIn(strings.NewReader(""))
	oldNeedsSudo := wizardNeedsSudoPasswordFunc
	defer func() { wizardNeedsSudoPasswordFunc = oldNeedsSudo }()
	wizardNeedsSudoPasswordFunc = func() bool { return false }
	if err := provisionWizard(provisionCmd); err != nil {
		t.Fatal(err)
	}
	if len(selectedRoles) != 0 || playbookPath != "" || check || assumeYes || askBecomePass {
		t.Fatalf("wizard changed flag globals: roles=%v playbook=%q check=%t yes=%t ask=%t", selectedRoles, playbookPath, check, assumeYes, askBecomePass)
	}
	expected := []bool{}
	if preview {
		expected = append(expected, true)
	}
	if apply {
		expected = append(expected, false)
	}
	if !slices.Equal(checks, expected) {
		t.Fatalf("runs: %v, expected: %v", checks, expected)
	}
	if !strings.Contains(output.String(), "sailwright provision local --role tool") {
		t.Fatalf("output: %s", output.String())
	}
	files, _ := filepath.Glob(filepath.Join(root, "cache/selected-playbooks/*.yml"))
	if len(files) != 0 {
		t.Fatal("temporary playbook leaked")
	}
}

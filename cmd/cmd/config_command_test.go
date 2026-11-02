package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	"github.com/csautter/dev-alchemy/pkg/provision"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// resetCommandFlags restores every flag and I/O override in the command tree so
// consecutive rootCmd.Execute calls do not leak values or Changed state.
func resetCommandFlags(command *cobra.Command) {
	reset := func(f *pflag.Flag) {
		if slice, ok := f.Value.(pflag.SliceValue); ok {
			_ = slice.Replace(nil)
		} else {
			_ = f.Value.Set(f.DefValue)
		}
		f.Changed = false
	}
	command.Flags().VisitAll(reset)
	command.PersistentFlags().VisitAll(reset)
	// Clear I/O overrides so every command inherits the root's streams.
	command.SetIn(nil)
	command.SetOut(nil)
	command.SetErr(nil)
	for _, child := range command.Commands() {
		resetCommandFlags(child)
	}
}

// commandTestEnv isolates directories, config selection and provisioning for
// command-level tests and returns a runner for CLI argument lists.
func commandTestEnv(t *testing.T) func(args ...string) (string, error) {
	t.Helper()
	dirs := alchemy_build.GetDirectoriesInstance()
	previousDirs := *dirs
	root := t.TempDir()
	dirs.ProjectDir = filepath.Join(root, "project")
	dirs.CacheDir = filepath.Join(root, "cache")
	dirs.ConfigDir = filepath.Join(root, "config")
	previousHost, previousRun := currentHostLocalProvisionVirtualMachineFunc, runProvisionFunc
	currentHostLocalProvisionVirtualMachineFunc = linuxLocalProvisionVirtualMachine
	t.Cleanup(func() {
		*dirs = previousDirs
		currentHostLocalProvisionVirtualMachineFunc, runProvisionFunc = previousHost, previousRun
		resetCommandFlags(rootCmd)
		rootCmd.SetArgs(nil)
	})
	return func(args ...string) (string, error) {
		resetCommandFlags(rootCmd)
		var out bytes.Buffer
		rootCmd.SetArgs(args)
		rootCmd.SetIn(strings.NewReader(""))
		rootCmd.SetOut(&out)
		rootCmd.SetErr(io.Discard)
		err := rootCmd.Execute()
		return out.String(), err
	}
}

func writeCommandFixture(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestConfigCommandsHonorPrecedenceAndManageSources(t *testing.T) {
	run := commandTestEnv(t)
	work := t.TempDir()
	envConfig := filepath.Join(work, "env-sources.yml")
	flagConfig := filepath.Join(work, "flag-sources.yml")
	t.Setenv("DEV_ALCHEMY_ROLE_SOURCES_CONFIG", envConfig)
	team := filepath.Join(work, "team")
	writeCommandFixture(t, filepath.Join(team, "roles/tool/tasks/main.yml"), "---\n")
	writeCommandFixture(t, filepath.Join(team, "playbooks/setup.v2.yml"), "# Team setup\n- hosts: all\n")

	out, err := run("config", "path")
	if err != nil || !strings.Contains(out, envConfig+" (DEV_ALCHEMY_ROLE_SOURCES_CONFIG)") {
		t.Fatalf("env path: %q %v", out, err)
	}
	out, err = run("--config", flagConfig, "config", "path")
	if err != nil || !strings.Contains(out, flagConfig+" (flag)") {
		t.Fatalf("flag path: %q %v", out, err)
	}

	if _, err := run("--config", flagConfig, "config", "sources", "add", team, "--name", "team"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := os.Stat(envConfig); !os.IsNotExist(err) {
		t.Fatalf("--config must win over the environment, env file: %v", err)
	}
	if _, err := run("--config", flagConfig, "config", "sources", "add", team, "--name", "team"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate add: %v", err)
	}
	out, err = run("--config", flagConfig, "config", "sources", "list")
	if err != nil || !strings.Contains(out, "team\troles, playbooks\t"+team) {
		t.Fatalf("list: %q %v", out, err)
	}

	if _, err := run("--config", flagConfig, "config", "sources", "remove", "team"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	out, err = run("--config", flagConfig, "config", "sources", "list")
	if err != nil || strings.TrimSpace(out) != "" {
		t.Fatalf("list after remove: %q %v", out, err)
	}
	if _, err := run("--config", flagConfig, "config", "sources", "remove", "team"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("second remove: %v", err)
	}
}

func TestProvisionCatalogAndSelectionThroughCommands(t *testing.T) {
	run := commandTestEnv(t)
	work := t.TempDir()
	config := filepath.Join(work, "sources.yml")
	team := filepath.Join(work, "team")
	writeCommandFixture(t, filepath.Join(team, "roles/tool/tasks/main.yml"), "---\n")
	writeCommandFixture(t, filepath.Join(team, "roles/wintool/tasks/main.yml"), "---\n")
	writeCommandFixture(t, filepath.Join(team, "roles/wintool/meta/main.yml"), "galaxy_info:\n  platforms:\n    - name: Windows\n")
	writeCommandFixture(t, filepath.Join(team, "playbooks/setup.v2.yml"), "# Team setup\n- hosts: all\n")
	if _, err := run("--config", config, "config", "sources", "add", team, "--name", "team"); err != nil {
		t.Fatalf("add: %v", err)
	}

	out, err := run("--config", config, "provision", "catalog", "--json", "--os", "linux")
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	var catalog struct {
		SchemaVersion int `json:"schema_version"`
		Entries       []struct {
			Kind       string   `json:"kind"`
			Name       string   `json:"name"`
			Source     string   `json:"source"`
			Compatible bool     `json:"compatible"`
			Platforms  []string `json:"platforms"`
			Shadows    []string `json:"shadows"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &catalog); err != nil {
		t.Fatalf("catalog JSON: %v\n%s", err, out)
	}
	if catalog.SchemaVersion != 1 || len(catalog.Entries) != 3 {
		t.Fatalf("catalog: %+v", catalog)
	}
	compatible := map[string]bool{}
	for _, e := range catalog.Entries {
		if e.Source != "team" || e.Platforms == nil || e.Shadows == nil {
			t.Fatalf("entry fields: %+v", e)
		}
		compatible[e.Kind+":"+e.Name] = e.Compatible
	}
	if !compatible["role:tool"] || compatible["role:wintool"] || !compatible["playbook:setup.v2"] {
		t.Fatalf("compatibility: %v", compatible)
	}
	if !strings.Contains(out, `"platforms":[]`) || !strings.Contains(out, `"shadows":[]`) {
		t.Fatalf("expected empty arrays, not null: %s", out)
	}

	var captured []provision.ProvisionOptions
	runProvisionFunc = func(_ alchemy_build.VirtualMachineConfig, options provision.ProvisionOptions) error {
		data, err := os.ReadFile(options.PlaybookPath)
		if err != nil {
			t.Fatalf("playbook not readable during run: %v", err)
		}
		options.PlaybookPath += "\n" + string(data)
		captured = append(captured, options)
		return nil
	}
	if _, err := run("--config", config, "provision", "local", "--playbook", "setup.v2", "--check"); err != nil {
		t.Fatalf("dotted playbook: %v", err)
	}
	if _, err := run("--config", config, "provision", "local", "--role", "tool", "--check"); err != nil {
		t.Fatalf("role selection: %v", err)
	}
	if _, err := run("--config", config, "provision", "local", "--role", "tool", "--playbook", "setup.v2"); err == nil {
		t.Fatal("accepted --role together with --playbook")
	}
	if len(captured) != 2 {
		t.Fatalf("runs: %+v", captured)
	}
	if !strings.HasPrefix(captured[0].PlaybookPath, filepath.Join(team, "playbooks/setup.v2.yml")+"\n") || !captured[0].PlaybookPathExplicit || !captured[0].Check {
		t.Fatalf("dotted playbook options: %+v", captured[0])
	}
	if !strings.Contains(captured[1].PlaybookPath, filepath.Join(team, "roles/tool")) || !captured[1].PlaybookPathExplicit {
		t.Fatalf("role options: %+v", captured[1])
	}
}

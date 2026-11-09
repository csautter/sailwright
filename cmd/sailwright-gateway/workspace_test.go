package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runGatewayCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestWorkspaceCommandAddListRemove(t *testing.T) {
	base := t.TempDir()
	stateDir := filepath.Join(base, "state")
	host, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	yamlHost, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := "agent_dir: " + filepath.ToSlash(filepath.Join(base, "agent")) + "\nagent_workspaces:\n  - agent: /workspaces/static\n    host: " + filepath.ToSlash(yamlHost) + "\n"
	if err := os.WriteFile(filepath.Join(stateDir, "gateway.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	if out, err := runGatewayCLI(t, "--state-dir", stateDir, "workspace", "add", "/workspaces/roles", host); err != nil || !strings.Contains(out, "registered /workspaces/roles") {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if _, err := runGatewayCLI(t, "--state-dir", stateDir, "workspace", "add", "/workspaces/static", host); err == nil {
		t.Fatal("add must refuse an agent path configured in gateway.yaml")
	}
	out, err := runGatewayCLI(t, "--state-dir", stateDir, "workspace", "list")
	if err != nil || !strings.Contains(out, "/workspaces/static") || !strings.Contains(out, "gateway.yaml") || !strings.Contains(out, host) || !strings.Contains(out, "registered") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if _, err := runGatewayCLI(t, "--state-dir", stateDir, "workspace", "remove", "/workspaces/static"); err == nil || !strings.Contains(err.Error(), "gateway.yaml") {
		t.Fatalf("remove of a gateway.yaml entry: %v", err)
	}
	if out, err := runGatewayCLI(t, "--state-dir", stateDir, "workspace", "remove", "/workspaces/roles"); err != nil || !strings.Contains(out, "removed") {
		t.Fatalf("remove: %v\n%s", err, out)
	}
	if out, _ := runGatewayCLI(t, "--state-dir", stateDir, "workspace", "list"); strings.Contains(out, "/workspaces/roles") {
		t.Fatalf("removed workspace still listed:\n%s", out)
	}
}

package provision

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func catalogFixture(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogPrecedenceMetadataAndSelection(t *testing.T) {
	project := t.TempDir()
	dirs := withIsolatedAlchemyDirectories(t, project)
	first := t.TempDir()
	second := t.TempDir()
	for _, root := range []string{first, second, project} {
		catalogFixture(t, filepath.Join(root, "roles/tool/tasks/main.yaml"), "---\n")
		catalogFixture(t, filepath.Join(root, "playbooks/setup.yml"), "# Team setup\n- hosts: all\n")
	}
	catalogFixture(t, filepath.Join(first, "roles/tool/meta/main.yml"), "galaxy_info:\n  description: Example tool\n  platforms:\n    - name: Windows\n")
	catalogFixture(t, filepath.Join(project, "playbooks/role-sources-test.yml"), "- hosts: all\n")
	catalogFixture(t, filepath.Join(first, "playbooks/vars.yml"), "variable: value\n")
	writeRoleSourcesConfig(t, dirs.ConfigDir, "sources:\n  - name: first\n    path: "+first+"\n    roles_path: roles\n    playbooks_path: playbooks\n  - name: second\n    path: "+second+"\n    roles_path: roles\n")
	entries, err := Catalog("linux", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries: %+v", entries)
	}
	role := entries[0]
	if role.Source != "first" || role.Description != "Example tool" || role.Compatible || len(role.Shadows) != 2 {
		t.Fatalf("role: %+v", role)
	}
	path, cleanup, err := PrepareSelection([]string{"tool"}, "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var plays []map[string]interface{}
	// On Windows the role path is converted to the Cygwin form Ansible runs with.
	wantRole, err := ansibleRolePathForRuntime(filepath.Join(first, "roles", "tool"))
	if err != nil {
		t.Fatal(err)
	}
	if yaml.Unmarshal(data, &plays) != nil || len(plays) != 1 || !strings.Contains(string(data), wantRole) {
		t.Fatalf("playbook: %s", data)
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("temporary playbook was not removed")
	}
	if _, _, err := PrepareSelection([]string{"tool"}, "setup"); err == nil {
		t.Fatal("accepted roles and playbook")
	}
	if _, _, err := PrepareSelection([]string{"missing"}, ""); err == nil {
		t.Fatal("accepted unknown role")
	}
	path, _, err = PrepareSelection(nil, "setup")
	if err != nil || path != filepath.Join(first, "playbooks/setup.yml") {
		t.Fatalf("resolution: %s %v", path, err)
	}
	catalogFixture(t, filepath.Join(first, "roles/tool/meta/main.yml"), "galaxy_info: [broken")
	if _, err := Catalog("linux", false); err == nil {
		t.Fatal("accepted broken metadata")
	}
}

func TestCatalogUsesGitCacheWithoutCommands(t *testing.T) {
	project := t.TempDir()
	dirs := withIsolatedAlchemyDirectories(t, project)
	for _, p := range []string{"roles", "playbooks"} {
		if err := os.MkdirAll(filepath.Join(project, p), 0700); err != nil {
			t.Fatal(err)
		}
	}
	cached := dirs.CachePath(ansibleRoleSourcesCacheDir, "team")
	writeFakeGitCheckout(t, cached, "https://example.invalid/team.git")
	catalogFixture(t, filepath.Join(cached, "roles/tool/tasks/main.yml"), "---\n")
	writeRoleSourcesConfig(t, dirs.ConfigDir, "sources:\n  - name: team\n    type: git\n    url: https://example.invalid/team.git\n    roles_path: roles\n")
	previous := runRoleSourceGitCommand
	t.Cleanup(func() { runRoleSourceGitCommand = previous })
	runRoleSourceGitCommand = func(string, time.Duration, string, []string) (string, error) {
		t.Fatal("catalog fetched a cached source")
		return "", nil
	}
	entries, err := Catalog("linux", false)
	if err != nil || len(entries) != 1 {
		t.Fatalf("%+v %v", entries, err)
	}
}

func TestCatalogHonorsDefaultInclusionFlags(t *testing.T) {
	cases := []struct {
		roles, playbooks bool
	}{{true, true}, {true, false}, {false, true}, {false, false}}
	for _, tc := range cases {
		project := t.TempDir()
		dirs := withIsolatedAlchemyDirectories(t, project)
		team := t.TempDir()
		catalogFixture(t, filepath.Join(project, "roles/builtin/tasks/main.yml"), "---\n")
		catalogFixture(t, filepath.Join(project, "playbooks/builtin-setup.yml"), "- hosts: all\n")
		catalogFixture(t, filepath.Join(team, "roles/team/tasks/main.yml"), "---\n")
		catalogFixture(t, filepath.Join(team, "playbooks/team-setup.yml"), "- hosts: all\n")
		writeRoleSourcesConfig(t, dirs.ConfigDir, "include_default_roles: "+boolString(tc.roles)+"\ninclude_default_playbooks: "+boolString(tc.playbooks)+"\nsources:\n  - name: team\n    path: "+team+"\n    roles_path: roles\n    playbooks_path: playbooks\n")
		entries, err := Catalog("linux", false)
		if err != nil {
			t.Fatalf("%+v: %v", tc, err)
		}
		names := map[string]bool{}
		for _, e := range entries {
			names[e.Kind+":"+e.Name] = true
		}
		if !names["role:team"] || !names["playbook:team-setup"] || names["role:builtin"] != tc.roles || names["playbook:builtin-setup"] != tc.playbooks {
			t.Fatalf("%+v: entries %v", tc, names)
		}
		_, cleanup, err := PrepareSelection([]string{"builtin"}, "")
		cleanup()
		if (err == nil) != tc.roles {
			t.Fatalf("%+v: built-in role selection error %v", tc, err)
		}
		_, _, err = PrepareSelection(nil, "builtin-setup")
		if (err == nil) != tc.playbooks {
			t.Fatalf("%+v: built-in playbook selection error %v", tc, err)
		}
	}
}

func TestCatalogToleratesMissingBuiltInRoots(t *testing.T) {
	project := t.TempDir()
	dirs := withIsolatedAlchemyDirectories(t, project)
	team := t.TempDir()
	catalogFixture(t, filepath.Join(team, "roles/team/tasks/main.yml"), "---\n")
	writeRoleSourcesConfig(t, dirs.ConfigDir, "sources:\n  - name: team\n    path: "+team+"\n    roles_path: roles\n")
	entries, err := Catalog("linux", false)
	if err != nil || len(entries) != 1 || entries[0].Name != "team" {
		t.Fatalf("%+v %v", entries, err)
	}
}

func TestPrepareSelectionResolvesDottedPlaybookNames(t *testing.T) {
	project := t.TempDir()
	withIsolatedAlchemyDirectories(t, project)
	catalogFixture(t, filepath.Join(project, "roles/tool/tasks/main.yml"), "---\n")
	catalogFixture(t, filepath.Join(project, "playbooks/setup.v2.yml"), "- hosts: all\n")
	path, _, err := PrepareSelection(nil, "setup.v2")
	if err != nil || path != filepath.Join(project, "playbooks/setup.v2.yml") {
		t.Fatalf("resolution: %s %v", path, err)
	}
	for _, explicit := range []string{"setup.v2.yml", "site.YAML", "playbooks/setup", filepath.Join(project, "other")} {
		path, _, err := PrepareSelection(nil, explicit)
		if err != nil || path != explicit {
			t.Fatalf("explicit path %q resolved to %q: %v", explicit, path, err)
		}
	}
	if _, _, err := PrepareSelection(nil, "missing.v3"); err == nil {
		t.Fatal("accepted unknown dotted playbook name")
	}
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

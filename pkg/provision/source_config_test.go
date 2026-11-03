package provision

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigLocationPrecedence(t *testing.T) {
	project := t.TempDir()
	dirs := withIsolatedAlchemyDirectories(t, project)
	previous := ConfigOverride
	t.Cleanup(func() { ConfigOverride = previous })
	ConfigOverride = ""
	t.Chdir(t.TempDir())
	path, origin := ConfigLocation()
	if path != dirs.ConfigPath(ansibleRoleSourcesConfigFile) || origin != "default" {
		t.Fatalf("%s %s", path, origin)
	}
	catalogFixture(t, ansibleRoleSourcesConfigFile, "{}\n")
	cwd, _ := os.Getwd()
	path, origin = ConfigLocation()
	if path != filepath.Join(cwd, ansibleRoleSourcesConfigFile) || origin != "current directory" {
		t.Fatalf("%s %s", path, origin)
	}
	t.Setenv(ansibleRoleSourcesConfigEnvVar, "env.yml")
	path, origin = ConfigLocation()
	if path != "env.yml" || origin != ansibleRoleSourcesConfigEnvVar {
		t.Fatalf("%s %s", path, origin)
	}
	ConfigOverride = "flag.yml"
	path, origin = ConfigLocation()
	if path != "flag.yml" || origin != "flag" {
		t.Fatalf("%s %s", path, origin)
	}
}

func TestConfigSourceEditsPreserveCommentsAndOrder(t *testing.T) {
	dirs := withIsolatedAlchemyDirectories(t, t.TempDir())
	root := t.TempDir()
	catalogFixture(t, filepath.Join(root, "roles/tool/tasks/main.yml"), "---\n")
	catalogFixture(t, filepath.Join(root, "playbooks/setup.yml"), "- hosts: all\n")
	original := "# Team configuration\nplaybook: playbooks/setup.yml # default setup\ninclude_default_roles: true\nsources: [] # precedence order\n"
	writeRoleSourcesConfig(t, dirs.ConfigDir, original)
	if err := AddConfigSource(root, "team", ""); err != nil {
		t.Fatal(err)
	}
	config, _, _, err := loadCurrentAnsibleRoleSourcesConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Sources) != 1 || config.Sources[0].RolesPath != "roles" || config.Sources[0].PlaybooksPath != "playbooks" {
		t.Fatalf("%+v", config)
	}
	if err := AddConfigSource(root, "team", ""); err == nil {
		t.Fatal("duplicate was accepted")
	}
	path, _ := ConfigLocation()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, comment := range []string{"# Team configuration", "# default setup", "# precedence order"} {
		if !strings.Contains(text, comment) {
			t.Fatalf("lost comment %s: %s", comment, text)
		}
	}
	if strings.Index(text, "playbook:") > strings.Index(text, "include_default_roles:") {
		t.Fatal("changed mapping order")
	}
	if err := RemoveConfigSource("team"); err != nil {
		t.Fatal(err)
	}
	config, _, _, err = loadCurrentAnsibleRoleSourcesConfig()
	if err != nil || len(config.Sources) != 0 {
		t.Fatalf("%+v %v", config, err)
	}
	if err := RemoveConfigSource("missing"); err == nil {
		t.Fatal("removed nonexistent source")
	}
}

func TestConfigDetectsDirectRolesAndPlaybooksOnly(t *testing.T) {
	dirs := withIsolatedAlchemyDirectories(t, t.TempDir())
	root := t.TempDir()
	catalogFixture(t, filepath.Join(root, "tool/tasks/main.yaml"), "---\n")
	if err := AddConfigSource(root, "roles", ""); err != nil {
		t.Fatal(err)
	}
	plays := t.TempDir()
	catalogFixture(t, filepath.Join(plays, "setup.yaml"), "- hosts: all\n")
	if err := AddConfigSource(plays, "playbooks", ""); err != nil {
		t.Fatal(err)
	}
	config, _, _, err := loadCurrentAnsibleRoleSourcesConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Sources) != 1 || config.Sources[0].RolesPath != "." || len(config.PlaybookSources) != 1 || config.PlaybookSources[0].PlaybooksPath != "." {
		t.Fatalf("%+v", config)
	}
	path := dirs.ConfigPath(ansibleRoleSourcesConfigFile)
	before, _ := os.ReadFile(path)
	if err := AddConfigSource(t.TempDir(), "empty", ""); err == nil {
		t.Fatal("accepted empty source")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("failed add changed config")
	}
}

func TestConfigEditRefusesMultipleDocuments(t *testing.T) {
	dirs := withIsolatedAlchemyDirectories(t, t.TempDir())
	original := "sources: []\n---\nplaybook: important.yml\n"
	writeRoleSourcesConfig(t, dirs.ConfigDir, original)
	if err := RemoveConfigSource("missing"); err == nil || !strings.Contains(err.Error(), "one YAML document") {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(dirs.ConfigPath(ansibleRoleSourcesConfigFile))
	if err != nil || string(data) != original {
		t.Fatal("changed multi-document config")
	}
}

func TestMissingConfigSelectedByFlagFails(t *testing.T) {
	withIsolatedAlchemyDirectories(t, t.TempDir())
	previous := ConfigOverride
	t.Cleanup(func() { ConfigOverride = previous })
	ConfigOverride = filepath.Join(t.TempDir(), "typo.yml")
	if _, err := Catalog("linux", false); err == nil || !strings.Contains(err.Error(), "--config points at") {
		t.Fatalf("expected missing --config error, got %v", err)
	}
}

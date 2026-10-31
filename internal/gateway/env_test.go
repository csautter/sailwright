package gateway

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestChildEnvironmentKeepsEmbeddedAssetsGatewayOwned(t *testing.T) {
	dirs := NewManagedDirs(Config{StateDir: t.TempDir(), DataDir: t.TempDir()})
	dirs.AppData = t.TempDir()
	env := BuildChildEnv(dirs, []string{"SAILWRIGHT_PROJECT_DIR", "DEV_ALCHEMY_EMBEDDED_DATA_DIR", envConfigDir}, func(string) string { return "/untrusted" })
	values := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	if values["SAILWRIGHT_PROJECT_DIR"] != "" {
		t.Fatal("project override escaped gateway policy")
	}
	if values["DEV_ALCHEMY_EMBEDDED_DATA_DIR"] != dirs.EmbeddedData {
		t.Fatal("extraction root escaped gateway policy")
	}
	if values[envConfigDir] != dirs.Config {
		t.Fatal("config escaped gateway policy")
	}
	if got := dirs.PlaybookDir(); got != filepath.Join(dirs.EmbeddedData, "project", "playbooks") {
		t.Fatalf("unexpected playbook directory: %s", got)
	}
}

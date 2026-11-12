package gateway

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Built at runtime so secret scanners do not flag a hex literal.
var testProbeToken = strings.Repeat("0123456789abcdef", 2)

func workspaceTestConfig(t *testing.T) Config {
	t.Helper()
	base := t.TempDir()
	return Config{StateDir: filepath.Join(base, "state"), DataDir: filepath.Join(base, "data"), AgentDir: filepath.Join(base, "agent")}
}

func resolvedTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRegisteredWorkspacesRoundTrip(t *testing.T) {
	cfg := workspaceTestConfig(t)
	if err := ensurePrivateDir(cfg.StateDir); err != nil {
		t.Fatal(err)
	}
	host := resolvedTempDir(t)
	added, err := AddRegisteredWorkspace(cfg, WorkspaceMapping{Agent: " /workspaces/roles ", Host: host}, "abcd1234")
	if err != nil {
		t.Fatal(err)
	}
	if added.Agent != "/workspaces/roles" || added.Host != host || added.ApprovalID != "abcd1234" {
		t.Fatalf("added: %+v", added)
	}
	if _, err := AddRegisteredWorkspace(cfg, WorkspaceMapping{Agent: "/workspaces/roles", Host: host}, ""); err == nil || !strings.Contains(err.Error(), "already mapped") {
		t.Fatalf("duplicate add: %v", err)
	}
	loaded, err := LoadRegisteredWorkspaces(cfg)
	if err != nil || len(loaded) != 1 || loaded[0].Host != host {
		t.Fatalf("load: %+v %v", loaded, err)
	}
	if info, err := os.Stat(WorkspacesPath(cfg.StateDir)); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != privateFilePermission) {
		t.Fatalf("workspaces file: %v %v", info, err)
	}
	if removed, err := RemoveRegisteredWorkspace(cfg, "/workspaces/roles"); err != nil || !removed {
		t.Fatalf("remove: %v %v", removed, err)
	}
	if removed, err := RemoveRegisteredWorkspace(cfg, "/workspaces/roles"); err != nil || removed {
		t.Fatalf("second remove: %v %v", removed, err)
	}
	if loaded, err := LoadRegisteredWorkspaces(cfg); err != nil || len(loaded) != 0 {
		t.Fatalf("after remove: %+v %v", loaded, err)
	}
}

func TestLoadRegisteredWorkspacesRejectsInvalidFiles(t *testing.T) {
	cfg := workspaceTestConfig(t)
	if err := ensurePrivateDir(cfg.StateDir); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"not json":       "{",
		"wrong schema":   `{"schema_version":2,"workspaces":[]}`,
		"relative agent": `{"schema_version":1,"workspaces":[{"agent":"workspaces/x","host":"` + filepath.ToSlash(resolvedTempDir(t)) + `"}]}`,
		"gateway dir":    `{"schema_version":1,"workspaces":[{"agent":"/workspaces/x","host":` + jsonString(filepath.Dir(cfg.StateDir)) + `}]}`,
	} {
		if err := os.WriteFile(WorkspacesPath(cfg.StateDir), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadRegisteredWorkspaces(cfg); err == nil || !strings.Contains(err.Error(), workspacesFileName) {
			t.Fatalf("%s: expected an error naming the file, got %v", name, err)
		}
	}
}

func jsonString(value string) string {
	return `"` + strings.ReplaceAll(value, `\`, `\\`) + `"`
}

func TestValidateNewWorkspaceRejectsUnsafeHosts(t *testing.T) {
	cfg := workspaceTestConfig(t)
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	root := "/"
	if runtime.GOOS == "windows" {
		root = filepath.VolumeName(os.TempDir()) + `\`
	}
	home := resolvedTempDir(t)
	t.Setenv("HOME", filepath.Join(home, "me"))
	t.Setenv("USERPROFILE", filepath.Join(home, "me"))
	if err := os.Mkdir(filepath.Join(home, "me"), 0o700); err != nil {
		t.Fatal(err)
	}
	cases := map[string]WorkspaceMapping{
		"missing":      {Agent: "/workspaces/x", Host: filepath.Join(t.TempDir(), "missing")},
		"file":         {Agent: "/workspaces/x", Host: file},
		"root":         {Agent: "/workspaces/x", Host: root},
		"home":         {Agent: "/workspaces/x", Host: home},
		"gateway dirs": {Agent: "/workspaces/x", Host: filepath.Dir(cfg.StateDir)},
		"agent root":   {Agent: "/", Host: resolvedTempDir(t)},
	}
	if err := os.MkdirAll(filepath.Dir(cfg.StateDir), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, mapping := range cases {
		if _, err := ValidateNewWorkspace(cfg, mapping); err == nil {
			t.Errorf("%s: %+v was accepted", name, mapping)
		}
	}
	ok := resolvedTempDir(t)
	if got, err := ValidateNewWorkspace(cfg, WorkspaceMapping{Agent: "/workspaces/x/", Host: ok + string(filepath.Separator)}); err == nil {
		t.Fatalf("unclean agent path accepted: %+v", got)
	}
	if got, err := ValidateNewWorkspace(cfg, WorkspaceMapping{Agent: "/workspaces/x", Host: ok}); err != nil || got.Host != ok {
		t.Fatalf("valid workspace: %+v %v", got, err)
	}
}

func TestVerifyWorkspaceProbe(t *testing.T) {
	dir := t.TempDir()
	probe := filepath.Join(dir, WorkspaceProbeName)
	if err := VerifyWorkspaceProbe(dir, testProbeToken); err == nil || !strings.Contains(err.Error(), "has no") {
		t.Fatalf("missing probe: %v", err)
	}
	if err := os.WriteFile(probe, []byte(testProbeToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyWorkspaceProbe(dir, testProbeToken); err != nil {
		t.Fatalf("matching probe: %v", err)
	}
	if err := VerifyWorkspaceProbe(dir, strings.Repeat("f", 32)); err == nil || !strings.Contains(err.Error(), "not the directory mounted") {
		t.Fatalf("wrong token: %v", err)
	}
	for _, token := range []string{"", "short", strings.ToUpper(testProbeToken), strings.Repeat("a", 129)} {
		if err := VerifyWorkspaceProbe(dir, token); err == nil || !strings.Contains(err.Error(), "hex") {
			t.Fatalf("token %q: %v", token, err)
		}
	}
	if err := os.WriteFile(probe, bytes.Repeat([]byte("a"), maxProbeBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyWorkspaceProbe(dir, testProbeToken); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("oversized probe: %v", err)
	}
	if runtime.GOOS != "windows" {
		elsewhere := filepath.Join(t.TempDir(), "probe")
		if err := os.WriteFile(elsewhere, []byte(testProbeToken), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(probe); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, probe); err != nil {
			t.Fatal(err)
		}
		if err := VerifyWorkspaceProbe(dir, testProbeToken); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("symlinked probe: %v", err)
		}
	}
}

func TestWorkspaceRegistryMergesAndReloads(t *testing.T) {
	cfg := workspaceTestConfig(t)
	if err := ensurePrivateDir(cfg.StateDir); err != nil {
		t.Fatal(err)
	}
	static := resolvedTempDir(t)
	cfg.AgentWorkspaces = []WorkspaceMapping{{Agent: "/workspaces/static", Host: static}}
	var console bytes.Buffer
	registry, err := loadWorkspaceRegistry(cfg, &console)
	if err != nil {
		t.Fatal(err)
	}
	if all := registry.All(); len(all) != 1 {
		t.Fatalf("initial: %+v", all)
	}

	// The operator CLI writes the file while the gateway runs.
	other := resolvedTempDir(t)
	if _, err := AddRegisteredWorkspace(cfg, WorkspaceMapping{Agent: "/workspaces/other", Host: other}, ""); err != nil {
		t.Fatal(err)
	}
	if mapping, source, ok := registry.lookup("/workspaces/other"); !ok || mapping.Host != other || source != workspaceSourceFile {
		t.Fatalf("reload after CLI add: %+v %s %v", mapping, source, ok)
	}

	// gateway.yaml wins over a registered entry for the same agent path.
	shadow := []RegisteredWorkspace{{Agent: "/workspaces/static", Host: other}, {Agent: "/workspaces/other", Host: other}}
	if err := saveRegisteredWorkspaces(cfg.StateDir, shadow); err != nil {
		t.Fatal(err)
	}
	bumpModTime(t, WorkspacesPath(cfg.StateDir), 1)
	all := registry.All()
	if len(all) != 2 || all[0].Host != static {
		t.Fatalf("static must win: %+v", all)
	}

	// A broken file fails closed to the gateway.yaml workspaces and is reported once.
	if err := os.WriteFile(WorkspacesPath(cfg.StateDir), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	bumpModTime(t, WorkspacesPath(cfg.StateDir), 2)
	if all := registry.All(); len(all) != 1 || all[0].Agent != "/workspaces/static" {
		t.Fatalf("broken file: %+v", all)
	}
	registry.All()
	if strings.Count(console.String(), "registered workspaces ignored") != 1 {
		t.Fatalf("console: %q", console.String())
	}
}

// bumpModTime makes a rewrite visible even on filesystems with coarse timestamps.
func bumpModTime(t *testing.T, path string, hours int) {
	t.Helper()
	later := time.Now().Add(time.Duration(hours) * time.Hour)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

// swapForSymlink replaces dir with a symlink to target, as an agent can do for any
// directory inside its mount.
func swapForSymlink(t *testing.T, dir, target string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("directory symlinks need extra privileges on Windows")
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, dir); err != nil {
		t.Fatal(err)
	}
}

// A registered workspace below the agent's mount must not follow a symlink the agent
// swaps in after registration; gateway.yaml entries keep following it.
func TestRegisteredWorkspaceIsPinnedAgainstSymlinkSwap(t *testing.T) {
	mount := resolvedTempDir(t)
	secret := resolvedTempDir(t)
	if err := os.WriteFile(filepath.Join(secret, "id_ed25519"), []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(mount, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	registered := WorkspaceMapping{Agent: "/workspaces/sub", Host: sub, Registered: true}
	if _, _, err := TranslateAgentPath("/workspaces/sub", []WorkspaceMapping{registered}); err != nil {
		t.Fatalf("real directory: %v", err)
	}
	realSnapshot, err := CreateConfinedSnapshot("/workspaces/sub", registered, ".", sub, filepath.Join(t.TempDir(), "stage"), defaultLimits())
	if err != nil {
		t.Fatalf("snapshot of real directory: %v", err)
	}
	t.Cleanup(func() { _ = RemoveSnapshot(realSnapshot.Dir) })

	swapForSymlink(t, sub, secret)
	if _, _, err := TranslateAgentPath("/workspaces/sub", []WorkspaceMapping{registered}); err == nil || !strings.Contains(err.Error(), "must stay a real directory") {
		t.Fatalf("translate after swap: %v", err)
	}
	if _, err := CreateConfinedSnapshot("/workspaces/sub", registered, ".", sub, filepath.Join(t.TempDir(), "stage"), defaultLimits()); err == nil || !strings.Contains(err.Error(), "must stay a real directory") {
		t.Fatalf("snapshot after swap: %v", err)
	}
	static := registered
	static.Registered = false
	// The snapshot is read-only; remove it explicitly so TempDir cleanup works for non-root users.
	followed, err := CreateConfinedSnapshot("/workspaces/sub", static, ".", sub, filepath.Join(t.TempDir(), "stage"), defaultLimits())
	if err != nil {
		t.Fatalf("gateway.yaml entries keep following symlinks: %v", err)
	}
	t.Cleanup(func() { _ = RemoveSnapshot(followed.Dir) })
}

// The directory opened for the snapshot must be the one at the pinned path after the
// open, so swapping the path between open and check is caught.
func TestCheckPinnedWorkspaceRejectsReplacedDirectory(t *testing.T) {
	mount := resolvedTempDir(t)
	sub := filepath.Join(mount, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := WorkspaceMapping{Agent: "/workspaces/sub", Host: sub, Registered: true}
	root, err := os.OpenRoot(sub)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := checkPinnedWorkspace(root, workspace); err != nil {
		t.Fatalf("unchanged: %v", err)
	}
	if err := os.Rename(sub, filepath.Join(mount, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := checkPinnedWorkspace(root, workspace); err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Fatalf("replaced directory: %v", err)
	}
}

// Approval binds the stored host path to the one the operator saw.
func TestAddRegisteredWorkspaceRefusesPathChangedSinceApproval(t *testing.T) {
	cfg := workspaceTestConfig(t)
	mount := resolvedTempDir(t)
	elsewhere := resolvedTempDir(t)
	sub := filepath.Join(mount, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	swapForSymlink(t, sub, elsewhere)
	if _, err := addRegisteredWorkspace(cfg, WorkspaceMapping{Agent: "/workspaces/sub", Host: sub}, "id", sub); err == nil || !strings.Contains(err.Error(), "not the approved") {
		t.Fatalf("changed path accepted: %v", err)
	}
	if registered, err := LoadRegisteredWorkspaces(cfg); err != nil || len(registered) != 0 {
		t.Fatalf("nothing may be saved: %+v %v", registered, err)
	}
}

// Concurrent writers (gateway approvals, the operator CLI) must not lose updates.
func TestRegisteredWorkspaceWritesAreSerialized(t *testing.T) {
	cfg := workspaceTestConfig(t)
	const writers = 8
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		host := resolvedTempDir(t)
		agent := "/workspaces/w" + string(rune('a'+i))
		go func() {
			_, err := AddRegisteredWorkspace(cfg, WorkspaceMapping{Agent: agent, Host: host}, "")
			errs <- err
		}()
	}
	for i := 0; i < writers; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if registered, err := LoadRegisteredWorkspaces(cfg); err != nil || len(registered) != writers {
		t.Fatalf("lost updates: %d of %d (%v)", len(registered), writers, err)
	}
}

// A same-size rewrite within one timestamp tick still reaches a running gateway.
func TestWorkspaceRegistryReloadsSameSizeRewrite(t *testing.T) {
	cfg := workspaceTestConfig(t)
	if err := ensurePrivateDir(cfg.StateDir); err != nil {
		t.Fatal(err)
	}
	host := resolvedTempDir(t)
	stamp := time.Now().Add(-time.Hour).Truncate(time.Second)
	write := func(agent string) {
		if err := saveRegisteredWorkspaces(cfg.StateDir, []RegisteredWorkspace{{Agent: agent, Host: host, RegisteredAt: stamp}}); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(WorkspacesPath(cfg.StateDir), stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	write("/workspaces/aaa")
	registry, err := loadWorkspaceRegistry(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	write("/workspaces/bbb")
	if _, _, ok := registry.lookup("/workspaces/bbb"); !ok {
		t.Fatalf("same-size rewrite not reloaded: %+v", registry.All())
	}
	if _, _, ok := registry.lookup("/workspaces/aaa"); ok {
		t.Fatal("removed workspace still active")
	}
}

func TestRemoveRegisteredWorkspaceNormalizesAgentPath(t *testing.T) {
	cfg := workspaceTestConfig(t)
	if _, err := AddRegisteredWorkspace(cfg, WorkspaceMapping{Agent: "/workspaces/roles", Host: resolvedTempDir(t)}, ""); err != nil {
		t.Fatal(err)
	}
	if removed, err := RemoveRegisteredWorkspace(cfg, " /workspaces/roles/ "); err != nil || !removed {
		t.Fatalf("remove with trailing slash: %v %v", removed, err)
	}
}

func TestQuoteShellArg(t *testing.T) {
	cases := []struct {
		arg     string
		windows bool
		want    string
	}{
		{"/workspaces/roles", false, "/workspaces/roles"},
		{"/home/me/My Roles", false, "'/home/me/My Roles'"},
		{"/home/me/it's", false, `'/home/me/it'\''s'`},
		{"", false, "''"},
		{`C:\Users\me\roles`, true, `C:\Users\me\roles`},
		{`C:\Users\John Doe\roles`, true, `"C:\Users\John Doe\roles"`},
	}
	for _, c := range cases {
		if got := quoteShellArg(c.arg, c.windows); got != c.want {
			t.Errorf("quoteShellArg(%q, %v) = %s, want %s", c.arg, c.windows, got, c.want)
		}
	}
}

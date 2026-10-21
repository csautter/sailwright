package gateway

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func defaultLimits() SnapshotLimits {
	return SnapshotLimits{MaxBytes: 1 << 20, MaxFiles: 100}
}

func TestTranslateAgentPath(t *testing.T) {
	host, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"ansible", filepath.Join("other", "x")} {
		if err := os.MkdirAll(filepath.Join(host, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	workspaces := []WorkspaceMapping{{Agent: "/workspaces/repo", Host: host}, {Agent: "/workspaces/repo/nested", Host: filepath.Join(host, "other")}}

	got, _, err := TranslateAgentPath("/workspaces/repo/ansible/", workspaces)
	if err != nil || got != filepath.Join(host, "ansible") {
		t.Fatalf("got %q, %v", got, err)
	}
	got, _, err = TranslateAgentPath("/workspaces/repo/nested/x", workspaces)
	if err != nil || got != filepath.Join(host, "other", "x") {
		t.Fatalf("expected the longest mapping to win, got %q, %v", got, err)
	}
	for _, bad := range []string{"relative/path", "/workspaces/repo/../../etc", "/workspaces/repository", "/etc"} {
		if got, _, err := TranslateAgentPath(bad, workspaces); err == nil {
			t.Fatalf("expected %q to be rejected, got %q", bad, got)
		}
	}
}

func TestTranslateAgentPathRejectsSymlinkedRoot(t *testing.T) {
	host := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret"), "x")
	if err := os.Symlink(outside, filepath.Join(host, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	workspaces := []WorkspaceMapping{{Agent: "/workspaces/repo", Host: host}}
	if got, _, err := TranslateAgentPath("/workspaces/repo/link", workspaces); err == nil {
		t.Fatalf("expected symlinked bundle_root to be rejected, got %q", got)
	}
}

func TestCreateSnapshotCopiesBundleReadOnlyAndIsImmuneToLaterEdits(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "site.yml"), "- hosts: all\n")
	writeFile(t, filepath.Join(src, "roles", "demo", "tasks", "main.yml"), "- debug: msg=hi\n")
	writeFile(t, filepath.Join(src, ".git", "config"), "ignored")

	snapshot, err := CreateSnapshot("/workspaces/repo/ansible", src, filepath.Join(t.TempDir(), "staging"), defaultLimits())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	t.Cleanup(func() { _ = RemoveSnapshot(snapshot.Dir) })

	if snapshot.Files != 2 || snapshot.TreeHash == "" {
		t.Fatalf("unexpected snapshot %+v", snapshot)
	}
	if _, err := os.Stat(filepath.Join(snapshot.Dir, ".git")); !os.IsNotExist(err) {
		t.Fatalf(".git must not be copied: %v", err)
	}

	writeFile(t, filepath.Join(src, "site.yml"), "- hosts: all\n  tasks: [{command: evil}]\n")
	copied, err := os.ReadFile(filepath.Join(snapshot.Dir, "site.yml"))
	if err != nil || string(copied) != "- hosts: all\n" {
		t.Fatalf("snapshot content changed after source edit: %q, %v", copied, err)
	}

	if runtime.GOOS != "windows" {
		if err := os.WriteFile(filepath.Join(snapshot.Dir, "site.yml"), []byte("x"), 0o600); err == nil && os.Getuid() != 0 {
			t.Fatal("snapshot files must be read-only")
		}
	}

	again, err := CreateSnapshot("/workspaces/repo/ansible", src, filepath.Join(t.TempDir(), "staging"), defaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = RemoveSnapshot(again.Dir) })
	if again.TreeHash == snapshot.TreeHash {
		t.Fatal("tree hash must change when content changes")
	}
}

func TestCreateSnapshotRejectsEscapingSymlinksAndLimits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need extra privileges on Windows")
	}
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret"), "host secret")

	src := t.TempDir()
	writeFile(t, filepath.Join(src, "ok.yml"), "ok")
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(src, "leak")); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateSnapshot("/w/a", src, t.TempDir(), defaultLimits()); err == nil {
		t.Fatal("expected a symlink to outside the bundle to be rejected")
	}

	src2 := t.TempDir()
	writeFile(t, filepath.Join(src2, "target.yml"), "content")
	if err := os.Symlink("target.yml", filepath.Join(src2, "alias.yml")); err != nil {
		t.Fatal(err)
	}
	snapshot, err := CreateSnapshot("/w/a", src2, t.TempDir(), defaultLimits())
	if err != nil {
		t.Fatalf("a symlink inside the bundle should be copied: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(snapshot.Dir, "alias.yml"))
	if string(data) != "content" {
		t.Fatalf("expected dereferenced symlink content, got %q", data)
	}
	_ = RemoveSnapshot(snapshot.Dir)

	src3 := t.TempDir()
	if err := os.Mkdir(filepath.Join(src3, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("dir", filepath.Join(src3, "loop")); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateSnapshot("/w/a", src3, t.TempDir(), defaultLimits()); err == nil {
		t.Fatal("expected directory symlinks to be rejected")
	}
}

func TestCreateSnapshotEnforcesSizeAndFileLimits(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "big"), strings.Repeat("x", 2048))
	if _, err := CreateSnapshot("/w/a", src, t.TempDir(), SnapshotLimits{MaxBytes: 1024, MaxFiles: 10}); err == nil {
		t.Fatal("expected the size limit to be enforced")
	}

	src2 := t.TempDir()
	for i := 0; i < 5; i++ {
		writeFile(t, filepath.Join(src2, string(rune('a'+i))), "x")
	}
	if _, err := CreateSnapshot("/w/a", src2, t.TempDir(), SnapshotLimits{MaxBytes: 1024, MaxFiles: 3}); err == nil {
		t.Fatal("expected the file limit to be enforced")
	}
}

func TestRewriteBundlePaths(t *testing.T) {
	snapshot := Snapshot{AgentRoot: "/workspaces/repo/ansible", Dir: filepath.Join(string(filepath.Separator), "staging", "bundle-1")}
	args := []string{
		"provision", "ubuntu",
		"--playbook", "/workspaces/repo/ansible/site.yml",
		"--inventory-path=/workspaces/repo/ansible/inv.yml",
		"--", "-e", "@/workspaces/repo/ansible/vars.yml",
		"/workspaces/repo/ansible-other/x.yml",
		"/etc/passwd",
		"relative.yml",
	}
	got := RewriteBundlePaths(args, snapshot)
	want := []string{
		"provision", "ubuntu",
		"--playbook", filepath.Join(snapshot.Dir, "site.yml"),
		"--inventory-path=" + filepath.Join(snapshot.Dir, "inv.yml"),
		"--", "-e", "@" + filepath.Join(snapshot.Dir, "vars.yml"),
		"/workspaces/repo/ansible-other/x.yml",
		"/etc/passwd",
		"relative.yml",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("arg %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRemoveStaleStagingRemovesOnlyGatewayLeftovers(t *testing.T) {
	staging := t.TempDir()
	bundle := filepath.Join(staging, "bundle-123")
	writeFile(t, filepath.Join(bundle, "site.yml"), "x")
	if err := makeTreeReadOnly(bundle); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(staging, "become-456"), "pw")
	writeFile(t, filepath.Join(staging, "unrelated"), "keep")

	RemoveStaleStaging(staging)

	for _, name := range []string{"bundle-123", "become-456"} {
		if _, err := os.Stat(filepath.Join(staging, name)); !os.IsNotExist(err) {
			t.Fatalf("%s must be removed: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(staging, "unrelated")); err != nil {
		t.Fatalf("unrelated entries must stay: %v", err)
	}
}

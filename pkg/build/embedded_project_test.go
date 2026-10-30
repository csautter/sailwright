package build

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	runtimeassets "github.com/csautter/dev-alchemy"
)

func TestEnsureProjectDir_PrefersGitCheckout(t *testing.T) {
	repoRoot := t.TempDir()
	workingDir := filepath.Join(repoRoot, "nested", "dir")
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("failed to create fake .git directory: %v", err)
	}
	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		t.Fatalf("failed to create working directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "go.mod"), []byte("module "+sailwrightModulePath+"\n\ngo 1.26.0\n"), 0o644); err != nil {
		t.Fatalf("failed to create go.mod: %v", err)
	}

	projectDir, err := ensureProjectDir(workingDir, t.TempDir())
	if err != nil {
		t.Fatalf("ensureProjectDir returned error: %v", err)
	}
	if projectDir != repoRoot {
		t.Fatalf("expected repo root %q, got %q", repoRoot, projectDir)
	}
}

func TestSailwrightModulePathMatchesGoMod(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if !isSailwrightCheckout(repoRoot) {
		t.Fatalf("module path in %s does not match sailwrightModulePath %q", filepath.Join(repoRoot, "go.mod"), sailwrightModulePath)
	}
}

func TestEnsureProjectDir_IgnoresOtherGitRepos(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("failed to create fake .git directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "ansible.cfg"), []byte("[defaults]\n"), 0o644); err != nil {
		t.Fatalf("failed to create ansible.cfg: %v", err)
	}
	appDataDir := t.TempDir()

	projectDir, err := ensureProjectDir(repoRoot, appDataDir)
	if err != nil {
		t.Fatalf("ensureProjectDir returned error: %v", err)
	}
	wantProjectDir := filepath.Join(appDataDir, embeddedProjectDirName)
	if projectDir != wantProjectDir {
		t.Fatalf("expected embedded project dir %q, got %q", wantProjectDir, projectDir)
	}
}

func TestIsSailwrightCheckout(t *testing.T) {
	tests := []struct {
		name  string
		goMod string
		want  bool
	}{
		{name: "plain module line", goMod: "module " + sailwrightModulePath + "\n\ngo 1.26.0\n", want: true},
		{name: "quoted module path", goMod: "module \"" + sailwrightModulePath + "\"\n", want: true},
		{name: "CRLF line endings", goMod: "module " + sailwrightModulePath + "\r\n\r\ngo 1.26.0\r\n", want: true},
		{name: "leading comment and trailing comment", goMod: "// team fork\nmodule " + sailwrightModulePath + " // pinned\n", want: true},
		{name: "other module", goMod: "module example.com/team-setup\n", want: false},
		{name: "module path prefix", goMod: "module " + sailwrightModulePath + "/tools\n", want: false},
		{name: "no module line", goMod: "go 1.26.0\n", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(tt.goMod), 0o644); err != nil {
				t.Fatalf("failed to create go.mod: %v", err)
			}
			if got := isSailwrightCheckout(dir); got != tt.want {
				t.Fatalf("isSailwrightCheckout() = %v, want %v", got, tt.want)
			}
		})
	}

	if isSailwrightCheckout(t.TempDir()) {
		t.Fatal("expected a directory without go.mod not to be a Sailwright checkout")
	}
}

func TestFindSailwrightCheckout_AcceptsGitFile(t *testing.T) {
	// Worktrees and submodules have a .git file instead of a directory.
	repoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoRoot, ".git"), []byte("gitdir: /elsewhere/.git/worktrees/main\n"), 0o644); err != nil {
		t.Fatalf("failed to create .git file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "go.mod"), []byte("module "+sailwrightModulePath+"\n"), 0o644); err != nil {
		t.Fatalf("failed to create go.mod: %v", err)
	}

	if got := findSailwrightCheckout(repoRoot); got != repoRoot {
		t.Fatalf("expected worktree root %q, got %q", repoRoot, got)
	}
}

func TestFindSailwrightCheckout_StopsAtNearestRepo(t *testing.T) {
	// A non-Sailwright repo nested inside a checkout is not the checkout.
	checkoutRoot := t.TempDir()
	nestedRepo := filepath.Join(checkoutRoot, "roles", "team-role")
	for _, dir := range []string{filepath.Join(checkoutRoot, ".git"), filepath.Join(nestedRepo, ".git")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("failed to create fake .git directory: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(checkoutRoot, "go.mod"), []byte("module "+sailwrightModulePath+"\n"), 0o644); err != nil {
		t.Fatalf("failed to create go.mod: %v", err)
	}

	if got := findSailwrightCheckout(nestedRepo); got != "" {
		t.Fatalf("expected no checkout for nested repo, got %q", got)
	}
}

func TestEnsureProjectDir_IgnoresGoReposWithOtherModules(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("failed to create fake .git directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "go.mod"), []byte("module example.com/team-setup\n\ngo 1.26.0\n"), 0o644); err != nil {
		t.Fatalf("failed to create go.mod: %v", err)
	}
	appDataDir := t.TempDir()

	projectDir, err := ensureProjectDir(repoRoot, appDataDir)
	if err != nil {
		t.Fatalf("ensureProjectDir returned error: %v", err)
	}
	wantProjectDir := filepath.Join(appDataDir, embeddedProjectDirName)
	if projectDir != wantProjectDir {
		t.Fatalf("expected embedded project dir %q, got %q", wantProjectDir, projectDir)
	}
}

func TestEnsureProjectDir_FallsBackToEmbeddedProject(t *testing.T) {
	appDataDir := t.TempDir()

	projectDir, err := ensureProjectDir(t.TempDir(), appDataDir)
	if err != nil {
		t.Fatalf("ensureProjectDir returned error: %v", err)
	}

	wantProjectDir := filepath.Join(appDataDir, embeddedProjectDirName)
	if projectDir != wantProjectDir {
		t.Fatalf("expected embedded project dir %q, got %q", wantProjectDir, projectDir)
	}

	for _, relPath := range []string{
		"ansible.cfg",
		filepath.Join("playbooks", "setup.yml"),
		filepath.Join("scripts", "macos", "sailwright-install-dependencies.sh"),
		filepath.Join("scripts", "linux", "sailwright-install-dependencies.sh"),
		filepath.Join("scripts", "linux", "install-go-from-go-dev.sh"),
		filepath.Join("scripts", "windows", "sailwright-self-setup.ps1"),
		"go.mod",
		filepath.Join("deployments", "utm", "create-utm-vm.sh"),
	} {
		if _, err := os.Stat(filepath.Join(projectDir, relPath)); err != nil {
			t.Fatalf("expected extracted asset %q to exist: %v", relPath, err)
		}
	}
}

func TestEnsureEmbeddedProjectDir_SkipsSyncWhenManifestMatches(t *testing.T) {
	appDataDir := t.TempDir()

	projectDir, err := ensureEmbeddedProjectDir(appDataDir)
	if err != nil {
		t.Fatalf("ensureEmbeddedProjectDir returned error: %v", err)
	}

	managedFile := filepath.Join(projectDir, "ansible.cfg")
	if err := os.WriteFile(managedFile, []byte("locally modified"), 0o644); err != nil {
		t.Fatalf("failed to modify extracted asset: %v", err)
	}

	if _, err := ensureEmbeddedProjectDir(appDataDir); err != nil {
		t.Fatalf("second ensureEmbeddedProjectDir returned error: %v", err)
	}

	content, err := os.ReadFile(managedFile)
	if err != nil {
		t.Fatalf("failed to read managed file after second ensure: %v", err)
	}
	if string(content) != "locally modified" {
		t.Fatalf("expected matching manifest to skip sync, got %q", string(content))
	}
}

func TestEnsureEmbeddedProjectDir_MarksShellScriptsExecutableOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX execute bits are not portable on Windows")
	}

	projectDir, err := ensureEmbeddedProjectDir(t.TempDir())
	if err != nil {
		t.Fatalf("ensureEmbeddedProjectDir returned error: %v", err)
	}

	info, err := os.Stat(filepath.Join(projectDir, "scripts", "macos", "sailwright-install-dependencies.sh"))
	if err != nil {
		t.Fatalf("failed to stat extracted script: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("expected extracted shell script to be executable, got mode %o", info.Mode().Perm())
	}
}

func TestEnsureEmbeddedProjectDir_UsesManagedPermissionsForDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions are not portable on Windows")
	}

	projectDir, err := ensureEmbeddedProjectDir(t.TempDir())
	if err != nil {
		t.Fatalf("ensureEmbeddedProjectDir returned error: %v", err)
	}

	info, err := os.Stat(filepath.Join(projectDir, "scripts", "macos"))
	if err != nil {
		t.Fatalf("failed to stat extracted directory: %v", err)
	}
	if got := info.Mode().Perm(); got != managedDirPermission {
		t.Fatalf("expected extracted directory permissions %o, got %o", managedDirPermission, got)
	}
}

func TestEnsureEmbeddedProjectDir_PrunesStaleEntriesWhenManifestChanges(t *testing.T) {
	appDataDir := t.TempDir()

	projectDir, err := ensureEmbeddedProjectDir(appDataDir)
	if err != nil {
		t.Fatalf("ensureEmbeddedProjectDir returned error: %v", err)
	}

	staleFile := filepath.Join(projectDir, "scripts", "macos", ".venv", "keep.txt")
	if err := os.MkdirAll(filepath.Dir(staleFile), 0o755); err != nil {
		t.Fatalf("failed to create stale directory: %v", err)
	}
	if err := os.WriteFile(staleFile, []byte("stale"), 0o644); err != nil {
		t.Fatalf("failed to create stale file: %v", err)
	}

	conflictingPath := filepath.Join(projectDir, "ansible.cfg")
	if err := os.Remove(conflictingPath); err != nil {
		t.Fatalf("failed to remove embedded file for conflict setup: %v", err)
	}
	if err := os.MkdirAll(conflictingPath, 0o755); err != nil {
		t.Fatalf("failed to create conflicting directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(conflictingPath, "nested.txt"), []byte("conflict"), 0o644); err != nil {
		t.Fatalf("failed to create nested conflict file: %v", err)
	}

	manifestPath := filepath.Join(projectDir, embeddedProjectManifestFile)
	if err := os.WriteFile(manifestPath, []byte("stale-manifest\n"), 0o600); err != nil {
		t.Fatalf("failed to write stale manifest: %v", err)
	}

	if _, err := ensureEmbeddedProjectDir(appDataDir); err != nil {
		t.Fatalf("second ensureEmbeddedProjectDir returned error: %v", err)
	}

	if _, err := os.Stat(staleFile); !os.IsNotExist(err) {
		t.Fatalf("expected stale file to be pruned, got err=%v", err)
	}

	info, err := os.Stat(conflictingPath)
	if err != nil {
		t.Fatalf("expected conflicting path to be restored as file: %v", err)
	}
	if info.IsDir() {
		t.Fatalf("expected conflicting path to be restored as file")
	}
}

// scriptRepoPathPattern matches repo files that a shell script reaches through
// ${project_root}/... or ${script_dir}/... (the bare $var spelling as well). A
// trailing "$" marks a path that is concatenated with a variable and cannot be
// resolved statically.
var scriptRepoPathPattern = regexp.MustCompile(`\$\{?(project_root|script_dir|SCRIPT_DIR)\}?((?:/[A-Za-z0-9_.-]+)+/?)(\$?)`)

// scriptRepoReference is a repo file that a shell script references, resolved
// to a path relative to the project root.
type scriptRepoReference struct {
	match  string
	target string
}

// scriptRepoReferences returns the repo files that the script at scriptPath
// references through scriptRepoPathPattern. project_root resolves against the
// project root and script_dir/SCRIPT_DIR against the script's directory, so a
// script must only use project_root for the repo root (a parameter carrying
// the root counts). Paths concatenated with another variable, the root itself
// and paths outside the root are skipped.
func scriptRepoReferences(scriptPath string, content string) []scriptRepoReference {
	var references []scriptRepoReference
	for _, match := range scriptRepoPathPattern.FindAllStringSubmatch(content, -1) {
		variable, relPath, concatenated := match[1], match[2], match[3]
		if concatenated != "" {
			continue
		}

		base := "."
		if variable != "project_root" {
			base = path.Dir(scriptPath)
		}
		target := path.Join(base, strings.TrimPrefix(relPath, "/"))
		if target == "." || target == ".." || strings.HasPrefix(target, "../") {
			continue
		}

		references = append(references, scriptRepoReference{match: match[0], target: target})
	}
	return references
}

func TestScriptRepoReferences(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		want    []string
	}{
		{name: "braced project_root", content: `cat "${project_root}/go.mod"`, want: []string{"go.mod"}},
		{name: "bare project_root", content: `cat $project_root/go.mod`, want: []string{"go.mod"}},
		{name: "script_dir is relative to the script", content: `bash "${script_dir}/helper.sh"`, want: []string{"scripts/linux/helper.sh"}},
		{name: "SCRIPT_DIR is relative to the script", content: `bash "$SCRIPT_DIR/helper.sh"`, want: []string{"scripts/linux/helper.sh"}},
		{name: "parent segments resolve", content: `ls "${script_dir}/../macos/x.sh"`, want: []string{"scripts/macos/x.sh"}},
		{name: "trailing slash names a directory", content: `ls "${project_root}/playbooks/"`, want: []string{"playbooks"}},
		{name: "several references", content: "a=${project_root}/a\nb=${script_dir}/b", want: []string{"a", "scripts/linux/b"}},
		{name: "concatenated with a variable", content: `cat "${project_root}/cache/${name}"`, want: nil},
		{name: "project root itself", content: `cd "${script_dir}/../.."`, want: nil},
		{name: "outside the project root", content: `cd "${script_dir}/../../../elsewhere"`, want: nil},
		{name: "other variables", content: `cat "${repo_root}/go.mod" "${HOME}/.bashrc"`, want: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, reference := range scriptRepoReferences("scripts/linux/install.sh", tc.content) {
				got = append(got, reference.target)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

// Release binaries run the embedded scripts outside a git checkout, so every
// repo file they reference must be embedded as well. Only the forms matched by
// scriptRepoReferences are checked; PowerShell scripts are not scanned.
func TestEmbeddedShellScriptRepoReferencesAreEmbedded(t *testing.T) {
	assets := runtimeassets.FS()

	err := fs.WalkDir(assets, ".", func(scriptPath string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(scriptPath, ".sh") {
			return err
		}

		content, err := fs.ReadFile(assets, scriptPath)
		if err != nil {
			return err
		}

		for _, reference := range scriptRepoReferences(scriptPath, string(content)) {
			if _, err := fs.Stat(assets, reference.target); err != nil {
				t.Errorf("%s references %q, which is not embedded in the release binary (add it to runtimeassets.go)", scriptPath, reference.match)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded assets: %v", err)
	}
}

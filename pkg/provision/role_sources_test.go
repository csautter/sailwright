package provision

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

func TestResolveAnsibleRolePathsReturnsDefaultWhenConfigMissing(t *testing.T) {
	projectDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectDir, "roles"), 0o755); err != nil {
		t.Fatalf("failed to create default roles directory: %v", err)
	}
	withIsolatedAlchemyDirectories(t, projectDir)

	rolePaths, err := resolveAnsibleRolePaths(projectDir)
	if err != nil {
		t.Fatalf("resolveAnsibleRolePaths returned error: %v", err)
	}
	if got, want := strings.Join(rolePaths, ";"), filepath.Join(projectDir, "roles"); got != want {
		t.Fatalf("expected default role path %q, got %q", want, got)
	}
}

func TestResolveAnsibleRolePathsRejectsMissingExplicitConfig(t *testing.T) {
	projectDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectDir, "roles"), 0o755); err != nil {
		t.Fatalf("failed to create default roles directory: %v", err)
	}
	withIsolatedAlchemyDirectories(t, projectDir)
	missingConfig := filepath.Join(t.TempDir(), "missing-role-sources.yml")
	t.Setenv(ansibleRoleSourcesConfigEnvVar, missingConfig)

	if rolePaths, err := resolveAnsibleRolePaths(projectDir); err == nil {
		t.Fatalf("expected error for missing explicit config, got role paths %q", rolePaths)
	} else if !strings.Contains(err.Error(), ansibleRoleSourcesConfigEnvVar) || !strings.Contains(err.Error(), missingConfig) {
		t.Fatalf("expected error to name %s and %q, got %v", ansibleRoleSourcesConfigEnvVar, missingConfig, err)
	}

	if _, _, err := resolveConfiguredProvisionPlaybookPath(projectDir); err == nil {
		t.Fatal("expected playbook resolution to reject missing explicit config")
	}
}

func TestResolveAnsibleRolePathsLayersLocalSourcesBeforeDefault(t *testing.T) {
	projectDir := t.TempDir()
	dirs := withIsolatedAlchemyDirectories(t, projectDir)

	for _, relPath := range []string{
		filepath.Join("roles-from-config-one"),
		filepath.Join("nested", "roles-from-config-two"),
	} {
		if err := os.MkdirAll(filepath.Join(dirs.ConfigDir, relPath), 0o755); err != nil {
			t.Fatalf("failed to create local role source %q: %v", relPath, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(projectDir, "roles"), 0o755); err != nil {
		t.Fatalf("failed to create default roles directory: %v", err)
	}

	config := strings.Join([]string{
		"sources:",
		"  - name: specific",
		"    type: local",
		"    path: roles-from-config-one",
		"  - name: base",
		"    type: local",
		"    path: nested/roles-from-config-two",
		"",
	}, "\n")
	writeRoleSourcesConfig(t, dirs.ConfigDir, config)

	rolePaths, err := resolveAnsibleRolePaths(projectDir)
	if err != nil {
		t.Fatalf("resolveAnsibleRolePaths returned error: %v", err)
	}

	want := strings.Join([]string{
		filepath.Join(dirs.ConfigDir, "roles-from-config-one"),
		filepath.Join(dirs.ConfigDir, "nested", "roles-from-config-two"),
		filepath.Join(projectDir, "roles"),
	}, ";")
	if got := strings.Join(rolePaths, ";"); got != want {
		t.Fatalf("expected layered role paths %q, got %q", want, got)
	}
}

func TestResolveAnsibleRolePathsCanDisableDefaultRoles(t *testing.T) {
	projectDir := t.TempDir()
	dirs := withIsolatedAlchemyDirectories(t, projectDir)
	roleSource := filepath.Join(dirs.ConfigDir, "roles-only")
	if err := os.MkdirAll(roleSource, 0o755); err != nil {
		t.Fatalf("failed to create local role source: %v", err)
	}

	config := strings.Join([]string{
		"include_default_roles: false",
		"sources:",
		"  - type: local",
		"    path: roles-only",
		"",
	}, "\n")
	writeRoleSourcesConfig(t, dirs.ConfigDir, config)

	rolePaths, err := resolveAnsibleRolePaths(projectDir)
	if err != nil {
		t.Fatalf("resolveAnsibleRolePaths returned error: %v", err)
	}
	if got := strings.Join(rolePaths, ";"); got != roleSource {
		t.Fatalf("expected only configured role source %q, got %q", roleSource, got)
	}
}

func TestResolveProvisionPlaybookPathUsesRoleSourceConfigDefault(t *testing.T) {
	projectDir := t.TempDir()
	dirs := withIsolatedAlchemyDirectories(t, projectDir)
	writeProjectFile(t, projectDir, filepath.Join("playbooks", "role-sources-test.yml"), "---\n")
	writeRoleSourcesConfig(t, dirs.ConfigDir, strings.Join([]string{
		"playbook: ./playbooks/role-sources-test.yml",
		"sources: []",
		"",
	}, "\n"))

	playbookPath, err := resolveProvisionPlaybookPath(ProvisionOptions{
		PlaybookPath: defaultProvisionPlaybook,
	})
	if err != nil {
		t.Fatalf("resolveProvisionPlaybookPath returned error: %v", err)
	}

	want := filepath.Join(projectDir, "playbooks", "role-sources-test.yml")
	if playbookPath != want {
		t.Fatalf("expected configured playbook path %q, got %q", want, playbookPath)
	}
}

func TestResolveProvisionPlaybookPathUsesRoleSourcesTestFallback(t *testing.T) {
	projectDir := t.TempDir()
	withIsolatedAlchemyDirectories(t, projectDir)

	playbookPath, err := resolveProvisionPlaybookPath(ProvisionOptions{})
	if err != nil {
		t.Fatalf("resolveProvisionPlaybookPath returned error: %v", err)
	}
	if playbookPath != defaultProvisionPlaybook {
		t.Fatalf("expected built-in default playbook %q, got %q", defaultProvisionPlaybook, playbookPath)
	}
}

func TestResolveProvisionPlaybookPathUsesLocalPlaybookSource(t *testing.T) {
	projectDir := t.TempDir()
	dirs := withIsolatedAlchemyDirectories(t, projectDir)
	externalPlaybooksDir := filepath.Join(dirs.ConfigDir, "external-playbooks")
	writeProjectFile(t, externalPlaybooksDir, "custom.yml", "---\n")
	writeRoleSourcesConfig(t, dirs.ConfigDir, strings.Join([]string{
		"playbook: custom.yml",
		"include_default_playbooks: false",
		"playbook_sources:",
		"  - name: external-playbooks",
		"    type: local",
		"    path: external-playbooks",
		"",
	}, "\n"))

	playbookPath, err := resolveProvisionPlaybookPath(ProvisionOptions{
		PlaybookPath: defaultProvisionPlaybook,
	})
	if err != nil {
		t.Fatalf("resolveProvisionPlaybookPath returned error: %v", err)
	}

	want := filepath.Join(externalPlaybooksDir, "custom.yml")
	if playbookPath != want {
		t.Fatalf("expected external playbook path %q, got %q", want, playbookPath)
	}
}

func TestResolveProvisionPlaybookPathUsesPlaybooksPathFromRoleSource(t *testing.T) {
	projectDir := t.TempDir()
	dirs := withIsolatedAlchemyDirectories(t, projectDir)
	sourceRoot := filepath.Join(dirs.ConfigDir, "stack")
	writeProjectFile(t, sourceRoot, filepath.Join("roles", "placeholder", "tasks", "main.yml"), "---\n")
	writeProjectFile(t, sourceRoot, filepath.Join("playbooks", "stack.yml"), "---\n")
	writeRoleSourcesConfig(t, dirs.ConfigDir, strings.Join([]string{
		"playbook: stack.yml",
		"include_default_playbooks: false",
		"sources:",
		"  - name: stack",
		"    type: local",
		"    path: stack",
		"    roles_path: roles",
		"    playbooks_path: playbooks",
		"",
	}, "\n"))

	playbookPath, err := resolveProvisionPlaybookPath(ProvisionOptions{
		PlaybookPath: defaultProvisionPlaybook,
	})
	if err != nil {
		t.Fatalf("resolveProvisionPlaybookPath returned error: %v", err)
	}

	want := filepath.Join(sourceRoot, "playbooks", "stack.yml")
	if playbookPath != want {
		t.Fatalf("expected playbook from source playbooks_path %q, got %q", want, playbookPath)
	}
}

func TestResolveProvisionPlaybookPathClonesGitPlaybookSource(t *testing.T) {
	projectDir := t.TempDir()
	dirs := withIsolatedAlchemyDirectories(t, projectDir)
	writeRoleSourcesConfig(t, dirs.ConfigDir, strings.Join([]string{
		"playbook: custom.yml",
		"include_default_playbooks: false",
		"playbook_sources:",
		"  - name: remote-playbooks",
		"    type: git",
		"    url: https://example.test/dev-alchemy-playbooks.git",
		"    ref: main",
		"    playbooks_path: playbooks",
		"    pull: false",
		"",
	}, "\n"))

	calls := fakeRoleSourceGit(t, func(_ string, _ time.Duration, _ string, args []string) (string, error) {
		if len(args) > 0 && args[0] == "clone" {
			checkoutDir := args[len(args)-1]
			if err := os.MkdirAll(filepath.Join(checkoutDir, ".git"), 0o755); err != nil {
				t.Fatalf("failed to create fake git checkout: %v", err)
			}
			writeProjectFile(t, checkoutDir, filepath.Join("playbooks", "custom.yml"), "---\n")
		}
		return "", nil
	})

	playbookPath, err := resolveProvisionPlaybookPath(ProvisionOptions{
		PlaybookPath: defaultProvisionPlaybook,
	})
	if err != nil {
		t.Fatalf("resolveProvisionPlaybookPath returned error: %v", err)
	}

	checkoutDir := filepath.Join(dirs.CacheDir, ansiblePlaybookSourcesCacheDir, "remote-playbooks")
	want := filepath.Join(checkoutDir, "playbooks", "custom.yml")
	if playbookPath != want {
		t.Fatalf("expected git playbook path %q, got %q", want, playbookPath)
	}
	if !gitCallsContain(*calls, "|clone https://example.test/dev-alchemy-playbooks.git "+checkoutDir) {
		t.Fatalf("expected git clone call, got %v", *calls)
	}
	if !gitCallsContain(*calls, checkoutDir+"|checkout main") {
		t.Fatalf("expected checkout of configured ref, got %v", *calls)
	}
}

func TestResolveProvisionPlaybookPathRejectsMissingConfiguredPlaybookSource(t *testing.T) {
	projectDir := t.TempDir()
	dirs := withIsolatedAlchemyDirectories(t, projectDir)
	writeRoleSourcesConfig(t, dirs.ConfigDir, strings.Join([]string{
		"playbook: custom.yml",
		"include_default_playbooks: false",
		"playbook_sources: []",
		"",
	}, "\n"))

	_, err := resolveProvisionPlaybookPath(ProvisionOptions{
		PlaybookPath: defaultProvisionPlaybook,
	})
	if err == nil {
		t.Fatal("expected missing configured playbook source to fail")
	}
	if !strings.Contains(err.Error(), "include_default_playbooks") {
		t.Fatalf("expected include_default_playbooks guidance, got: %v", err)
	}
}

func TestResolveProvisionPlaybookPathExplicitFlagOverridesRoleSourceConfig(t *testing.T) {
	projectDir := t.TempDir()
	dirs := withIsolatedAlchemyDirectories(t, projectDir)
	writeRoleSourcesConfig(t, dirs.ConfigDir, strings.Join([]string{
		"playbook: ./playbooks/role-sources-test.yml",
		"sources: []",
		"",
	}, "\n"))

	playbookPath, err := resolveProvisionPlaybookPath(ProvisionOptions{
		PlaybookPath:         bundledSetupProvisionPlaybook,
		PlaybookPathExplicit: true,
	})
	if err != nil {
		t.Fatalf("resolveProvisionPlaybookPath returned error: %v", err)
	}
	if playbookPath != bundledSetupProvisionPlaybook {
		t.Fatalf("expected explicit CLI playbook %q to win, got %q", bundledSetupProvisionPlaybook, playbookPath)
	}
}

func TestResolveProvisionPlaybookPathRejectsConflictingConfigAliases(t *testing.T) {
	projectDir := t.TempDir()
	dirs := withIsolatedAlchemyDirectories(t, projectDir)
	writeRoleSourcesConfig(t, dirs.ConfigDir, strings.Join([]string{
		"playbook: ./playbooks/one.yml",
		"playbook_path: ./playbooks/two.yml",
		"sources: []",
		"",
	}, "\n"))

	_, err := resolveProvisionPlaybookPath(ProvisionOptions{})
	if err == nil {
		t.Fatal("expected conflicting playbook aliases to fail")
	}
	if !strings.Contains(err.Error(), "sets both playbook and playbook_path") {
		t.Fatalf("expected alias conflict error, got: %v", err)
	}
}

func TestResolveAnsibleRolePathsClonesGitSourceAndHonorsPullFalse(t *testing.T) {
	projectDir := t.TempDir()
	dirs := withIsolatedAlchemyDirectories(t, projectDir)
	writeRoleSourcesConfig(t, dirs.ConfigDir, strings.Join([]string{
		"include_default_roles: false",
		"sources:",
		"  - name: public-base",
		"    type: git",
		"    url: https://example.test/dev-alchemy-roles.git",
		"    ref: main",
		"    roles_path: roles",
		"    pull: false",
		"",
	}, "\n"))

	calls := fakeRoleSourceGit(t, func(_ string, _ time.Duration, _ string, args []string) (string, error) {
		if len(args) > 0 && args[0] == "clone" {
			checkoutDir := args[len(args)-1]
			if err := os.MkdirAll(filepath.Join(checkoutDir, ".git"), 0o755); err != nil {
				t.Fatalf("failed to create fake git checkout: %v", err)
			}
			if err := os.MkdirAll(filepath.Join(checkoutDir, "roles"), 0o755); err != nil {
				t.Fatalf("failed to create fake git roles path: %v", err)
			}
		}
		return "", nil
	})

	rolePaths, err := resolveAnsibleRolePaths(projectDir)
	if err != nil {
		t.Fatalf("resolveAnsibleRolePaths returned error: %v", err)
	}

	wantRolePath := filepath.Join(dirs.CacheDir, ansibleRoleSourcesCacheDir, "public-base", "roles")
	if got := strings.Join(rolePaths, ";"); got != wantRolePath {
		t.Fatalf("expected git role path %q, got %q", wantRolePath, got)
	}
	if !gitCallsContain(*calls, "|clone https://example.test/dev-alchemy-roles.git "+filepath.Join(dirs.CacheDir, ansibleRoleSourcesCacheDir, "public-base")) {
		t.Fatalf("expected git clone call, got %v", *calls)
	}
	if !gitCallsContain(*calls, filepath.Join(dirs.CacheDir, ansibleRoleSourcesCacheDir, "public-base")+"|checkout main") {
		t.Fatalf("expected checkout of configured ref, got %v", *calls)
	}
	if gitCallsContain(*calls, "pull") || gitCallsContain(*calls, "fetch") {
		t.Fatalf("did not expect pull/fetch when pull is false for a fresh clone, got %v", *calls)
	}
}

func TestResolveAnsibleRolePathsUpdatesExistingGitBranch(t *testing.T) {
	projectDir := t.TempDir()
	dirs := withIsolatedAlchemyDirectories(t, projectDir)
	checkoutDir := filepath.Join(dirs.CacheDir, ansibleRoleSourcesCacheDir, "public-base")
	for _, path := range []string{filepath.Join(checkoutDir, ".git"), filepath.Join(checkoutDir, "roles")} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("failed to create fake checkout path %q: %v", path, err)
		}
	}
	writeRoleSourcesConfig(t, dirs.ConfigDir, strings.Join([]string{
		"include_default_roles: false",
		"sources:",
		"  - name: public-base",
		"    type: git",
		"    url: https://example.test/dev-alchemy-roles.git",
		"    ref: main",
		"    roles_path: roles",
		"",
	}, "\n"))

	calls := fakeRoleSourceGit(t, nil)

	if _, err := resolveAnsibleRolePaths(projectDir); err != nil {
		t.Fatalf("resolveAnsibleRolePaths returned error: %v", err)
	}

	for _, want := range []string{
		checkoutDir + "|fetch --tags --prune origin",
		checkoutDir + "|rev-parse --verify --quiet refs/remotes/origin/main",
		checkoutDir + "|rev-parse --verify --quiet refs/heads/main",
		checkoutDir + "|checkout main",
		checkoutDir + "|pull --ff-only origin main",
	} {
		if !gitCallsContain(*calls, want) {
			t.Fatalf("expected git call %q, got %v", want, *calls)
		}
	}
}

func TestRunGitForRoleSourceRedactsURLCredentialsInErrors(t *testing.T) {
	fakeRoleSourceGit(t, func(_ string, _ time.Duration, _ string, _ []string) (string, error) {
		return "", errors.New("clone failed")
	})

	err := runGitForRoleSource("", "clone", "https://token@example.test/org/private.git", "/tmp/checkout")
	if err == nil {
		t.Fatal("expected git command error")
	}
	if strings.Contains(err.Error(), "token") {
		t.Fatalf("expected credential to be redacted, got: %v", err)
	}
	if !strings.Contains(err.Error(), "https://***REDACTED***@example.test/org/private.git") {
		t.Fatalf("expected redacted URL in error, got: %v", err)
	}
}

func TestResolveAnsibleRolePathsRejectsEscapingGitRolesPath(t *testing.T) {
	projectDir := t.TempDir()
	dirs := withIsolatedAlchemyDirectories(t, projectDir)
	checkoutDir := filepath.Join(dirs.CacheDir, ansibleRoleSourcesCacheDir, "bad-source")
	if err := os.MkdirAll(filepath.Join(checkoutDir, ".git"), 0o755); err != nil {
		t.Fatalf("failed to create fake checkout: %v", err)
	}
	writeRoleSourcesConfig(t, dirs.ConfigDir, strings.Join([]string{
		"include_default_roles: false",
		"sources:",
		"  - name: bad-source",
		"    type: git",
		"    url: https://example.test/dev-alchemy-roles.git",
		"    roles_path: ../outside",
		"    pull: false",
		"",
	}, "\n"))
	fakeRoleSourceGit(t, func(_ string, _ time.Duration, _ string, _ []string) (string, error) {
		return "", nil
	})

	_, err := resolveAnsibleRolePaths(projectDir)
	if err == nil {
		t.Fatal("expected escaping roles_path to fail")
	}
	if !strings.Contains(err.Error(), "roles_path must stay inside") {
		t.Fatalf("expected roles_path validation error, got: %v", err)
	}
}

func TestAnsibleRuntimeEnvForProjectIncludesResolvedRolesPath(t *testing.T) {
	projectDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectDir, "roles"), 0o755); err != nil {
		t.Fatalf("failed to create default roles directory: %v", err)
	}
	withIsolatedAlchemyDirectories(t, projectDir)

	entries, err := ansibleRuntimeEnvForProject(projectDir)
	if err != nil {
		t.Fatalf("ansibleRuntimeEnvForProject returned error: %v", err)
	}
	expectedRolesPath := filepath.Join(projectDir, "roles")
	if runtimeGoos() == "windows" {
		expectedRolesPath, err = windowsPathToCygwinPath(expectedRolesPath)
		if err != nil {
			t.Fatalf("failed to convert expected role path for cygwin: %v", err)
		}
	}
	combined := strings.Join(entries, ";")
	for _, required := range []string{
		"ANSIBLE_FORCE_COLOR=true",
		"ANSIBLE_ROLES_PATH=" + expectedRolesPath,
	} {
		if !strings.Contains(combined, required) {
			t.Fatalf("expected env %q in %q", required, combined)
		}
	}
}

func TestAnsibleRuntimeEnvForBundledSetupPlaybookIncludesDefaultRoleFallback(t *testing.T) {
	projectDir := t.TempDir()
	dirs := withIsolatedAlchemyDirectories(t, projectDir)
	configuredRolePath := filepath.Join(dirs.ConfigDir, "roles-from-config")
	for _, path := range []string{
		configuredRolePath,
		filepath.Join(projectDir, "roles"),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("failed to create role path %q: %v", path, err)
		}
	}
	writeRoleSourcesConfig(t, dirs.ConfigDir, strings.Join([]string{
		"include_default_roles: false",
		"sources:",
		"  - type: local",
		"    path: roles-from-config",
		"",
	}, "\n"))

	entries, err := ansibleRuntimeEnvForProjectPlaybook(projectDir, "./playbooks/setup.yml")
	if err != nil {
		t.Fatalf("ansibleRuntimeEnvForProjectPlaybook returned error: %v", err)
	}

	expectedRolesPath, err := ansibleRolesPathEnvValue([]string{
		configuredRolePath,
		filepath.Join(projectDir, "roles"),
	})
	if err != nil {
		t.Fatalf("failed to build expected roles path: %v", err)
	}
	combined := strings.Join(entries, ";")
	if !strings.Contains(combined, "ANSIBLE_ROLES_PATH="+expectedRolesPath) {
		t.Fatalf("expected setup playbook role fallback %q in %q", expectedRolesPath, combined)
	}
}

func TestAnsibleRuntimeEnvForRoleSourcesTestPlaybookIncludesTestRoles(t *testing.T) {
	projectDir := t.TempDir()
	for _, path := range []string{
		filepath.Join(projectDir, "roles"),
		filepath.Join(projectDir, "roles_test_2"),
		filepath.Join(projectDir, "roles_test_1"),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("failed to create role path %q: %v", path, err)
		}
	}
	withIsolatedAlchemyDirectories(t, projectDir)

	entries, err := ansibleRuntimeEnvForProjectPlaybook(projectDir, "./playbooks/role-sources-test.yml")
	if err != nil {
		t.Fatalf("ansibleRuntimeEnvForProjectPlaybook returned error: %v", err)
	}

	expectedRolesPath, err := ansibleRolesPathEnvValue([]string{
		filepath.Join(projectDir, "roles"),
		filepath.Join(projectDir, "roles_test_2"),
		filepath.Join(projectDir, "roles_test_1"),
	})
	if err != nil {
		t.Fatalf("failed to build expected roles path: %v", err)
	}
	combined := strings.Join(entries, ";")
	if !strings.Contains(combined, "ANSIBLE_ROLES_PATH="+expectedRolesPath) {
		t.Fatalf("expected role-source test paths %q in %q", expectedRolesPath, combined)
	}
}

func TestAnsibleRolesPathEnvValueConvertsWindowsPathsForCygwin(t *testing.T) {
	previousGOOS := runtimeGOOS
	runtimeGOOS = "windows"
	t.Cleanup(func() {
		runtimeGOOS = previousGOOS
	})

	value, err := ansibleRolesPathEnvValue([]string{
		`C:\Users\tester\roles`,
		`/cygdrive/d/roles`,
	})
	if err != nil {
		t.Fatalf("ansibleRolesPathEnvValue returned error: %v", err)
	}
	if value != "/cygdrive/c/Users/tester/roles:/cygdrive/d/roles" {
		t.Fatalf("expected cygwin role path list, got %q", value)
	}
}

func withIsolatedAlchemyDirectories(t *testing.T, projectDir string) *alchemy_build.Directories {
	t.Helper()
	t.Setenv(ansibleRoleSourcesConfigEnvVar, "")

	dirs := alchemy_build.GetDirectoriesInstance()
	previous := *dirs
	t.Cleanup(func() {
		*dirs = previous
	})

	appDataDir := t.TempDir()
	dirs.WorkingDir = projectDir
	dirs.ProjectDir = projectDir
	dirs.AppDataDir = appDataDir
	dirs.ConfigDir = filepath.Join(t.TempDir(), "config")
	dirs.CacheDir = filepath.Join(appDataDir, "cache")
	dirs.VagrantDir = filepath.Join(appDataDir, ".vagrant")
	dirs.PackerCacheDir = filepath.Join(appDataDir, "packer_cache")
	if err := os.MkdirAll(dirs.ConfigDir, 0o700); err != nil {
		t.Fatalf("failed to create isolated config dir: %v", err)
	}
	if err := os.MkdirAll(dirs.CacheDir, 0o700); err != nil {
		t.Fatalf("failed to create isolated cache dir: %v", err)
	}

	return dirs
}

func writeRoleSourcesConfig(t *testing.T, configDir string, content string) {
	t.Helper()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	configPath := filepath.Join(configDir, ansibleRoleSourcesConfigFile)
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write role sources config: %v", err)
	}
}

func writeProjectFile(t *testing.T, root string, relPath string, content string) {
	t.Helper()
	targetPath := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatalf("failed to create fixture directory for %q: %v", relPath, err)
	}
	if err := os.WriteFile(targetPath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write fixture file %q: %v", relPath, err)
	}
}

func fakeRoleSourceGit(
	t *testing.T,
	handle func(workingDir string, timeout time.Duration, executable string, args []string) (string, error),
) *[]string {
	t.Helper()

	previousRunner := runRoleSourceGitCommand
	previousLookPath := lookPathProvisionCommand
	calls := []string{}
	runRoleSourceGitCommand = func(workingDir string, timeout time.Duration, executable string, args []string) (string, error) {
		calls = append(calls, workingDir+"|"+strings.Join(args, " "))
		if handle != nil {
			return handle(workingDir, timeout, executable, args)
		}
		return "", nil
	}
	lookPathProvisionCommand = func(name string) (string, error) {
		if name != "git" {
			return "", errors.New("unexpected executable lookup")
		}
		return "/usr/bin/git", nil
	}
	t.Cleanup(func() {
		runRoleSourceGitCommand = previousRunner
		lookPathProvisionCommand = previousLookPath
	})

	return &calls
}

func gitCallsContain(calls []string, want string) bool {
	for _, call := range calls {
		if strings.Contains(call, want) {
			return true
		}
	}
	return false
}

package provision

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	"gopkg.in/yaml.v3"
)

const (
	ansibleRoleSourcesConfigEnvVar   = "DEV_ALCHEMY_ROLE_SOURCES_CONFIG"
	ansibleRoleSourcesConfigFile     = "ansible-role-sources.yml"
	ansibleRoleSourcesCacheDir       = "ansible-role-sources"
	ansiblePlaybookSourcesCacheDir   = "ansible-playbook-sources"
	ansibleRoleSourceGitCommandTime  = 10 * time.Minute
	defaultAnsibleSourceSubdirectory = "."
)

type ansibleRoleSourcesConfig struct {
	IncludeDefaultRoles     *bool                     `json:"include_default_roles" yaml:"include_default_roles"`
	IncludeDefaultPlaybooks *bool                     `json:"include_default_playbooks" yaml:"include_default_playbooks"`
	Playbook                string                    `json:"playbook" yaml:"playbook"`
	PlaybookPath            string                    `json:"playbook_path" yaml:"playbook_path"`
	Sources                 []ansibleRoleSourceConfig `json:"sources" yaml:"sources"`
	PlaybookSources         []ansibleRoleSourceConfig `json:"playbook_sources" yaml:"playbook_sources"`
}

type ansibleRoleSourceConfig struct {
	Name          string `json:"name" yaml:"name"`
	Type          string `json:"type" yaml:"type"`
	Path          string `json:"path" yaml:"path"`
	URL           string `json:"url" yaml:"url"`
	Ref           string `json:"ref" yaml:"ref"`
	RolesPath     string `json:"roles_path" yaml:"roles_path"`
	PlaybooksPath string `json:"playbooks_path" yaml:"playbooks_path"`
	Update        string `json:"update" yaml:"update"`
	Pull          *bool  `json:"pull" yaml:"pull"`
}

type roleSourceGitCommandRunner func(workingDir string, timeout time.Duration, executable string, args []string) (string, error)

var runRoleSourceGitCommand roleSourceGitCommandRunner = runCommandWithCombinedOutput
var runtimeGOOS = runtime.GOOS

// ansibleRoleSourcesConfigPath returns the effective role-source config path
// and, when it was selected explicitly (--config or
// DEV_ALCHEMY_ROLE_SOURCES_CONFIG), the name of that selector.
func ansibleRoleSourcesConfigPath(_ *alchemy_build.Directories) (string, string) {
	path, origin := ConfigLocation()
	switch origin {
	case "flag":
		return path, "--config"
	case ansibleRoleSourcesConfigEnvVar:
		return path, ansibleRoleSourcesConfigEnvVar
	}
	return path, ""
}

func resolveAnsibleRolePaths(projectDir string) ([]string, error) {
	return resolveAnsibleRolePathsWithOptions(projectDir, false)
}

func resolveAnsibleRolePathsWithDefaultFallback(projectDir string) ([]string, error) {
	return resolveAnsibleRolePathsWithOptions(projectDir, true)
}

func resolveAnsibleRolePathsWithOptions(projectDir string, forceDefaultRoles bool) ([]string, error) {
	config, configPath, exists, err := loadCurrentAnsibleRoleSourcesConfig()
	if err != nil {
		return nil, err
	}
	if !exists {
		return []string{defaultAnsibleRolePath(projectDir)}, nil
	}

	directories := alchemy_build.GetDirectoriesInstance()
	cacheRoot := directories.CachePath(ansibleRoleSourcesCacheDir)
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create ansible role source cache %q: %w", cacheRoot, err)
	}

	configDir := filepath.Dir(configPath)
	rolePaths := make([]string, 0, len(config.Sources)+1)
	usedGitCacheNames := map[string]struct{}{}
	for index, source := range config.Sources {
		rolePath, err := resolveAnsibleRoleSource(source, index, configDir, cacheRoot, usedGitCacheNames)
		if err != nil {
			return nil, err
		}
		rolePaths = appendUniquePath(rolePaths, rolePath)
	}

	if includeDefaultAnsibleRoles(config) || forceDefaultRoles {
		rolePaths = appendUniquePath(rolePaths, defaultAnsibleRolePath(projectDir))
	}
	if len(rolePaths) == 0 {
		return nil, fmt.Errorf("%q must configure at least one role source or enable include_default_roles", configPath)
	}

	return rolePaths, nil
}

func resolveConfiguredProvisionPlaybookPath(projectDir string) (string, bool, error) {
	config, configPath, exists, err := loadCurrentAnsibleRoleSourcesConfig()
	if err != nil {
		return "", false, err
	}
	if !exists {
		return "", false, nil
	}

	playbookPath, ok, err := configuredProvisionPlaybookPath(config, configPath)
	if err != nil {
		return "", false, err
	}
	if !ok {
		if !hasConfiguredPlaybookSources(config) {
			return "", false, nil
		}
		playbookPath = defaultProvisionPlaybook
	}

	resolvedPath, err := resolvePlaybookPathFromConfiguredSources(projectDir, config, configPath, playbookPath)
	return resolvedPath, true, err
}

func configuredProvisionPlaybookPath(config ansibleRoleSourcesConfig, configPath string) (string, bool, error) {
	playbook := strings.TrimSpace(config.Playbook)
	playbookPath := strings.TrimSpace(config.PlaybookPath)
	if playbook != "" && playbookPath != "" && playbook != playbookPath {
		return "", false, fmt.Errorf("%q sets both playbook and playbook_path to different values", configPath)
	}
	if playbook != "" {
		return playbook, true, nil
	}
	if playbookPath != "" {
		return playbookPath, true, nil
	}
	return "", false, nil
}

func hasConfiguredPlaybookSources(config ansibleRoleSourcesConfig) bool {
	if config.IncludeDefaultPlaybooks != nil {
		return true
	}
	if len(config.PlaybookSources) > 0 {
		return true
	}
	for _, source := range config.Sources {
		if strings.TrimSpace(source.PlaybooksPath) != "" {
			return true
		}
	}
	return false
}

func loadCurrentAnsibleRoleSourcesConfig() (ansibleRoleSourcesConfig, string, bool, error) {
	directories := alchemy_build.GetDirectoriesInstance()
	configPath, explicitSelector := ansibleRoleSourcesConfigPath(directories)

	config, exists, err := loadAnsibleRoleSourcesConfig(configPath)
	if err == nil && !exists && explicitSelector != "" {
		// An explicitly selected config must not silently fall back to the bundled roles.
		return ansibleRoleSourcesConfig{}, configPath, false, fmt.Errorf("%s points at %q, which does not exist", explicitSelector, configPath)
	}
	return config, configPath, exists, err
}

func loadAnsibleRoleSourcesConfig(configPath string) (ansibleRoleSourcesConfig, bool, error) {
	content, err := os.ReadFile(configPath) // #nosec G304 -- configPath is the documented user-selected role-source config file.
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ansibleRoleSourcesConfig{}, false, nil
		}
		return ansibleRoleSourcesConfig{}, false, fmt.Errorf("read ansible role sources config %q: %w", configPath, err)
	}

	config := ansibleRoleSourcesConfig{}
	if strings.TrimSpace(string(content)) == "" {
		return config, true, nil
	}

	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil && !errors.Is(err, io.EOF) {
		return ansibleRoleSourcesConfig{}, true, fmt.Errorf("parse ansible role sources config %q: %w", configPath, err)
	}

	return config, true, nil
}

func resolvePlaybookPathFromConfiguredSources(projectDir string, config ansibleRoleSourcesConfig, configPath string, playbookPath string) (string, error) {
	playbookPath = strings.TrimSpace(playbookPath)
	if filepath.IsAbs(playbookPath) {
		return filepath.Clean(playbookPath), nil
	}

	playbookRoots, err := resolveAnsiblePlaybookSourcePaths(projectDir, config, configPath)
	if err != nil {
		return "", err
	}

	candidatePaths := make([]string, 0, len(playbookRoots))
	for _, playbookRoot := range playbookRoots {
		candidatePath := filepath.Clean(filepath.Join(playbookRoot, filepath.FromSlash(playbookPath)))
		candidatePaths = append(candidatePaths, candidatePath)
		if isRegularFile(candidatePath) {
			return candidatePath, nil
		}
	}

	return "", fmt.Errorf("playbook %q was not found in configured playbook sources: %s", playbookPath, strings.Join(candidatePaths, ", "))
}

func resolveAnsiblePlaybookSourcePaths(projectDir string, config ansibleRoleSourcesConfig, configPath string) ([]string, error) {
	directories := alchemy_build.GetDirectoriesInstance()
	cacheRoot := directories.CachePath(ansiblePlaybookSourcesCacheDir)
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create ansible playbook source cache %q: %w", cacheRoot, err)
	}

	configDir := filepath.Dir(configPath)
	playbookPaths := make([]string, 0, len(config.PlaybookSources)+len(config.Sources)+1)
	usedGitCacheNames := map[string]struct{}{}
	for index, source := range config.PlaybookSources {
		playbookPath, err := resolveAnsiblePlaybookSource(source, index, configDir, cacheRoot, usedGitCacheNames)
		if err != nil {
			return nil, err
		}
		playbookPaths = appendUniquePath(playbookPaths, playbookPath)
	}
	for index, source := range config.Sources {
		if strings.TrimSpace(source.PlaybooksPath) == "" {
			continue
		}
		playbookPath, err := resolveAnsiblePlaybookSource(source, index, configDir, cacheRoot, usedGitCacheNames)
		if err != nil {
			return nil, err
		}
		playbookPaths = appendUniquePath(playbookPaths, playbookPath)
	}

	if includeDefaultAnsiblePlaybooks(config) {
		playbookPaths = appendUniquePath(playbookPaths, filepath.Clean(projectDir))
	}
	if len(playbookPaths) == 0 {
		return nil, fmt.Errorf("%q must configure at least one playbook source or enable include_default_playbooks", configPath)
	}

	return playbookPaths, nil
}

func resolveAnsibleRoleSource(
	source ansibleRoleSourceConfig,
	index int,
	configDir string,
	cacheRoot string,
	usedGitCacheNames map[string]struct{},
) (string, error) {
	sourceType, err := normalizeAnsibleRoleSourceType(source)
	if err != nil {
		return "", fmt.Errorf("invalid ansible role source %s: %w", roleSourceLabel(source, index), err)
	}

	switch sourceType {
	case "local":
		return resolveLocalAnsibleRoleSource(source, index, configDir)
	case "git":
		return resolveGitAnsibleRoleSource(source, index, cacheRoot, usedGitCacheNames)
	default:
		return "", fmt.Errorf("invalid ansible role source %s: unsupported type %q", roleSourceLabel(source, index), source.Type)
	}
}

func normalizeAnsibleRoleSourceType(source ansibleRoleSourceConfig) (string, error) {
	sourceType := strings.ToLower(strings.TrimSpace(source.Type))
	if sourceType == "" {
		switch {
		case strings.TrimSpace(source.URL) != "":
			sourceType = "git"
		case strings.TrimSpace(source.Path) != "":
			sourceType = "local"
		}
	}

	switch sourceType {
	case "local", "folder", "directory", "path":
		return "local", nil
	case "git":
		return "git", nil
	default:
		return "", fmt.Errorf("type must be local or git")
	}
}

func resolveLocalAnsibleRoleSource(source ansibleRoleSourceConfig, index int, configDir string) (string, error) {
	if strings.TrimSpace(source.Path) == "" {
		return "", fmt.Errorf("invalid local ansible role source %s: path is required", roleSourceLabel(source, index))
	}

	sourcePath, err := resolveConfiguredPath(source.Path, configDir)
	if err != nil {
		return "", fmt.Errorf("invalid local ansible role source %s: %w", roleSourceLabel(source, index), err)
	}
	rolePath, err := ansibleSourceSubdirectoryPath(sourcePath, source.RolesPath, "roles_path")
	if err != nil {
		return "", fmt.Errorf("invalid local ansible role source %s: %w", roleSourceLabel(source, index), err)
	}
	if err := validateRoleSourceDirectory(rolePath); err != nil {
		return "", fmt.Errorf("invalid local ansible role source %s: %w", roleSourceLabel(source, index), err)
	}

	return rolePath, nil
}

func resolveGitAnsibleRoleSource(
	source ansibleRoleSourceConfig,
	index int,
	cacheRoot string,
	usedGitCacheNames map[string]struct{},
) (string, error) {
	if strings.TrimSpace(source.URL) == "" {
		return "", fmt.Errorf("invalid git ansible role source %s: url is required", roleSourceLabel(source, index))
	}

	cacheName, err := gitSourceCacheName(source, source.RolesPath)
	if err != nil {
		return "", fmt.Errorf("invalid git ansible role source %s: %w", roleSourceLabel(source, index), err)
	}
	if _, exists := usedGitCacheNames[cacheName]; exists {
		return "", fmt.Errorf("invalid git ansible role source %s: cache name %q is already used", roleSourceLabel(source, index), cacheName)
	}
	usedGitCacheNames[cacheName] = struct{}{}

	checkoutDir := filepath.Join(cacheRoot, cacheName)
	if err := ensureGitRoleSource(source, checkoutDir); err != nil {
		return "", fmt.Errorf("prepare git ansible role source %s: %w", roleSourceLabel(source, index), err)
	}

	rolesPath, err := ansibleSourceSubdirectoryPath(checkoutDir, source.RolesPath, "roles_path")
	if err != nil {
		return "", fmt.Errorf("invalid git ansible role source %s: %w", roleSourceLabel(source, index), err)
	}
	if err := validateRoleSourceDirectory(rolesPath); err != nil {
		return "", fmt.Errorf("invalid git ansible role source %s: %w", roleSourceLabel(source, index), err)
	}

	return rolesPath, nil
}

func resolveAnsiblePlaybookSource(
	source ansibleRoleSourceConfig,
	index int,
	configDir string,
	cacheRoot string,
	usedGitCacheNames map[string]struct{},
) (string, error) {
	sourceType, err := normalizeAnsibleRoleSourceType(source)
	if err != nil {
		return "", fmt.Errorf("invalid ansible playbook source %s: %w", roleSourceLabel(source, index), err)
	}

	switch sourceType {
	case "local":
		return resolveLocalAnsiblePlaybookSource(source, index, configDir)
	case "git":
		return resolveGitAnsiblePlaybookSource(source, index, cacheRoot, usedGitCacheNames)
	default:
		return "", fmt.Errorf("invalid ansible playbook source %s: unsupported type %q", roleSourceLabel(source, index), source.Type)
	}
}

func resolveLocalAnsiblePlaybookSource(source ansibleRoleSourceConfig, index int, configDir string) (string, error) {
	if strings.TrimSpace(source.Path) == "" {
		return "", fmt.Errorf("invalid local ansible playbook source %s: path is required", roleSourceLabel(source, index))
	}

	sourcePath, err := resolveConfiguredPath(source.Path, configDir)
	if err != nil {
		return "", fmt.Errorf("invalid local ansible playbook source %s: %w", roleSourceLabel(source, index), err)
	}
	playbookPath, err := ansibleSourceSubdirectoryPath(sourcePath, source.PlaybooksPath, "playbooks_path")
	if err != nil {
		return "", fmt.Errorf("invalid local ansible playbook source %s: %w", roleSourceLabel(source, index), err)
	}
	if err := validatePlaybookSourceDirectory(playbookPath); err != nil {
		return "", fmt.Errorf("invalid local ansible playbook source %s: %w", roleSourceLabel(source, index), err)
	}

	return playbookPath, nil
}

func resolveGitAnsiblePlaybookSource(
	source ansibleRoleSourceConfig,
	index int,
	cacheRoot string,
	usedGitCacheNames map[string]struct{},
) (string, error) {
	if strings.TrimSpace(source.URL) == "" {
		return "", fmt.Errorf("invalid git ansible playbook source %s: url is required", roleSourceLabel(source, index))
	}

	cacheName, err := gitSourceCacheName(source, source.PlaybooksPath)
	if err != nil {
		return "", fmt.Errorf("invalid git ansible playbook source %s: %w", roleSourceLabel(source, index), err)
	}
	if _, exists := usedGitCacheNames[cacheName]; exists {
		return "", fmt.Errorf("invalid git ansible playbook source %s: cache name %q is already used", roleSourceLabel(source, index), cacheName)
	}
	usedGitCacheNames[cacheName] = struct{}{}

	checkoutDir := filepath.Join(cacheRoot, cacheName)
	if err := ensureGitRoleSource(source, checkoutDir); err != nil {
		return "", fmt.Errorf("prepare git ansible playbook source %s: %w", roleSourceLabel(source, index), err)
	}

	playbookPath, err := ansibleSourceSubdirectoryPath(checkoutDir, source.PlaybooksPath, "playbooks_path")
	if err != nil {
		return "", fmt.Errorf("invalid git ansible playbook source %s: %w", roleSourceLabel(source, index), err)
	}
	if err := validatePlaybookSourceDirectory(playbookPath); err != nil {
		return "", fmt.Errorf("invalid git ansible playbook source %s: %w", roleSourceLabel(source, index), err)
	}

	return playbookPath, nil
}

func ensureGitRoleSource(source ansibleRoleSourceConfig, checkoutDir string) error {
	if _, err := lookPathProvisionCommand("git"); err != nil {
		return fmt.Errorf("git executable is required for git role sources: %w", err)
	}

	checkoutExists := true
	info, err := os.Stat(checkoutDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect checkout %q: %w", checkoutDir, err)
		}
		checkoutExists = false
	} else if !info.IsDir() {
		return fmt.Errorf("checkout path %q exists but is not a directory", checkoutDir)
	}

	if !checkoutExists {
		if err := os.MkdirAll(filepath.Dir(checkoutDir), 0o700); err != nil {
			return fmt.Errorf("create checkout parent directory: %w", err)
		}
		if err := runGitForRoleSource("", "clone", strings.TrimSpace(source.URL), checkoutDir); err != nil {
			_ = os.RemoveAll(checkoutDir)
			return err
		}
		if strings.TrimSpace(source.Ref) != "" {
			return checkoutGitRoleSourceRef(checkoutDir, source.Ref)
		}
		return nil
	}

	if err := validateGitCheckout(checkoutDir); err != nil {
		return err
	}

	if shouldUpdateGitRoleSource(source) {
		if err := runGitForRoleSource(checkoutDir, "fetch", "--tags", "--prune", "origin"); err != nil {
			return err
		}
		if strings.TrimSpace(source.Ref) != "" {
			return updateGitRoleSourceRef(checkoutDir, source.Ref)
		}
		return runGitForRoleSource(checkoutDir, "pull", "--ff-only")
	}

	if strings.TrimSpace(source.Ref) != "" {
		return checkoutGitRoleSourceRef(checkoutDir, source.Ref)
	}

	return nil
}

func validateGitCheckout(checkoutDir string) error {
	info, err := os.Stat(filepath.Join(checkoutDir, ".git"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("checkout path %q exists but is not a git repository", checkoutDir)
		}
		return fmt.Errorf("inspect checkout %q: %w", checkoutDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("checkout path %q has an invalid .git entry", checkoutDir)
	}
	return nil
}

func shouldUpdateGitRoleSource(source ansibleRoleSourceConfig) bool {
	if source.Pull != nil {
		return *source.Pull
	}

	update := strings.ToLower(strings.TrimSpace(source.Update))
	switch update {
	case "", "auto", "always", "pull", "true", "yes":
		return true
	case "never", "none", "false", "no":
		return false
	default:
		return true
	}
}

func updateGitRoleSourceRef(checkoutDir string, ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}

	if gitRefExists(checkoutDir, "refs/remotes/origin/"+ref) {
		if gitRefExists(checkoutDir, "refs/heads/"+ref) {
			if err := runGitForRoleSource(checkoutDir, "checkout", ref); err != nil {
				return err
			}
		} else if err := runGitForRoleSource(checkoutDir, "checkout", "-B", ref, "origin/"+ref); err != nil {
			return err
		}
		return runGitForRoleSource(checkoutDir, "pull", "--ff-only", "origin", ref)
	}

	return checkoutGitRoleSourceRef(checkoutDir, ref)
}

func checkoutGitRoleSourceRef(checkoutDir string, ref string) error {
	return runGitForRoleSource(checkoutDir, "checkout", strings.TrimSpace(ref))
}

func gitRefExists(checkoutDir string, ref string) bool {
	_, err := runRoleSourceGitCommand(
		checkoutDir,
		ansibleRoleSourceGitCommandTime,
		"git",
		[]string{"rev-parse", "--verify", "--quiet", ref},
	)
	return err == nil
}

func runGitForRoleSource(workingDir string, args ...string) error {
	if _, err := runRoleSourceGitCommand(workingDir, ansibleRoleSourceGitCommandTime, "git", args); err != nil {
		return fmt.Errorf("git %s failed: %w", strings.Join(sanitizeCommandArgsForLogs(args), " "), err)
	}
	return nil
}

func ansibleSourceSubdirectoryPath(sourceRoot string, configuredSubdirectory string, fieldName string) (string, error) {
	subdirectory := strings.TrimSpace(configuredSubdirectory)
	if subdirectory == "" {
		subdirectory = defaultAnsibleSourceSubdirectory
	}

	cleanSubdirectory := filepath.Clean(filepath.FromSlash(subdirectory))
	if filepath.IsAbs(cleanSubdirectory) || cleanSubdirectory == ".." || strings.HasPrefix(cleanSubdirectory, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s must stay inside the configured source: %q", fieldName, configuredSubdirectory)
	}

	return filepath.Join(sourceRoot, cleanSubdirectory), nil
}

func validateRoleSourceDirectory(rolePath string) error {
	info, err := os.Stat(rolePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("role path %q does not exist", rolePath)
		}
		return fmt.Errorf("inspect role path %q: %w", rolePath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("role path %q is not a directory", rolePath)
	}
	return nil
}

func validatePlaybookSourceDirectory(playbookPath string) error {
	info, err := os.Stat(playbookPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("playbook path %q does not exist", playbookPath)
		}
		return fmt.Errorf("inspect playbook path %q: %w", playbookPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("playbook path %q is not a directory", playbookPath)
	}
	return nil
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func includeDefaultAnsibleRoles(config ansibleRoleSourcesConfig) bool {
	return config.IncludeDefaultRoles == nil || *config.IncludeDefaultRoles
}

func includeDefaultAnsiblePlaybooks(config ansibleRoleSourcesConfig) bool {
	return config.IncludeDefaultPlaybooks == nil || *config.IncludeDefaultPlaybooks
}

func defaultAnsibleRolePath(projectDir string) string {
	return filepath.Clean(filepath.Join(projectDir, "roles"))
}

func roleSourceLabel(source ansibleRoleSourceConfig, index int) string {
	if name := strings.TrimSpace(source.Name); name != "" {
		return fmt.Sprintf("%q", name)
	}
	return fmt.Sprintf("#%d", index+1)
}

func gitSourceCacheName(source ansibleRoleSourceConfig, sourceSubdirectory string) (string, error) {
	if name := strings.TrimSpace(source.Name); name != "" {
		sanitized := sanitizeRoleSourceCacheName(name)
		if sanitized == "" {
			return "", fmt.Errorf("name %q cannot be used as a cache directory name", name)
		}
		return sanitized, nil
	}

	sourceURL := strings.TrimRight(strings.TrimSpace(source.URL), "/")
	baseName := filepath.Base(sourceURL)
	baseName = strings.TrimSuffix(baseName, ".git")
	if baseName == "." || baseName == string(filepath.Separator) || baseName == "" {
		baseName = "source"
	}
	baseName = sanitizeRoleSourceCacheName(baseName)
	if baseName == "" {
		baseName = "source"
	}

	hashInput := strings.Join([]string{
		strings.TrimSpace(source.URL),
		strings.TrimSpace(source.Ref),
		strings.TrimSpace(sourceSubdirectory),
	}, "\x00")
	hash := sha256.Sum256([]byte(hashInput))
	return baseName + "-" + hex.EncodeToString(hash[:])[:12], nil
}

func sanitizeRoleSourceCacheName(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	builder.Grow(len(value))
	previousSeparator := false
	for _, char := range strings.ToLower(value) {
		isAllowed := unicode.IsLetter(char) || unicode.IsDigit(char) || char == '.' || char == '_' || char == '-'
		if isAllowed {
			builder.WriteRune(char)
			previousSeparator = false
			continue
		}
		if !previousSeparator {
			builder.WriteByte('-')
			previousSeparator = true
		}
	}

	return strings.Trim(builder.String(), ".-_")
}

func resolveConfiguredPath(configuredPath string, baseDir string) (string, error) {
	expanded, err := expandUserPath(os.ExpandEnv(strings.TrimSpace(configuredPath)))
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(expanded) {
		return filepath.Clean(expanded), nil
	}
	return filepath.Clean(filepath.Join(baseDir, expanded)), nil
}

func expandUserPath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory for %q: %w", path, err)
		}
		if path == "~" {
			return homeDir, nil
		}
		return filepath.Join(homeDir, path[2:]), nil
	}
	return path, nil
}

func appendUniquePath(paths []string, path string) []string {
	for _, existing := range paths {
		if existing == path {
			return paths
		}
	}
	return append(paths, path)
}

func ansibleRuntimeEnvForProject(projectDir string) ([]string, error) {
	return ansibleRuntimeEnvForProjectPlaybook(projectDir, "")
}

func ansibleRuntimeEnvForProjectPlaybook(projectDir string, playbookPath string) ([]string, error) {
	env := ansibleRuntimeEnv()
	resolveRolePaths := resolveAnsibleRolePaths
	if isBundledSetupProvisionPlaybook(projectDir, playbookPath) {
		resolveRolePaths = resolveAnsibleRolePathsWithDefaultFallback
	}

	rolePaths, err := resolveRolePaths(projectDir)
	if err != nil {
		return nil, err
	}
	if isBundledRoleSourcesTestProvisionPlaybook(projectDir, playbookPath) {
		rolePaths = appendRoleSourcesTestPaths(projectDir, rolePaths)
	}

	envValue, err := ansibleRolesPathEnvValue(rolePaths)
	if err != nil {
		return nil, err
	}
	env = append(env, "ANSIBLE_ROLES_PATH="+envValue)

	return env, nil
}

func isBundledSetupProvisionPlaybook(projectDir string, playbookPath string) bool {
	return isBundledProvisionPlaybook(projectDir, playbookPath, bundledSetupProvisionPlaybook)
}

func isBundledRoleSourcesTestProvisionPlaybook(projectDir string, playbookPath string) bool {
	return isBundledProvisionPlaybook(projectDir, playbookPath, defaultProvisionPlaybook)
}

func isBundledProvisionPlaybook(projectDir string, playbookPath string, bundledPlaybookPath string) bool {
	playbookPath = strings.TrimSpace(playbookPath)
	if playbookPath == "" {
		return false
	}

	defaultPlaybookPath := filepath.Clean(filepath.Join(projectDir, filepath.FromSlash(bundledPlaybookPath)))
	candidatePath := filepath.Clean(filepath.FromSlash(playbookPath))
	if !filepath.IsAbs(candidatePath) {
		candidatePath = filepath.Clean(filepath.Join(projectDir, candidatePath))
	}

	return candidatePath == defaultPlaybookPath
}

func appendRoleSourcesTestPaths(projectDir string, rolePaths []string) []string {
	for _, rolePath := range []string{
		filepath.Join(projectDir, "roles_test_2"),
		filepath.Join(projectDir, "roles_test_1"),
	} {
		rolePaths = appendUniquePath(rolePaths, filepath.Clean(rolePath))
	}
	return rolePaths
}

func ansibleRolesPathEnvValue(rolePaths []string) (string, error) {
	resolvedPaths := make([]string, 0, len(rolePaths))
	for _, rolePath := range rolePaths {
		resolvedPath, err := ansibleRolePathForRuntime(rolePath)
		if err != nil {
			return "", err
		}
		resolvedPaths = append(resolvedPaths, resolvedPath)
	}

	return strings.Join(resolvedPaths, ansibleRolesPathSeparator()), nil
}

func ansibleRolePathForRuntime(rolePath string) (string, error) {
	if runtimeGoos() != "windows" {
		return rolePath, nil
	}
	if strings.HasPrefix(rolePath, "/") {
		return rolePath, nil
	}

	return windowsPathToCygwinPath(rolePath)
}

func ansibleRolesPathSeparator() string {
	return ":"
}

func runtimeGoos() string {
	return runtimeGOOS
}

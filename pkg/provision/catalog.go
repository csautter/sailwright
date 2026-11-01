package provision

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	"gopkg.in/yaml.v3"
)

// CatalogEntry describes the first effective role or playbook with a given name.
type CatalogEntry struct {
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Source      string   `json:"source"`
	Path        string   `json:"path"`
	Platforms   []string `json:"platforms"`
	Compatible  bool     `json:"compatible"`
	Shadows     []string `json:"shadows"`
}

type catalogRoot struct{ path, source, kind string }

// Catalog reads cached Git sources without fetching unless refresh is requested.
// A missing checkout is cloned on first use.
func Catalog(targetOS string, refresh bool) ([]CatalogEntry, error) {
	dirs := alchemy_build.GetDirectoriesInstance()
	config, configPath, _, err := loadCurrentAnsibleRoleSourcesConfig()
	if err != nil {
		return nil, err
	}
	roots := []catalogRoot{}
	add := func(source ansibleRoleSourceConfig, index int, kind string) error {
		typ, err := normalizeAnsibleRoleSourceType(source)
		if err != nil {
			return err
		}
		sub, cache := source.RolesPath, ansibleRoleSourcesCacheDir
		if kind == "playbook" {
			sub, cache = source.PlaybooksPath, ansiblePlaybookSourcesCacheDir
		}
		var root string
		if typ == "local" {
			if strings.TrimSpace(source.Path) == "" {
				return fmt.Errorf("local source %s requires a path", roleSourceLabel(source, index))
			}
			root, err = resolveConfiguredPath(source.Path, filepath.Dir(configPath))
		} else {
			var name string
			name, err = gitSourceCacheName(source, sub)
			root = dirs.CachePath(filepath.Join(cache, name))
			if err == nil {
				if _, statErr := os.Stat(root); refresh || os.IsNotExist(statErr) {
					err = ensureGitRoleSource(source, root)
				} else if statErr != nil {
					err = statErr
				} else {
					err = validateGitCheckout(root)
				}
			}
		}
		if err != nil {
			return err
		}
		root, err = ansibleSourceSubdirectoryPath(root, sub, kind+"s_path")
		if err != nil {
			return err
		}
		label := source.Name
		if label == "" {
			label = source.Path
			if typ == "git" {
				label = source.URL
			}
		}
		roots = append(roots, catalogRoot{root, label, kind})
		return nil
	}
	for i, s := range config.Sources {
		if err := add(s, i, "role"); err != nil {
			return nil, err
		}
	}
	for i, s := range config.PlaybookSources {
		if err := add(s, i, "playbook"); err != nil {
			return nil, err
		}
	}
	for i, s := range config.Sources {
		if s.PlaybooksPath != "" {
			if err := add(s, i, "playbook"); err != nil {
				return nil, err
			}
		}
	}
	roots = append(roots, catalogRoot{filepath.Join(dirs.ProjectDir, "roles"), "built-in (example)", "role"}, catalogRoot{filepath.Join(dirs.ProjectDir, "playbooks"), "built-in (example)", "playbook"})
	result := []CatalogEntry{}
	seen := map[string]int{}
	for _, root := range roots {
		files, err := os.ReadDir(root.path)
		if err != nil {
			return nil, fmt.Errorf("read catalog source %s: %w", root.source, err)
		}
		for _, file := range files {
			path := filepath.Join(root.path, file.Name())
			entry := CatalogEntry{Kind: root.kind, Name: file.Name(), Source: root.source, Path: path, Compatible: true, Platforms: []string{}, Shadows: []string{}}
			if root.kind == "role" {
				if !file.IsDir() || (!isRegularFile(filepath.Join(path, "tasks/main.yml")) && !isRegularFile(filepath.Join(path, "tasks/main.yaml"))) {
					continue
				}
				meta := filepath.Join(path, "meta/main.yml")
				if !isRegularFile(meta) {
					meta = filepath.Join(path, "meta/main.yaml")
				}
				if data, err := os.ReadFile(meta); err == nil {
					var doc struct {
						GalaxyInfo struct {
							Description string `yaml:"description"`
							Platforms   []struct {
								Name string `yaml:"name"`
							} `yaml:"platforms"`
						} `yaml:"galaxy_info"`
					}
					if err := yaml.Unmarshal(data, &doc); err != nil {
						return nil, fmt.Errorf("parse role metadata %s: %w", meta, err)
					}
					entry.Description = doc.GalaxyInfo.Description
					for _, p := range doc.GalaxyInfo.Platforms {
						entry.Platforms = append(entry.Platforms, p.Name)
					}
				} else if !os.IsNotExist(err) {
					return nil, err
				}
				entry.Compatible = compatiblePlatforms(entry.Platforms, targetOS)
			} else {
				if file.IsDir() || (filepath.Ext(path) != ".yml" && filepath.Ext(path) != ".yaml") || file.Name() == "role-sources-test.yml" {
					continue
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return nil, err
				}
				var plays []map[string]interface{}
				if yaml.Unmarshal(data, &plays) != nil || len(plays) == 0 {
					continue
				}
				valid := true
				for _, p := range plays {
					if _, ok := p["hosts"]; !ok {
						valid = false
					}
				}
				if !valid {
					continue
				}
				entry.Name = strings.TrimSuffix(file.Name(), filepath.Ext(path))
				for _, line := range strings.Split(string(data), "\n") {
					if strings.HasPrefix(strings.TrimSpace(line), "#") {
						entry.Description = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
						break
					}
				}
			}
			key := entry.Kind + ":" + entry.Name
			if i, ok := seen[key]; ok {
				result[i].Shadows = append(result[i].Shadows, entry.Source)
				continue
			}
			seen[key] = len(result)
			result = append(result, entry)
		}
	}
	return result, nil
}

func compatiblePlatforms(platforms []string, target string) bool {
	if len(platforms) == 0 || target == "" {
		return true
	}
	target = strings.ToLower(target)
	for _, p := range platforms {
		p = strings.ToLower(p)
		if p == target || ((target == "darwin" || target == "macos" || target == "macosx") && (p == "darwin" || p == "macos" || p == "macosx")) || (strings.HasPrefix(target, "windows") && p == "windows") || (target == "linux" && (p == "ubuntu" || p == "debian" || p == "el" || p == "fedora" || p == "archlinux")) {
			return true
		}
	}
	return false
}

// PrepareSelection resolves catalog names and creates a private, disposable role playbook.
func PrepareSelection(roles []string, playbook string) (string, func(), error) {
	cleanup := func() {}
	if len(roles) > 0 && playbook != "" {
		return "", cleanup, fmt.Errorf("choose roles or a playbook, not both")
	}
	if len(roles) == 0 && (playbook == "" || filepath.Ext(playbook) != "" || strings.ContainsAny(playbook, `/\`)) {
		return playbook, cleanup, nil
	}
	entries, err := Catalog("", false)
	if err != nil {
		return "", cleanup, err
	}
	if len(roles) == 0 {
		for _, e := range entries {
			if e.Kind == "playbook" && e.Name == playbook {
				return e.Path, cleanup, nil
			}
		}
		return "", cleanup, fmt.Errorf("unknown playbook %q; run `sailwright provision catalog`", playbook)
	}
	selected := []string{}
	for _, name := range roles {
		found := false
		for _, e := range entries {
			if e.Kind == "role" && e.Name == name {
				rolePath, err := ansibleRolePathForRuntime(e.Path)
				if err != nil {
					return "", cleanup, err
				}
				selected = append(selected, rolePath)
				found = true
				break
			}
		}
		if !found {
			return "", cleanup, fmt.Errorf("unknown role %q; run `sailwright provision catalog`", name)
		}
	}
	data, err := yaml.Marshal([]interface{}{map[string]interface{}{"hosts": "all", "gather_facts": true, "roles": selected}})
	if err != nil {
		return "", cleanup, err
	}
	dir := alchemy_build.GetDirectoriesInstance().CachePath("selected-playbooks")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", cleanup, err
	}
	file, err := os.CreateTemp(dir, "roles-*.yml")
	if err != nil {
		return "", cleanup, err
	}
	cleanup = func() { _ = os.Remove(file.Name()) }
	if _, err = file.Write(data); err != nil {
		file.Close()
		cleanup()
		return "", func() {}, err
	}
	if err = file.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return file.Name(), cleanup, nil
}

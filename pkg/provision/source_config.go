package provision

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	"gopkg.in/yaml.v3"
)

// ConfigOverride is the explicit global CLI --config path.
var ConfigOverride string

// ConfigLocation reports the effective path and its precedence origin.
func ConfigLocation() (string, string) {
	dirs := alchemy_build.GetDirectoriesInstance()
	if strings.TrimSpace(ConfigOverride) != "" {
		return filepath.Clean(ConfigOverride), "flag"
	}
	if p := strings.TrimSpace(os.Getenv(ansibleRoleSourcesConfigEnvVar)); p != "" {
		return filepath.Clean(p), ansibleRoleSourcesConfigEnvVar
	}
	if cwd, err := os.Getwd(); err == nil {
		p := filepath.Join(cwd, ansibleRoleSourcesConfigFile)
		if isRegularFile(p) {
			return p, "current directory"
		}
	}
	return dirs.ConfigPath(ansibleRoleSourcesConfigFile), "default"
}

// ConfiguredSource is a read-only view of one configured role or playbook source.
type ConfiguredSource struct {
	Name          string
	Type          string
	Path          string
	URL           string
	Ref           string
	RolesPath     string
	PlaybooksPath string
}

// ConfigSources returns role sources and playbook-only sources in their
// configured precedence order.
func ConfigSources() (roleSources []ConfiguredSource, playbookSources []ConfiguredSource, err error) {
	config, _, _, err := loadCurrentAnsibleRoleSourcesConfig()
	if err != nil {
		return nil, nil, err
	}
	return toConfiguredSources(config.Sources), toConfiguredSources(config.PlaybookSources), nil
}

func toConfiguredSources(sources []ansibleRoleSourceConfig) []ConfiguredSource {
	out := make([]ConfiguredSource, 0, len(sources))
	for _, s := range sources {
		out = append(out, ConfiguredSource{Name: s.Name, Type: s.Type, Path: s.Path, URL: s.URL, Ref: s.Ref, RolesPath: s.RolesPath, PlaybooksPath: s.PlaybooksPath})
	}
	return out
}

// AddConfigSource detects role/playbook roots, refusing duplicate names.
func AddConfigSource(location, name, ref string) error {
	path, _ := ConfigLocation()
	doc, err := readConfigDocument(path)
	if err != nil {
		return err
	}
	source := ansibleRoleSourceConfig{Name: name, Ref: ref}
	isGit := strings.Contains(location, "://") || strings.HasPrefix(location, "git@") || strings.HasSuffix(location, ".git")
	root := ""
	if isGit {
		source.Type, source.URL = "git", location
		if source.Name == "" {
			source.Name = sanitizeRoleSourceCacheName(strings.TrimSuffix(filepath.Base(strings.TrimRight(location, "/")), ".git"))
		}
		if source.Name == "" {
			return fmt.Errorf("provide a source name with --name")
		}
	} else {
		source.Type = "local"
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			return cwdErr
		}
		root, err = resolveConfiguredPath(location, cwd)
		if err != nil {
			return err
		}
		source.Path = root
		if source.Name == "" {
			source.Name = sanitizeRoleSourceCacheName(filepath.Base(root))
		}
		if ref != "" {
			return fmt.Errorf("--ref is only valid for Git sources")
		}
	}
	if source.Name == "" {
		return fmt.Errorf("provide a source name with --name")
	}
	for _, key := range []string{"sources", "playbook_sources"} {
		if seq := mappingValue(doc.Content[0], key); seq != nil {
			for _, item := range seq.Content {
				n := mappingValue(item, "name")
				if n != nil && n.Value == source.Name {
					return fmt.Errorf("source %q already exists; remove it first or choose another name", source.Name)
				}
			}
		}
	}
	if isGit {
		cacheName, err := gitSourceCacheName(source, "")
		if err != nil {
			return err
		}
		root = alchemy_build.GetDirectoriesInstance().CachePath(ansibleRoleSourcesCacheDir, cacheName)
		if err := ensureGitRoleSource(source, root); err != nil {
			return err
		}
	}
	roles := directoryExists(filepath.Join(root, "roles"))
	playbooks := directoryExists(filepath.Join(root, "playbooks"))
	directRoles, directPlaybooks := false, false
	if !roles || !playbooks {
		entries, err := os.ReadDir(root)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			p := filepath.Join(root, entry.Name())
			if entry.IsDir() && isAnsibleRoleDir(p) {
				directRoles = true
			}
			if !entry.IsDir() {
				_, ok, err := readAnsiblePlaybook(p)
				if err != nil {
					return err
				}
				if ok {
					directPlaybooks = true
				}
			}
		}
	}
	if !roles && !playbooks && !directRoles && !directPlaybooks {
		return fmt.Errorf("%q contains no roles or playbooks", location)
	}
	fields := map[string]string{"name": source.Name, "type": source.Type}
	if isGit {
		fields["url"] = source.URL
		if ref != "" {
			fields["ref"] = ref
		}
	} else {
		fields["path"] = source.Path
	}
	key := "sources"
	if roles {
		fields["roles_path"] = "roles"
	} else if directRoles {
		fields["roles_path"] = "."
	} else {
		key = "playbook_sources"
	}
	if playbooks {
		fields["playbooks_path"] = "playbooks"
	} else if directPlaybooks {
		fields["playbooks_path"] = "."
	}
	var item yaml.Node
	if err := item.Encode(fields); err != nil {
		return err
	}
	seq := mappingValue(doc.Content[0], key)
	if seq == nil {
		seq = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		doc.Content[0].Content = append(doc.Content[0].Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, seq)
	}
	if seq.Kind != yaml.SequenceNode {
		return fmt.Errorf("%s must be a sequence", key)
	}
	seq.Content = append(seq.Content, &item)
	return writeConfigDocument(path, doc)
}

func RemoveConfigSource(name string) error {
	path, _ := ConfigLocation()
	doc, err := readConfigDocument(path)
	if err != nil {
		return err
	}
	found := false
	for _, key := range []string{"sources", "playbook_sources"} {
		if seq := mappingValue(doc.Content[0], key); seq != nil {
			items := seq.Content[:0]
			for _, item := range seq.Content {
				n := mappingValue(item, "name")
				if n != nil && n.Value == name {
					found = true
				} else {
					items = append(items, item)
				}
			}
			seq.Content = items
		}
	}
	if !found {
		return fmt.Errorf("source %q does not exist", name)
	}
	return writeConfigDocument(path, doc)
}

func directoryExists(path string) bool { info, err := os.Stat(path); return err == nil && info.IsDir() }
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}
func readConfigDocument(path string) (*yaml.Node, error) {
	if _, _, err := loadAnsibleRoleSourcesConfig(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	var doc yaml.Node
	if len(bytes.TrimSpace(data)) > 0 {
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		if err := decoder.Decode(&doc); err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		var extra yaml.Node
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("config must contain exactly one YAML document")
		}
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config must be a YAML mapping")
	}
	return &doc, nil
}
func writeConfigDocument(path string, doc *yaml.Node) error {
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(doc); err != nil {
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	// Refuse symlinks: replacing one would silently change which configuration is edited.
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("config %q is not a regular file", path)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".role-sources-*.yml")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(out.Bytes()); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

package extension

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	ExecutablePrefix = "alchemy-"
	ProtocolVersion  = "1"

	ProtocolEnvVar = "DEV_ALCHEMY_EXTENSION_PROTOCOL"
	NameEnvVar     = "DEV_ALCHEMY_EXTENSION_NAME"
)

type Executable struct {
	Name string
	Path string
}

type DiscoverOptions struct {
	PathEnv string
}

type RunOptions struct {
	Name     string
	Args     []string
	PathEnv  string
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
	ExtraEnv []string
}

func Discover(options DiscoverOptions) ([]Executable, error) {
	pathEnv := options.PathEnv
	if pathEnv == "" {
		pathEnv = os.Getenv("PATH")
	}
	if strings.TrimSpace(pathEnv) == "" {
		return nil, nil
	}

	seen := make(map[string]Executable)
	for _, dir := range filepath.SplitList(pathEnv) {
		if strings.TrimSpace(dir) == "" {
			continue
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			name, ok := NameFromExecutable(entry.Name())
			if !ok {
				continue
			}
			if err := ValidateName(name); err != nil {
				continue
			}
			if _, exists := seen[name]; exists {
				continue
			}

			path := filepath.Join(dir, entry.Name())
			executable, err := isExecutableFile(path)
			if err != nil || !executable {
				continue
			}

			seen[name] = Executable{Name: name, Path: path}
		}
	}

	extensions := make([]Executable, 0, len(seen))
	for _, extension := range seen {
		extensions = append(extensions, extension)
	}
	sort.Slice(extensions, func(i, j int) bool {
		return extensions[i].Name < extensions[j].Name
	})

	return extensions, nil
}

func Resolve(name string, options DiscoverOptions) (Executable, error) {
	normalizedName := NormalizeName(name)
	if err := ValidateName(normalizedName); err != nil {
		return Executable{}, err
	}

	extensions, err := Discover(options)
	if err != nil {
		return Executable{}, err
	}
	for _, extension := range extensions {
		if extension.Name == normalizedName {
			return extension, nil
		}
	}

	return Executable{}, fmt.Errorf("extension %q not found on PATH; expected an executable named %q", normalizedName, ExecutablePrefix+normalizedName)
}

func Run(ctx context.Context, options RunOptions) error {
	extension, err := Resolve(options.Name, DiscoverOptions{PathEnv: options.PathEnv})
	if err != nil {
		return err
	}

	stdin := options.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stdout := options.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := options.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	// #nosec G204 -- extension resolution restricts execution to alchemy-* executables found on PATH; arguments are passed without shell parsing.
	cmd := exec.CommandContext(ctx, extension.Path, options.Args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = extensionEnvironment(os.Environ(), extension.Name, options.PathEnv, options.ExtraEnv)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run extension %q (%s): %w", extension.Name, extension.Path, err)
	}

	return nil
}

func NormalizeName(name string) string {
	normalized := strings.TrimSpace(name)
	if strings.HasPrefix(normalized, ExecutablePrefix) {
		normalized = strings.TrimPrefix(normalized, ExecutablePrefix)
	}
	return normalized
}

func ValidateName(name string) error {
	if name == "" {
		return errors.New("extension name cannot be empty")
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("extension name cannot contain path separators: %q", name)
	}

	for index, value := range name {
		valid := isASCIILetter(value) ||
			isASCIIDigit(value) ||
			value == '-' ||
			value == '_' ||
			value == '.'
		if !valid {
			return fmt.Errorf("extension name contains unsupported character %q: %q", value, name)
		}
		if index == 0 && (value == '-' || value == '.') {
			return fmt.Errorf("extension name must start with a letter, digit, or underscore: %q", name)
		}
	}

	return nil
}

func isASCIILetter(value rune) bool {
	return (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z')
}

func isASCIIDigit(value rune) bool {
	return value >= '0' && value <= '9'
}

func NameFromExecutable(filename string) (string, bool) {
	if !strings.HasPrefix(filename, ExecutablePrefix) {
		return "", false
	}

	name := strings.TrimPrefix(filename, ExecutablePrefix)
	name = trimExecutableSuffix(name)
	if name == "" {
		return "", false
	}

	return name, true
}

func trimExecutableSuffix(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".exe", ".cmd", ".bat", ".com", ".ps1":
		return strings.TrimSuffix(name, filepath.Ext(name))
	default:
		return name
	}
}

func isExecutableFile(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		return false, nil
	}
	if runtime.GOOS == "windows" {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".exe", ".cmd", ".bat", ".com", ".ps1":
			return true, nil
		default:
			return false, nil
		}
	}

	return info.Mode().Perm()&0o111 != 0, nil
}

func extensionEnvironment(baseEnv []string, name string, pathEnv string, extraEnv []string) []string {
	env := append([]string{}, baseEnv...)
	env = append(env, extraEnv...)
	if pathEnv != "" {
		env = upsertEnv(env, "PATH", pathEnv)
	}
	env = upsertEnv(env, ProtocolEnvVar, ProtocolVersion)
	env = upsertEnv(env, NameEnvVar, name)
	return env
}

func upsertEnv(env []string, key string, value string) []string {
	prefix := key + "="
	next := make([]string, 0, len(env)+1)
	replaced := false
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			if !replaced {
				next = append(next, prefix+value)
				replaced = true
			}
			continue
		}
		next = append(next, entry)
	}
	if !replaced {
		next = append(next, prefix+value)
	}
	return next
}

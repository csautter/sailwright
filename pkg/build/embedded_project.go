package build

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	runtimeassets "github.com/csautter/dev-alchemy"
)

const (
	embeddedProjectDirName      = "project"
	embeddedProjectManifestFile = ".embedded-assets.sha256"
	// The Go module path keeps the pre-rename name until the deferred module
	// rename in ADR 0004. Change it together with go.mod.
	sailwrightModulePath = "github.com/csautter/dev-alchemy"
)

func ensureProjectDir(workingDir string, appDataDir string) (string, error) {
	if projectDir := determineTopLevelDirWithGit(workingDir); projectDir != "" {
		return projectDir, nil
	}

	return ensureEmbeddedProjectDir(appDataDir)
}

func determineTopLevelDirWithGit(workingDir string) string {
	dir := workingDir
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return ""
		}
	}

	for {
		gitPath := filepath.Join(dir, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			// Only a Sailwright checkout is a project dir. Other repos, such as
			// a team's setup repo, fall back to the embedded project.
			if isSailwrightCheckout(dir) {
				return dir
			}
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return ""
}

func isSailwrightCheckout(dir string) bool {
	content, err := os.ReadFile(filepath.Join(dir, "go.mod")) // #nosec G304 -- dir is a parent of the working directory.
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "module" {
			return strings.Trim(fields[1], `"`) == sailwrightModulePath
		}
	}
	return false
}

func ensureEmbeddedProjectDir(appDataDir string) (string, error) {
	projectDir := filepath.Join(appDataDir, embeddedProjectDirName)
	if err := os.MkdirAll(projectDir, managedDirPermission); err != nil {
		return "", fmt.Errorf("create embedded project directory: %w", err)
	}

	projectRoot, err := os.OpenRoot(projectDir)
	if err != nil {
		return "", fmt.Errorf("open embedded project directory: %w", err)
	}
	defer projectRoot.Close()

	manifestHash, err := runtimeassets.ManifestHash()
	if err != nil {
		return "", fmt.Errorf("build embedded asset manifest: %w", err)
	}

	if manifestMatches(projectRoot, embeddedProjectManifestFile, manifestHash) {
		return projectDir, nil
	}

	if err := syncEmbeddedProject(runtimeassets.FS(), projectRoot); err != nil {
		return "", fmt.Errorf("extract embedded project assets: %w", err)
	}

	if err := projectRoot.WriteFile(embeddedProjectManifestFile, []byte(manifestHash+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write embedded asset manifest: %w", err)
	}

	return projectDir, nil
}

func manifestMatches(root *os.Root, path string, want string) bool {
	content, err := root.ReadFile(path)
	if err != nil {
		return false
	}

	return strings.TrimSpace(string(content)) == want
}

func syncEmbeddedProject(source fs.FS, destination *os.Root) error {
	sourceEntries, err := embeddedProjectEntries(source)
	if err != nil {
		return err
	}

	if err := pruneEmbeddedProject(destination, sourceEntries); err != nil {
		return err
	}

	return fs.WalkDir(source, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}

		targetPath := filepath.FromSlash(path)
		if d.IsDir() {
			return destination.MkdirAll(targetPath, managedDirPermission)
		}

		content, err := fs.ReadFile(source, path)
		if err != nil {
			return err
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := embeddedFileMode(path, info.Mode())

		parentDir := filepath.Dir(targetPath)
		if parentDir != "." {
			if err := destination.MkdirAll(parentDir, managedDirPermission); err != nil {
				return err
			}
		}

		if existing, err := destination.ReadFile(targetPath); err == nil && bytes.Equal(existing, content) {
			return destination.Chmod(targetPath, mode)
		}

		return destination.WriteFile(targetPath, content, mode)
	})
}

type embeddedProjectEntry struct {
	isDir bool
}

func embeddedProjectEntries(source fs.FS) (map[string]embeddedProjectEntry, error) {
	entries := make(map[string]embeddedProjectEntry)

	err := fs.WalkDir(source, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}

		entries[path] = embeddedProjectEntry{isDir: d.IsDir()}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return entries, nil
}

type staleEmbeddedProjectEntry struct {
	path  string
	isDir bool
}

func pruneEmbeddedProject(destination *os.Root, sourceEntries map[string]embeddedProjectEntry) error {
	var staleEntries []staleEmbeddedProjectEntry

	if err := fs.WalkDir(destination.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." || path == embeddedProjectManifestFile {
			return nil
		}

		sourceEntry, ok := sourceEntries[path]
		if ok && sourceEntry.isDir == d.IsDir() {
			return nil
		}

		staleEntries = append(staleEntries, staleEmbeddedProjectEntry{
			path:  path,
			isDir: d.IsDir(),
		})
		return nil
	}); err != nil {
		return err
	}

	sort.Slice(staleEntries, func(i, j int) bool {
		return strings.Count(staleEntries[i].path, "/") > strings.Count(staleEntries[j].path, "/")
	})

	for _, entry := range staleEntries {
		targetPath := filepath.FromSlash(entry.path)
		var err error
		if entry.isDir {
			err = destination.RemoveAll(targetPath)
		} else {
			err = destination.Remove(targetPath)
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}

	return nil
}

func embeddedFileMode(path string, mode fs.FileMode) fs.FileMode {
	perm := mode.Perm()
	if strings.HasSuffix(path, ".sh") {
		return 0o755
	}
	if perm == 0 {
		return 0o644
	}
	if perm&0o111 != 0 {
		return perm
	}
	return 0o644
}

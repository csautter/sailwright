package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// SnapshotLimits bound what a bundle snapshot may copy.
type SnapshotLimits struct {
	MaxBytes int64
	MaxFiles int
}

// Snapshot is a read-only, gateway-owned copy of an agent-provided bundle. Approved
// commands run against the snapshot, so editing the bundle after approval changes
// nothing.
type Snapshot struct {
	AgentRoot string `json:"agent_root"`
	HostRoot  string `json:"host_root"`
	Dir       string `json:"dir"`
	TreeHash  string `json:"tree_hash"`
	Files     int    `json:"files"`
	Bytes     int64  `json:"bytes"`
}

// skippedSnapshotDirs are never copied; they are not needed to run Ansible content.
var skippedSnapshotDirs = map[string]bool{".git": true, "node_modules": true, ".venv": true}

// TranslateAgentPath maps a path inside the agent container to the host path that
// backs it. Only configured agent workspaces are accepted.
func TranslateAgentPath(agentPath string, workspaces []WorkspaceMapping) (string, WorkspaceMapping, error) {
	if !strings.HasPrefix(agentPath, "/") {
		return "", WorkspaceMapping{}, fmt.Errorf("bundle_root %q must be an absolute path inside the agent workspace", agentPath)
	}
	cleaned := path.Clean(agentPath)
	var best WorkspaceMapping
	for _, mapping := range workspaces {
		if (cleaned == mapping.Agent || strings.HasPrefix(cleaned, mapping.Agent+"/")) && len(mapping.Agent) > len(best.Agent) {
			best = mapping
		}
	}
	if best.Agent == "" {
		return "", WorkspaceMapping{}, fmt.Errorf("bundle_root %q is not inside a configured agent workspace", agentPath)
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(cleaned, best.Agent), "/")
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", WorkspaceMapping{}, fmt.Errorf("bundle_root %q escapes the agent workspace", agentPath)
	}
	return filepath.Join(best.Host, filepath.FromSlash(rel)), best, nil
}

// CreateSnapshot copies hostRoot into a new directory below stagingRoot. Files are
// read through os.Root, so symlinks cannot escape the bundle even if the agent swaps
// files while the copy runs. Symlinks to directories are rejected.
func CreateSnapshot(agentRoot string, hostRoot string, stagingRoot string, limits SnapshotLimits) (Snapshot, error) {
	info, err := os.Stat(hostRoot)
	if err != nil {
		return Snapshot{}, fmt.Errorf("inspect bundle_root: %w", err)
	}
	if !info.IsDir() {
		return Snapshot{}, fmt.Errorf("bundle_root %q is not a directory", agentRoot)
	}

	if err := ensurePrivateDir(stagingRoot); err != nil {
		return Snapshot{}, err
	}
	dir, err := os.MkdirTemp(stagingRoot, "bundle-")
	if err != nil {
		return Snapshot{}, err
	}

	snapshot, err := copyBundle(hostRoot, dir, limits)
	if err != nil {
		_ = RemoveSnapshot(dir)
		return Snapshot{}, err
	}
	snapshot.AgentRoot = agentRoot
	snapshot.HostRoot = hostRoot
	snapshot.Dir = dir
	if err := makeTreeReadOnly(dir); err != nil {
		_ = RemoveSnapshot(dir)
		return Snapshot{}, err
	}
	return snapshot, nil
}

type snapshotEntry struct {
	rel  string
	mode string
	hash string
}

func copyBundle(hostRoot string, dir string, limits SnapshotLimits) (Snapshot, error) {
	root, err := os.OpenRoot(hostRoot)
	if err != nil {
		return Snapshot{}, err
	}
	defer root.Close()

	var entries []snapshotEntry
	var total int64
	files := 0

	walkErr := fs.WalkDir(root.FS(), ".", func(rel string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if entry.IsDir() && skippedSnapshotDirs[entry.Name()] {
			return fs.SkipDir
		}

		target := filepath.Join(dir, filepath.FromSlash(rel))
		info, err := root.Lstat(rel)
		if err != nil {
			return err
		}

		mode := info.Mode()
		if mode&fs.ModeSymlink != 0 {
			resolved, err := root.Stat(rel)
			if err != nil {
				return fmt.Errorf("symlink %s points outside the bundle or nowhere: %w", rel, err)
			}
			if resolved.IsDir() {
				return fmt.Errorf("symlink %s points to a directory; directory symlinks are not supported in bundles", rel)
			}
			mode = resolved.Mode()
		}

		switch {
		case mode.IsDir():
			entries = append(entries, snapshotEntry{rel: rel, mode: "dir"})
			return os.Mkdir(target, 0o700)
		case mode.IsRegular():
			files++
			if files > limits.MaxFiles {
				return fmt.Errorf("bundle has more than %d files", limits.MaxFiles)
			}
			hash, size, err := copyRootFile(root, rel, target, limits.MaxBytes-total)
			if err != nil {
				return err
			}
			total += size
			fileMode := "file"
			if mode.Perm()&0o111 != 0 {
				fileMode = "exec"
			}
			entries = append(entries, snapshotEntry{rel: rel, mode: fileMode, hash: hash})
			if fileMode == "exec" && runtime.GOOS != "windows" {
				return os.Chmod(target, 0o700)
			}
			return nil
		default:
			return fmt.Errorf("%s is not a regular file or directory", rel)
		}
	})
	if walkErr != nil {
		return Snapshot{}, walkErr
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })
	treeHash := sha256.New()
	for _, entry := range entries {
		fmt.Fprintf(treeHash, "%s\x00%s\x00%s\n", entry.rel, entry.mode, entry.hash)
	}
	return Snapshot{TreeHash: hex.EncodeToString(treeHash.Sum(nil)), Files: files, Bytes: total}, nil
}

func copyRootFile(root *os.Root, rel string, target string, budget int64) (string, int64, error) {
	src, err := root.Open(rel)
	if err != nil {
		return "", 0, err
	}
	defer src.Close()
	// #nosec G304 -- target is inside the gateway-owned staging directory.
	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	limited := io.LimitReader(src, budget+1)
	size, copyErr := io.Copy(io.MultiWriter(dst, hash), limited)
	closeErr := dst.Close()
	if copyErr != nil {
		return "", 0, copyErr
	}
	if closeErr != nil {
		return "", 0, closeErr
	}
	if size > budget {
		return "", 0, errors.New("bundle exceeds the snapshot size limit")
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func makeTreeReadOnly(dir string) error {
	// Walk bottom-up order is not needed: directories stay traversable (0500).
	return filepath.WalkDir(dir, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.Chmod(p, 0o500)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o100 != 0 {
			return os.Chmod(p, 0o500)
		}
		return os.Chmod(p, 0o400)
	})
}

// RemoveSnapshot restores write permission and removes a snapshot directory.
func RemoveSnapshot(dir string) error {
	if dir == "" {
		return nil
	}
	_ = filepath.WalkDir(dir, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			_ = os.Chmod(p, 0o700)
		} else {
			_ = os.Chmod(p, 0o600)
		}
		return nil
	})
	return os.RemoveAll(dir)
}

// RewriteBundlePaths replaces absolute agent paths inside the bundle with the matching
// snapshot paths, including the value part of --flag=value and @file arguments.
// Relative paths need no rewrite because approved commands run inside the snapshot.
func RewriteBundlePaths(args []string, snapshot Snapshot) []string {
	out := make([]string, len(args))
	for i, arg := range args {
		out[i] = rewriteBundleArg(arg, snapshot)
	}
	return out
}

func rewriteBundleArg(arg string, snapshot Snapshot) string {
	prefix := ""
	value := arg
	if strings.HasPrefix(arg, "-") {
		eq := strings.Index(arg, "=")
		if eq < 0 {
			return arg
		}
		prefix, value = arg[:eq+1], arg[eq+1:]
	}
	if strings.HasPrefix(value, "@") {
		prefix += "@"
		value = value[1:]
	}
	if !strings.HasPrefix(value, "/") {
		return arg
	}
	cleaned := path.Clean(value)
	if cleaned != snapshot.AgentRoot && !strings.HasPrefix(cleaned, snapshot.AgentRoot+"/") {
		return arg
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(cleaned, snapshot.AgentRoot), "/")
	return prefix + filepath.Join(snapshot.Dir, filepath.FromSlash(rel))
}

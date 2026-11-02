package selfupdate

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func Executable() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

// Installation classifies the resolved path, including Homebrew's symlink targets.
func Installation(executable, version string) string {
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	p := "/" + strings.Trim(filepath.ToSlash(executable), "/") + "/"
	if strings.Contains(strings.ToLower(p), "/cellar/") {
		return "homebrew"
	}
	if strings.Contains(strings.ToLower(p), "/scoop/") || strings.Contains(strings.ToLower(p), "/winget/") {
		return "package-manager"
	}
	if !ValidVersion(version) {
		return "development"
	}
	return "standalone"
}

// Writable probes the directory before making any release requests, without sudo.
func Writable(executable string) error {
	info, err := os.Stat(executable)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("update target must be a regular file")
	}
	dir := filepath.Dir(executable)
	info, err = os.Stat(dir)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0222 == 0 {
		return fmt.Errorf("%s is not writable; rerun the installer into a user-owned directory", dir)
	}
	f, err := os.CreateTemp(dir, ".sailwright-write-*")
	if err != nil {
		return fmt.Errorf("%s is not writable (no automatic sudo): %w", dir, err)
	}
	name := f.Name()
	err = f.Close()
	_ = os.Remove(name)
	return err
}

type Replacement struct {
	Path string
	Data []byte
}

// Replace stages all verified binaries beside their targets before changing anything.
// Unix renames each binary atomically. Windows keeps the previous executable as .old.
// If any rename fails, completed changes are rolled back.
func Replace(items []Replacement) error {
	return replace(items, os.Rename, os.Link)
}

func replace(items []Replacement, rename, link func(string, string) error) (result error) {
	if len(items) == 0 {
		return nil
	}
	unlock, err := updateLock(items[0].Path)
	if err != nil {
		return err
	}
	defer unlock()
	type staged struct {
		target, temp, backup string
		backed, installed    bool
	}
	stages := make([]staged, len(items))
	defer func() {
		for i := len(stages) - 1; i >= 0; i-- {
			s := &stages[i]
			if result != nil && s.backed {
				if runtime.GOOS == "windows" && s.installed {
					_ = os.Remove(s.target)
				}
				if err := rename(s.backup, s.target); err != nil {
					result = errors.Join(result, fmt.Errorf("restore %s from %s: %w", s.target, s.backup, err))
					s.backup = ""
				}
			}
			if s.temp != "" {
				_ = os.Remove(s.temp)
			}
			if s.backup != "" && (result != nil || runtime.GOOS != "windows") {
				_ = os.Remove(s.backup)
			}
		}
	}()
	for i, item := range items {
		info, err := os.Stat(item.Path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular binary", item.Path)
		}
		s := &stages[i]
		s.target = item.Path
		f, err := os.CreateTemp(filepath.Dir(item.Path), ".sailwright-new-*")
		if err != nil {
			return err
		}
		s.temp = f.Name()
		_, writeErr := f.Write(item.Data)
		chmodErr := f.Chmod(info.Mode().Perm())
		syncErr := f.Sync()
		closeErr := f.Close()
		if err := errors.Join(writeErr, chmodErr, syncErr, closeErr); err != nil {
			return err
		}
		backup := item.Path + ".old"
		if _, err := os.Lstat(backup); err == nil {
			if runtime.GOOS == "windows" {
				return fmt.Errorf("previous backup exists at %s; stop the gateway and restart the CLI to clean it up", backup)
			}
			return fmt.Errorf("previous backup exists at %s; recover any interrupted update, then delete this backup file and retry", backup)
		} else if !os.IsNotExist(err) {
			return err
		}
		s.backup = backup
		if runtime.GOOS != "windows" {
			// A hard link preserves the old inode, including a running executable.
			// Filesystems without hard links (exFAT, some FUSE/network mounts) get a copy.
			if err := link(s.target, s.backup); err != nil {
				if copyErr := copyFile(s.target, s.backup, info.Mode().Perm()); copyErr != nil {
					return fmt.Errorf("back up %s: %w", s.target, errors.Join(err, copyErr))
				}
			}
			s.backed = true
		}
	}
	if runtime.GOOS == "windows" {
		// Reserve all old paths first. An in-use gateway that cannot be renamed aborts
		// here, before installing either new binary; the deferred rollback restores it.
		for i := range stages {
			s := &stages[i]
			if err := rename(s.target, s.backup); err != nil {
				return fmt.Errorf("stop sailwright-gateway and retry: rename %s: %w", s.target, err)
			}
			s.backed = true
		}
	}
	for i := range stages {
		s := &stages[i]
		if err := rename(s.temp, s.target); err != nil {
			return fmt.Errorf("replace %s: %w", s.target, err)
		}
		s.installed = true
	}
	return nil
}

// copyFile creates dst exclusively and removes it again if copying fails.
func copyFile(src, dst string, perm os.FileMode) (err error) {
	in, err := os.Open(src) // #nosec G304 -- path derived from os.Executable(), not from untrusted input.
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm) // #nosec G304 -- path derived from os.Executable(), not from untrusted input.
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(dst)
		}
	}()
	_, copyErr := io.Copy(out, in)
	return errors.Join(copyErr, out.Sync(), out.Close())
}

// Cleanup removes Windows backups on a subsequent invocation. In-use files stay put.
func Cleanup(executable string) {
	if runtime.GOOS != "windows" || executable == "" {
		return
	}
	unlock, err := updateLock(executable)
	if err != nil {
		return
	}
	defer unlock()
	for _, name := range []string{executable + ".old", filepath.Join(filepath.Dir(executable), "sailwright-gateway.exe.old")} {
		_ = os.Remove(name)
	}
}

// updateLock keeps a persistent lock file: deleting it could let another process
// lock a different inode. The OS releases the lock even if the updater is killed.
func updateLock(executable string) (func(), error) {
	lock := executable + ".update-lock"
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_RDWR, 0600) // #nosec G304 -- path derived from os.Executable(), not from untrusted input.
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another update is running or cannot lock %s: %w", lock, err)
	}
	return func() { _ = f.Close() }, nil
}

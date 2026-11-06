package oci

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// errPullRollbackFailed means a failed promotion could not restore every previous
// artifact, so the local set may mix pulled and previous files.
var errPullRollbackFailed = errors.New("rollback of pulled artifacts failed")

// errPullBackupCleanup means the pulled artifacts were promoted, but backups of the
// replaced ones could not be removed. The next build removes them as leftovers.
var errPullBackupCleanup = errors.New("remove OCI pull backups")

type artifactReplacement struct {
	finalPath string
	backup    string
	replaced  bool
}

func promotePulledArtifacts(stagingRoot string, files []ArtifactFile) error {
	replacements := make([]artifactReplacement, 0, len(files))
	for _, file := range files {
		stagedPath := filepath.Join(stagingRoot, filepath.FromSlash(file.Name))
		if _, err := os.Stat(stagedPath); err != nil {
			if rollbackErr := rollbackPulledArtifacts(replacements); rollbackErr != nil {
				return fmt.Errorf("inspect pulled artifact %s: %w; rollback failed: %w", stagedPath, err, rollbackErr)
			}
			if os.IsNotExist(err) {
				return fmt.Errorf("pulled artifact %s is missing from OCI staging directory", file.Name)
			}
			return fmt.Errorf("inspect pulled artifact %s: %w", stagedPath, err)
		}

		if err := os.MkdirAll(filepath.Dir(file.Path), 0o700); err != nil {
			if rollbackErr := rollbackPulledArtifacts(replacements); rollbackErr != nil {
				return fmt.Errorf("create artifact directory for %s: %w; rollback failed: %w", file.Path, err, rollbackErr)
			}
			return fmt.Errorf("create artifact directory for %s: %w", file.Path, err)
		}

		replacement := artifactReplacement{finalPath: file.Path}
		if _, err := os.Lstat(file.Path); err == nil {
			replacement.backup = fmt.Sprintf("%s.dev-alchemy-oci-backup-%d", file.Path, time.Now().UnixNano())
			if err := os.Rename(file.Path, replacement.backup); err != nil {
				if rollbackErr := rollbackPulledArtifacts(replacements); rollbackErr != nil {
					return fmt.Errorf("back up existing artifact %s: %w; rollback failed: %w", file.Path, err, rollbackErr)
				}
				return fmt.Errorf("back up existing artifact %s: %w", file.Path, err)
			}
			replacement.replaced = true
		} else if err != nil && !os.IsNotExist(err) {
			if rollbackErr := rollbackPulledArtifacts(replacements); rollbackErr != nil {
				return fmt.Errorf("inspect existing artifact %s: %w; rollback failed: %w", file.Path, err, rollbackErr)
			}
			return fmt.Errorf("inspect existing artifact %s: %w", file.Path, err)
		}

		replacements = append(replacements, replacement)
		if err := os.Rename(stagedPath, file.Path); err != nil {
			if rollbackErr := rollbackPulledArtifacts(replacements); rollbackErr != nil {
				return fmt.Errorf("promote pulled artifact %s to %s: %w; rollback failed: %w", stagedPath, file.Path, err, rollbackErr)
			}
			return fmt.Errorf("promote pulled artifact %s to %s: %w", stagedPath, file.Path, err)
		}
	}

	var cleanupErrs []error
	for _, replacement := range replacements {
		if replacement.replaced {
			if err := os.RemoveAll(replacement.backup); err != nil {
				cleanupErrs = append(cleanupErrs, fmt.Errorf("remove OCI pull backup %s: %w", replacement.backup, err))
			}
		}
	}
	if len(cleanupErrs) > 0 {
		return fmt.Errorf("%w: %w", errPullBackupCleanup, errors.Join(cleanupErrs...))
	}
	return nil
}

func rollbackPulledArtifacts(replacements []artifactReplacement) error {
	var rollbackErrs []error
	for i := len(replacements) - 1; i >= 0; i-- {
		replacement := replacements[i]
		if err := os.RemoveAll(replacement.finalPath); err != nil && !os.IsNotExist(err) {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("remove partially pulled artifact %s: %w", replacement.finalPath, err))
			continue
		}
		if replacement.replaced {
			if err := os.Rename(replacement.backup, replacement.finalPath); err != nil && !os.IsNotExist(err) {
				rollbackErrs = append(rollbackErrs, fmt.Errorf("restore artifact backup %s: %w", replacement.backup, err))
			}
		}
	}
	if len(rollbackErrs) > 0 {
		return fmt.Errorf("%w: %w", errPullRollbackFailed, errors.Join(rollbackErrs...))
	}
	return nil
}

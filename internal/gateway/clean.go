package gateway

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// CacheDir is one gateway-owned cache directory and the bytes it uses.
type CacheDir struct {
	Path  string
	Bytes int64
}

// CacheUsage is what the gateway may delete without touching created VMs, its config
// or a cache it shares with the operator's own sailwright.
type CacheUsage struct {
	Dirs  []CacheDir
	Total int64
}

// CleanableDirs returns the cache and Packer cache below the data dir. A shared cache
// dir belongs to the operator's own sailwright and is never cleaned. VM state
// (.vagrant, libvirt images) and the isolated config are never included.
func CleanableDirs(cfg Config) []string {
	dirs := NewManagedDirs(cfg)
	var cleanable []string
	for _, dir := range []string{dirs.Cache, dirs.PackerCache} {
		if cfg.SharedCacheDir != "" && filepath.Clean(dir) == filepath.Clean(cfg.SharedCacheDir) {
			continue
		}
		if !isWithin(cfg.DataDir, dir) || filepath.Clean(dir) == filepath.Clean(cfg.DataDir) {
			continue
		}
		cleanable = append(cleanable, dir)
	}
	return cleanable
}

// MeasureCache sums the size of the regular files in the cleanable dirs. Symlinks
// are not followed.
func MeasureCache(cfg Config) (CacheUsage, error) {
	var usage CacheUsage
	for _, dir := range CleanableDirs(cfg) {
		size, err := dirSize(dir)
		if err != nil {
			return CacheUsage{}, err
		}
		usage.Dirs = append(usage.Dirs, CacheDir{Path: dir, Bytes: size})
		usage.Total += size
	}
	return usage, nil
}

func dirSize(dir string) (int64, error) {
	var size int64
	err := filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			size += info.Size()
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("measure %s: %w", dir, err)
	}
	return size, nil
}

// ClearCache deletes the contents of the cleanable dirs and keeps the dirs themselves.
// It continues after errors (for example files still in use on Windows) and returns
// them joined.
func ClearCache(cfg Config) error {
	var errs []error
	for _, dir := range CleanableDirs(cfg) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
			}
			continue
		}
		for _, entry := range entries {
			if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// CleanPromptThreshold returns the cache size from which the gateway offers to clean
// on Ctrl+C, or 0 if the prompt is disabled.
func (c Config) CleanPromptThreshold() int64 {
	if c.CleanPromptMinGB < 0 {
		return 0
	}
	return int64(c.CleanPromptMinGB) * diskBytesPerGB
}

// FormatGB renders a byte count for the operator.
func FormatGB(bytes int64) string {
	return fmt.Sprintf("%.1f GB", float64(bytes)/diskBytesPerGB)
}

// CleanQuestion describes the usage and asks whether to delete it.
func (u CacheUsage) CleanQuestion() string {
	var b strings.Builder
	fmt.Fprintf(&b, "The gateway cache uses %s:\n", FormatGB(u.Total))
	for _, dir := range u.Dirs {
		fmt.Fprintf(&b, "  %-10s %s\n", FormatGB(dir.Bytes), dir.Path)
	}
	b.WriteString("Delete it? Created VMs are not affected; the next build downloads and builds again.")
	return b.String()
}

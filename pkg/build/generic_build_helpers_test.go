package build

import (
	"path/filepath"
	"testing"
)

// useTestDirectories points the managed directories at a temporary tree so that
// RunBuildScript does not touch the real cache or extract the embedded project.
func useTestDirectories(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	previous := directoriesInstance
	directoriesInstance = &Directories{
		WorkingDir:     root,
		AppDataDir:     root,
		ProjectDir:     root,
		ConfigDir:      filepath.Join(root, "config"),
		CacheDir:       filepath.Join(root, "cache"),
		VagrantDir:     filepath.Join(root, "vagrant"),
		PackerCacheDir: filepath.Join(root, "packer_cache"),
	}
	directoriesInstance.GetDirectories()
	t.Cleanup(func() { directoriesInstance = previous })
	return root
}

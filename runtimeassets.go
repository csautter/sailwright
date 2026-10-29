package runtimeassets

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
)

// EmbeddedFiles contains the repo assets that runtime Go code and the scripts it invokes
// expect to exist on disk when running outside a git checkout.
//
//go:embed ansible.cfg playbooks inventory roles roles_test_1 roles_test_2 build/packer deployments/vagrant deployments/utm scripts/linux scripts/macos scripts/windows go.mod
var EmbeddedFiles embed.FS

// FS returns the embedded runtime asset filesystem.
func FS() fs.FS {
	return EmbeddedFiles
}

// ManifestHash returns a stable digest for the embedded runtime asset set.
func ManifestHash() (string, error) {
	hash := sha256.New()

	if err := fs.WalkDir(EmbeddedFiles, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		content, err := fs.ReadFile(EmbeddedFiles, path)
		if err != nil {
			return err
		}

		if _, err := hash.Write([]byte(path)); err != nil {
			return err
		}
		if _, err := hash.Write([]byte{0}); err != nil {
			return err
		}
		if _, err := hash.Write(content); err != nil {
			return err
		}
		if _, err := hash.Write([]byte{0}); err != nil {
			return err
		}

		return nil
	}); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

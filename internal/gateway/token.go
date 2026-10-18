package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const tokenBytes = 32

var (
	envNamePattern   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	sha256HexPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
	tokenPattern     = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// TokenPath returns the bearer token file inside the state dir.
func TokenPath(stateDir string) string {
	return filepath.Join(stateDir, tokenFileName)
}

// LoadOrCreateToken returns the bearer token, creating a new random one when missing.
func LoadOrCreateToken(stateDir string) (string, error) {
	token, err := LoadToken(stateDir)
	if err == nil {
		return token, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return RotateToken(stateDir)
}

// LoadToken reads the bearer token and refuses files that others can read.
func LoadToken(stateDir string) (string, error) {
	path := TokenPath(stateDir)
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("token file %s is accessible by other users (mode %s); run `chmod 600 %s` or rotate the token", path, info.Mode().Perm(), path)
	}
	// #nosec G304 -- fixed file inside the operator's state directory.
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if !tokenPattern.MatchString(token) {
		return "", fmt.Errorf("token file %s is malformed; rotate the token", path)
	}
	return token, nil
}

// RotateToken writes a new random bearer token and returns it.
func RotateToken(stateDir string) (string, error) {
	if err := ensurePrivateDir(stateDir); err != nil {
		return "", fmt.Errorf("create state dir: %w", err)
	}
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	token := hex.EncodeToString(raw)
	if err := writePrivateFileAtomic(TokenPath(stateDir), []byte(token+"\n")); err != nil {
		return "", fmt.Errorf("write token: %w", err)
	}
	return token, nil
}

func writePrivateFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(privateFilePermission); err != nil && runtime.GOOS != "windows" {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

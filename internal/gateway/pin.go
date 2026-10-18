package gateway

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const pinnedBinaryPermission = 0o500

// PinnedBinary is the gateway-owned copy of the sailwright binary under test.
// Only this copy is ever executed, so changing the source after startup has no effect.
type PinnedBinary struct {
	Source string
	Path   string
	SHA256 string
	Size   int64
}

// HashFile returns the SHA-256 of a file as lowercase hex.
func HashFile(path string) (string, int64, error) {
	// #nosec G304 -- path is the operator-selected binary or its pinned copy.
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

// PinBinary copies source into <stateDir>/bin and returns the pinned copy. When
// expectedSHA256 is set, the source must match it.
func PinBinary(source string, stateDir string, expectedSHA256 string) (PinnedBinary, error) {
	if strings.TrimSpace(source) == "" {
		return PinnedBinary{}, errors.New("no sailwright binary configured; pass --binary or set binary in gateway.yaml")
	}
	absSource, err := filepath.Abs(source)
	if err != nil {
		return PinnedBinary{}, err
	}
	info, err := os.Stat(absSource)
	if err != nil {
		return PinnedBinary{}, fmt.Errorf("inspect binary: %w", err)
	}
	if !info.Mode().IsRegular() {
		return PinnedBinary{}, fmt.Errorf("binary %s is not a regular file", absSource)
	}

	binDir := filepath.Join(stateDir, "bin")
	if err := ensurePrivateDir(binDir); err != nil {
		return PinnedBinary{}, fmt.Errorf("create pinned binary dir: %w", err)
	}
	name := "sailwright"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	target := filepath.Join(binDir, name)

	tmp, err := os.CreateTemp(binDir, ".pin-*")
	if err != nil {
		return PinnedBinary{}, err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	// #nosec G304 -- operator-selected binary.
	src, err := os.Open(absSource)
	if err != nil {
		_ = tmp.Close()
		return PinnedBinary{}, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(tmp, hash), src)
	_ = src.Close()
	closeErr := tmp.Close()
	if copyErr != nil {
		return PinnedBinary{}, fmt.Errorf("copy binary: %w", copyErr)
	}
	if closeErr != nil {
		return PinnedBinary{}, closeErr
	}
	sum := hex.EncodeToString(hash.Sum(nil))
	if expectedSHA256 != "" && sum != expectedSHA256 {
		return PinnedBinary{}, fmt.Errorf("binary %s has SHA-256 %s, expected %s", absSource, sum, expectedSHA256)
	}

	// #nosec G302 -- owner-only, read-and-execute pinned binary.
	if err := os.Chmod(tmpName, pinnedBinaryPermission); err != nil && runtime.GOOS != "windows" {
		return PinnedBinary{}, err
	}
	if runtime.GOOS == "windows" {
		// Rename cannot replace a read-only file on Windows.
		_ = os.Chmod(target, 0o600)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return PinnedBinary{}, fmt.Errorf("install pinned binary: %w", err)
	}

	pinned := PinnedBinary{Source: absSource, Path: target, SHA256: sum, Size: size}
	if err := pinned.Verify(); err != nil {
		return PinnedBinary{}, err
	}
	return pinned, nil
}

// Verify re-hashes the pinned copy and fails if it changed since pinning.
func (p PinnedBinary) Verify() error {
	sum, _, err := HashFile(p.Path)
	if err != nil {
		return fmt.Errorf("verify pinned binary: %w", err)
	}
	if sum != p.SHA256 {
		return fmt.Errorf("pinned binary %s changed (SHA-256 %s, expected %s); restart the gateway", p.Path, sum, p.SHA256)
	}
	return nil
}

// ConfirmPin asks the operator on the terminal to confirm the binary hash. The reader
// is shared with the approval console so no buffered input is lost.
func ConfirmPin(pinned PinnedBinary, in *bufio.Reader, out io.Writer) (bool, error) {
	fmt.Fprintf(out, "\nsailwright binary to pin\n  source: %s\n  size:   %d bytes\n  sha256: %s\n\nOnly this exact binary will be executed for agents. Pin it? [y/N]: ",
		pinned.Source, pinned.Size, pinned.SHA256)
	line, err := in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

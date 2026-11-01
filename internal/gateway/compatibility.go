package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

type stateMetadata struct {
	SchemaVersion int    `json:"schema_version"`
	LockVersion   int    `json:"lock_version"`
	Version       string `json:"version"`
}

// Versions of the CLI state contract this gateway understands.
const (
	supportedStateSchema = 1
	supportedLockVersion = 2
)

// CheckCompatibility checks the state contract before any shared-state command.
// The pinned CLI must report a compatible contract in shared mode. The installed
// `sailwright` on PATH is only probed for comparison, with installedEnv (which
// must not carry passthrough secrets): a missing contract there is a warning, an
// incompatible reported contract is an error in shared mode.
func CheckCompatibility(ctx context.Context, pinned PinnedBinary, dirs ManagedDirs, env, installedEnv []string, shared bool, out io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	read := func(binary PinnedBinary, env []string) (stateMetadata, error) {
		var output bytes.Buffer
		code, err := (&Runner{Binary: binary, Env: env}).Run(ctx, []string{"--gateway-metadata"}, dirs.Work, &output)
		if err != nil || code != 0 {
			return stateMetadata{}, fmt.Errorf("%s does not report a state contract (exit %d): %v", binary.Path, code, err)
		}
		var metadata stateMetadata
		if err := json.Unmarshal(output.Bytes(), &metadata); err != nil {
			return metadata, fmt.Errorf("invalid CLI state contract from %s: %w", binary.Path, err)
		}
		return metadata, nil
	}
	metadata, err := read(pinned, env)
	if err != nil {
		if shared {
			return err
		}
		fmt.Fprintln(out, "warning:", err)
		return nil
	}
	if shared && (metadata.SchemaVersion != supportedStateSchema || metadata.LockVersion != supportedLockVersion) {
		return fmt.Errorf("incompatible pinned CLI state schema or locking protocol; use a compatible binary or state_isolation: isolated")
	}
	installed, err := exec.LookPath("sailwright")
	if err != nil {
		fmt.Fprintln(out, "warning: installed sailwright CLI not found; version comparison unavailable")
		return nil
	}
	if sameFile(installed, pinned.Source) || sameFile(installed, pinned.Path) {
		return nil
	}
	hash, size, err := HashFile(installed)
	if err != nil {
		return err
	}
	other, err := read(PinnedBinary{Path: installed, SHA256: hash, Size: size}, installedEnv)
	if err != nil {
		if shared {
			fmt.Fprintf(out, "warning: %v\nwarning: that CLI predates cross-process locking; do not run it while gateway jobs use the shared state, or update it\n", err)
		} else {
			fmt.Fprintln(out, "warning:", err)
		}
		return nil
	}
	if metadata.Version != other.Version {
		fmt.Fprintf(out, "warning: pinned CLI version %s differs from installed CLI %s\n", metadata.Version, other.Version)
	}
	if shared && (metadata.SchemaVersion != other.SchemaVersion || other.LockVersion != supportedLockVersion) {
		return fmt.Errorf("installed and pinned CLI have incompatible state contracts")
	}
	return nil
}

func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	left, err := os.Stat(a)
	if err != nil {
		return false
	}
	right, err := os.Stat(b)
	return err == nil && os.SameFile(left, right)
}

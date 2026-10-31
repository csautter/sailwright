package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"time"
)

type stateMetadata struct {
	SchemaVersion int    `json:"schema_version"`
	LockVersion   int    `json:"lock_version"`
	Version       string `json:"version"`
}

// CheckCompatibility checks the state contract before any shared-state command.
func CheckCompatibility(ctx context.Context, pinned PinnedBinary, dirs ManagedDirs, env []string, shared bool, out io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	read := func(binary PinnedBinary) (stateMetadata, error) {
		var output bytes.Buffer
		code, err := (&Runner{Binary: binary, Env: env}).Run(ctx, []string{"--gateway-metadata"}, dirs.Work, &output)
		if err != nil || code != 0 {
			return stateMetadata{}, fmt.Errorf("CLI does not report a state contract (exit %d): %v", code, err)
		}
		var metadata stateMetadata
		if err := json.Unmarshal(output.Bytes(), &metadata); err != nil {
			return metadata, fmt.Errorf("invalid CLI state contract: %w", err)
		}
		return metadata, nil
	}
	metadata, err := read(pinned)
	if err != nil {
		if shared {
			return err
		}
		fmt.Fprintln(out, "warning:", err)
		return nil
	}
	if shared && (metadata.SchemaVersion != 1 || metadata.LockVersion != 1) {
		return fmt.Errorf("incompatible pinned CLI state schema or locking protocol; use a compatible binary or state_isolation: isolated")
	}
	installed, err := exec.LookPath("sailwright")
	if err != nil {
		fmt.Fprintln(out, "warning: installed sailwright CLI not found; version comparison unavailable")
		return nil
	}
	hash, size, err := HashFile(installed)
	if err != nil {
		return err
	}
	other, err := read(PinnedBinary{Path: installed, SHA256: hash, Size: size})
	if err != nil {
		if shared {
			return fmt.Errorf("installed CLI cannot safely share state: %w", err)
		}
		fmt.Fprintln(out, "warning:", err)
		return nil
	}
	if metadata.Version != other.Version {
		fmt.Fprintf(out, "warning: pinned CLI version %s differs from installed CLI %s\n", metadata.Version, other.Version)
	}
	if shared && (metadata.SchemaVersion != other.SchemaVersion || other.LockVersion != 1) {
		return fmt.Errorf("installed and pinned CLI have incompatible state contracts")
	}
	return nil
}

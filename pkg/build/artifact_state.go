package build

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
)

// ArtifactState describes whether a build artifact in the cache can be used.
type ArtifactState string

const (
	// ArtifactStateExists means the artifact was completed by a successful build or pull.
	ArtifactStateExists ArtifactState = "exists"
	// ArtifactStateUnverified means the artifact has no completion marker, e.g. because
	// an older sailwright version produced it. It is still used, see legacyArtifactState.
	ArtifactStateUnverified ArtifactState = "unverified"
	// ArtifactStateIncomplete means a build or pull did not finish: the completion marker
	// does not match the artifact, or only staging/backup files of an interrupted build
	// are left.
	ArtifactStateIncomplete ArtifactState = "incomplete"
	// ArtifactStateMissing means there is no artifact.
	ArtifactStateMissing ArtifactState = "missing"
)

// Usable reports whether builds may skip the artifact and VM commands may use it.
func (s ArtifactState) Usable() bool {
	return s == ArtifactStateExists || s == ArtifactStateUnverified
}

const (
	// artifactCompletionMarkerSuffix is appended to the artifact path to name its
	// completion marker. The marker is written after the artifact reached its final path.
	artifactCompletionMarkerSuffix = ".sailwright-complete"
	artifactCompletionMarkerFormat = 1

	stagedBuildArtifactInfix   = ".dev-alchemy-build-"
	buildArtifactBackupInfix   = ".dev-alchemy-backup-"
	replaceArtifactBackupInfix = ".dev-alchemy-replace-backup-"
	ociPullArtifactBackupInfix = ".dev-alchemy-oci-backup-"
)

// artifactCompletionMarker is the JSON content of a completion marker.
type artifactCompletionMarker struct {
	Format            int       `json:"format"`
	CompletedAt       time.Time `json:"completed_at"`
	SailwrightVersion string    `json:"sailwright_version,omitempty"`
	// Size is the artifact size in bytes; -1 for directories, which are not checked.
	Size int64 `json:"size"`
}

func artifactCompletionMarkerPath(artifact string) string {
	return artifact + artifactCompletionMarkerSuffix
}

// legacyArtifactState decides about an artifact without a completion marker. Builds
// promote staged artifacts with an atomic rename, so the final path never holds a
// partial artifact from this version; an artifact without a marker comes from an older
// sailwright version, a cache restore or a manual copy. Treating it as unverified but
// usable avoids rebuilding every existing image after an update. Make this return
// ArtifactStateIncomplete to force a rebuild instead.
func legacyArtifactState(string) ArtifactState {
	return ArtifactStateUnverified
}

// BuildArtifactState returns the combined state of the build artifacts of config.
func BuildArtifactState(config VirtualMachineConfig) (ArtifactState, error) {
	artifacts, err := resolveExpectedBuildArtifacts(config)
	if err != nil {
		return ArtifactStateMissing, err
	}
	return artifactsState(artifacts)
}

// artifactsState combines the states of several artifacts: one incomplete artifact
// makes the set incomplete, one missing artifact makes it missing.
func artifactsState(artifacts []string) (ArtifactState, error) {
	if len(artifacts) == 0 {
		return ArtifactStateMissing, errors.New("no build artifacts defined")
	}
	rank := map[ArtifactState]int{
		ArtifactStateExists:     0,
		ArtifactStateUnverified: 1,
		ArtifactStateMissing:    2,
		ArtifactStateIncomplete: 3,
	}
	combined := ArtifactStateExists
	for _, artifact := range artifacts {
		state, err := artifactState(artifact)
		if err != nil {
			return ArtifactStateMissing, err
		}
		if rank[state] > rank[combined] {
			combined = state
		}
	}
	return combined, nil
}

func artifactState(artifact string) (ArtifactState, error) {
	info, err := os.Stat(artifact)
	if os.IsNotExist(err) {
		leftovers, err := buildArtifactLeftovers(artifact)
		if err != nil {
			return ArtifactStateMissing, err
		}
		if len(leftovers) > 0 {
			return ArtifactStateIncomplete, nil
		}
		return ArtifactStateMissing, nil
	}
	if err != nil {
		return ArtifactStateMissing, fmt.Errorf("inspect build artifact %s: %w", artifact, err)
	}

	marker, err := readArtifactCompletionMarker(artifact)
	if os.IsNotExist(err) {
		return legacyArtifactState(artifact), nil
	}
	if err != nil {
		return ArtifactStateIncomplete, nil
	}
	if marker.Size >= 0 && (info.IsDir() || marker.Size != info.Size()) {
		return ArtifactStateIncomplete, nil
	}
	return ArtifactStateExists, nil
}

func readArtifactCompletionMarker(artifact string) (artifactCompletionMarker, error) {
	var marker artifactCompletionMarker
	// #nosec G304 -- the marker path is derived from a managed build artifact path.
	data, err := os.ReadFile(artifactCompletionMarkerPath(artifact))
	if err != nil {
		return marker, err
	}
	if err := json.Unmarshal(data, &marker); err != nil {
		return marker, fmt.Errorf("parse completion marker for %s: %w", artifact, err)
	}
	if marker.Format != artifactCompletionMarkerFormat {
		return marker, fmt.Errorf("unsupported completion marker format %d for %s", marker.Format, artifact)
	}
	return marker, nil
}

// MarkBuildArtifactsComplete writes the completion markers of artifacts that reached
// their final path, e.g. after a successful build or OCI pull.
func MarkBuildArtifactsComplete(artifacts []string) error {
	var errs []error
	for _, artifact := range artifacts {
		if err := writeArtifactCompletionMarker(artifact); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func writeArtifactCompletionMarker(artifact string) error {
	info, err := os.Stat(artifact)
	if err != nil {
		return fmt.Errorf("inspect completed build artifact %s: %w", artifact, err)
	}
	size := info.Size()
	if info.IsDir() {
		size = -1
	}
	data, err := json.MarshalIndent(artifactCompletionMarker{
		Format:            artifactCompletionMarkerFormat,
		CompletedAt:       time.Now().UTC(),
		SailwrightVersion: sailwrightVersion(),
		Size:              size,
	}, "", "  ")
	if err != nil {
		return err
	}
	markerPath := artifactCompletionMarkerPath(artifact)
	tmpPath := markerPath + ".tmp"
	// #nosec G306 -- the marker holds no secrets and must stay readable for non-root CI steps after sudo-created builds.
	if err := os.WriteFile(tmpPath, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write completion marker for %s: %w", artifact, err)
	}
	if err := os.Rename(tmpPath, markerPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write completion marker for %s: %w", artifact, err)
	}
	return nil
}

// RemoveBuildArtifactCompletionMarkers removes the completion markers of artifacts,
// e.g. before their final path is replaced.
func RemoveBuildArtifactCompletionMarkers(artifacts []string) error {
	var errs []error
	for _, artifact := range artifacts {
		if err := os.Remove(artifactCompletionMarkerPath(artifact)); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove completion marker for %s: %w", artifact, err))
		}
	}
	return errors.Join(errs...)
}

// buildArtifactLeftovers returns staging and backup files that interrupted builds or
// pulls left next to artifact.
func buildArtifactLeftovers(artifact string) ([]string, error) {
	dir := filepath.Dir(artifact)
	base := filepath.Base(artifact)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)

	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect build artifact directory %s: %w", dir, err)
	}
	var leftovers []string
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case strings.HasPrefix(name, stem+stagedBuildArtifactInfix) && strings.HasSuffix(name, ext):
		case strings.HasPrefix(name, base+buildArtifactBackupInfix),
			strings.HasPrefix(name, base+replaceArtifactBackupInfix),
			strings.HasPrefix(name, base+ociPullArtifactBackupInfix):
		default:
			continue
		}
		if strings.HasSuffix(name, artifactCompletionMarkerSuffix) {
			continue
		}
		leftovers = append(leftovers, filepath.Join(dir, name))
	}
	return leftovers, nil
}

// removeBuildArtifactLeftovers removes staging and backup files of earlier interrupted
// builds before a new build of the same artifacts starts. Two concurrent builds of the
// same target are not supported anyway: they share the Packer output directory.
func removeBuildArtifactLeftovers(artifacts []string) error {
	var errs []error
	for _, artifact := range artifacts {
		leftovers, err := buildArtifactLeftovers(artifact)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, leftover := range leftovers {
			log.Printf("Removing leftover file of an interrupted build: %s", leftover)
			if err := os.RemoveAll(leftover); err != nil {
				errs = append(errs, fmt.Errorf("remove leftover build file %s: %w", leftover, err))
			}
		}
	}
	return errors.Join(errs...)
}

func sailwrightVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return ""
	}
	return info.Main.Version
}

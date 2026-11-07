package build

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
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

	// The infixes keep the old dev-alchemy name so that leftovers of earlier versions
	// are still recognised. buildArtifactBackupInfix is no longer written: older
	// versions backed up artifacts that unstaged builds overwrote in place.
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
	// Pending marks an artifact whose set is being replaced by a build or pull. It
	// keeps a set that was only partly replaced from looking complete or unverified.
	Pending bool `json:"pending,omitempty"`
	// LinuxCloneIdentity is set only by local Hyper-V Ubuntu builds whose shutdown
	// clears machine identity before export. Pulls cannot infer this from bytes.
	LinuxCloneIdentity bool `json:"linux_clone_identity,omitempty"`
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
	if err != nil || marker.Pending {
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
	return markBuildArtifactsComplete(artifacts, false)
}

func markBuildArtifactsComplete(artifacts []string, linuxCloneIdentity bool) error {
	var errs []error
	for _, artifact := range artifacts {
		if err := writeArtifactCompletionMarker(artifact, linuxCloneIdentity); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func writeArtifactCompletionMarker(artifact string, linuxCloneIdentity bool) error {
	info, err := os.Stat(artifact)
	if err != nil {
		return fmt.Errorf("inspect completed build artifact %s: %w", artifact, err)
	}
	size := info.Size()
	if info.IsDir() {
		size = -1
	}
	return writeArtifactMarkerFile(artifact, artifactCompletionMarker{
		Format:             artifactCompletionMarkerFormat,
		CompletedAt:        time.Now().UTC(),
		SailwrightVersion:  sailwrightVersion(),
		Size:               size,
		LinuxCloneIdentity: linuxCloneIdentity,
	})
}

// HasLinuxCloneIdentity reports whether a matching completed local build was
// prepared for independent first-boot DHCP identities. Legacy/pulled images do
// not make this guarantee, even when they are otherwise usable.
func HasLinuxCloneIdentity(artifact string) bool {
	marker, err := readArtifactCompletionMarker(artifact)
	if err != nil || !marker.LinuxCloneIdentity {
		return false
	}
	state, err := artifactState(artifact)
	return err == nil && state == ArtifactStateExists
}

// MarkBuildArtifactsPending marks all artifacts of a set as incomplete before a build
// or pull replaces any of them. If the replacement stops partway, the set stays
// incomplete instead of mixing new and old artifacts without markers, which would
// count as unverified and usable. MarkBuildArtifactsComplete replaces the markers once
// the whole set is in place. restore puts the previous markers back, for a replacement
// that left every artifact as it was.
func MarkBuildArtifactsPending(artifacts []string) (restore func() error, err error) {
	previous := make(map[string][]byte, len(artifacts))
	for _, artifact := range artifacts {
		// #nosec G304 -- the marker path is derived from a managed build artifact path.
		data, err := os.ReadFile(artifactCompletionMarkerPath(artifact))
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("read completion marker for %s: %w", artifact, err)
		}
		previous[artifact] = data
	}
	restore = func() error {
		var errs []error
		for artifact, data := range previous {
			markerPath := artifactCompletionMarkerPath(artifact)
			if data == nil {
				if err := os.Remove(markerPath); err != nil && !os.IsNotExist(err) {
					errs = append(errs, fmt.Errorf("remove pending marker for %s: %w", artifact, err))
				}
				continue
			}
			// #nosec G306 -- the marker holds no secrets and must stay readable for non-root CI steps after sudo-created builds.
			if err := os.WriteFile(markerPath, data, 0o644); err != nil {
				errs = append(errs, fmt.Errorf("restore completion marker for %s: %w", artifact, err))
			}
		}
		return errors.Join(errs...)
	}

	for _, artifact := range artifacts {
		// #nosec G301 -- cache artifact directories must remain traversable for non-root CI steps after sudo-created builds.
		if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
			return nil, errors.Join(fmt.Errorf("create artifact directory for %s: %w", artifact, err), restore())
		}
		if err := writeArtifactMarkerFile(artifact, artifactCompletionMarker{
			Format:            artifactCompletionMarkerFormat,
			SailwrightVersion: sailwrightVersion(),
			Size:              -1,
			Pending:           true,
		}); err != nil {
			return nil, errors.Join(err, restore())
		}
	}
	return restore, nil
}

func writeArtifactMarkerFile(artifact string, marker artifactCompletionMarker) error {
	data, err := json.MarshalIndent(marker, "", "  ")
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
// e.g. together with the artifacts.
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

// recoverBuildArtifactBackups puts the previous artifact back when an interrupted
// build or pull left it only as a backup next to a missing final path, e.g. after a
// crash between the two renames of a replacement. Without this, the leftover cleanup
// would delete the only copy. The restored artifact has no completion marker, so it is
// unverified but usable.
func recoverBuildArtifactBackups(artifacts []string) error {
	var errs []error
	for _, artifact := range artifacts {
		if _, err := os.Lstat(artifact); err == nil || !os.IsNotExist(err) {
			continue
		}
		leftovers, err := buildArtifactLeftovers(artifact)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		backup, ok := newestBuildArtifactBackup(artifact, leftovers)
		if !ok {
			continue
		}
		log.Printf("Restoring the previous build artifact from the backup of an interrupted build or pull: %s -> %s", backup, artifact)
		if err := os.Rename(backup, artifact); err != nil {
			errs = append(errs, fmt.Errorf("restore build artifact backup %s: %w", backup, err))
		}
	}
	return errors.Join(errs...)
}

// newestBuildArtifactBackup returns the most recent backup among leftovers. Backup
// names end with a UnixNano timestamp.
func newestBuildArtifactBackup(artifact string, leftovers []string) (string, bool) {
	base := filepath.Base(artifact)
	newest, newestStamp := "", int64(-1)
	for _, leftover := range leftovers {
		name := filepath.Base(leftover)
		for _, infix := range []string{buildArtifactBackupInfix, replaceArtifactBackupInfix, ociPullArtifactBackupInfix} {
			suffix, found := strings.CutPrefix(name, base+infix)
			if !found {
				continue
			}
			stamp, err := strconv.ParseInt(suffix, 10, 64)
			if err != nil {
				continue
			}
			if stamp > newestStamp {
				newest, newestStamp = leftover, stamp
			}
		}
	}
	return newest, newestStamp >= 0
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

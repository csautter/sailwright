package oci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
)

// ForeignArtifactConfirmation decides whether a compatible artifact built for a
// different host OS may be pulled into the requested local artifact slot.
type ForeignArtifactConfirmation func(context.Context, ForeignArtifact) (bool, error)

type ForeignArtifact struct {
	OS                             string
	UbuntuType                     string
	Arch                           string
	SourceHostOS                   alchemy_build.HostOsType
	SourceVirtualizationEngine     alchemy_build.VirtualizationEngine
	TargetHostOS                   alchemy_build.HostOsType
	TargetVirtualizationEngine     alchemy_build.VirtualizationEngine
	SourceHostOSAnnotation         string
	SourceVirtualizationAnnotation string
}

type remoteManifest struct {
	descriptor ocispec.Descriptor
	layers     []ocispec.Descriptor
}

func manifestAnnotations(vm alchemy_build.VirtualMachineConfig, reference string) map[string]string {
	slugVM := vm
	slug := alchemy_build.GenerateVirtualMachineSlug(&slugVM)
	return map[string]string{
		ocispec.AnnotationTitle:              "dev-alchemy-" + slug,
		ocispec.AnnotationCreated:            time.Now().UTC().Format(time.RFC3339),
		ocispec.AnnotationVendor:             "dev-alchemy",
		ocispec.AnnotationDescription:        manifestDescription(vm, reference),
		ocispec.AnnotationDocumentation:      "https://github.com/csautter/dev-alchemy",
		ocispec.AnnotationSource:             "https://github.com/csautter/dev-alchemy",
		ocispec.AnnotationAuthors:            "Sailwright",
		ocispec.AnnotationRefName:            slug,
		AnnotationVMOS:                       vm.OS,
		AnnotationVMType:                     vm.UbuntuType,
		AnnotationVMArch:                     vm.Arch,
		AnnotationVMHostOS:                   string(vm.HostOs),
		AnnotationVMVirtualizationEngine:     string(vm.VirtualizationEngine),
		AnnotationVMSlug:                     slug,
		"org.opencontainers.image.component": "vm-build-artifact",
	}
}

// manifestDescription is shown on registry pages such as GHCR, which also
// advertise a docker pull command that cannot work for these artifacts. The
// pull command names the push reference, so it goes stale if the artifact is
// later copied to another repository.
func manifestDescription(vm alchemy_build.VirtualMachineConfig, reference string) string {
	pull := "sailwright pull " + reference + " --os " + vm.OS
	if strings.EqualFold(vm.OS, "ubuntu") {
		pull += " --type " + vm.UbuntuType
	}
	pull += " --arch " + vm.Arch

	hosts := fmt.Sprintf("Built on a %s host with %s; usable on %s hosts with %s", vm.HostOs, vm.VirtualizationEngine, vm.HostOs, vm.VirtualizationEngine)
	for _, target := range foreignPullTargets(vm) {
		hosts += fmt.Sprintf(" and %s hosts with %s", target.HostOs, target.VirtualizationEngine)
	}
	hosts += " only."

	// The host sentence goes last so GHCR's 512-character truncation cuts it
	// before the pull command.
	return "Sailwright VM disk image, not a Docker image: docker pull does not work. Install with: " + pull + ". " + hosts
}

// foreignPullTargets returns the configs on other hosts that accept vm's
// artifact as a compatible foreign artifact on pull.
func foreignPullTargets(vm alchemy_build.VirtualMachineConfig) []alchemy_build.VirtualMachineConfig {
	if !isForeignArtifactGuestOS(vm.OS) {
		return nil
	}
	var targets []alchemy_build.VirtualMachineConfig
	for _, target := range alchemy_build.AvailableVirtualMachineConfigs() {
		if !isDarwinLinuxHostPair(vm.HostOs, target.HostOs) {
			continue
		}
		if target.OS == vm.OS && target.UbuntuType == vm.UbuntuType && target.Arch == vm.Arch {
			targets = append(targets, target)
		}
	}
	return targets
}

func validateRemoteManifest(ctx context.Context, repo *remote.Repository, ref string, vm alchemy_build.VirtualMachineConfig, expected []ArtifactFile, opts PullOptions) (remoteManifest, error) {
	desc, err := repo.Resolve(ctx, ref)
	if err != nil {
		return remoteManifest{}, fmt.Errorf("resolve OCI artifact %s: %w", ref, err)
	}
	manifestBytes, err := content.FetchAll(ctx, repo, desc)
	if err != nil {
		return remoteManifest{}, fmt.Errorf("fetch OCI artifact manifest %s: %w", ref, err)
	}

	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return remoteManifest{}, fmt.Errorf("decode OCI artifact manifest %s: %w", ref, err)
	}
	if manifest.ArtifactType != ArtifactType {
		return remoteManifest{}, fmt.Errorf("OCI artifact %s has artifact type %q, expected %q", ref, manifest.ArtifactType, ArtifactType)
	}
	if err := validateManifestForPull(ctx, manifest, vm, expected, opts); err != nil {
		return remoteManifest{}, err
	}
	return remoteManifest{
		descriptor: desc,
		layers:     slices.Clone(manifest.Layers),
	}, nil
}

func validateManifestForPull(ctx context.Context, manifest ocispec.Manifest, vm alchemy_build.VirtualMachineConfig, expected []ArtifactFile, opts PullOptions) error {
	if err := validateManifestTarget(manifest, vm); err == nil {
		return validateManifestLayers(manifest, expected)
	} else if foreign, ok := compatibleForeignArtifact(manifest, vm); ok {
		if err := validateManifestLayers(manifest, expected); err != nil {
			return err
		}
		if opts.ConfirmForeignArtifactUse == nil {
			return err
		}
		confirmed, confirmErr := opts.ConfirmForeignArtifactUse(ctx, foreign)
		if confirmErr != nil {
			return fmt.Errorf("confirm foreign OCI artifact use: %w", confirmErr)
		}
		if !confirmed {
			return errors.New("foreign OCI artifact use cancelled")
		}
		return nil
	} else {
		return err
	}
}

func validateManifestTarget(manifest ocispec.Manifest, vm alchemy_build.VirtualMachineConfig) error {
	annotations := manifest.Annotations
	if annotations == nil {
		return errors.New("OCI artifact manifest is missing Sailwright target annotations")
	}

	checks := manifestTargetChecks(vm)
	for _, check := range checks {
		if got := annotations[check.key]; got != check.want {
			return fmt.Errorf("OCI artifact target annotation %s=%q, expected %q", check.key, got, check.want)
		}
	}
	return nil
}

func manifestTargetChecks(vm alchemy_build.VirtualMachineConfig) []targetAnnotationCheck {
	return []targetAnnotationCheck{
		{key: AnnotationVMOS, want: vm.OS},
		{key: AnnotationVMType, want: vm.UbuntuType},
		{key: AnnotationVMArch, want: vm.Arch},
		{key: AnnotationVMHostOS, want: string(vm.HostOs)},
		{key: AnnotationVMVirtualizationEngine, want: string(vm.VirtualizationEngine)},
	}
}

type targetAnnotationCheck struct {
	key  string
	want string
}

func compatibleForeignArtifact(manifest ocispec.Manifest, vm alchemy_build.VirtualMachineConfig) (ForeignArtifact, bool) {
	annotations := manifest.Annotations
	if annotations == nil {
		return ForeignArtifact{}, false
	}
	if !isForeignArtifactGuestOS(vm.OS) {
		return ForeignArtifact{}, false
	}

	var foreignTargetMismatch bool
	for _, check := range manifestTargetChecks(vm) {
		got := annotations[check.key]
		if got == check.want {
			continue
		}
		switch check.key {
		case AnnotationVMHostOS, AnnotationVMVirtualizationEngine:
			foreignTargetMismatch = true
		default:
			return ForeignArtifact{}, false
		}
	}
	if !foreignTargetMismatch {
		return ForeignArtifact{}, false
	}

	sourceHostOS, ok := normalizeArtifactHostOS(annotations[AnnotationVMHostOS])
	if !ok || !isDarwinLinuxHostPair(sourceHostOS, vm.HostOs) {
		return ForeignArtifact{}, false
	}
	sourceEngine := annotations[AnnotationVMVirtualizationEngine]
	if sourceEngine == "" {
		return ForeignArtifact{}, false
	}

	return ForeignArtifact{
		OS:                             vm.OS,
		UbuntuType:                     vm.UbuntuType,
		Arch:                           vm.Arch,
		SourceHostOS:                   sourceHostOS,
		SourceVirtualizationEngine:     alchemy_build.VirtualizationEngine(sourceEngine),
		TargetHostOS:                   vm.HostOs,
		TargetVirtualizationEngine:     vm.VirtualizationEngine,
		SourceHostOSAnnotation:         annotations[AnnotationVMHostOS],
		SourceVirtualizationAnnotation: sourceEngine,
	}, true
}

func isForeignArtifactGuestOS(osName string) bool {
	normalized := strings.ToLower(osName)
	return normalized == "ubuntu" || strings.HasPrefix(normalized, "windows")
}

func normalizeArtifactHostOS(value string) (alchemy_build.HostOsType, bool) {
	switch strings.ToLower(value) {
	case "linux", string(alchemy_build.HostOsLinux):
		return alchemy_build.HostOsLinux, true
	case "macos", string(alchemy_build.HostOsDarwin):
		return alchemy_build.HostOsDarwin, true
	case string(alchemy_build.HostOsWindows):
		return alchemy_build.HostOsWindows, true
	default:
		return "", false
	}
}

func isDarwinLinuxHostPair(source alchemy_build.HostOsType, target alchemy_build.HostOsType) bool {
	return (source == alchemy_build.HostOsDarwin && target == alchemy_build.HostOsLinux) ||
		(source == alchemy_build.HostOsLinux && target == alchemy_build.HostOsDarwin)
}

func validateManifestLayers(manifest ocispec.Manifest, expected []ArtifactFile) error {
	expectedByName := make(map[string]ArtifactFile, len(expected))
	for _, file := range expected {
		expectedByName[file.Name] = file
	}

	seen := make(map[string]bool, len(manifest.Layers))
	for _, layer := range manifest.Layers {
		name := layer.Annotations[ocispec.AnnotationTitle]
		if name == "" {
			return errors.New("OCI artifact layer is missing title annotation")
		}
		if seen[name] {
			return fmt.Errorf("OCI artifact contains duplicate layer %q", name)
		}
		expectedFile, ok := expectedByName[name]
		if !ok {
			return fmt.Errorf("OCI artifact contains unexpected layer %q", name)
		}
		if layer.MediaType != expectedFile.MediaType {
			return fmt.Errorf("OCI artifact layer %q has media type %q, expected %q", name, layer.MediaType, expectedFile.MediaType)
		}
		seen[name] = true
	}

	if len(manifest.Layers) != len(expected) {
		return fmt.Errorf("OCI artifact contains %d layers, expected %d", len(manifest.Layers), len(expected))
	}

	for _, expectedFile := range expected {
		if !seen[expectedFile.Name] {
			return fmt.Errorf("OCI artifact is missing expected layer %q", expectedFile.Name)
		}
	}
	return nil
}

package oci

import (
	"context"
	"strings"
	"testing"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestValidateManifestForPullRejectsForeignWithoutConfirmation(t *testing.T) {
	localVM := linuxQemuOCIConfig("ubuntu", "server", "arm64")
	manifest := testOCIManifest(
		darwinUtmOCIConfig("ubuntu", "server", "arm64"),
		"ubuntu/qemu-ubuntu-server-packer-arm64.qcow2",
	)

	err := validateManifestForPull(
		context.Background(),
		manifest,
		localVM,
		[]ArtifactFile{{Name: "ubuntu/qemu-ubuntu-server-packer-arm64.qcow2", MediaType: MediaTypeQCOW2}},
		PullOptions{},
	)
	if err == nil {
		t.Fatal("expected foreign artifact to require confirmation")
	}
	if !strings.Contains(err.Error(), `dev.alchemy.vm.host_os="darwin", expected "debian"`) {
		t.Fatalf("expected strict host_os validation error, got %q", err.Error())
	}
}

func TestValidateManifestForPullAllowsConfirmedDarwinLinuxForeignArtifacts(t *testing.T) {
	tests := []struct {
		name      string
		localVM   alchemy_build.VirtualMachineConfig
		remoteVM  alchemy_build.VirtualMachineConfig
		layerName string
	}{
		{
			name:      "ubuntu",
			localVM:   linuxQemuOCIConfig("ubuntu", "server", "arm64"),
			remoteVM:  darwinUtmOCIConfig("ubuntu", "server", "arm64"),
			layerName: "ubuntu/qemu-ubuntu-server-packer-arm64.qcow2",
		},
		{
			name:      "windows",
			localVM:   linuxQemuOCIConfig("windows11", "", "amd64"),
			remoteVM:  darwinUtmOCIConfig("windows11", "", "amd64"),
			layerName: "windows11/qemu-windows11-amd64.qcow2",
		},
		{
			name:      "reverse direction",
			localVM:   darwinUtmOCIConfig("ubuntu", "desktop", "amd64"),
			remoteVM:  linuxQemuOCIConfig("ubuntu", "desktop", "amd64"),
			layerName: "ubuntu/qemu-ubuntu-desktop-packer-amd64.qcow2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var confirmed bool
			err := validateManifestForPull(
				context.Background(),
				testOCIManifest(tt.remoteVM, tt.layerName),
				tt.localVM,
				[]ArtifactFile{{Name: tt.layerName, MediaType: MediaTypeQCOW2}},
				PullOptions{
					ConfirmForeignArtifactUse: func(ctx context.Context, foreign ForeignArtifact) (bool, error) {
						confirmed = true
						if foreign.OS != tt.localVM.OS {
							t.Fatalf("expected foreign OS %q, got %q", tt.localVM.OS, foreign.OS)
						}
						if foreign.Arch != tt.localVM.Arch {
							t.Fatalf("expected foreign arch %q, got %q", tt.localVM.Arch, foreign.Arch)
						}
						if foreign.SourceHostOS != tt.remoteVM.HostOs {
							t.Fatalf("expected source host OS %q, got %q", tt.remoteVM.HostOs, foreign.SourceHostOS)
						}
						if foreign.TargetHostOS != tt.localVM.HostOs {
							t.Fatalf("expected target host OS %q, got %q", tt.localVM.HostOs, foreign.TargetHostOS)
						}
						return true, nil
					},
				},
			)
			if err != nil {
				t.Fatalf("expected confirmed foreign artifact to validate: %v", err)
			}
			if !confirmed {
				t.Fatal("expected foreign artifact confirmation callback")
			}
		})
	}
}

func TestValidateManifestForPullRejectsCancelledForeignArtifact(t *testing.T) {
	localVM := linuxQemuOCIConfig("ubuntu", "server", "arm64")
	layerName := "ubuntu/qemu-ubuntu-server-packer-arm64.qcow2"

	err := validateManifestForPull(
		context.Background(),
		testOCIManifest(darwinUtmOCIConfig("ubuntu", "server", "arm64"), layerName),
		localVM,
		[]ArtifactFile{{Name: layerName, MediaType: MediaTypeQCOW2}},
		PullOptions{
			ConfirmForeignArtifactUse: func(ctx context.Context, foreign ForeignArtifact) (bool, error) {
				return false, nil
			},
		},
	)
	if err == nil {
		t.Fatal("expected cancelled foreign artifact to fail")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("expected cancellation error, got %q", err.Error())
	}
}

func TestValidateManifestForPullRejectsIncompatibleForeignArtifact(t *testing.T) {
	localVM := linuxQemuOCIConfig("ubuntu", "server", "arm64")
	remoteVM := darwinUtmOCIConfig("ubuntu", "server", "amd64")
	layerName := "ubuntu/qemu-ubuntu-server-packer-arm64.qcow2"

	err := validateManifestForPull(
		context.Background(),
		testOCIManifest(remoteVM, layerName),
		localVM,
		[]ArtifactFile{{Name: layerName, MediaType: MediaTypeQCOW2}},
		PullOptions{
			ConfirmForeignArtifactUse: func(ctx context.Context, foreign ForeignArtifact) (bool, error) {
				t.Fatal("did not expect confirmation for an incompatible artifact target")
				return false, nil
			},
		},
	)
	if err == nil {
		t.Fatal("expected incompatible artifact target to fail")
	}
	if !strings.Contains(err.Error(), `dev.alchemy.vm.arch="amd64", expected "arm64"`) {
		t.Fatalf("expected strict arch validation error, got %q", err.Error())
	}
}

func TestValidateManifestLayersRejectsUnexpectedLayer(t *testing.T) {
	err := validateManifestLayers(
		ocispec.Manifest{
			Layers: []ocispec.Descriptor{
				{
					MediaType: MediaTypeQCOW2,
					Annotations: map[string]string{
						ocispec.AnnotationTitle: "unexpected.qcow2",
					},
				},
			},
		},
		[]ArtifactFile{{Name: "expected.qcow2", MediaType: MediaTypeQCOW2}},
	)
	if err == nil {
		t.Fatal("expected unexpected layer to fail validation")
	}
	if !strings.Contains(err.Error(), "unexpected layer") {
		t.Fatalf("expected unexpected layer error, got %q", err.Error())
	}
}

func TestValidateManifestLayersRejectsDuplicateLayerName(t *testing.T) {
	layerName := "expected.qcow2"

	err := validateManifestLayers(
		ocispec.Manifest{
			Layers: []ocispec.Descriptor{
				{
					MediaType: MediaTypeQCOW2,
					Annotations: map[string]string{
						ocispec.AnnotationTitle: layerName,
					},
				},
				{
					MediaType: MediaTypeQCOW2,
					Annotations: map[string]string{
						ocispec.AnnotationTitle: layerName,
					},
				},
			},
		},
		[]ArtifactFile{{Name: layerName, MediaType: MediaTypeQCOW2}},
	)
	if err == nil {
		t.Fatal("expected duplicate layer to fail validation")
	}
	if !strings.Contains(err.Error(), "duplicate layer") {
		t.Fatalf("expected duplicate layer error, got %q", err.Error())
	}
}

func linuxQemuOCIConfig(osName string, ubuntuType string, arch string) alchemy_build.VirtualMachineConfig {
	return alchemy_build.VirtualMachineConfig{
		OS:                   osName,
		UbuntuType:           ubuntuType,
		Arch:                 arch,
		HostOs:               alchemy_build.HostOsLinux,
		VirtualizationEngine: alchemy_build.VirtualizationEngineQemu,
	}
}

func darwinUtmOCIConfig(osName string, ubuntuType string, arch string) alchemy_build.VirtualMachineConfig {
	return alchemy_build.VirtualMachineConfig{
		OS:                   osName,
		UbuntuType:           ubuntuType,
		Arch:                 arch,
		HostOs:               alchemy_build.HostOsDarwin,
		VirtualizationEngine: alchemy_build.VirtualizationEngineUtm,
	}
}

func testOCIManifest(vm alchemy_build.VirtualMachineConfig, layerName string) ocispec.Manifest {
	return ocispec.Manifest{
		ArtifactType: ArtifactType,
		Annotations:  manifestAnnotations(vm, "registry.example/sailwright:test"),
		Layers: []ocispec.Descriptor{
			{
				MediaType: MediaTypeQCOW2,
				Annotations: map[string]string{
					ocispec.AnnotationTitle: layerName,
				},
			},
		},
	}
}

func TestManifestDescriptionExplainsSailwrightPull(t *testing.T) {
	const ref = "ghcr.io/csautter/ubuntu-24:server-arm64-linux-build"
	got := manifestDescription(linuxQemuOCIConfig("ubuntu", "server", "arm64"), ref)

	for _, want := range []string{
		"not a Docker image",
		"sailwright pull " + ref + " --os ubuntu --type server --arch arm64",
		"usable on Linux (QEMU) and macOS (UTM) hosts, not on Windows hosts",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("description %q does not contain %q", got, want)
		}
	}
	// GHCR truncates package descriptions longer than 512 characters.
	if len(got) > 512 {
		t.Errorf("description is %d characters, want at most 512", len(got))
	}
}

func TestManifestDescriptionLimitsWindowsHostArtifacts(t *testing.T) {
	vm := alchemy_build.VirtualMachineConfig{
		OS:                   "windows11",
		Arch:                 "amd64",
		HostOs:               alchemy_build.HostOsWindows,
		VirtualizationEngine: alchemy_build.VirtualizationEngineHyperv,
	}
	got := manifestDescription(vm, "ghcr.io/example/windows11:hyperv")

	if !strings.Contains(got, "sailwright pull ghcr.io/example/windows11:hyperv --os windows11 --arch amd64.") {
		t.Errorf("description %q has unexpected pull command", got)
	}
	if !strings.Contains(got, "usable on windows hosts only") {
		t.Errorf("description %q does not restrict to windows hosts", got)
	}
}

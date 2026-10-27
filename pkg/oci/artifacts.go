package oci

import (
	"context"
	"fmt"
	"os"
	"slices"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	oras "oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/file"
)

const (
	ArtifactType = "application/vnd.dev-alchemy.vm-build.v1"

	MediaTypeArtifact   = "application/vnd.dev-alchemy.vm-build.artifact.v1"
	MediaTypeQCOW2      = "application/vnd.dev-alchemy.vm-build.qcow2.v1"
	MediaTypeVagrantBox = "application/vnd.dev-alchemy.vm-build.vagrant-box.v1"

	AnnotationVMOS                   = "dev.alchemy.vm.os"
	AnnotationVMType                 = "dev.alchemy.vm.type"
	AnnotationVMArch                 = "dev.alchemy.vm.arch"
	AnnotationVMHostOS               = "dev.alchemy.vm.host_os"
	AnnotationVMVirtualizationEngine = "dev.alchemy.vm.virtualization_engine"
	AnnotationVMSlug                 = "dev.alchemy.vm.slug"
)

type PushOptions struct {
	RegistryOptions
	Progress TransferProgress
}

type PullOptions struct {
	RegistryOptions
	Progress                  TransferProgress
	ConfirmForeignArtifactUse ForeignArtifactConfirmation
}

type ArtifactFile struct {
	Name      string
	Path      string
	MediaType string
	Digest    string
	Size      int64
}

type TransferResult struct {
	Reference string
	Digest    string
	MediaType string
	Size      int64
	Artifacts []ArtifactFile
}

func Push(ctx context.Context, vm alchemy_build.VirtualMachineConfig, reference string, opts PushOptions) (TransferResult, error) {
	reportTransferStatus(opts.Progress, "Resolving local artifact paths")
	layout, err := resolveArtifactLayout(vm)
	if err != nil {
		return TransferResult{}, err
	}

	reportTransferStatus(opts.Progress, "Parsing OCI reference %s", reference)
	remoteRef, err := parsePushReference(reference)
	if err != nil {
		return TransferResult{}, err
	}

	reportTransferStatus(opts.Progress, "Preparing local OCI artifact store")
	fs, err := file.New(layout.root)
	if err != nil {
		return TransferResult{}, fmt.Errorf("create OCI file store: %w", err)
	}
	defer fs.Close()

	layers := make([]ocispec.Descriptor, 0, len(layout.files))
	files := make([]ArtifactFile, 0, len(layout.files))
	for _, artifact := range layout.files {
		reportTransferStatus(opts.Progress, "Hashing local artifact %s", artifact.Path)
		desc, err := fs.Add(ctx, artifact.Name, artifact.MediaType, artifact.Path)
		if err != nil {
			return TransferResult{}, fmt.Errorf("add artifact %s to OCI store: %w", artifact.Path, err)
		}
		artifact.Digest = desc.Digest.String()
		artifact.Size = desc.Size
		layers = append(layers, desc)
		files = append(files, artifact)
	}

	reportTransferStatus(opts.Progress, "Packing OCI artifact manifest")
	manifestDesc, err := oras.PackManifest(ctx, fs, oras.PackManifestVersion1_1, ArtifactType, oras.PackManifestOptions{
		Layers:              layers,
		ManifestAnnotations: manifestAnnotations(vm, reference),
	})
	if err != nil {
		return TransferResult{}, fmt.Errorf("pack OCI artifact manifest: %w", err)
	}
	if err := fs.Tag(ctx, manifestDesc, remoteRef.reference); err != nil {
		return TransferResult{}, fmt.Errorf("tag local OCI artifact: %w", err)
	}

	reportTransferStatus(opts.Progress, "Preparing OCI registry client")
	repo, err := newRepository(remoteRef, opts.RegistryOptions)
	if err != nil {
		return TransferResult{}, err
	}

	reportTransferStatus(opts.Progress, "Uploading OCI artifact")
	pushedDesc, err := copyArtifact(ctx, fs, remoteRef.reference, repo, remoteRef.reference, descriptorTotal(append(layers, manifestDesc)...), opts.Progress)
	if err != nil {
		return TransferResult{}, fmt.Errorf("push OCI artifact %s: %w", reference, err)
	}

	return transferResult(reference, pushedDesc, files), nil
}

func Pull(ctx context.Context, vm alchemy_build.VirtualMachineConfig, reference string, opts PullOptions) (TransferResult, error) {
	reportTransferStatus(opts.Progress, "Resolving local artifact paths")
	layout, err := resolveArtifactLayout(vm)
	if err != nil {
		return TransferResult{}, err
	}

	reportTransferStatus(opts.Progress, "Parsing OCI reference %s", reference)
	remoteRef, err := parsePullReference(reference)
	if err != nil {
		return TransferResult{}, err
	}

	reportTransferStatus(opts.Progress, "Preparing OCI registry client")
	repo, err := newRepository(remoteRef, opts.RegistryOptions)
	if err != nil {
		return TransferResult{}, err
	}
	reportTransferStatus(opts.Progress, "Resolving and validating OCI artifact manifest")
	remoteManifest, err := validateRemoteManifest(ctx, repo, remoteRef.reference, vm, layout.files, opts)
	if err != nil {
		return TransferResult{}, err
	}
	manifestDesc := remoteManifest.descriptor

	if err := os.MkdirAll(layout.root, 0o700); err != nil {
		return TransferResult{}, fmt.Errorf("create artifact root %s: %w", layout.root, err)
	}
	reportTransferStatus(opts.Progress, "Preparing OCI pull staging directory")
	stagingRoot, err := os.MkdirTemp(layout.root, ".dev-alchemy-oci-pull-*")
	if err != nil {
		return TransferResult{}, fmt.Errorf("create OCI pull staging directory: %w", err)
	}
	defer os.RemoveAll(stagingRoot)

	fs, err := file.New(stagingRoot)
	if err != nil {
		return TransferResult{}, fmt.Errorf("create OCI file store: %w", err)
	}
	defer fs.Close()

	reportTransferStatus(opts.Progress, "Downloading OCI artifact")
	if _, err := copyArtifact(ctx, repo, remoteRef.reference, fs, "pulled", descriptorTotal(append(remoteManifest.layers, manifestDesc)...), opts.Progress); err != nil {
		return TransferResult{}, fmt.Errorf("pull OCI artifact %s: %w", reference, err)
	}

	reportTransferStatus(opts.Progress, "Promoting pulled artifacts into the local cache")
	if err := promotePulledArtifacts(stagingRoot, layout.files); err != nil {
		return TransferResult{}, err
	}

	pulledFiles := slices.Clone(layout.files)
	for i := range pulledFiles {
		info, err := os.Stat(pulledFiles[i].Path)
		if err != nil {
			return TransferResult{}, fmt.Errorf("inspect pulled artifact %s: %w", pulledFiles[i].Path, err)
		}
		pulledFiles[i].Size = info.Size()
	}

	return transferResult(reference, manifestDesc, pulledFiles), nil
}

func transferResult(reference string, desc ocispec.Descriptor, files []ArtifactFile) TransferResult {
	return TransferResult{
		Reference: reference,
		Digest:    desc.Digest.String(),
		MediaType: desc.MediaType,
		Size:      desc.Size,
		Artifacts: slices.Clone(files),
	}
}

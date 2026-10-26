package workflowcheck

import (
	"slices"
	"strings"
	"testing"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	alchemy_oci "github.com/csautter/dev-alchemy/pkg/oci"
)

const (
	pullPublishedImagesWorkflow = "test-pull-published-images.yml"
	repositoryOwnerExpression   = "${{ github.repository_owner }}"
	publishedRepositoryOwner    = "csautter"
)

// Released CLIs offer alchemy_oci.PublishedUbuntuReference in `sailwright build`,
// so the publish job must keep producing exactly those references.
func TestLinuxBuildWorkflowPublishesPrebuiltOfferReferences(t *testing.T) {
	t.Parallel()

	wf := loadWorkflow(t, linuxBuildWorkflow)
	image := strings.ReplaceAll(wf.Env["OCI_UBUNTU_IMAGE"], repositoryOwnerExpression, publishedRepositoryOwner)
	if image != alchemy_oci.PublishedUbuntuRepository {
		t.Errorf("OCI_UBUNTU_IMAGE resolves to %q, but the CLI offers %q", image, alchemy_oci.PublishedUbuntuRepository)
	}

	wantReference := strings.Replace(
		alchemy_oci.PublishedUbuntuReference("${{ matrix.ubuntu_type }}", "${{ matrix.arch }}"),
		alchemy_oci.PublishedUbuntuRepository, "${OCI_UBUNTU_IMAGE}", 1,
	)
	publish := wf.job(t, linuxPublishJob)
	if !slices.ContainsFunc(publish.Steps, func(s step) bool {
		return strings.Contains(s.Run, "go run cmd/main.go push") && strings.Contains(s.Run, wantReference)
	}) {
		t.Errorf("publish job must push to %s", wantReference)
	}

	published := map[string]bool{}
	for _, entry := range publish.Strategy.Matrix.Include {
		published[entry["ubuntu_type"]+"-"+entry["arch"]] = true
	}
	for _, vm := range alchemy_build.AvailableVirtualMachineConfigs() {
		offered := vm.OS == "ubuntu" &&
			(vm.HostOs == alchemy_build.HostOsLinux && vm.VirtualizationEngine == alchemy_build.VirtualizationEngineQemu ||
				vm.HostOs == alchemy_build.HostOsDarwin && vm.VirtualizationEngine == alchemy_build.VirtualizationEngineUtm)
		if offered && !published[vm.UbuntuType+"-"+vm.Arch] {
			t.Errorf("publish matrix must include %s-%s, which the CLI offers on %s hosts", vm.UbuntuType, vm.Arch, vm.HostOs)
		}
	}
}

func TestPullPublishedImagesWorkflowRunsOnPullDependencies(t *testing.T) {
	t.Parallel()

	wf := loadWorkflow(t, pullPublishedImagesWorkflow)
	required := []string{
		"pkg/oci/**",
		"pkg/build/**",
		"cmd/cmd/oci.go",
		"cmd/cmd/build_prebuilt.go",
		"cmd/cmd/vm_listing.go",
		"go.mod",
		"go.sum",
		".github/workflows/" + pullPublishedImagesWorkflow,
	}
	for event, paths := range map[string][]string{"push": wf.On.Push.Paths, "pull_request": wf.On.PullRequest.Paths} {
		for _, path := range required {
			if !slices.Contains(paths, path) {
				t.Errorf("workflow %s trigger must include %s", event, path)
			}
		}
	}

	if image := strings.ReplaceAll(wf.Env["OCI_UBUNTU_REPOSITORY"], repositoryOwnerExpression, publishedRepositoryOwner); "ghcr.io/"+image != alchemy_oci.PublishedUbuntuRepository {
		t.Errorf("OCI_UBUNTU_REPOSITORY resolves to ghcr.io/%s, but the CLI offers %s", image, alchemy_oci.PublishedUbuntuRepository)
	}
}

package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

func TestIsBuildSupported(t *testing.T) {
	tests := []struct {
		name string
		vm   alchemy_build.VirtualMachineConfig
		want bool
	}{
		{
			name: "darwin utm ubuntu supported",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "ubuntu",
				Arch:                 "arm64",
				HostOs:               alchemy_build.HostOsDarwin,
				VirtualizationEngine: alchemy_build.VirtualizationEngineUtm,
			},
			want: true,
		},
		{
			name: "darwin tart macos unsupported for build",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "macos",
				Arch:                 "arm64",
				HostOs:               alchemy_build.HostOsDarwin,
				VirtualizationEngine: alchemy_build.VirtualizationEngineTart,
			},
			want: false,
		},
		{
			name: "windows hyperv windows11 supported",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "windows11",
				Arch:                 "amd64",
				HostOs:               alchemy_build.HostOsWindows,
				VirtualizationEngine: alchemy_build.VirtualizationEngineHyperv,
			},
			want: true,
		},
		{
			name: "windows virtualbox ubuntu unsupported",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "ubuntu",
				Arch:                 "amd64",
				HostOs:               alchemy_build.HostOsWindows,
				VirtualizationEngine: alchemy_build.VirtualizationEngineVirtualBox,
			},
			want: false,
		},
		{
			name: "linux qemu ubuntu supported",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "ubuntu",
				Arch:                 "arm64",
				HostOs:               alchemy_build.HostOsLinux,
				VirtualizationEngine: alchemy_build.VirtualizationEngineQemu,
			},
			want: true,
		},
		{
			name: "linux qemu windows11 supported",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "windows11",
				Arch:                 "amd64",
				HostOs:               alchemy_build.HostOsLinux,
				VirtualizationEngine: alchemy_build.VirtualizationEngineQemu,
			},
			want: true,
		},
	}

	for _, tt := range tests {
		if got := isBuildSupported(tt.vm); got != tt.want {
			t.Fatalf("%s: expected %v, got %v", tt.name, tt.want, got)
		}
	}
}

func TestIsBuildIncludedByDefault(t *testing.T) {
	tests := []struct {
		name string
		vm   alchemy_build.VirtualMachineConfig
		want bool
	}{
		{
			name: "windows hyperv windows11 included by default",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "windows11",
				Arch:                 "amd64",
				HostOs:               alchemy_build.HostOsWindows,
				VirtualizationEngine: alchemy_build.VirtualizationEngineHyperv,
			},
			want: true,
		},
		{
			name: "windows virtualbox windows11 excluded by default",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "windows11",
				Arch:                 "amd64",
				HostOs:               alchemy_build.HostOsWindows,
				VirtualizationEngine: alchemy_build.VirtualizationEngineVirtualBox,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		if got := isBuildIncludedByDefault(tt.vm); got != tt.want {
			t.Fatalf("%s: expected %v, got %v", tt.name, tt.want, got)
		}
	}
}

func TestAvailableBuildVirtualMachinesOnlyReturnsSupportedConfigs(t *testing.T) {
	for _, vm := range availableBuildVirtualMachines() {
		if !isBuildSupported(vm) {
			t.Fatalf("expected only supported build configs, got engine %q", vm.VirtualizationEngine)
		}
	}
}

func TestDefaultBuildVirtualMachinesExcludesUnstableConfigs(t *testing.T) {
	for _, vm := range defaultBuildVirtualMachines() {
		if !isBuildIncludedByDefault(vm) {
			t.Fatalf("expected only default build configs, got engine %q", vm.VirtualizationEngine)
		}
	}
}

func TestResolveBuildVirtualMachineRequiresEngineForAmbiguousSelection(t *testing.T) {
	vms := []alchemy_build.VirtualMachineConfig{
		{
			OS:                   "windows11",
			Arch:                 "amd64",
			HostOs:               alchemy_build.HostOsWindows,
			VirtualizationEngine: alchemy_build.VirtualizationEngineHyperv,
		},
		{
			OS:                   "windows11",
			Arch:                 "amd64",
			HostOs:               alchemy_build.HostOsWindows,
			VirtualizationEngine: alchemy_build.VirtualizationEngineVirtualBox,
		},
	}

	_, err := resolveBuildVirtualMachine(vms, "windows11", "", "amd64", "")
	if err == nil {
		t.Fatal("expected ambiguous selection error, got nil")
	}
	if !strings.Contains(err.Error(), "--engine") {
		t.Fatalf("expected error to mention --engine, got %q", err.Error())
	}
}

func TestResolveBuildVirtualMachineSelectsRequestedEngine(t *testing.T) {
	vms := []alchemy_build.VirtualMachineConfig{
		{
			OS:                   "windows11",
			Arch:                 "amd64",
			HostOs:               alchemy_build.HostOsWindows,
			VirtualizationEngine: alchemy_build.VirtualizationEngineHyperv,
		},
		{
			OS:                   "windows11",
			Arch:                 "amd64",
			HostOs:               alchemy_build.HostOsWindows,
			VirtualizationEngine: alchemy_build.VirtualizationEngineVirtualBox,
		},
	}

	vm, err := resolveBuildVirtualMachine(vms, "windows11", "", "amd64", "virtualbox")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if vm.VirtualizationEngine != alchemy_build.VirtualizationEngineVirtualBox {
		t.Fatalf("expected virtualbox engine, got %q", vm.VirtualizationEngine)
	}
}

func TestBuildHelpIncludesEngineFlag(t *testing.T) {
	var buf bytes.Buffer
	buildCmd.SetOut(&buf)
	buildCmd.SetErr(&buf)

	if err := buildCmd.Help(); err != nil {
		t.Fatalf("expected no help error, got %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "--engine string") {
		t.Fatalf("expected --engine flag in help output, got %q", output)
	}
	if !strings.Contains(output, "--verbose") {
		t.Fatalf("expected --verbose flag in help output, got %q", output)
	}
	if !strings.Contains(output, "build all stable VM configurations") {
		t.Fatalf("expected help output to describe stable all builds, got %q", output)
	}
}

func TestBuildListRowIncludesArtifactStatus(t *testing.T) {
	previousInspector := inspectBuildArtifactExists
	t.Cleanup(func() {
		inspectBuildArtifactExists = previousInspector
	})

	inspectBuildArtifactExists = func(vm alchemy_build.VirtualMachineConfig) (bool, error) {
		return vm.OS == "windows11", nil
	}

	var buf bytes.Buffer
	err := printVirtualMachineCombinationTable(
		&buf,
		"Available build combinations for host OS: windows",
		"No build combinations are available for the current host OS.",
		[]alchemy_build.VirtualMachineConfig{
			{
				OS:                   "windows11",
				Arch:                 "amd64",
				HostOs:               alchemy_build.HostOsWindows,
				VirtualizationEngine: alchemy_build.VirtualizationEngineHyperv,
			},
			{
				OS:                   "ubuntu",
				UbuntuType:           "server",
				Arch:                 "amd64",
				HostOs:               alchemy_build.HostOsWindows,
				VirtualizationEngine: alchemy_build.VirtualizationEngineHyperv,
			},
		},
		[]string{"OS", "Type", "Arch", "Build"},
		buildListRow,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Build") {
		t.Fatalf("expected build status header, got %q", output)
	}
	if !strings.Contains(output, "windows11") || !strings.Contains(output, "exists") {
		t.Fatalf("expected existing windows build artifact row, got %q", output)
	}
	if !strings.Contains(output, "ubuntu") || !strings.Contains(output, "missing") {
		t.Fatalf("expected missing ubuntu build artifact row, got %q", output)
	}
}

func TestBuildCommandReturnsErrorWhenBuildFails(t *testing.T) {
	vms := availableBuildVirtualMachines()
	if len(vms) == 0 {
		t.Skip("no build targets available on this host")
	}
	vm := vms[0]

	stubPrebuiltOffer(t)
	previousRunner, previousArch, previousType, previousEngine := runBuildFunc, arch, osType, buildEngine
	t.Cleanup(func() {
		runBuildFunc, arch, osType, buildEngine = previousRunner, previousArch, previousType, previousEngine
	})

	runBuildFunc = func(alchemy_build.VirtualMachineConfig) error {
		return errors.New("packer exploded")
	}
	arch, osType, buildEngine = vm.Arch, vm.UbuntuType, string(vm.VirtualizationEngine)

	err := buildCmd.RunE(buildCmd, []string{vm.OS})
	if err == nil || !strings.Contains(err.Error(), "packer exploded") {
		t.Fatalf("expected build failure to be returned, got %v", err)
	}
}

func TestBuildCommandPrebuiltOfferDecidesWhetherToBuild(t *testing.T) {
	vms := availableBuildVirtualMachines()
	index := slices.IndexFunc(vms, func(vm alchemy_build.VirtualMachineConfig) bool {
		return prebuiltArtifactReference(vm) != ""
	})
	if index == -1 {
		t.Skip("no build target with a published prebuilt image on this host")
	}
	vm := vms[index]

	for _, accept := range []bool{true, false} {
		t.Run(fmt.Sprintf("accept=%v", accept), func(t *testing.T) {
			stubs := stubPrebuiltOffer(t)
			stubs.answer = accept
			previousRunner, previousArch, previousType, previousEngine := runBuildFunc, arch, osType, buildEngine
			previousIn := buildCmd.InOrStdin()
			t.Cleanup(func() {
				runBuildFunc, arch, osType, buildEngine = previousRunner, previousArch, previousType, previousEngine
				buildCmd.SetIn(previousIn)
			})

			built := false
			runBuildFunc = func(alchemy_build.VirtualMachineConfig) error {
				built = true
				return nil
			}
			arch, osType, buildEngine = vm.Arch, vm.UbuntuType, string(vm.VirtualizationEngine)
			buildCmd.SetIn(strings.NewReader(""))

			if err := buildCmd.RunE(buildCmd, []string{vm.OS}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if built == accept || (stubs.pulled != "") != accept {
				t.Fatalf("accept=%v: built=%v pulled=%q", accept, built, stubs.pulled)
			}
		})
	}
}

func TestBuildCommandReturnsErrorForInvalidCombination(t *testing.T) {
	previousRunner := runBuildFunc
	t.Cleanup(func() { runBuildFunc = previousRunner })
	runBuildFunc = func(alchemy_build.VirtualMachineConfig) error {
		t.Fatal("build must not run for an invalid combination")
		return nil
	}

	if err := buildCmd.RunE(buildCmd, []string{"not-an-os"}); err == nil {
		t.Fatal("expected an error for an invalid combination")
	}
}

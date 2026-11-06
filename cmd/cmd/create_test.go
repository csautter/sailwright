package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

func TestRunDeployReturnsErrorForUnsupportedEngine(t *testing.T) {
	vm := alchemy_build.VirtualMachineConfig{
		OS:                   "windows11",
		Arch:                 "amd64",
		VirtualizationEngine: alchemy_build.VirtualizationEngineVirtualBox,
	}

	err := runDeploy(vm)
	if err == nil {
		t.Fatalf("expected error for unsupported engine %q, got nil", vm.VirtualizationEngine)
	}
	if !strings.Contains(err.Error(), string(alchemy_build.VirtualizationEngineVirtualBox)) {
		t.Fatalf("expected error to mention engine %q, got %q", vm.VirtualizationEngine, err.Error())
	}
}

func TestIsCreateSupported(t *testing.T) {
	tests := []struct {
		name string
		vm   alchemy_build.VirtualMachineConfig
		want bool
	}{
		{
			name: "utm supported",
			vm: alchemy_build.VirtualMachineConfig{
				VirtualizationEngine: alchemy_build.VirtualizationEngineUtm,
			},
			want: true,
		},
		{
			name: "hyperv supported",
			vm: alchemy_build.VirtualMachineConfig{
				VirtualizationEngine: alchemy_build.VirtualizationEngineHyperv,
			},
			want: true,
		},
		{
			name: "tart supported",
			vm: alchemy_build.VirtualMachineConfig{
				VirtualizationEngine: alchemy_build.VirtualizationEngineTart,
			},
			want: true,
		},
		{
			name: "qemu supported",
			vm: alchemy_build.VirtualMachineConfig{
				HostOs:               alchemy_build.HostOsLinux,
				VirtualizationEngine: alchemy_build.VirtualizationEngineQemu,
				OS:                   "ubuntu",
				UbuntuType:           "server",
				Arch:                 "amd64",
			},
			want: true,
		},
		{
			name: "qemu windows supported",
			vm: alchemy_build.VirtualMachineConfig{
				HostOs:               alchemy_build.HostOsLinux,
				VirtualizationEngine: alchemy_build.VirtualizationEngineQemu,
				OS:                   "windows11",
				Arch:                 "amd64",
			},
			want: true,
		},
		{
			name: "virtualbox unsupported",
			vm: alchemy_build.VirtualMachineConfig{
				VirtualizationEngine: alchemy_build.VirtualizationEngineVirtualBox,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		if got := isCreateSupported(tt.vm); got != tt.want {
			t.Fatalf("%s: expected %v, got %v", tt.name, tt.want, got)
		}
	}
}

func TestAvailableCreateVirtualMachinesOnlyReturnsSupportedConfigs(t *testing.T) {
	for _, vm := range availableCreateVirtualMachines() {
		if !isCreateSupported(vm) {
			t.Fatalf("expected only supported create configs, got engine %q", vm.VirtualizationEngine)
		}
	}
}

func TestAvailableCreateVirtualMachinesMarksLinuxCrossArchitectureTargetsUnstable(t *testing.T) {
	withCurrentHostArchitecture(t, "amd64")

	vms := availableCreateVirtualMachinesForHostOS(alchemy_build.HostOsLinux)
	requireVMStatus(t, vms, "server", "amd64", "stable")
	requireVMStatus(t, vms, "server", "arm64", "unstable")
	requireOnlyArch(t, defaultCreateVirtualMachinesForHostOS(alchemy_build.HostOsLinux), "amd64")
}

func TestAvailableCreateVirtualMachinesMarksLinuxCrossArchitectureTargetsUnstableOnArm64(t *testing.T) {
	withCurrentHostArchitecture(t, "arm64")

	vms := availableCreateVirtualMachinesForHostOS(alchemy_build.HostOsLinux)
	requireVMStatus(t, vms, "desktop", "arm64", "stable")
	requireVMStatus(t, vms, "desktop", "amd64", "unstable")
	requireOnlyArch(t, defaultCreateVirtualMachinesForHostOS(alchemy_build.HostOsLinux), "arm64")
}

func TestRunCreateAllSkipsUnstableLinuxCrossArchitectureTargets(t *testing.T) {
	withCurrentHostArchitecture(t, "amd64")
	previousRunDeployFunc := runDeployFunc
	t.Cleanup(func() {
		runDeployFunc = previousRunDeployFunc
	})

	var created []alchemy_build.VirtualMachineConfig
	runDeployFunc = func(vm alchemy_build.VirtualMachineConfig) error {
		created = append(created, vm)
		return nil
	}

	if err := runCreateAll(defaultCreateVirtualMachinesForHostOS(alchemy_build.HostOsLinux)); err != nil {
		t.Fatalf("expected create all to succeed, got %v", err)
	}

	if len(created) != 3 {
		t.Fatalf("expected create all to run the three native amd64 Linux targets, got %d", len(created))
	}
	requireOnlyArch(t, created, "amd64")
}

func TestPrintAvailableCreateCombinationsIncludesExistingTargetState(t *testing.T) {
	previousTargetInspector := inspectCreateTargetExists
	previousArtifactInspector := inspectCreateArtifactExists
	t.Cleanup(func() {
		inspectCreateTargetExists = previousTargetInspector
		inspectCreateArtifactExists = previousArtifactInspector
	})

	inspectCreateTargetExists = func(vm alchemy_build.VirtualMachineConfig) (bool, error) {
		return vm.OS == "windows11", nil
	}
	inspectCreateArtifactExists = func(vm alchemy_build.VirtualMachineConfig) (bool, error) {
		return vm.OS != "ubuntu", nil
	}

	var buf bytes.Buffer
	err := printVirtualMachineCombinationTable(
		&buf,
		"Available create combinations for host OS: windows",
		"No create combinations are available for the current host OS.",
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
			{
				OS:                   "macos",
				Arch:                 "arm64",
				HostOs:               alchemy_build.HostOsDarwin,
				VirtualizationEngine: alchemy_build.VirtualizationEngineTart,
			},
		},
		[]string{"OS", "Type", "Arch", "Artifact", "Create"},
		func(vm alchemy_build.VirtualMachineConfig) ([]string, error) {
			targetExists, err := inspectCreateTargetExists(vm)
			if err != nil {
				return nil, err
			}

			if vm.VirtualizationEngine == alchemy_build.VirtualizationEngineTart {
				createState := "ready to create"
				if targetExists {
					createState = "already created"
				}
				return []string{vm.OS, displayVirtualMachineType(vm), vm.Arch, "public image", createState}, nil
			}

			artifactsExist, err := inspectCreateArtifactExists(vm)
			if err != nil {
				return nil, err
			}

			artifactState := "missing"
			createState := "build required"
			if targetExists {
				createState = "already created"
			}
			if artifactsExist {
				artifactState = "exists"
				if !targetExists {
					createState = "ready to create"
				}
			}

			return []string{vm.OS, displayVirtualMachineType(vm), vm.Arch, artifactState, createState}, nil
		},
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "windows11") || !strings.Contains(output, "already created") {
		t.Fatalf("expected existing windows create target row, got %q", output)
	}
	if !strings.Contains(output, "ubuntu") || !strings.Contains(output, "build required") {
		t.Fatalf("expected missing ubuntu artifact row, got %q", output)
	}
	if !strings.Contains(output, "macos") || !strings.Contains(output, "public image") || !strings.Contains(output, "ready to create") {
		t.Fatalf("expected tart row to keep public image artifact state, got %q", output)
	}
}

// The gateway decides from `exists` whether a VM it owned is gone; the display text
// of the "create" column may change freely.
func TestCreateListJSONReportsExists(t *testing.T) {
	previousTargetInspector := inspectCreateTargetExists
	previousArtifactInspector := inspectCreateArtifactExists
	previousJSON := listOutputJSON
	t.Cleanup(func() {
		inspectCreateTargetExists = previousTargetInspector
		inspectCreateArtifactExists = previousArtifactInspector
		listOutputJSON = previousJSON
	})
	listOutputJSON = true
	inspected := 0
	inspectCreateTargetExists = func(vm alchemy_build.VirtualMachineConfig) (bool, error) {
		inspected++
		return vm.OS == "windows11", nil
	}
	inspectCreateArtifactExists = func(alchemy_build.VirtualMachineConfig) (bool, error) { return true, nil }
	vms := []alchemy_build.VirtualMachineConfig{
		{OS: "windows11", Arch: "amd64", HostOs: alchemy_build.HostOsWindows, VirtualizationEngine: alchemy_build.VirtualizationEngineHyperv},
		{OS: "ubuntu", UbuntuType: "server", Arch: "amd64", HostOs: alchemy_build.HostOsWindows, VirtualizationEngine: alchemy_build.VirtualizationEngineHyperv},
		{OS: "macos", Arch: "arm64", HostOs: alchemy_build.HostOsDarwin, VirtualizationEngine: alchemy_build.VirtualizationEngineTart},
	}

	var buf bytes.Buffer
	if err := writeCreateCombinations(&buf, vms); err != nil {
		t.Fatal(err)
	}
	var listing struct {
		Targets []struct {
			OS     string `json:"os"`
			Exists *bool  `json:"exists"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(buf.Bytes(), &listing); err != nil {
		t.Fatalf("%v: %s", err, buf.String())
	}
	if len(listing.Targets) != len(vms) {
		t.Fatalf("targets: %s", buf.String())
	}
	for _, target := range listing.Targets {
		if target.Exists == nil || *target.Exists != (target.OS == "windows11") {
			t.Fatalf("exists of %s: %s", target.OS, buf.String())
		}
	}
	if inspected != len(vms) {
		t.Fatalf("each VM must be inspected once, got %d inspections", inspected)
	}
}

func TestRunDeployReturnsErrorWhenCreateTargetAlreadyExists(t *testing.T) {
	previousTargetInspector := inspectCreateTargetExists
	t.Cleanup(func() {
		inspectCreateTargetExists = previousTargetInspector
	})

	inspectCreateTargetExists = func(vm alchemy_build.VirtualMachineConfig) (bool, error) {
		return true, nil
	}

	vm := alchemy_build.VirtualMachineConfig{
		OS:                   "ubuntu",
		UbuntuType:           "server",
		Arch:                 "amd64",
		HostOs:               alchemy_build.HostOsWindows,
		VirtualizationEngine: alchemy_build.VirtualizationEngineHyperv,
	}

	err := runDeploy(vm)
	if err == nil {
		t.Fatal("expected create preflight to fail when target already exists")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected existing-target error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "sailwright start ubuntu --type server --arch amd64") {
		t.Fatalf("expected start hint in error, got %q", err.Error())
	}
}

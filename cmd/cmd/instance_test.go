package cmd

import (
	"bytes"
	"testing"

	build "github.com/csautter/dev-alchemy/pkg/build"
	"github.com/csautter/dev-alchemy/pkg/provision"
	"github.com/spf13/cobra"
)

func TestCreateAndStartSelectInstance(t *testing.T) {
	oldDeploy, oldStart := runDeployFunc, runStartFunc
	oldArch, oldType := arch, osType
	defer func() { runDeployFunc = oldDeploy; runStartFunc = oldStart; arch = oldArch; osType = oldType }()
	for _, command := range []*cobra.Command{createCmd, startCmd} {
		var got build.VirtualMachineConfig
		run := func(vm build.VirtualMachineConfig) error { got = vm; return nil }
		runDeployFunc = run
		runStartFunc = run
		old := commandInstanceName(command)
		changed := command.Flags().Lookup("name").Changed
		command.Flags().Set("name", "work")
		arch = "amd64"
		osType = "server"
		if err := command.RunE(command, []string{"ubuntu"}); err != nil {
			t.Fatal(err)
		}
		if got.Name != "work" || got.OS != "ubuntu" {
			t.Fatalf("%s selected %+v", command.Name(), got)
		}
		got = build.VirtualMachineConfig{}
		command.Flags().Set("name", "default")
		if err := command.RunE(command, []string{"ubuntu"}); err == nil || got.OS != "" {
			t.Fatalf("%s dispatched reserved name", command.Name())
		}
		command.Flags().Set("name", old)
		command.Flags().Lookup("name").Changed = changed
	}
}

func TestBulkInstancesRequireExplicitFlag(t *testing.T) {
	old := discoverNamedInstances
	defer func() { discoverNamedInstances = old }()
	base := []build.VirtualMachineConfig{{OS: "ubuntu", Arch: "amd64"}}
	calls := 0
	discoverNamedInstances = func(vms []build.VirtualMachineConfig) ([]build.VirtualMachineConfig, error) {
		calls++
		named := vms[0]
		named.Name = "work"
		return []build.VirtualMachineConfig{named}, nil
	}
	command := &cobra.Command{}
	command.Flags().Bool("all-instances", false, "")
	addInstanceFlag(command)
	got, err := includeAllInstances(command, base)
	if err != nil || len(got) != 1 || calls != 0 {
		t.Fatalf("implicit bulk touched names: %+v %v", got, err)
	}
	command.Flags().Set("all-instances", "true")
	got, err = includeAllInstances(command, base)
	if err != nil || len(got) != 2 || got[1].Name != "work" {
		t.Fatal(got, err)
	}
	if validateInstanceSelector(command, []string{"ubuntu"}) == nil {
		t.Fatal("accepted --all-instances without all")
	}
	command.Flags().Set("name", "work")
	if validateInstanceSelector(command, []string{"all"}) == nil {
		t.Fatal("accepted --name with all")
	}
}

func TestVMQueriesSelectNamedInstance(t *testing.T) {
	oldInfo, oldID := inspectVMInfo, inspectVMIdentity
	defer func() { inspectVMInfo = oldInfo; inspectVMIdentity = oldID }()
	var got build.VirtualMachineConfig
	inspectVMInfo = func(vm build.VirtualMachineConfig) (provision.VMInfo, error) {
		got = vm
		return provision.VMInfo{Name: vm.Name}, nil
	}
	inspectVMIdentity = func(vm build.VirtualMachineConfig) (provision.VMIdentity, error) {
		got = vm
		return provision.VMIdentity{Name: vm.Name}, nil
	}
	for _, verb := range []string{"info", "identity"} {
		command := newVMCommand()
		var out bytes.Buffer
		command.SetOut(&out)
		args := []string{verb, "ubuntu", "--name", "work"}
		if verb == "info" {
			args = append(args, "--json")
		}
		command.SetArgs(args)
		if err := command.Execute(); err != nil {
			t.Fatal(err)
		}
		if got.Name != "work" || !bytes.Contains(out.Bytes(), []byte(`"name":"work"`)) {
			t.Fatal(got, out.String())
		}
	}
}

func TestNamedOperationLockTarget(t *testing.T) {
	command := &cobra.Command{}
	command.Flags().String("arch", "amd64", "")
	command.Flags().String("type", "server", "")
	addInstanceFlag(command)
	command.Flags().Set("name", "work")
	got, _ := operationLockTarget(command, []string{"ubuntu"}, false)
	if got != "ubuntu/server/amd64/work" {
		t.Fatal(got)
	}
}

func TestStopAndDestroyBulkExcludeNamesByDefault(t *testing.T) {
	oldStop, oldDestroy, oldDiscover := runStopFunc, runDestroyFunc, discoverNamedInstances
	defer func() { runStopFunc = oldStop; runDestroyFunc = oldDestroy; discoverNamedInstances = oldDiscover }()
	discoverNamedInstances = func(bases []build.VirtualMachineConfig) ([]build.VirtualMachineConfig, error) {
		vm := bases[0]
		vm.Name = "work"
		return []build.VirtualMachineConfig{vm}, nil
	}
	for _, command := range []*cobra.Command{stopCmd, destroyCmd} {
		var selected []build.VirtualMachineConfig
		run := func(vm build.VirtualMachineConfig) error { selected = append(selected, vm); return nil }
		runStopFunc = run
		runDestroyFunc = run
		oldName, oldAll := commandInstanceName(command), command.Flags().Lookup("all-instances").Value.String()
		oldChanged := command.Flags().Lookup("name").Changed
		command.Flags().Set("name", "")
		command.Flags().Lookup("name").Changed = false
		command.Flags().Set("all-instances", "false")
		if err := command.RunE(command, []string{"all"}); err != nil {
			t.Fatal(err)
		}
		for _, vm := range selected {
			if vm.Name != "" {
				t.Fatal("implicit bulk operation selected named VM")
			}
		}
		selected = nil
		command.Flags().Set("all-instances", "true")
		if err := command.RunE(command, []string{"all"}); err != nil {
			t.Fatal(err)
		}
		if selected[len(selected)-1].Name != "work" {
			t.Fatal("explicit bulk omitted name")
		}
		command.Flags().Set("name", oldName)
		command.Flags().Lookup("name").Changed = oldChanged
		command.Flags().Set("all-instances", oldAll)
	}
}

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	"github.com/spf13/cobra"
)

func TestPrintVirtualMachineCombinationTableGroupsBySortedEngine(t *testing.T) {
	vms := []alchemy_build.VirtualMachineConfig{
		{
			OS:                   "windows11",
			Arch:                 "amd64",
			VirtualizationEngine: alchemy_build.VirtualizationEngineVirtualBox,
		},
		{
			OS:                   "ubuntu",
			Arch:                 "amd64",
			UbuntuType:           "server",
			VirtualizationEngine: alchemy_build.VirtualizationEngineHyperv,
		},
		{
			OS:                   "windows11",
			Arch:                 "arm64",
			VirtualizationEngine: alchemy_build.VirtualizationEngineHyperv,
		},
	}

	var buf bytes.Buffer
	err := printVirtualMachineCombinationTable(
		&buf,
		"Available test combinations for host OS: windows",
		"No test combinations are available for the current host OS.",
		vms,
		[]string{"OS", "Type", "Arch"},
		func(vm alchemy_build.VirtualMachineConfig) ([]string, error) {
			return []string{vm.OS, displayVirtualMachineType(vm), vm.Arch}, nil
		},
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Available test combinations for host OS: windows") {
		t.Fatalf("expected title in output, got %q", output)
	}

	hypervIndex := strings.Index(output, "Virtualization engine: hyperv")
	virtualboxIndex := strings.Index(output, "Virtualization engine: virtualbox (unstable)")
	if hypervIndex == -1 || virtualboxIndex == -1 {
		t.Fatalf("expected both virtualization engines in output, got %q", output)
	}
	if hypervIndex > virtualboxIndex {
		t.Fatalf("expected sorted engine sections, got %q", output)
	}

	headerCount := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "OS") && strings.Contains(line, "Type") && strings.Contains(line, "Arch") {
			headerCount++
		}
	}
	if headerCount != 2 {
		t.Fatalf("expected headers for each engine section, got %q", output)
	}
	if !strings.Contains(output, "ubuntu     server  amd64") {
		t.Fatalf("expected ubuntu row in output, got %q", output)
	}
	if !strings.Contains(output, "windows11  -       arm64") {
		t.Fatalf("expected windows row with fallback type in output, got %q", output)
	}
}

func TestPrintVirtualMachineCombinationTableWritesEmptyMessage(t *testing.T) {
	var buf bytes.Buffer
	err := printVirtualMachineCombinationTable(
		&buf,
		"Available test combinations for host OS: windows",
		"No test combinations are available for the current host OS.",
		nil,
		[]string{"OS", "Type", "Arch"},
		func(vm alchemy_build.VirtualMachineConfig) ([]string, error) {
			return []string{vm.OS, displayVirtualMachineType(vm), vm.Arch}, nil
		},
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "No test combinations are available for the current host OS.") {
		t.Fatalf("expected empty message in output, got %q", output)
	}
}

func TestPrintVirtualMachineCombinationTableReturnsRowBuilderError(t *testing.T) {
	wantErr := errors.New("row failed")

	err := printVirtualMachineCombinationTable(
		&bytes.Buffer{},
		"Available test combinations for host OS: windows",
		"No test combinations are available for the current host OS.",
		[]alchemy_build.VirtualMachineConfig{
			{
				OS:                   "windows11",
				Arch:                 "amd64",
				VirtualizationEngine: alchemy_build.VirtualizationEngineHyperv,
			},
		},
		[]string{"OS", "Type", "Arch"},
		func(vm alchemy_build.VirtualMachineConfig) ([]string, error) {
			return nil, wantErr
		},
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected error %v, got %v", wantErr, err)
	}
}

func TestPrintVirtualMachineCombinationTableWritesJSON(t *testing.T) {
	previous := listOutputJSON
	t.Cleanup(func() { listOutputJSON = previous })
	listOutputJSON = true

	vms := []alchemy_build.VirtualMachineConfig{
		{OS: "ubuntu", UbuntuType: "server", Arch: "amd64", VirtualizationEngine: alchemy_build.VirtualizationEngineQemu},
		{OS: "local", Arch: "-", VirtualizationEngine: "local"},
	}

	var buf bytes.Buffer
	err := printVirtualMachineCombinationTable(
		&buf,
		"Available test combinations for host OS: linux",
		"No test combinations are available for the current host OS.",
		vms,
		[]string{"OS", "Type", "Arch", "Status", "Create State"},
		func(vm alchemy_build.VirtualMachineConfig) ([]string, error) {
			return []string{vm.OS, displayVirtualMachineType(vm), vm.Arch, "stable", "ready"}, nil
		},
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	output := buf.String()
	if strings.Count(output, "\n") != 1 {
		t.Fatalf("expected a single JSON line, got %q", output)
	}

	var listing vmListingJSON
	if err := json.Unmarshal([]byte(output), &listing); err != nil {
		t.Fatalf("expected valid JSON, got %v: %q", err, output)
	}
	if listing.SchemaVersion != vmListingSchemaVersion || len(listing.Targets) != 2 {
		t.Fatalf("unexpected listing %+v", listing)
	}

	ubuntu := listing.Targets[0]
	if ubuntu.OS != "ubuntu" || ubuntu.Type != "server" || ubuntu.Arch != "amd64" || ubuntu.Engine != "qemu" {
		t.Fatalf("unexpected ubuntu target %+v", ubuntu)
	}
	if ubuntu.Fields["status"] != "stable" || ubuntu.Fields["create_state"] != "ready" {
		t.Fatalf("unexpected ubuntu fields %+v", ubuntu.Fields)
	}
	if _, ok := ubuntu.Fields["os"]; ok {
		t.Fatalf("identity columns must not be repeated in fields: %+v", ubuntu.Fields)
	}
	if ubuntu.Address != nil {
		t.Fatalf("address is reserved and must be null, got %v", *ubuntu.Address)
	}

	local := listing.Targets[1]
	if local.Arch != "" || local.Type != "" {
		t.Fatalf("expected placeholder arch/type to be empty, got %+v", local)
	}
}

func TestPrintVirtualMachineCombinationTableWritesEmptyJSONTargets(t *testing.T) {
	previous := listOutputJSON
	t.Cleanup(func() { listOutputJSON = previous })
	listOutputJSON = true

	var buf bytes.Buffer
	err := printVirtualMachineCombinationTable(&buf, "title", "empty", nil, []string{"OS"}, nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if strings.TrimSpace(buf.String()) != `{"schema_version":1,"targets":[]}` {
		t.Fatalf("unexpected output %q", buf.String())
	}
}

func TestListSubcommandsExposeJSONFlag(t *testing.T) {
	for _, command := range []*cobra.Command{
		buildListCmd, createListCmd, startListCmd, stopListCmd, destroyListCmd, provisionListCmd, pushListCmd, pullListCmd,
	} {
		if command.Flags().Lookup("json") == nil {
			t.Fatalf("expected --json flag on %q", command.CommandPath())
		}
	}
}

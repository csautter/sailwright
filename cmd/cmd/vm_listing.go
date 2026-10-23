package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	"github.com/spf13/cobra"
)

type vmTableRowBuilder func(vm alchemy_build.VirtualMachineConfig) ([]string, error)

// vmListingSchemaVersion is the version of the JSON document printed by `<command> list --json`.
const vmListingSchemaVersion = 1

// listOutputJSON is bound to the --json flag of every list subcommand.
var listOutputJSON bool

type vmListingJSON struct {
	SchemaVersion int                   `json:"schema_version"`
	Targets       []vmListingTargetJSON `json:"targets"`
}

type vmListingTargetJSON struct {
	OS     string            `json:"os"`
	Type   string            `json:"type"`
	Arch   string            `json:"arch"`
	Engine string            `json:"engine"`
	Fields map[string]string `json:"fields"`
	// Address is reserved for the VM network address and is always null for now.
	Address *string `json:"address"`
}

func addListJSONFlag(command *cobra.Command) {
	command.Flags().BoolVar(&listOutputJSON, "json", false, "Print the listing as a single-line JSON document")
}

func printVirtualMachineCombinationTable(
	writer io.Writer,
	title string,
	emptyMessage string,
	vms []alchemy_build.VirtualMachineConfig,
	headers []string,
	rowBuilder vmTableRowBuilder,
) error {
	if listOutputJSON {
		return writeVirtualMachineCombinationJSON(writer, vms, headers, rowBuilder)
	}

	fmt.Fprintf(writer, "%s\n", title)
	if len(vms) == 0 {
		fmt.Fprintf(writer, "%s\n", emptyMessage)
		return nil
	}

	grouped := alchemy_build.GroupVirtualMachineConfigsByVirtualizationEngine(vms)
	for _, engine := range alchemy_build.VirtualizationEnginesForVirtualMachineConfigs(vms) {
		fmt.Fprintf(writer, "\nVirtualization engine: %s\n", alchemy_build.DisplayVirtualizationEngine(engine))
		if err := writeVirtualMachineTable(writer, headers, grouped[engine], rowBuilder); err != nil {
			return err
		}
	}

	return nil
}

// writeVirtualMachineCombinationJSON prints the listing as one JSON line. Columns beyond
// OS/Type/Arch are emitted as fields keyed by their lower-cased header.
func writeVirtualMachineCombinationJSON(
	writer io.Writer,
	vms []alchemy_build.VirtualMachineConfig,
	headers []string,
	rowBuilder vmTableRowBuilder,
) error {
	listing := vmListingJSON{SchemaVersion: vmListingSchemaVersion, Targets: []vmListingTargetJSON{}}
	for _, vm := range vms {
		row, err := rowBuilder(vm)
		if err != nil {
			return err
		}
		fields := map[string]string{}
		for i, header := range headers {
			key := listingFieldKey(header)
			if i >= len(row) || key == "os" || key == "type" || key == "arch" {
				continue
			}
			fields[key] = row[i]
		}
		listing.Targets = append(listing.Targets, vmListingTargetJSON{
			OS:     vm.OS,
			Type:   vm.UbuntuType,
			Arch:   listingArch(vm.Arch),
			Engine: string(vm.VirtualizationEngine),
			Fields: fields,
		})
	}

	encoded, err := json.Marshal(listing)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "%s\n", encoded)
	return err
}

func listingArch(arch string) string {
	if arch == "-" {
		return ""
	}
	return arch
}

func listingFieldKey(header string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(header)), " ", "_")
}

func writeVirtualMachineTable(
	writer io.Writer,
	headers []string,
	vms []alchemy_build.VirtualMachineConfig,
	rowBuilder vmTableRowBuilder,
) error {
	tw := tabwriter.NewWriter(writer, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, vm := range vms {
		row, err := rowBuilder(vm)
		if err != nil {
			return err
		}
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	return tw.Flush()
}

func displayVirtualMachineType(vm alchemy_build.VirtualMachineConfig) string {
	if vm.UbuntuType == "" {
		return "-"
	}
	return vm.UbuntuType
}

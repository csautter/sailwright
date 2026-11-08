package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	alchemy_deploy "github.com/csautter/dev-alchemy/pkg/deploy"
	"github.com/spf13/cobra"
)

type vmExistsReporter func(vm alchemy_build.VirtualMachineConfig) *bool

type vmTableRowBuilder func(vm alchemy_build.VirtualMachineConfig) ([]string, error)

type vmListingSecurityInspector func(vm alchemy_build.VirtualMachineConfig) (alchemy_deploy.NetworkMode, string, bool, error)

func existingGuestSecurityInfo(vm alchemy_build.VirtualMachineConfig) (alchemy_deploy.NetworkMode, string, bool, error) {
	return guestSecurityInfo(vm, false)
}

// Listings retain targets when optional credential metadata cannot be read.
// Lifecycle guards still propagate the same error before making changes.
func listingGuestSecurityInfo(vm alchemy_build.VirtualMachineConfig, inspect vmListingSecurityInspector) (alchemy_deploy.NetworkMode, string) {
	network, origin, private, err := inspect(vm)
	if err != nil {
		origin, private = "unknown", false
	}
	warnGuestSecurity(os.Stderr, vm, network, origin, private)
	return network, origin
}

// vmListingSchemaVersion is the version of the JSON document printed by `<command> list --json`.
const vmListingSchemaVersion = 2

// listOutputJSON is bound to the --json flag of every list subcommand.
var listOutputJSON bool

type vmListingJSON struct {
	SchemaVersion int                   `json:"schema_version"`
	Targets       []vmListingTargetJSON `json:"targets"`
}

type vmListingTargetJSON struct {
	Name   string            `json:"name"`
	OS     string            `json:"os"`
	Type   string            `json:"type"`
	Arch   string            `json:"arch"`
	Engine string            `json:"engine"`
	Fields map[string]string `json:"fields"`
	// Exists is set by create listings after inspecting the VM.
	Exists *bool `json:"exists,omitempty"`
	// Address discovery is performed by vm info, not listings.
	Address           *string `json:"address"`
	NetworkMode       string  `json:"network_mode"`
	NetworkDetail     string  `json:"network_detail"`
	CredentialsOrigin string  `json:"credentials_origin"`
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
	return printVirtualMachineCombinationsWithSecurity(writer, title, emptyMessage, vms, headers, rowBuilder, existingGuestSecurityInfo)
}

func printVirtualMachineCombinationsWithSecurity(
	writer io.Writer,
	title string,
	emptyMessage string,
	vms []alchemy_build.VirtualMachineConfig,
	headers []string,
	rowBuilder vmTableRowBuilder,
	inspectSecurity vmListingSecurityInspector,
	existsReporters ...vmExistsReporter,
) error {
	if listOutputJSON {
		return writeVirtualMachineCombinationJSON(writer, vms, headers, rowBuilder, inspectSecurity, existsReporters...)
	}

	fmt.Fprintf(writer, "%s\n", title)
	if len(vms) == 0 {
		fmt.Fprintf(writer, "%s\n", emptyMessage)
		return nil
	}

	grouped := alchemy_build.GroupVirtualMachineConfigsByVirtualizationEngine(vms)
	for _, engine := range alchemy_build.VirtualizationEnginesForVirtualMachineConfigs(vms) {
		fmt.Fprintf(writer, "\nVirtualization engine: %s\n", alchemy_build.DisplayVirtualizationEngine(engine))
		if err := writeVirtualMachineTable(writer, headers, grouped[engine], rowBuilder, inspectSecurity); err != nil {
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
	inspectSecurity vmListingSecurityInspector,
	existsReporters ...vmExistsReporter,
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
		var exists *bool
		if len(existsReporters) > 0 && existsReporters[0] != nil {
			exists = existsReporters[0](vm)
		}
		network, origin := listingGuestSecurityInfo(vm, inspectSecurity)
		listing.Targets = append(listing.Targets, vmListingTargetJSON{
			Name:        vm.Name,
			NetworkMode: network.Mode, NetworkDetail: network.Detail, CredentialsOrigin: origin,
			OS:     vm.OS,
			Type:   vm.UbuntuType,
			Arch:   listingArch(vm.Arch),
			Engine: string(vm.VirtualizationEngine),
			Fields: fields,
			Exists: exists,
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
	inspectSecurity vmListingSecurityInspector,
) error {
	tw := tabwriter.NewWriter(writer, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(append(append([]string{}, headers...), "Name", "NET", "CREDS"), "\t"))
	for _, vm := range vms {
		row, err := rowBuilder(vm)
		if err != nil {
			return err
		}
		name := vm.Name
		if name == "" {
			name = "default"
		}
		network, origin := listingGuestSecurityInfo(vm, inspectSecurity)
		fmt.Fprintln(tw, strings.Join(append(row, name, network.Mode, origin), "\t"))
	}
	return tw.Flush()
}

func displayVirtualMachineType(vm alchemy_build.VirtualMachineConfig) string {
	if vm.UbuntuType == "" {
		return "-"
	}
	return vm.UbuntuType
}

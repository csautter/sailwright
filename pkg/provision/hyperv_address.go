package provision

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strings"
	"time"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	alchemy_deploy "github.com/csautter/dev-alchemy/pkg/deploy"
)

const (
	// hypervSwitchEnvVar selects the switch in the Hyper-V Vagrantfiles.
	hypervSwitchEnvVar     = "VAGRANT_HYPERV_SWITCH"
	hypervDefaultSwitch    = "Default Switch"
	hypervAdapterQueryTime = 10 * time.Second
)

// hypervAdapter is one VM network adapter as reported by hypervAdapterScript.
type hypervAdapter struct {
	Switch     string   `json:"switch"`
	SwitchType string   `json:"switch_type"`
	Addresses  []string `json:"addresses"`
	// HostPrefixes are the host's own IPv4 addresses on the switch's vEthernet
	// adapter, e.g. 172.20.16.1/20 for the Default Switch.
	HostPrefixes []string `json:"host_prefixes"`
}

// hypervAdapterScript lists the VM's adapters with the switch they are attached to
// and the host-side subnet of that switch. The guest reports its addresses through
// integration services; the switch and host subnet come from the host, so they
// decide which reported address is accepted.
func hypervAdapterScript(vmName string) string {
	quoted := "'" + strings.ReplaceAll(vmName, "'", "''") + "'"
	return `$ErrorActionPreference = 'Stop'
$adapters = @(foreach ($a in @(Get-VMNetworkAdapter -VMName ` + quoted + `)) {
  $type = ''
  $prefixes = @()
  if ($a.SwitchName) {
    $switch = Get-VMSwitch -Name $a.SwitchName -ErrorAction SilentlyContinue
    if ($switch) { $type = [string]$switch.SwitchType }
    $prefixes = @(Get-NetIPAddress -InterfaceAlias ('vEthernet (' + $a.SwitchName + ')') -AddressFamily IPv4 -ErrorAction SilentlyContinue | ForEach-Object { $_.IPAddress + '/' + $_.PrefixLength })
  }
  [pscustomobject]@{ switch = [string]$a.SwitchName; switch_type = $type; addresses = @($a.IPAddresses | ForEach-Object { [string]$_ }); host_prefixes = $prefixes }
})
ConvertTo-Json -InputObject $adapters -Depth 4 -Compress`
}

// hypervSwitch returns the switch sailwright's Vagrantfiles attach VMs to.
func hypervSwitch(getenv func(string) string) string {
	if value := strings.TrimSpace(getenv(hypervSwitchEnvVar)); value != "" {
		return value
	}
	return hypervDefaultSwitch
}

// hypervNetworkMode maps a switch type to the vm info network mode. Internal
// switches (the Default Switch, or an internal switch with NAT as on the CI
// runners) are only reachable from this host; external switches bridge the LAN.
func hypervNetworkMode(switchType string) string {
	switch strings.ToLower(switchType) {
	case "internal":
		return "nat"
	case "external":
		return "bridged"
	}
	return "unknown"
}

// selectHyperVAddress picks the address the host can reach: an IPv4 unicast
// address of an adapter on a host-reachable switch, inside the host's subnet on that
// switch when the host has one. Adapters on the configured switch come first.
func selectHyperVAddress(adapters []hypervAdapter, preferredSwitch string) (netip.Addr, string, error) {
	ordered := append([]hypervAdapter(nil), adapters...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return strings.EqualFold(ordered[i].Switch, preferredSwitch) && !strings.EqualFold(ordered[j].Switch, preferredSwitch)
	})
	var rejected []string
	for _, adapter := range ordered {
		name := adapter.Switch
		if name == "" {
			name = "no switch"
		}
		if adapter.Switch == "" || strings.EqualFold(adapter.SwitchType, "private") {
			rejected = append(rejected, fmt.Sprintf("%s (not reachable from the host)", name))
			continue
		}
		var prefixes []netip.Prefix
		for _, value := range adapter.HostPrefixes {
			if prefix, err := netip.ParsePrefix(strings.TrimSpace(value)); err == nil && prefix.Addr().Is4() {
				prefixes = append(prefixes, prefix.Masked())
			}
		}
		var outside []string
		for _, value := range adapter.Addresses {
			address, err := netip.ParseAddr(strings.TrimSpace(value))
			if err != nil || !address.Is4() || !address.IsGlobalUnicast() || address.IsLinkLocalUnicast() {
				continue
			}
			if len(prefixes) == 0 || prefixContains(prefixes, address) {
				return address, hypervNetworkMode(adapter.SwitchType), nil
			}
			outside = append(outside, address.String())
		}
		switch {
		case len(outside) > 0:
			rejected = append(rejected, fmt.Sprintf("%s: %s outside the host subnet %s", name, strings.Join(outside, ", "), joinPrefixes(prefixes)))
		default:
			rejected = append(rejected, fmt.Sprintf("%s: no IPv4 address reported yet", name))
		}
	}
	if len(rejected) == 0 {
		return netip.Addr{}, "", fmt.Errorf("VM has no network adapter")
	}
	return netip.Addr{}, "", fmt.Errorf("no host-reachable IPv4 address (%s)", strings.Join(rejected, "; "))
}

func prefixContains(prefixes []netip.Prefix, address netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func joinPrefixes(prefixes []netip.Prefix) string {
	values := make([]string, len(prefixes))
	for i, prefix := range prefixes {
		values[i] = prefix.String()
	}
	return strings.Join(values, ", ")
}

// parseHypervAdapters reads the JSON document from the script output, ignoring any
// warning lines PowerShell prints before it.
func parseHypervAdapters(output string) ([]hypervAdapter, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "[") {
			continue
		}
		var adapters []hypervAdapter
		if err := json.Unmarshal([]byte(line), &adapters); err != nil {
			return nil, fmt.Errorf("parse Hyper-V adapter list: %w", err)
		}
		return adapters, nil
	}
	return nil, fmt.Errorf("Hyper-V adapter query printed no adapter list")
}

// discoverHyperVNetwork queries the hypervisor for the VM's reachable address and
// network mode. Discovery also works when Vagrant state belongs to another data
// directory. When the query finds no usable address, Vagrant discovery is the
// fallback.
func discoverHyperVNetwork(vm alchemy_build.VirtualMachineConfig) (string, string, error) {
	settings, err := alchemy_deploy.ResolveHypervVagrantExecutionSettings(vm)
	if err != nil {
		return "", "", err
	}
	name := ""
	for _, value := range settings.VagrantEnv {
		if strings.HasPrefix(value, "VAGRANT_VM_NAME=") {
			name = strings.TrimPrefix(value, "VAGRANT_VM_NAME=")
		}
	}
	if name == "" {
		return "", "", fmt.Errorf("managed Hyper-V VM name unavailable")
	}
	output, queryErr := runProvisionCommandWithCombinedOutputWithEnv(alchemy_build.GetDirectoriesInstance().ProjectDir, hypervAdapterQueryTime, "powershell.exe", []string{"-NoProfile", "-NonInteractive", "-Command", hypervAdapterScript(name)}, nil)
	var adapters []hypervAdapter
	preferred := hypervSwitch(os.Getenv)
	if queryErr == nil {
		adapters, queryErr = parseHypervAdapters(output)
	}
	if queryErr == nil {
		address, mode, err := selectHyperVAddress(adapters, preferred)
		if err == nil {
			return address.String(), mode, nil
		}
		queryErr = err
	}
	// Vagrant asks the guest directly, which also covers integration services that
	// have not reported an address yet. Its answer passes the same switch checks.
	var ip string
	if vm.OS == "ubuntu" {
		ip, err = discoverLinuxVagrantIPv4(settings.VagrantDir, settings.VagrantEnv)
	} else {
		ip, err = discoverWindowsVagrantIPv4(settings.VagrantDir, settings.VagrantEnv)
	}
	if err != nil {
		return "", "", fmt.Errorf("Hyper-V VM %s: %v; Vagrant discovery failed: %w", name, queryErr, err)
	}
	if len(adapters) == 0 {
		return ip, "unknown", nil
	}
	for i := range adapters {
		adapters[i].Addresses = []string{ip}
	}
	address, mode, err := selectHyperVAddress(adapters, preferred)
	if err != nil {
		return "", "", fmt.Errorf("Hyper-V VM %s: Vagrant reported %s: %w", name, ip, err)
	}
	return address.String(), mode, nil
}

// DiscoverHyperVAddress returns the host-reachable address of a managed Hyper-V VM.
func DiscoverHyperVAddress(vm alchemy_build.VirtualMachineConfig) (string, error) {
	ip, _, err := discoverHyperVNetwork(vm)
	return ip, err
}

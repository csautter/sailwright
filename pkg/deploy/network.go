package deploy

import (
	"encoding/json"
	"encoding/xml"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

// NetworkMode describes exposure, rather than promising a particular firewall configuration.
// Host-only networks are classified as nat because they do not expose the guest to the LAN.
type NetworkMode struct {
	Mode   string `json:"mode"`
	Detail string `json:"detail"`
}

var runNetworkCommand = runCommandWithCombinedOutput

func unknownNetwork(detail string) NetworkMode { return NetworkMode{"unknown", detail} }

// InspectNetworkMode inspects the current VM, or the intended deployment when creating is true.
// Failed or incomplete inspection always returns unknown.
func InspectNetworkMode(vm alchemy_build.VirtualMachineConfig, creating bool) NetworkMode {
	project := alchemy_build.GetDirectoriesInstance().ProjectDir
	run := func(executable string, args ...string) (string, error) {
		return runNetworkCommand(project, 15*time.Second, executable, args)
	}
	switch vm.VirtualizationEngine {
	case alchemy_build.VirtualizationEngineUtm:
		var file string
		if creating {
			osName := strings.Split(vm.OS, "-")[0]
			file = filepath.Join(project, "deployments", "utm", osName+"-"+vm.Arch, "config.plist")
		} else {
			bundle, err := utmVirtualMachinePath(vm)
			if err != nil {
				return unknownNetwork("UTM VM path unavailable")
			}
			file = filepath.Join(bundle, "config.plist")
		}
		// #nosec G304 -- file is derived from the managed UTM bundle or bundled deployment template, never guest output.
		data, err := os.ReadFile(file)
		if err != nil {
			return unknownNetwork("UTM configuration unavailable")
		}
		if strings.HasPrefix(string(data), "bplist") {
			output, err := run("plutil", "-convert", "xml1", "-o", "-", file)
			if err != nil {
				return unknownNetwork("UTM plist conversion failed")
			}
			data = []byte(output)
		}
		return utmNetworkMode(data)
	case alchemy_build.VirtualizationEngineHyperv:
		var command string
		if creating {
			name := defaultIfEmpty(strings.TrimSpace(os.Getenv("VAGRANT_HYPERV_SWITCH")), "Default Switch")
			command = "Get-VMSwitch -Name " + powershellSingleQuote(name)
		} else {
			settings, err := resolveHypervVagrantDeploySettings(vm, project)
			if err != nil {
				return unknownNetwork("Hyper-V settings unavailable")
			}
			name, err := hypervVagrantVMName(settings.VagrantEnv)
			if err != nil {
				return unknownNetwork("Hyper-V VM name unavailable")
			}
			command = "Get-VMNetworkAdapter -VMName " + powershellSingleQuote(name) + " | ForEach-Object { Get-VMSwitch -Name $_.SwitchName -ErrorAction Stop }"
		}
		output, err := run("powershell", "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference='Stop'; $switches = @("+command+" | Select-Object Name, @{Name='SwitchType';Expression={$_.SwitchType.ToString()}}); ConvertTo-Json -InputObject $switches -Compress")
		if err != nil {
			return unknownNetwork("Hyper-V switch inspection failed")
		}
		return hypervNetworkMode(output)
	case alchemy_build.VirtualizationEngineQemu:
		uri := linuxLibvirtURI()
		if creating {
			if !linuxLibvirtUsesSystemConnection(uri) {
				return NetworkMode{"nat", "libvirt session user networking"}
			}
			output, err := run("virsh", "--connect", uri, "net-dumpxml", "default")
			if err != nil {
				return unknownNetwork("libvirt default network inspection failed")
			}
			return libvirtNetworkMode(output)
		}
		output, err := run("virsh", "--connect", uri, "dumpxml", linuxLibvirtDomainName(vm))
		if err != nil {
			return unknownNetwork("libvirt domain inspection failed")
		}
		var domain struct {
			XMLName    xml.Name `xml:"domain"`
			Interfaces []struct {
				Type   string `xml:"type,attr"`
				Source struct {
					Network string `xml:"network,attr"`
				} `xml:"source"`
			} `xml:"devices>interface"`
		}
		if xml.Unmarshal([]byte(output), &domain) != nil {
			return unknownNetwork("invalid libvirt domain XML")
		}
		modes := []NetworkMode{}
		for _, adapter := range domain.Interfaces {
			switch adapter.Type {
			case "user":
				modes = append(modes, NetworkMode{"nat", "libvirt user networking"})
			case "bridge", "direct":
				modes = append(modes, NetworkMode{"bridged", "libvirt " + adapter.Type + " interface"})
			case "network":
				xmlOutput, err := run("virsh", "--connect", uri, "net-dumpxml", adapter.Source.Network)
				if err != nil {
					modes = append(modes, unknownNetwork("libvirt network inspection failed"))
				} else {
					modes = append(modes, libvirtNetworkMode(xmlOutput))
				}
			default:
				modes = append(modes, unknownNetwork("unrecognized libvirt interface"))
			}
		}
		return combineNetworkModes(modes)
	case alchemy_build.VirtualizationEngineTart:
		if creating {
			return tartPlannedNetworkMode()
		}
		output, err := run("ps", "-ww", "-axo", "command=")
		if err != nil {
			return unknownNetwork("Tart process inspection failed")
		}
		if mode, found := tartProcessNetworkMode(output, tartMacOSVMName(vm)); found {
			return mode
		}
		stateOutput, err := run("tart", "list", "--format", "json")
		state, valid := tartListLocalVMStateFromJSON(stateOutput, tartMacOSVMName(vm))
		if err != nil || !valid || state.running {
			return unknownNetwork("Tart running command unavailable")
		}
		return tartPlannedNetworkMode()
	default:
		return unknownNetwork("network inspection unavailable for this engine")
	}
}

func combineNetworkModes(modes []NetworkMode) NetworkMode {
	if len(modes) == 0 {
		return unknownNetwork("no network adapters found")
	}
	result := NetworkMode{"nat", ""}
	details := []string{}
	for _, mode := range modes {
		details = append(details, mode.Detail)
		if mode.Mode == "bridged" || mode.Mode == "unknown" && result.Mode != "bridged" {
			result.Mode = mode.Mode
		}
	}
	result.Detail = strings.Join(details, "; ")
	return result
}

type plistNode struct {
	XMLName  xml.Name
	Text     string      `xml:",chardata"`
	Children []plistNode `xml:",any"`
}

func utmNetworkMode(data []byte) NetworkMode {
	var plist plistNode
	if xml.Unmarshal(data, &plist) != nil || plist.XMLName.Local != "plist" {
		return unknownNetwork("invalid UTM plist")
	}
	for _, dict := range plist.Children {
		for i, key := range dict.Children {
			if key.XMLName.Local != "key" || key.Text != "Network" || i+1 >= len(dict.Children) {
				continue
			}
			modes := []NetworkMode{}
			for _, adapter := range dict.Children[i+1].Children {
				modes = append(modes, utmAdapterNetworkMode(adapter))
			}
			return combineNetworkModes(modes)
		}
	}
	return unknownNetwork("UTM network configuration unavailable")
}

// plistDictionary rejects duplicate keys and malformed dictionaries so ambiguous
// configuration cannot provide a false NAT result.
func plistDictionary(node plistNode) (map[string]plistNode, bool) {
	if node.XMLName.Local != "dict" || len(node.Children)%2 != 0 {
		return nil, false
	}
	values := make(map[string]plistNode, len(node.Children)/2)
	for i := 0; i < len(node.Children); i += 2 {
		key := node.Children[i]
		if key.XMLName.Local != "key" {
			return nil, false
		}
		if _, duplicate := values[key.Text]; duplicate {
			return nil, false
		}
		values[key.Text] = node.Children[i+1]
	}
	return values, true
}

func utmAdapterNetworkMode(adapter plistNode) NetworkMode {
	values, valid := plistDictionary(adapter)
	if !valid || values["Mode"].XMLName.Local != "string" {
		return unknownNetwork("UTM adapter mode unavailable")
	}
	value := values["Mode"].Text
	switch value {
	case "Bridged":
		return NetworkMode{"bridged", "UTM Bridged"}
	case "Shared", "Host":
		return NetworkMode{"nat", "UTM " + value}
	case "Emulated":
		forwards, exists := values["PortForward"]
		if exists {
			if forwards.XMLName.Local != "array" {
				return unknownNetwork("UTM port forwarding configuration invalid")
			}
			for _, forward := range forwards.Children {
				if !utmPortForwardIsLoopback(forward) {
					return unknownNetwork("UTM Emulated with non-loopback or unknown port forwarding")
				}
			}
		}
		return NetworkMode{"nat", "UTM Emulated"}
	default:
		return unknownNetwork("UTM " + value)
	}
}

func utmPortForwardIsLoopback(forward plistNode) bool {
	values, valid := plistDictionary(forward)
	if !valid {
		return false
	}
	address, exists := values["HostAddress"]
	// UTM before 4.4 used camel-case keys for port forwards.
	legacy, legacyExists := values["hostAddress"]
	if exists && legacyExists {
		return false
	}
	if legacyExists {
		address = legacy
	}
	if address.XMLName.Local != "string" {
		return false
	}
	// Missing/empty addresses are not positive evidence of a loopback binding.
	ip := net.ParseIP(address.Text)
	return ip != nil && ip.IsLoopback()
}

func hypervNetworkMode(output string) NetworkMode {
	var switches []struct{ Name, SwitchType string }
	if json.Unmarshal([]byte(output), &switches) != nil {
		return unknownNetwork("invalid Hyper-V switch output")
	}
	modes := []NetworkMode{}
	for _, sw := range switches {
		mode := unknownNetwork("Hyper-V " + sw.Name + " (" + sw.SwitchType + ")")
		switch sw.SwitchType {
		case "External":
			mode.Mode = "bridged"
		case "Internal", "Private":
			mode.Mode = "nat"
		}
		modes = append(modes, mode)
	}
	return combineNetworkModes(modes)
}

func libvirtNetworkMode(output string) NetworkMode {
	var network struct {
		XMLName xml.Name `xml:"network"`
		Forward *struct {
			Mode string `xml:"mode,attr"`
			NAT  struct {
				IPv6 string `xml:"ipv6,attr"`
			} `xml:"nat"`
		} `xml:"forward"`
		IPs []struct {
			Family string `xml:"family,attr"`
		} `xml:"ip"`
	}
	if xml.Unmarshal([]byte(output), &network) != nil {
		return unknownNetwork("invalid libvirt network XML")
	}
	if network.Forward == nil {
		return NetworkMode{"nat", "libvirt isolated network"}
	}
	mode := unknownNetwork("libvirt forward mode " + network.Forward.Mode)
	switch network.Forward.Mode {
	case "nat", "": // A forward element without a mode defaults to NAT.
		mode.Mode = "nat"
		for _, ip := range network.IPs {
			if ip.Family == "ipv6" && network.Forward.NAT.IPv6 != "yes" {
				return NetworkMode{"bridged", "libvirt NAT with routed IPv6"}
			}
		}
	case "bridge", "open", "route":
		mode.Mode = "bridged"
	}
	return mode
}

func tartPlannedNetworkMode() NetworkMode {
	if strings.TrimSpace(os.Getenv(tartMacOSBridgedInterfaceEnvVar)) != "" {
		return NetworkMode{"bridged", "Tart bridged interface configured"}
	}
	return NetworkMode{"nat", "Tart default NAT"}
}

func tartProcessNetworkMode(output, name string) (NetworkMode, bool) {
	var modes []NetworkMode
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		// ps loses argv boundaries. A trailing name alone does not prove which VM
		// this process runs; only understood options may precede a NAT target.
		if !strings.HasSuffix(line, " "+name) {
			continue
		}
		parts := strings.Fields(strings.TrimSuffix(line, " "+name))
		if len(parts) < 2 || filepath.Base(parts[0]) != "tart" || parts[1] != "run" {
			continue
		}
		modes = append(modes, tartRunArgumentsNetworkMode(parts[2:]))
	}
	if len(modes) == 0 {
		return NetworkMode{}, false
	}
	return combineNetworkModes(modes), true
}

func tartRunArgumentsNetworkMode(args []string) NetworkMode {
	mode := NetworkMode{"nat", "Tart running with NAT"}
	for _, arg := range args {
		if arg == "--net-bridged" || strings.HasPrefix(arg, "--net-bridged=") {
			return NetworkMode{"bridged", "Tart --net-bridged"}
		}
		switch arg {
		case "--net-softnet", "--no-graphics", "--no-audio", "--no-clipboard", "--no-usb-accessories":
			// Known boolean flags do not introduce an unknown network backend or
			// consume a value that could actually be part of another VM's name.
		default:
			mode = unknownNetwork("Tart network backend or process arguments ambiguous")
		}
	}
	return mode
}

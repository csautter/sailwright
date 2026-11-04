package deploy

import (
	"encoding/json"
	"encoding/xml"
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
				mode := unknownNetwork("UTM adapter mode unavailable")
				for j, field := range adapter.Children {
					if field.XMLName.Local != "key" || field.Text != "Mode" || j+1 >= len(adapter.Children) {
						continue
					}
					value := adapter.Children[j+1].Text
					mode.Detail = "UTM " + value
					switch value {
					case "Shared", "Emulated", "Host":
						mode.Mode = "nat"
					case "Bridged":
						mode.Mode = "bridged"
					}
				}
				modes = append(modes, mode)
			}
			return combineNetworkModes(modes)
		}
	}
	return unknownNetwork("UTM network configuration unavailable")
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
		Forward struct {
			Mode string `xml:"mode,attr"`
		} `xml:"forward"`
	}
	if xml.Unmarshal([]byte(output), &network) != nil {
		return unknownNetwork("invalid libvirt network XML")
	}
	mode := unknownNetwork("libvirt forward mode " + network.Forward.Mode)
	switch network.Forward.Mode {
	case "nat":
		mode.Mode = "nat"
	case "":
		mode = NetworkMode{"nat", "libvirt isolated network"}
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
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		// ps prints argv without quoting; matching the entire trailing VM name also handles spaces.
		if !strings.HasSuffix(line, " "+name) {
			continue
		}
		parts := strings.Fields(strings.TrimSuffix(line, " "+name))
		if len(parts) < 2 || filepath.Base(parts[0]) != "tart" || parts[1] != "run" {
			continue
		}
		for _, arg := range parts[2:] {
			if arg == "--net-bridged" || strings.HasPrefix(arg, "--net-bridged=") {
				return NetworkMode{"bridged", "Tart --net-bridged"}, true
			}
		}
		return NetworkMode{"nat", "Tart running with NAT"}, true
	}
	return NetworkMode{}, false
}

package provision

import (
	"encoding/xml"
	"time"
)

// discoverLibvirtNetworkMode inspects active host configuration. A connection URI
// or a network named "default" does not establish that the VM uses NAT. Failed
// or ambiguous discovery stays unknown so the gateway keeps its LAN audit warning.
func discoverLibvirtNetworkMode(project, domain, uri string, run func(string, time.Duration, string, []string) (string, error)) string {
	query := func(command, name string) (string, error) {
		return run(project, 5*time.Second, "virsh", []string{"--connect", uri, command, name})
	}
	output, err := query("dumpxml", domain)
	if err != nil {
		return "unknown"
	}
	var vm struct {
		XMLName    xml.Name `xml:"domain"`
		Interfaces []struct {
			Type   string `xml:"type,attr"`
			Source struct {
				Network string `xml:"network,attr"`
			} `xml:"source"`
		} `xml:"devices>interface"`
	}
	if xml.Unmarshal([]byte(output), &vm) != nil || len(vm.Interfaces) == 0 {
		return "unknown"
	}
	result := ""
	for _, nic := range vm.Interfaces {
		mode := "unknown"
		switch nic.Type {
		case "user":
			mode = "user"
		case "bridge", "direct":
			mode = "bridged"
		case "network":
			if nic.Source.Network != "" {
				networkXML, err := query("net-dumpxml", nic.Source.Network)
				var network struct {
					XMLName xml.Name `xml:"network"`
					Forward struct {
						Mode string `xml:"mode,attr"`
					} `xml:"forward"`
				}
				if err == nil && xml.Unmarshal([]byte(networkXML), &network) == nil {
					switch network.Forward.Mode {
					case "nat":
						mode = "nat"
					case "bridge":
						mode = "bridged"
					}
				}
			}
		}
		// Every interface must have the same known mode before suppressing warnings.
		if result == "" {
			result = mode
		} else if mode != result {
			result = "unknown"
		}
	}
	return result
}

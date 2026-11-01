package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// vmAccessListing is the listing that names the VMs vm_info and tunnels can reach.
const vmAccessListing = "start"

// sshControlPersist keeps a shared SSH connection open this long after its last
// session ends.
const sshControlPersist = "60s"

// VMInfoOutput contains guest-only credentials and controller configuration.
type VMInfoOutput struct {
	OS               string `json:"os"`
	Type             string `json:"type"`
	Arch             string `json:"arch"`
	Engine           string `json:"engine"`
	Address          string `json:"address"`
	NetworkMode      string `json:"network_mode"`
	Protocol         string `json:"protocol"`
	Port             int    `json:"port"`
	Username         string `json:"username"`
	Password         string `json:"password"`
	SSHConfig        string `json:"ssh_config"`
	AnsibleInventory string `json:"ansible_inventory"`
	ListenCommand    string `json:"listen_command,omitempty"`
}

func (g *Gateway) toolVMInfo(ctx context.Context, _ *mcp.CallToolRequest, in vmTargetInput) (*mcp.CallToolResult, VMInfoOutput, error) {
	target, err := g.grammar().ResolveVMTarget(vmAccessListing, in.OS, in.Type, in.Arch, "")
	if err != nil {
		return nil, VMInfoOutput{}, err
	}
	key := target.Key()
	// Resolve first, so the operator is only asked about a VM that is running.
	vm, err := g.vmResolver.Resolve(ctx, target.OS, target.Type, target.Arch)
	if err != nil {
		if !g.ownership.allowed(key) {
			// The VM may be the operator's: its details stay on the operator console.
			return nil, VMInfoOutput{}, fmt.Errorf("%w: VM %s is not running or cannot be inspected", ErrVMAccessUnavailable, key)
		}
		return nil, VMInfoOutput{}, err
	}
	allowed, err := g.ownership.confirm(key, vm.InstanceID)
	g.recordOwnership(err)
	if !allowed {
		if err := g.requestVMAccess("vm_info", vm.OS, vm.Type, vm.Arch, vm.InstanceID); err != nil {
			return nil, VMInfoOutput{}, err
		}
	}
	if err := allowedTunnelAddress(vm.Address); err != nil {
		return nil, VMInfoOutput{}, err
	}
	if !tunnelPorts[vm.Port] {
		return nil, VMInfoOutput{}, fmt.Errorf("guest service port is outside the tunnel allowlist")
	}
	out := VMInfoOutput{OS: vm.OS, Type: vm.Type, Arch: vm.Arch, Engine: vm.Engine, Address: vm.Address.String(), NetworkMode: vm.NetworkMode, Protocol: vm.Protocol, Port: vm.Port, Username: vm.Username, Password: vm.Password}
	alias := "sailwright-" + strings.ReplaceAll(key, "/", "-")
	proxy := "sailwright-gateway vm tunnel " + key + " " + strconv.Itoa(vm.Port)
	host := map[string]any{"ansible_user": vm.Username, "ansible_password": vm.Password, "ansible_port": vm.Port}
	if vm.Protocol == "ssh" {
		if strings.ContainsAny(vm.Username, "\r\n\x00") {
			return nil, VMInfoOutput{}, fmt.Errorf("invalid SSH username")
		}
		// Connection sharing runs ssh and scp over one tunnel instead of starting a
		// tunnel, a CLI lookup and a hypervisor query for every command.
		out.SSHConfig = fmt.Sprintf("Host %s\n  HostName %s\n  User %s\n  Port %d\n  ProxyCommand %s\n  ControlMaster auto\n  ControlPath ~/.ssh/sailwright-%%C\n  ControlPersist %s\n", alias, vm.Address, strconv.Quote(vm.Username), vm.Port, proxy, sshControlPersist)
		host["ansible_host"], host["ansible_connection"] = vm.Address.String(), "ssh"
		host["ansible_ssh_common_args"] = "-o ProxyCommand=" + strconv.Quote(proxy)
		host["ansible_become_password"] = vm.Password
	} else {
		localPort := 15985
		scheme := "http"
		if vm.Port == 5986 {
			localPort, scheme = 15986, "https"
		}
		out.ListenCommand = proxy + " --listen 127.0.0.1:" + strconv.Itoa(localPort)
		host["ansible_host"], host["ansible_port"], host["ansible_connection"] = "127.0.0.1", localPort, "winrm"
		host["ansible_winrm_transport"], host["ansible_winrm_scheme"] = "basic", scheme
		host["ansible_winrm_server_cert_validation"] = "ignore"
	}
	inventory := map[string]any{"all": map[string]any{"hosts": map[string]any{alias: host}}}
	data, err := json.MarshalIndent(inventory, "", "  ")
	if err != nil {
		return nil, VMInfoOutput{}, err
	}
	out.AnsibleInventory = string(data) + "\n"
	if err := g.audit.Record(AuditEvent{Event: "vm_info", Params: map[string]any{"os": vm.OS, "type": vm.Type, "arch": vm.Arch}}); err != nil {
		return nil, VMInfoOutput{}, err
	}
	return nil, out, nil
}

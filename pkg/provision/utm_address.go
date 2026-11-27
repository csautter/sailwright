package provision

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

type dhcpdLease struct {
	Name      string
	IPAddress string
}

// resolveMultipleUtmARPCandidates disambiguates multiple ARP IPv4 candidates for
// the same UTM MAC (such as a stale first-boot ARP entry alongside the current
// DHCP lease after guest identity change).
func resolveMultipleUtmARPCandidates(
	projectDir string,
	vm alchemy_build.VirtualMachineConfig,
	macAddress string,
	candidates []string,
	deadline time.Time,
	options utmIPv4DiscoveryOptions,
) (string, error) {
	expectedHostname := strings.TrimSpace(alchemy_build.InstanceHostname(vm))
	leaseNote := fmt.Sprintf("no DHCP lease in %q matched hostname %q", options.dhcpLeasesPath, expectedHostname)

	var leaseMatched []string
	leaseContent, leaseErr := options.readFile(options.dhcpLeasesPath)
	if leaseErr != nil {
		leaseNote = fmt.Sprintf("could not read DHCP leases %q: %v", options.dhcpLeasesPath, leaseErr)
	} else {
		leaseMatched = matchUtmDHCPLeaseIPs(string(leaseContent), expectedHostname, candidates)
		if len(leaseMatched) == 1 {
			return leaseMatched[0], nil
		}
		if len(leaseMatched) > 1 {
			leaseNote = fmt.Sprintf("multiple DHCP leases in %q matched hostname %q (%s)", options.dhcpLeasesPath, expectedHostname, strings.Join(leaseMatched, ", "))
		}
	}

	port, err := resolveUtmGuestServicePort(projectDir, vm)
	if err != nil {
		return "", fmt.Errorf(
			"could not resolve UTM guest service port for MAC %q candidates [%s]: %w",
			macAddress,
			strings.Join(candidates, ", "),
			err,
		)
	}

	probeOrder := orderUtmProbeCandidates(leaseMatched, candidates)
	reachableIP, budgetExhausted := probeUtmCandidateServicePort(probeOrder, port, deadline, options)
	if reachableIP != "" {
		return reachableIP, nil
	}

	if budgetExhausted {
		return "", fmt.Errorf(
			"could not determine current IPv4 address for UTM MAC %q from candidates [%s] before discovery budget %s expired (%s; checked TCP port %d)",
			macAddress,
			strings.Join(candidates, ", "),
			options.discoveryBudget,
			leaseNote,
			port,
		)
	}

	return "", fmt.Errorf(
		"could not determine current IPv4 address for UTM MAC %q from candidates [%s]: %s and none accepted a TCP connection on port %d",
		macAddress,
		strings.Join(candidates, ", "),
		leaseNote,
		port,
	)
}

func parseDHCPDLeases(content string) []dhcpdLease {
	var (
		leases  []dhcpdLease
		inBlock bool
		current dhcpdLease
	)

	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		switch line {
		case "":
			continue
		case "{":
			inBlock = true
			current = dhcpdLease{}
			continue
		case "}":
			if inBlock && (current.Name != "" || current.IPAddress != "") {
				leases = append(leases, current)
			}
			inBlock = false
			current = dhcpdLease{}
			continue
		}

		if !inBlock {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)

		switch key {
		case "name":
			current.Name = value
		case "ip_address":
			parsedIP := net.ParseIP(value)
			if parsedIP != nil && parsedIP.To4() != nil && !parsedIP.IsLoopback() && !parsedIP.IsUnspecified() {
				current.IPAddress = parsedIP.To4().String()
			}
		}
	}

	return leases
}

func matchUtmDHCPLeaseIPs(leasesContent string, expectedHostname string, candidates []string) []string {
	expectedHostname = strings.TrimSpace(expectedHostname)
	if expectedHostname == "" || len(candidates) == 0 {
		return nil
	}

	candidateSet := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidateSet[candidate] = struct{}{}
	}

	var matched []string
	seen := make(map[string]struct{})
	for _, lease := range parseDHCPDLeases(leasesContent) {
		if lease.Name == "" || lease.IPAddress == "" {
			continue
		}
		if !strings.EqualFold(lease.Name, expectedHostname) {
			continue
		}
		if _, ok := candidateSet[lease.IPAddress]; !ok {
			continue
		}
		if _, already := seen[lease.IPAddress]; already {
			continue
		}
		seen[lease.IPAddress] = struct{}{}
		matched = append(matched, lease.IPAddress)
	}

	return matched
}

func resolveUtmGuestServicePort(projectDir string, vm alchemy_build.VirtualMachineConfig) (int, error) {
	if vm.OS == "windows11" {
		connection, err := loadWindowsUtmAnsibleConnectionConfig(projectDir)
		if err != nil {
			return 0, err
		}
		return parseGuestPort(connection.Port, utmIPv4ProbePort)
	}

	connection, err := loadUbuntuUtmAnsibleConnectionConfig(projectDir)
	if err != nil {
		return 0, err
	}
	return parseGuestPort(connection.Port, 22)
}

func orderUtmProbeCandidates(preferred []string, all []string) []string {
	if len(preferred) == 0 {
		return all
	}

	ordered := make([]string, 0, len(all))
	seen := make(map[string]struct{}, len(all))
	for _, ip := range preferred {
		if _, ok := seen[ip]; !ok {
			seen[ip] = struct{}{}
			ordered = append(ordered, ip)
		}
	}
	for _, ip := range all {
		if _, ok := seen[ip]; !ok {
			seen[ip] = struct{}{}
			ordered = append(ordered, ip)
		}
	}
	return ordered
}

func probeUtmCandidateServicePort(
	candidates []string,
	port int,
	deadline time.Time,
	options utmIPv4DiscoveryOptions,
) (string, bool) {
	portText := strconv.Itoa(port)
	for _, candidateIP := range candidates {
		remaining := deadline.Sub(options.now())
		if remaining <= 0 {
			return "", true
		}

		dialTimeout := clampDurationToRemaining(options.candidateProbeTimeout, remaining)
		dialCtx, cancel := context.WithTimeout(context.Background(), dialTimeout)
		address := net.JoinHostPort(candidateIP, portText)
		conn, err := options.dial(dialCtx, "tcp4", address)
		cancel()
		if err == nil {
			if conn != nil {
				_ = conn.Close()
			}
			return candidateIP, false
		}
	}

	return "", false
}

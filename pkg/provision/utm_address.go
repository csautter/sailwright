package provision

import (
	"context"
	"fmt"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

const dhcpdInfiniteLeaseExpiry uint64 = 0xffffffff

type dhcpdLease struct {
	Name      string
	IPAddress string
	HasExpiry bool
	ExpiresAt uint64
}

func (l dhcpdLease) isExpiredAt(now time.Time) bool {
	if !l.HasExpiry {
		return false
	}
	nowUnix := now.Unix()
	if nowUnix <= 0 {
		return false
	}
	return l.ExpiresAt < uint64(nowUnix)
}

func (l dhcpdLease) sortExpiryKey() uint64 {
	if !l.HasExpiry {
		return math.MaxUint64
	}
	return l.ExpiresAt
}

// resolveMultipleUtmARPCandidates disambiguates multiple ARP IPv4 candidates for
// the same UTM MAC (such as a stale first-boot ARP entry alongside the current
// DHCP lease after guest identity change).
//
// The returned terminal flag is true only for deterministic configuration errors
// (such as an invalid .env file or invalid guest service port), so transient
// states before ARP priming, lease flush, or guest service readiness remain
// retryable within the discovery budget.
func resolveMultipleUtmARPCandidates(
	projectDir string,
	vm alchemy_build.VirtualMachineConfig,
	macAddress string,
	candidates []string,
	deadline time.Time,
	options utmIPv4DiscoveryOptions,
) (string, bool, error) {
	expectedHostname := strings.TrimSpace(alchemy_build.InstanceHostname(vm))
	leaseNote := fmt.Sprintf("no DHCP lease in %q matched hostname %q", options.dhcpLeasesPath, expectedHostname)

	var activeMatched []string
	leaseContent, leaseErr := options.readFile(options.dhcpLeasesPath)
	if leaseErr != nil {
		leaseNote = fmt.Sprintf("could not read DHCP leases %q: %v", options.dhcpLeasesPath, leaseErr)
	} else {
		var expiredMatched []string
		activeMatched, expiredMatched = matchUtmDHCPLeaseIPs(string(leaseContent), expectedHostname, candidates, options.now())
		if len(activeMatched) == 1 {
			return activeMatched[0], false, nil
		}
		if len(activeMatched) > 1 {
			leaseNote = fmt.Sprintf(
				"multiple active DHCP leases in %q matched hostname %q (%s)",
				options.dhcpLeasesPath,
				expectedHostname,
				strings.Join(activeMatched, ", "),
			)
		} else if len(expiredMatched) > 0 {
			leaseNote = fmt.Sprintf(
				"only expired DHCP lease(s) in %q matched hostname %q (%s)",
				options.dhcpLeasesPath,
				expectedHostname,
				strings.Join(expiredMatched, ", "),
			)
		}
	}

	port, err := resolveUtmGuestServicePort(projectDir, vm)
	if err != nil {
		return "", true, fmt.Errorf(
			"could not resolve UTM guest service port for MAC %q candidates [%s]: %w",
			macAddress,
			strings.Join(candidates, ", "),
			err,
		)
	}

	probeOrder := orderUtmProbeCandidates(activeMatched, candidates)
	reachableIP, budgetExhausted := probeUtmCandidateServicePort(probeOrder, port, deadline, options)
	if reachableIP != "" {
		return reachableIP, false, nil
	}

	if budgetExhausted {
		return "", false, fmt.Errorf(
			"could not determine current IPv4 address for UTM MAC %q from candidates [%s] before discovery budget %s expired (%s; checked TCP port %d)",
			macAddress,
			strings.Join(candidates, ", "),
			options.discoveryBudget,
			leaseNote,
			port,
		)
	}

	return "", false, fmt.Errorf(
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
		block   strings.Builder
		quote   byte
	)

	for i := 0; i < len(content); i++ {
		ch := content[i]

		if quote != 0 {
			if ch == '\n' {
				// Lease values are single-line; recover from an unclosed quote at EOL.
				quote = 0
				if inBlock {
					block.WriteByte(ch)
				}
				continue
			}
			if ch == quote {
				quote = 0
			}
			if inBlock {
				block.WriteByte(ch)
			}
			continue
		}

		switch ch {
		case '#':
			for i < len(content) && content[i] != '\n' {
				i++
			}
			if inBlock && i < len(content) {
				block.WriteByte('\n')
			}
		case '"', '\'':
			if inBlock {
				if prevNonHorizontalSpaceIsEquals(block.String()) {
					quote = ch
				}
				block.WriteByte(ch)
			}
		case '{':
			// Start a fresh block; any previous unclosed block is malformed and discarded.
			inBlock = true
			block.Reset()
		case '}':
			if inBlock {
				if lease, ok := parseDHCPDLeaseBlock(block.String()); ok {
					leases = append(leases, lease)
				}
				inBlock = false
				block.Reset()
			}
		default:
			if inBlock {
				block.WriteByte(ch)
			}
		}
	}

	return leases
}

func prevNonHorizontalSpaceIsEquals(s string) bool {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ' ' || s[i] == '\t' {
			continue
		}
		return s[i] == '='
	}
	return false
}

func parseDHCPDLeaseBlock(body string) (dhcpdLease, bool) {
	var lease dhcpdLease
	n := len(body)
	i := 0

	isSep := func(b byte) bool {
		return b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == ';'
	}
	isHSpace := func(b byte) bool {
		return b == ' ' || b == '\t'
	}

	for i < n {
		for i < n && isSep(body[i]) {
			i++
		}
		if i >= n {
			break
		}

		keyStart := i
		for i < n && !isSep(body[i]) && body[i] != '=' {
			i++
		}
		key := strings.ToLower(body[keyStart:i])

		for i < n && isHSpace(body[i]) {
			i++
		}
		if i >= n || body[i] != '=' {
			continue
		}
		i++ // consume '='

		for i < n && isHSpace(body[i]) {
			i++
		}
		if i >= n || isSep(body[i]) {
			continue
		}

		var value string
		if body[i] == '"' || body[i] == '\'' {
			q := body[i]
			i++
			valStart := i
			for i < n && body[i] != q && body[i] != '\n' {
				i++
			}
			value = strings.TrimSpace(body[valStart:i])
			if i < n && body[i] == q {
				i++
			}
		} else {
			valStart := i
			for i < n && !isSep(body[i]) {
				i++
			}
			value = strings.TrimSpace(body[valStart:i])
		}

		switch key {
		case "name":
			lease.Name = value
		case "ip_address":
			parsedIP := net.ParseIP(value)
			if parsedIP != nil && parsedIP.To4() != nil && !parsedIP.IsLoopback() && !parsedIP.IsUnspecified() {
				lease.IPAddress = parsedIP.To4().String()
			}
		case "lease":
			if parsed, err := strconv.ParseUint(value, 0, 64); err == nil && parsed != dhcpdInfiniteLeaseExpiry {
				lease.HasExpiry = true
				lease.ExpiresAt = parsed
			} else {
				lease.HasExpiry = false
				lease.ExpiresAt = 0
			}
		}
	}

	if lease.Name == "" && lease.IPAddress == "" {
		return dhcpdLease{}, false
	}
	return lease, true
}

func matchUtmDHCPLeaseIPs(
	leasesContent string,
	expectedHostname string,
	candidates []string,
	now time.Time,
) (active []string, expired []string) {
	expectedHostname = strings.TrimSpace(expectedHostname)
	if expectedHostname == "" || len(candidates) == 0 {
		return nil, nil
	}

	candidateSet := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidateSet[candidate] = struct{}{}
	}

	type activeMatch struct {
		ip        string
		expiryKey uint64
	}

	var (
		activeByIP  = make(map[string]int)
		activeList  []activeMatch
		expiredSet  = make(map[string]struct{})
		expiredList []string
	)

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

		if lease.isExpiredAt(now) {
			if _, seen := expiredSet[lease.IPAddress]; !seen {
				expiredSet[lease.IPAddress] = struct{}{}
				expiredList = append(expiredList, lease.IPAddress)
			}
			continue
		}

		expiryKey := lease.sortExpiryKey()
		if pos, exists := activeByIP[lease.IPAddress]; exists {
			if expiryKey > activeList[pos].expiryKey {
				activeList[pos].expiryKey = expiryKey
			}
			continue
		}
		activeByIP[lease.IPAddress] = len(activeList)
		activeList = append(activeList, activeMatch{
			ip:        lease.IPAddress,
			expiryKey: expiryKey,
		})
	}

	if len(activeList) > 1 {
		sort.SliceStable(activeList, func(i, j int) bool {
			return activeList[i].expiryKey > activeList[j].expiryKey
		})
	}

	if len(activeList) > 0 {
		active = make([]string, 0, len(activeList))
		for _, m := range activeList {
			active = append(active, m.ip)
		}
	}

	for _, ip := range expiredList {
		if _, isActive := activeByIP[ip]; !isActive {
			expired = append(expired, ip)
		}
	}

	return active, expired
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

	return "", deadline.Sub(options.now()) <= 0
}

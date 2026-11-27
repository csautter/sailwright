package provision

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

const (
	recordedM2ARPWithMultipleMatches = `? (127.0.0.1) at 00:00:00:00:00:00 on lo0 ifscope [loopback]
? (192.168.65.1) at 3e:22:fb:10:0:2 on bridge101 ifscope [ethernet]
? (192.168.65.2) at a6:e3:1e:ad:7b:67 on bridge101 ifscope [ethernet]
? (192.168.65.3) at a6:99:11:22:33:44 on bridge101 ifscope [ethernet]
? (192.168.65.4) at a6:e3:1e:ad:7b:67 on bridge101 ifscope [ethernet]
`

	recordedM2DHCPDLeases = `{
	name=ubuntu-server-arm64-dbg-092-dev-alchemy
	ip_address=192.168.65.4
	hw_address=ff,f1:f5:dd:7f:0:2:0:0:ab:11:c7:16:00:00:00:00
	identifier=ff,f1:f5:dd:7f:0:2:0:0:ab:11:c7:16:00:00:00:00
	lease=0x6747a1b2
}
{
	name=ubuntu-server-arm64-e2e-a-dev-alchemy
	ip_address=192.168.65.3
	hw_address=ff,a1:b2:c3:d4:0:2:0:0:ab:11:c7:16:11:22:33:44
	identifier=ff,a1:b2:c3:d4:0:2:0:0:ab:11:c7:16:11:22:33:44
	lease=0x6747a1b3
}
{
	name=ubuntu-server
	ip_address=192.168.65.2
	hw_address=ff,00:11:22:33:ea:e4:7e:b9:2c:62:aa:62
	identifier=ff,00:11:22:33:ea:e4:7e:b9:2c:62:aa:62
	lease=0x6747a001
}
`
)

type spyNetConn struct {
	net.Conn
	closed bool
}

func (s *spyNetConn) Close() error {
	s.closed = true
	return nil
}

func TestExtractIPv4CandidatesForMacAddress_DeduplicatesPreservingOrder(t *testing.T) {
	output := `
? (192.168.65.2) at a6:e3:1e:ad:7b:67 on bridge101 ifscope [ethernet]
? (192.168.65.4) at a6:e3:1e:ad:7b:67 on bridge101 ifscope [ethernet]
? (192.168.65.2) at a6:e3:1e:ad:7b:67 on bridge100 ifscope [ethernet]
`
	candidates, err := extractIPv4CandidatesForMacAddress(output, "A6:E3:1E:AD:7B:67")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"192.168.65.2", "192.168.65.4"}
	if !reflect.DeepEqual(candidates, want) {
		t.Fatalf("candidates = %v, want %v", candidates, want)
	}

	duplicateSingle := `
? (192.168.65.2) at a6:e3:1e:ad:7b:67 on bridge101 ifscope [ethernet]
? (192.168.65.2) at a6:e3:1e:ad:7b:67 on bridge100 ifscope [ethernet]
`
	single, err := extractIPv4CandidatesForMacAddress(duplicateSingle, "A6:E3:1E:AD:7B:67")
	if err != nil {
		t.Fatalf("unexpected error for duplicate single IP: %v", err)
	}
	if !reflect.DeepEqual(single, []string{"192.168.65.2"}) {
		t.Fatalf("single deduplicated = %v, want [192.168.65.2]", single)
	}
}

func TestDiscoverUtmVMIPv4_MultipleARPMatchesSelectsMatchingDHCPLease(t *testing.T) {
	vm := alchemy_build.VirtualMachineConfig{
		Name:                 "dbg-092",
		OS:                   "ubuntu",
		UbuntuType:           "server",
		Arch:                 "arm64",
		HostOs:               alchemy_build.HostOsDarwin,
		VirtualizationEngine: alchemy_build.VirtualizationEngineUtm,
	}
	writeTestUtmConfigPlist(t, vm, "A6:E3:1E:AD:7B:67")

	leasesPath := filepath.Join(t.TempDir(), "dhcpd_leases")
	if err := os.WriteFile(leasesPath, []byte(recordedM2DHCPDLeases), 0o644); err != nil {
		t.Fatalf("failed to write test dhcpd_leases: %v", err)
	}

	ip, err := discoverUtmVMIPv4WithOptions(t.TempDir(), vm, utmIPv4DiscoveryOptions{
		readFile:       os.ReadFile,
		dhcpLeasesPath: leasesPath,
		runCommand: func(_ string, _ time.Duration, executable string, args []string) (string, error) {
			if executable != "arp" || !reflect.DeepEqual(args, []string{"-an"}) {
				t.Fatalf("unexpected command: %s %v", executable, args)
			}
			return recordedM2ARPWithMultipleMatches, nil
		},
		primeARPCache: func(context.Context) ([]string, error) {
			t.Fatal("did not expect primeARPCache when ARP has matches")
			return nil, nil
		},
		dial: func(_ context.Context, _ string, address string) (net.Conn, error) {
			t.Fatalf("did not expect TCP dial to %s when DHCP lease uniquely matched", address)
			return nil, nil
		},
		sleep: func(time.Duration) {
			t.Fatal("did not expect sleep when DHCP lease matched")
		},
	})
	if err != nil {
		t.Fatalf("expected discovery to succeed via DHCP lease, got: %v", err)
	}
	if ip != "192.168.65.4" {
		t.Fatalf("expected current IP 192.168.65.4 for dbg-092, got %q", ip)
	}
}

func TestDiscoverUtmVMIPv4_MultipleARPMatchesCaseInsensitiveWindowsLease(t *testing.T) {
	vm := alchemy_build.VirtualMachineConfig{
		Name:                 "win-092",
		OS:                   "windows11",
		Arch:                 "arm64",
		HostOs:               alchemy_build.HostOsDarwin,
		VirtualizationEngine: alchemy_build.VirtualizationEngineUtm,
	}
	writeTestUtmConfigPlist(t, vm, "A6:E3:1E:AD:7B:67")

	expectedUpperHostname := strings.ToUpper(alchemy_build.InstanceHostname(vm))
	leasesContent := fmt.Sprintf(`{
	name=%s
	ip_address=192.168.65.4
}
{
	name=WIN-FIRSTBOOT
	ip_address=192.168.65.2
}
`, expectedUpperHostname)

	leasesPath := filepath.Join(t.TempDir(), "dhcpd_leases")
	if err := os.WriteFile(leasesPath, []byte(leasesContent), 0o644); err != nil {
		t.Fatalf("failed to write test dhcpd_leases: %v", err)
	}

	ip, err := discoverUtmVMIPv4WithOptions(t.TempDir(), vm, utmIPv4DiscoveryOptions{
		readFile:       os.ReadFile,
		dhcpLeasesPath: leasesPath,
		runCommand: func(_ string, _ time.Duration, _ string, _ []string) (string, error) {
			return recordedM2ARPWithMultipleMatches, nil
		},
		dial: func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("did not expect TCP dial when Windows DHCP lease matched")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("expected Windows lease match to succeed, got: %v", err)
	}
	if ip != "192.168.65.4" {
		t.Fatalf("expected 192.168.65.4, got %q", ip)
	}
}

func TestDiscoverUtmVMIPv4_MultipleARPMatchesReachabilityFallbackUbuntuAndWindows(t *testing.T) {
	t.Run("ubuntu ssh port 22 when no lease matches", func(t *testing.T) {
		vm := alchemy_build.VirtualMachineConfig{
			Name:                 "dbg-092",
			OS:                   "ubuntu",
			UbuntuType:           "server",
			Arch:                 "arm64",
			HostOs:               alchemy_build.HostOsDarwin,
			VirtualizationEngine: alchemy_build.VirtualizationEngineUtm,
		}
		writeTestUtmConfigPlist(t, vm, "A6:E3:1E:AD:7B:67")

		// Lease file has no entry for dbg-092 (only e2e-a and ubuntu-server).
		leasesPath := filepath.Join(t.TempDir(), "dhcpd_leases")
		nonMatchingLeases := `{
	name=ubuntu-server-arm64-e2e-a-dev-alchemy
	ip_address=192.168.65.3
}
{
	name=ubuntu-server
	ip_address=192.168.65.2
}
`
		if err := os.WriteFile(leasesPath, []byte(nonMatchingLeases), 0o644); err != nil {
			t.Fatalf("write dhcpd_leases: %v", err)
		}

		var dialed []string
		acceptedConn := &spyNetConn{}
		ip, err := discoverUtmVMIPv4WithOptions(t.TempDir(), vm, utmIPv4DiscoveryOptions{
			readFile:       os.ReadFile,
			dhcpLeasesPath: leasesPath,
			runCommand: func(_ string, _ time.Duration, _ string, _ []string) (string, error) {
				return recordedM2ARPWithMultipleMatches, nil
			},
			dial: func(_ context.Context, network string, address string) (net.Conn, error) {
				if network != "tcp4" {
					t.Fatalf("expected tcp4 dial, got %q", network)
				}
				dialed = append(dialed, address)
				if address == "192.168.65.4:22" {
					return acceptedConn, nil
				}
				return nil, errors.New("connection refused")
			},
		})
		if err != nil {
			t.Fatalf("expected reachability fallback to succeed, got: %v", err)
		}
		if ip != "192.168.65.4" {
			t.Fatalf("expected 192.168.65.4, got %q", ip)
		}
		if !reflect.DeepEqual(dialed, []string{"192.168.65.2:22", "192.168.65.4:22"}) {
			t.Fatalf("unexpected dial sequence: %v", dialed)
		}
		if !acceptedConn.closed {
			t.Fatal("expected accepted TCP probe connection to be closed immediately")
		}
	})

	t.Run("windows winrm default 5985 and configured port", func(t *testing.T) {
		for _, tc := range []struct {
			name           string
			configuredPort string
			wantPort       string
		}{
			{name: "default 5985", configuredPort: "", wantPort: "5985"},
			{name: "configured 5986", configuredPort: "5986", wantPort: "5986"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				vm := alchemy_build.VirtualMachineConfig{
					Name:                 "win-092",
					OS:                   "windows11",
					Arch:                 "arm64",
					HostOs:               alchemy_build.HostOsDarwin,
					VirtualizationEngine: alchemy_build.VirtualizationEngineUtm,
				}
				writeTestUtmConfigPlist(t, vm, "A6:E3:1E:AD:7B:67")

				projectDir := t.TempDir()
				t.Setenv(utmWindowsAnsiblePortEnvVar, tc.configuredPort)

				// Lease points to an IP not in ARP candidates -> must fall back to WinRM reachability.
				leasesPath := filepath.Join(t.TempDir(), "dhcpd_leases")
				staleLease := fmt.Sprintf("{\n\tname=%s\n\tip_address=192.168.65.99\n}\n", alchemy_build.InstanceHostname(vm))
				if err := os.WriteFile(leasesPath, []byte(staleLease), 0o644); err != nil {
					t.Fatalf("write dhcpd_leases: %v", err)
				}

				var dialed []string
				acceptedConn := &spyNetConn{}
				ip, err := discoverUtmVMIPv4WithOptions(projectDir, vm, utmIPv4DiscoveryOptions{
					readFile:       os.ReadFile,
					dhcpLeasesPath: leasesPath,
					runCommand: func(_ string, _ time.Duration, _ string, _ []string) (string, error) {
						return recordedM2ARPWithMultipleMatches, nil
					},
					dial: func(_ context.Context, network string, address string) (net.Conn, error) {
						if network != "tcp4" {
							t.Fatalf("expected tcp4 dial, got %q", network)
						}
						dialed = append(dialed, address)
						if address == "192.168.65.4:"+tc.wantPort {
							return acceptedConn, nil
						}
						return nil, errors.New("connection refused")
					},
				})
				if err != nil {
					t.Fatalf("expected Windows WinRM reachability fallback to succeed, got: %v", err)
				}
				if ip != "192.168.65.4" {
					t.Fatalf("expected 192.168.65.4, got %q", ip)
				}
				wantDialed := []string{"192.168.65.2:" + tc.wantPort, "192.168.65.4:" + tc.wantPort}
				if !reflect.DeepEqual(dialed, wantDialed) {
					t.Fatalf("dialed = %v, want %v", dialed, wantDialed)
				}
				if !acceptedConn.closed {
					t.Fatal("expected accepted TCP probe connection to be closed immediately")
				}
			})
		}
	})
}

func TestDiscoverUtmVMIPv4_MultipleARPMatchesLeaseMissingOrUnreadableFallsBack(t *testing.T) {
	for _, tc := range []struct {
		name     string
		readErr  error
		useReal  bool
		wantText string
	}{
		{name: "missing lease file", useReal: true},
		{name: "unreadable lease file", readErr: os.ErrPermission},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm := alchemy_build.VirtualMachineConfig{
				Name:                 "dbg-092",
				OS:                   "ubuntu",
				UbuntuType:           "server",
				Arch:                 "arm64",
				HostOs:               alchemy_build.HostOsDarwin,
				VirtualizationEngine: alchemy_build.VirtualizationEngineUtm,
			}
			writeTestUtmConfigPlist(t, vm, "A6:E3:1E:AD:7B:67")

			missingPath := filepath.Join(t.TempDir(), "missing-dhcpd_leases")
			readFn := os.ReadFile
			if !tc.useReal {
				readFn = func(path string) ([]byte, error) {
					if path == missingPath {
						return nil, tc.readErr
					}
					return os.ReadFile(path)
				}
			}

			var dialed []string
			ip, err := discoverUtmVMIPv4WithOptions(t.TempDir(), vm, utmIPv4DiscoveryOptions{
				readFile:       readFn,
				dhcpLeasesPath: missingPath,
				runCommand: func(_ string, _ time.Duration, _ string, _ []string) (string, error) {
					return recordedM2ARPWithMultipleMatches, nil
				},
				dial: func(_ context.Context, _ string, address string) (net.Conn, error) {
					dialed = append(dialed, address)
					if address == "192.168.65.4:22" {
						return &spyNetConn{}, nil
					}
					return nil, errors.New("i/o timeout")
				},
			})
			if err != nil {
				t.Fatalf("expected fallback to reachability when lease file %s, got: %v", tc.name, err)
			}
			if ip != "192.168.65.4" {
				t.Fatalf("expected 192.168.65.4, got %q", ip)
			}
			if !reflect.DeepEqual(dialed, []string{"192.168.65.2:22", "192.168.65.4:22"}) {
				t.Fatalf("unexpected dial calls: %v", dialed)
			}
		})
	}
}

func TestDiscoverUtmVMIPv4_MultipleARPMatchesNoneReachableListsAllCandidates(t *testing.T) {
	vm := alchemy_build.VirtualMachineConfig{
		Name:                 "dbg-092",
		OS:                   "ubuntu",
		UbuntuType:           "server",
		Arch:                 "arm64",
		HostOs:               alchemy_build.HostOsDarwin,
		VirtualizationEngine: alchemy_build.VirtualizationEngineUtm,
	}
	writeTestUtmConfigPlist(t, vm, "A6:E3:1E:AD:7B:67")

	leasesPath := filepath.Join(t.TempDir(), "missing-dhcpd_leases")
	_, err := discoverUtmVMIPv4WithOptions(t.TempDir(), vm, utmIPv4DiscoveryOptions{
		readFile:       os.ReadFile,
		dhcpLeasesPath: leasesPath,
		runCommand: func(_ string, _ time.Duration, _ string, _ []string) (string, error) {
			return recordedM2ARPWithMultipleMatches, nil
		},
		primeARPCache: func(context.Context) ([]string, error) {
			t.Fatal("did not expect primeARPCache when ARP already returned candidates")
			return nil, nil
		},
		dial: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("connection refused")
		},
	})
	if err == nil {
		t.Fatal("expected error when no multiple ARP candidates are reachable")
	}
	for _, wantSubstr := range []string{"192.168.65.2", "192.168.65.4", "a6:e3:1e:ad:7b:67", "port 22"} {
		if !strings.Contains(err.Error(), wantSubstr) {
			t.Fatalf("expected error to contain %q, got: %v", wantSubstr, err)
		}
	}
	if strings.Contains(err.Error(), "192.168.65.3") {
		t.Fatalf("did not expect unrelated e2e-a IP 192.168.65.3 in candidates error: %v", err)
	}
}

func TestDiscoverUtmVMIPv4_SingleARPMatchSkipsLeaseAndPortProbing(t *testing.T) {
	// Unnamed default instance with duplicated identical IP in ARP output.
	vm := alchemy_build.VirtualMachineConfig{
		OS:                   "ubuntu",
		UbuntuType:           "server",
		Arch:                 "arm64",
		HostOs:               alchemy_build.HostOsDarwin,
		VirtualizationEngine: alchemy_build.VirtualizationEngineUtm,
	}
	writeTestUtmConfigPlist(t, vm, "A6:E3:1E:AD:7B:67")

	duplicateSingleARP := `? (192.168.65.2) at a6:e3:1e:ad:7b:67 on bridge101 ifscope [ethernet]
? (192.168.65.2) at a6:e3:1e:ad:7b:67 on bridge100 ifscope [ethernet]
`
	leasesPath := filepath.Join(t.TempDir(), "dhcpd_leases")

	ip, err := discoverUtmVMIPv4WithOptions(t.TempDir(), vm, utmIPv4DiscoveryOptions{
		dhcpLeasesPath: leasesPath,
		readFile: func(path string) ([]byte, error) {
			if path == leasesPath {
				t.Fatal("did not expect dhcpd_leases read on single ARP match")
			}
			return os.ReadFile(path)
		},
		runCommand: func(_ string, _ time.Duration, _ string, _ []string) (string, error) {
			return duplicateSingleARP, nil
		},
		primeARPCache: func(context.Context) ([]string, error) {
			t.Fatal("did not expect primeARPCache on single ARP match")
			return nil, nil
		},
		dial: func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("did not expect TCP dial on single ARP match")
			return nil, nil
		},
		sleep: func(time.Duration) {
			t.Fatal("did not expect sleep on single ARP match")
		},
	})
	if err != nil {
		t.Fatalf("expected single ARP match to succeed, got: %v", err)
	}
	if ip != "192.168.65.2" {
		t.Fatalf("expected 192.168.65.2, got %q", ip)
	}
}

func TestDiscoverUtmVMIPv4_UnnamedDefaultMultipleMatchesResolvesViaLeaseOrReachability(t *testing.T) {
	vm := alchemy_build.VirtualMachineConfig{
		OS:                   "ubuntu",
		UbuntuType:           "server",
		Arch:                 "arm64",
		HostOs:               alchemy_build.HostOsDarwin,
		VirtualizationEngine: alchemy_build.VirtualizationEngineUtm,
	}
	writeTestUtmConfigPlist(t, vm, "A6:E3:1E:AD:7B:67")

	leasesPath := filepath.Join(t.TempDir(), "dhcpd_leases")
	leases := fmt.Sprintf("{\n\tname=%s\n\tip_address=192.168.65.4\n}\n{\n\tname=ubuntu-server\n\tip_address=192.168.65.2\n}\n", alchemy_build.InstanceHostname(vm))
	if err := os.WriteFile(leasesPath, []byte(leases), 0o644); err != nil {
		t.Fatalf("write dhcpd_leases: %v", err)
	}

	ip, err := discoverUtmVMIPv4WithOptions(t.TempDir(), vm, utmIPv4DiscoveryOptions{
		readFile:       os.ReadFile,
		dhcpLeasesPath: leasesPath,
		runCommand: func(_ string, _ time.Duration, _ string, _ []string) (string, error) {
			return recordedM2ARPWithMultipleMatches, nil
		},
		dial: func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("did not expect TCP dial when default instance lease matched")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("expected unnamed default multiple ARP matches to resolve via lease, got: %v", err)
	}
	if ip != "192.168.65.4" {
		t.Fatalf("expected 192.168.65.4, got %q", ip)
	}
}

func TestDiscoverUtmVMIPv4_MultipleARPMatchesClampsProbeTimeoutToRemainingBudget(t *testing.T) {
	vm := alchemy_build.VirtualMachineConfig{
		Name:                 "dbg-092",
		OS:                   "ubuntu",
		UbuntuType:           "server",
		Arch:                 "arm64",
		HostOs:               alchemy_build.HostOsDarwin,
		VirtualizationEngine: alchemy_build.VirtualizationEngineUtm,
	}
	writeTestUtmConfigPlist(t, vm, "A6:E3:1E:AD:7B:67")

	nowTime := time.Date(2026, 11, 27, 20, 0, 0, 0, time.UTC)
	var arpCalls int
	var observedDialTimeouts []time.Duration

	_, err := discoverUtmVMIPv4WithOptions(t.TempDir(), vm, utmIPv4DiscoveryOptions{
		readFile:              os.ReadFile,
		dhcpLeasesPath:        filepath.Join(t.TempDir(), "missing-dhcpd_leases"),
		discoveryBudget:       2 * time.Second,
		retryInterval:         500 * time.Millisecond,
		arpCommandTimer:       time.Second,
		candidateProbeTimeout: 500 * time.Millisecond,
		now:                   func() time.Time { return nowTime },
		sleep: func(d time.Duration) {
			nowTime = nowTime.Add(d)
		},
		runCommand: func(_ string, _ time.Duration, _ string, _ []string) (string, error) {
			arpCalls++
			if arpCalls == 1 {
				// First attempt misses so primeARPCache consumes most of the budget.
				nowTime = nowTime.Add(300 * time.Millisecond)
				return "? (192.168.65.1) at 3e:22:fb:10:0:2 on bridge101 ifscope [ethernet]\n", nil
			}
			return recordedM2ARPWithMultipleMatches, nil
		},
		primeARPCache: func(context.Context) ([]string, error) {
			// Leaves 200ms after the 500ms sleep (300 + 1000 + 500 = 1800ms elapsed of 2000ms budget).
			nowTime = nowTime.Add(1000 * time.Millisecond)
			return []string{"bridge101"}, nil
		},
		dial: func(ctx context.Context, _ string, address string) (net.Conn, error) {
			dl, ok := ctx.Deadline()
			if !ok {
				t.Fatalf("expected dial context for %s to have a deadline", address)
			}
			observedDialTimeouts = append(observedDialTimeouts, time.Until(dl))
			nowTime = nowTime.Add(200 * time.Millisecond)
			return nil, errors.New("i/o timeout")
		},
	})
	if err == nil {
		t.Fatal("expected budget-exhausted error when dial consumes remaining budget")
	}
	if len(observedDialTimeouts) != 1 {
		t.Fatalf("expected probing to stop after budget ran out on first candidate, got %d dial calls", len(observedDialTimeouts))
	}
	if observedDialTimeouts[0] <= 0 || observedDialTimeouts[0] > 200*time.Millisecond {
		t.Fatalf("expected dial timeout clamped to <= 200ms remaining budget, got %s", observedDialTimeouts[0])
	}
	for _, wantSubstr := range []string{"192.168.65.2", "192.168.65.4", "a6:e3:1e:ad:7b:67", "port 22", "budget 2s"} {
		if !strings.Contains(err.Error(), wantSubstr) {
			t.Fatalf("expected error to include %q, got: %v", wantSubstr, err)
		}
	}
}

func TestProbeUtmCandidateServicePort_RealTCPListenerAndImmediateClose(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	_, portStr, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	port, err := parseGuestPort(portStr, 0)
	if err != nil {
		t.Fatalf("parseGuestPort: %v", err)
	}

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- conn
		}
	}()

	options := withDefaultUtmIPv4DiscoveryOptions(utmIPv4DiscoveryOptions{
		candidateProbeTimeout: time.Second,
	})
	deadline := time.Now().Add(5 * time.Second)
	got, exhausted := probeUtmCandidateServicePort([]string{"127.0.0.1"}, port, deadline, options)
	if exhausted || got != "127.0.0.1" {
		t.Fatalf("probeUtmCandidateServicePort = (%q, %v), want (%q, false)", got, exhausted, "127.0.0.1")
	}

	select {
	case conn := <-accepted:
		defer conn.Close()
		buf := make([]byte, 1)
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		if _, readErr := conn.Read(buf); readErr == nil {
			t.Fatal("expected client probe connection to be closed immediately (EOF)")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for server accept")
	}
}

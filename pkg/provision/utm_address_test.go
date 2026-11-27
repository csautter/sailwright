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

// recordedM2Now sits just before the recordedM2DHCPDLeases expiry timestamps
// (0x6747a001..0x6747a1b3) so recorded fixtures remain unexpired regardless of
// wall-clock date.
var recordedM2Now = time.Unix(0x67479f00, 0).UTC()

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
		now:            func() time.Time { return recordedM2Now },
		runCommand: func(_ string, _ time.Duration, executable string, args []string) (string, error) {
			if executable != "arp" || !reflect.DeepEqual(args, []string{"-an"}) {
				t.Fatalf("unexpected command: %s %v", executable, args)
			}
			return recordedM2ARPWithMultipleMatches, nil
		},
		primeARPCache: func(context.Context) ([]string, error) {
			t.Fatal("did not expect primeARPCache when active DHCP lease matched")
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
		now:            func() time.Time { return recordedM2Now },
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
			now:            func() time.Time { return recordedM2Now },
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
					now:            func() time.Time { return recordedM2Now },
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
		name    string
		readErr error
		useReal bool
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
				now:            func() time.Time { return recordedM2Now },
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

	nowTime := recordedM2Now
	var arpCalls, primeCalls int
	leasesPath := filepath.Join(t.TempDir(), "missing-dhcpd_leases")

	_, err := discoverUtmVMIPv4WithOptions(t.TempDir(), vm, utmIPv4DiscoveryOptions{
		readFile:              os.ReadFile,
		dhcpLeasesPath:        leasesPath,
		discoveryBudget:       5 * time.Second,
		retryInterval:         2 * time.Second,
		arpCommandTimer:       time.Second,
		candidateProbeTimeout: 500 * time.Millisecond,
		now:                   func() time.Time { return nowTime },
		sleep: func(d time.Duration) {
			nowTime = nowTime.Add(d)
		},
		runCommand: func(_ string, _ time.Duration, _ string, _ []string) (string, error) {
			arpCalls++
			return recordedM2ARPWithMultipleMatches, nil
		},
		primeARPCache: func(context.Context) ([]string, error) {
			primeCalls++
			nowTime = nowTime.Add(500 * time.Millisecond)
			return []string{"bridge101"}, nil
		},
		dial: func(context.Context, string, string) (net.Conn, error) {
			nowTime = nowTime.Add(200 * time.Millisecond)
			return nil, errors.New("connection refused")
		},
	})
	if err == nil {
		t.Fatal("expected error when no multiple ARP candidates are reachable")
	}
	if primeCalls == 0 || arpCalls < 2 {
		t.Fatalf("expected retry loop to prime ARP cache and retry before failing, got arpCalls=%d primeCalls=%d", arpCalls, primeCalls)
	}
	for _, wantSubstr := range []string{"192.168.65.2", "192.168.65.4", "a6:e3:1e:ad:7b:67", "could not read DHCP leases", "port 22"} {
		if !strings.Contains(err.Error(), wantSubstr) {
			t.Fatalf("expected error to contain %q, got: %v", wantSubstr, err)
		}
	}
	if strings.Contains(err.Error(), "192.168.65.3") {
		t.Fatalf("did not expect unrelated e2e-a IP 192.168.65.3 in candidates error: %v", err)
	}
}

func TestDiscoverUtmVMIPv4_MultipleARPMatchesPrimesAndRetriesUntilResolved(t *testing.T) {
	t.Run("priming adds current IP matching active lease on attempt 2", func(t *testing.T) {
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
			t.Fatalf("write dhcpd_leases: %v", err)
		}

		// Attempt 1 sees two stale ARP entries (.2 and .5) before priming adds .4.
		staleARP := `? (192.168.65.2) at a6:e3:1e:ad:7b:67 on bridge101 ifscope [ethernet]
? (192.168.65.5) at a6:e3:1e:ad:7b:67 on bridge101 ifscope [ethernet]
`
		nowTime := recordedM2Now
		var arpCalls, primeCalls int

		ip, err := discoverUtmVMIPv4WithOptions(t.TempDir(), vm, utmIPv4DiscoveryOptions{
			readFile:              os.ReadFile,
			dhcpLeasesPath:        leasesPath,
			discoveryBudget:       30 * time.Second,
			retryInterval:         2 * time.Second,
			arpCommandTimer:       5 * time.Second,
			candidateProbeTimeout: 500 * time.Millisecond,
			now:                   func() time.Time { return nowTime },
			sleep: func(d time.Duration) {
				nowTime = nowTime.Add(d)
			},
			runCommand: func(_ string, _ time.Duration, _ string, _ []string) (string, error) {
				arpCalls++
				if arpCalls == 1 {
					return staleARP, nil
				}
				return recordedM2ARPWithMultipleMatches, nil
			},
			primeARPCache: func(context.Context) ([]string, error) {
				primeCalls++
				nowTime = nowTime.Add(500 * time.Millisecond)
				return []string{"bridge101"}, nil
			},
			dial: func(context.Context, string, string) (net.Conn, error) {
				return nil, errors.New("connection refused")
			},
		})
		if err != nil {
			t.Fatalf("expected attempt 2 to resolve after ARP priming, got: %v", err)
		}
		if ip != "192.168.65.4" {
			t.Fatalf("expected 192.168.65.4, got %q", ip)
		}
		if arpCalls != 2 || primeCalls != 1 {
			t.Fatalf("expected arpCalls=2 primeCalls=1, got arpCalls=%d primeCalls=%d", arpCalls, primeCalls)
		}
	})

	t.Run("guest service becomes reachable on attempt 2 after priming", func(t *testing.T) {
		vm := alchemy_build.VirtualMachineConfig{
			Name:                 "dbg-092",
			OS:                   "ubuntu",
			UbuntuType:           "server",
			Arch:                 "arm64",
			HostOs:               alchemy_build.HostOsDarwin,
			VirtualizationEngine: alchemy_build.VirtualizationEngineUtm,
		}
		writeTestUtmConfigPlist(t, vm, "A6:E3:1E:AD:7B:67")

		nowTime := recordedM2Now
		var arpCalls, primeCalls int

		ip, err := discoverUtmVMIPv4WithOptions(t.TempDir(), vm, utmIPv4DiscoveryOptions{
			readFile:              os.ReadFile,
			dhcpLeasesPath:        filepath.Join(t.TempDir(), "missing-dhcpd_leases"),
			discoveryBudget:       30 * time.Second,
			retryInterval:         2 * time.Second,
			arpCommandTimer:       5 * time.Second,
			candidateProbeTimeout: 500 * time.Millisecond,
			now:                   func() time.Time { return nowTime },
			sleep: func(d time.Duration) {
				nowTime = nowTime.Add(d)
			},
			runCommand: func(_ string, _ time.Duration, _ string, _ []string) (string, error) {
				arpCalls++
				return recordedM2ARPWithMultipleMatches, nil
			},
			primeARPCache: func(context.Context) ([]string, error) {
				primeCalls++
				nowTime = nowTime.Add(500 * time.Millisecond)
				return []string{"bridge101"}, nil
			},
			dial: func(_ context.Context, _ string, address string) (net.Conn, error) {
				if arpCalls >= 2 && address == "192.168.65.4:22" {
					return &spyNetConn{}, nil
				}
				return nil, errors.New("connection refused")
			},
		})
		if err != nil {
			t.Fatalf("expected attempt 2 to resolve via TCP reachability, got: %v", err)
		}
		if ip != "192.168.65.4" {
			t.Fatalf("expected 192.168.65.4, got %q", ip)
		}
		if arpCalls != 2 || primeCalls != 1 {
			t.Fatalf("expected arpCalls=2 primeCalls=1, got arpCalls=%d primeCalls=%d", arpCalls, primeCalls)
		}
	})

	t.Run("invalid guest service port config fails immediately as terminal", func(t *testing.T) {
		vm := alchemy_build.VirtualMachineConfig{
			Name:                 "win-092",
			OS:                   "windows11",
			Arch:                 "arm64",
			HostOs:               alchemy_build.HostOsDarwin,
			VirtualizationEngine: alchemy_build.VirtualizationEngineUtm,
		}
		writeTestUtmConfigPlist(t, vm, "A6:E3:1E:AD:7B:67")
		t.Setenv(utmWindowsAnsiblePortEnvVar, "not-a-port")

		var arpCalls int
		_, err := discoverUtmVMIPv4WithOptions(t.TempDir(), vm, utmIPv4DiscoveryOptions{
			readFile:       os.ReadFile,
			dhcpLeasesPath: filepath.Join(t.TempDir(), "missing-dhcpd_leases"),
			now:            func() time.Time { return recordedM2Now },
			runCommand: func(_ string, _ time.Duration, _ string, _ []string) (string, error) {
				arpCalls++
				return recordedM2ARPWithMultipleMatches, nil
			},
			primeARPCache: func(context.Context) ([]string, error) {
				t.Fatal("did not expect primeARPCache on terminal guest port config error")
				return nil, nil
			},
			sleep: func(time.Duration) {
				t.Fatal("did not expect sleep on terminal guest port config error")
			},
		})
		if err == nil {
			t.Fatal("expected error for invalid UTM_WINDOWS_ANSIBLE_PORT")
		}
		if arpCalls != 1 {
			t.Fatalf("expected immediate terminal abort after 1 ARP attempt, got %d", arpCalls)
		}
		if !strings.Contains(err.Error(), "could not resolve UTM guest service port") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestDiscoverUtmVMIPv4_MultipleARPMatchesDHCPLeaseExpiryAndOrdering(t *testing.T) {
	baseNow := time.Unix(1800000000, 0).UTC()
	expiredHex := fmt.Sprintf("0x%x", baseNow.Add(-5*time.Minute).Unix())
	activeEarlierHex := fmt.Sprintf("0x%x", baseNow.Add(10*time.Minute).Unix())
	activeLaterHex := fmt.Sprintf("0x%x", baseNow.Add(60*time.Minute).Unix())

	vm := alchemy_build.VirtualMachineConfig{
		Name:                 "dbg-092",
		OS:                   "ubuntu",
		UbuntuType:           "server",
		Arch:                 "arm64",
		HostOs:               alchemy_build.HostOsDarwin,
		VirtualizationEngine: alchemy_build.VirtualizationEngineUtm,
	}
	hostname := alchemy_build.InstanceHostname(vm)

	t.Run("active and expired leases for same hostname returns active immediately", func(t *testing.T) {
		writeTestUtmConfigPlist(t, vm, "A6:E3:1E:AD:7B:67")
		leases := fmt.Sprintf(`{
	name=%s
	ip_address=192.168.65.2
	lease=%s
}
{
	name=%s
	ip_address=192.168.65.4
	lease=%s
}
`, hostname, expiredHex, hostname, activeLaterHex)

		leasesPath := filepath.Join(t.TempDir(), "dhcpd_leases")
		if err := os.WriteFile(leasesPath, []byte(leases), 0o644); err != nil {
			t.Fatalf("write dhcpd_leases: %v", err)
		}

		ip, err := discoverUtmVMIPv4WithOptions(t.TempDir(), vm, utmIPv4DiscoveryOptions{
			readFile:       os.ReadFile,
			dhcpLeasesPath: leasesPath,
			now:            func() time.Time { return baseNow },
			runCommand: func(_ string, _ time.Duration, _ string, _ []string) (string, error) {
				return recordedM2ARPWithMultipleMatches, nil
			},
			dial: func(_ context.Context, _ string, address string) (net.Conn, error) {
				t.Fatalf("did not expect TCP dial to %s when a single active lease matched", address)
				return nil, nil
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ip != "192.168.65.4" {
			t.Fatalf("expected active lease IP 192.168.65.4, got %q", ip)
		}
	})

	t.Run("two active leases probe highest expiry first ahead of remaining ARP candidates", func(t *testing.T) {
		writeTestUtmConfigPlist(t, vm, "A6:E3:1E:AD:7B:67")
		threeCandidateARP := `? (192.168.65.2) at a6:e3:1e:ad:7b:67 on bridge101 ifscope [ethernet]
? (192.168.65.4) at a6:e3:1e:ad:7b:67 on bridge101 ifscope [ethernet]
? (192.168.65.5) at a6:e3:1e:ad:7b:67 on bridge101 ifscope [ethernet]
`
		// .2 appears first in file with earlier expiry; .4 appears second with higher expiry; .5 has no lease.
		leases := fmt.Sprintf(`{
	name=%s
	ip_address=192.168.65.2
	lease=%s
}
{
	name=%s
	ip_address=192.168.65.4
	lease=%s
}
`, hostname, activeEarlierHex, hostname, activeLaterHex)

		leasesPath := filepath.Join(t.TempDir(), "dhcpd_leases")
		if err := os.WriteFile(leasesPath, []byte(leases), 0o644); err != nil {
			t.Fatalf("write dhcpd_leases: %v", err)
		}

		var dialed []string
		ip, err := discoverUtmVMIPv4WithOptions(t.TempDir(), vm, utmIPv4DiscoveryOptions{
			readFile:       os.ReadFile,
			dhcpLeasesPath: leasesPath,
			now:            func() time.Time { return baseNow },
			runCommand: func(_ string, _ time.Duration, _ string, _ []string) (string, error) {
				return threeCandidateARP, nil
			},
			dial: func(_ context.Context, _ string, address string) (net.Conn, error) {
				dialed = append(dialed, address)
				if address == "192.168.65.5:22" {
					return &spyNetConn{}, nil
				}
				return nil, errors.New("connection refused")
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ip != "192.168.65.5" {
			t.Fatalf("expected 192.168.65.5, got %q", ip)
		}
		wantDialed := []string{"192.168.65.4:22", "192.168.65.2:22", "192.168.65.5:22"}
		if !reflect.DeepEqual(dialed, wantDialed) {
			t.Fatalf("dialed = %v, want %v (highest expiry active lease first, then other active lease, then remaining ARP candidate)", dialed, wantDialed)
		}
	})

	t.Run("two active leases with tied expiry preserve file order", func(t *testing.T) {
		writeTestUtmConfigPlist(t, vm, "A6:E3:1E:AD:7B:67")
		leases := fmt.Sprintf(`{
	name=%s
	ip_address=192.168.65.2
	lease=%s
}
{
	name=%s
	ip_address=192.168.65.4
	lease=%s
}
`, hostname, activeLaterHex, hostname, activeLaterHex)

		leasesPath := filepath.Join(t.TempDir(), "dhcpd_leases")
		if err := os.WriteFile(leasesPath, []byte(leases), 0o644); err != nil {
			t.Fatalf("write dhcpd_leases: %v", err)
		}

		var dialed []string
		ip, err := discoverUtmVMIPv4WithOptions(t.TempDir(), vm, utmIPv4DiscoveryOptions{
			readFile:       os.ReadFile,
			dhcpLeasesPath: leasesPath,
			now:            func() time.Time { return baseNow },
			runCommand: func(_ string, _ time.Duration, _ string, _ []string) (string, error) {
				return recordedM2ARPWithMultipleMatches, nil
			},
			dial: func(_ context.Context, _ string, address string) (net.Conn, error) {
				dialed = append(dialed, address)
				if address == "192.168.65.4:22" {
					return &spyNetConn{}, nil
				}
				return nil, errors.New("connection refused")
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ip != "192.168.65.4" {
			t.Fatalf("expected 192.168.65.4, got %q", ip)
		}
		wantDialed := []string{"192.168.65.2:22", "192.168.65.4:22"}
		if !reflect.DeepEqual(dialed, wantDialed) {
			t.Fatalf("dialed = %v, want %v (file order preserved on expiry tie)", dialed, wantDialed)
		}
	})

	t.Run("single expired lease falls back to reachability", func(t *testing.T) {
		writeTestUtmConfigPlist(t, vm, "A6:E3:1E:AD:7B:67")
		leases := fmt.Sprintf(`{
	name=%s
	ip_address=192.168.65.2
	lease=%s
}
`, hostname, expiredHex)

		leasesPath := filepath.Join(t.TempDir(), "dhcpd_leases")
		if err := os.WriteFile(leasesPath, []byte(leases), 0o644); err != nil {
			t.Fatalf("write dhcpd_leases: %v", err)
		}

		var dialed []string
		ip, err := discoverUtmVMIPv4WithOptions(t.TempDir(), vm, utmIPv4DiscoveryOptions{
			readFile:       os.ReadFile,
			dhcpLeasesPath: leasesPath,
			now:            func() time.Time { return baseNow },
			runCommand: func(_ string, _ time.Duration, _ string, _ []string) (string, error) {
				return recordedM2ARPWithMultipleMatches, nil
			},
			dial: func(_ context.Context, _ string, address string) (net.Conn, error) {
				dialed = append(dialed, address)
				if address == "192.168.65.4:22" {
					return &spyNetConn{}, nil
				}
				return nil, errors.New("connection refused")
			},
		})
		if err != nil {
			t.Fatalf("expected fallback to reachability when only expired lease matched, got: %v", err)
		}
		if ip != "192.168.65.4" {
			t.Fatalf("expected 192.168.65.4, got %q", ip)
		}
		if !reflect.DeepEqual(dialed, []string{"192.168.65.2:22", "192.168.65.4:22"}) {
			t.Fatalf("dialed = %v, want [192.168.65.2:22 192.168.65.4:22]", dialed)
		}
	})

	t.Run("only expired lease noted in error when no candidate is reachable", func(t *testing.T) {
		writeTestUtmConfigPlist(t, vm, "A6:E3:1E:AD:7B:67")
		leases := fmt.Sprintf(`{
	name=%s
	ip_address=192.168.65.2
	lease=%s
}
`, hostname, expiredHex)

		leasesPath := filepath.Join(t.TempDir(), "dhcpd_leases")
		if err := os.WriteFile(leasesPath, []byte(leases), 0o644); err != nil {
			t.Fatalf("write dhcpd_leases: %v", err)
		}

		nowTime := baseNow
		_, err := discoverUtmVMIPv4WithOptions(t.TempDir(), vm, utmIPv4DiscoveryOptions{
			readFile:              os.ReadFile,
			dhcpLeasesPath:        leasesPath,
			discoveryBudget:       2 * time.Second,
			retryInterval:         time.Second,
			maxAttempts:           1,
			candidateProbeTimeout: 500 * time.Millisecond,
			now:                   func() time.Time { return nowTime },
			sleep:                 func(d time.Duration) { nowTime = nowTime.Add(d) },
			runCommand: func(_ string, _ time.Duration, _ string, _ []string) (string, error) {
				return recordedM2ARPWithMultipleMatches, nil
			},
			dial: func(context.Context, string, string) (net.Conn, error) {
				return nil, errors.New("connection refused")
			},
		})
		if err == nil {
			t.Fatal("expected error when only expired lease matched and no candidate accepted TCP")
		}
		for _, wantSubstr := range []string{"only expired DHCP lease", "192.168.65.2", "192.168.65.4", "port 22"} {
			if !strings.Contains(err.Error(), wantSubstr) {
				t.Fatalf("expected error to contain %q, got: %v", wantSubstr, err)
			}
		}
	})

	t.Run("infinite 0xffffffff and decimal lease timestamps", func(t *testing.T) {
		writeTestUtmConfigPlist(t, vm, "A6:E3:1E:AD:7B:67")
		expiredDec := fmt.Sprintf("%d", baseNow.Add(-time.Minute).Unix())
		leases := fmt.Sprintf(`{
	name=%s
	ip_address=192.168.65.2
	lease=%s
}
{
	name=%s
	ip_address=192.168.65.4
	lease=0xffffffff
}
`, hostname, expiredDec, hostname)

		leasesPath := filepath.Join(t.TempDir(), "dhcpd_leases")
		if err := os.WriteFile(leasesPath, []byte(leases), 0o644); err != nil {
			t.Fatalf("write dhcpd_leases: %v", err)
		}

		ip, err := discoverUtmVMIPv4WithOptions(t.TempDir(), vm, utmIPv4DiscoveryOptions{
			readFile:       os.ReadFile,
			dhcpLeasesPath: leasesPath,
			now:            func() time.Time { return baseNow },
			runCommand: func(_ string, _ time.Duration, _ string, _ []string) (string, error) {
				return recordedM2ARPWithMultipleMatches, nil
			},
			dial: func(context.Context, string, string) (net.Conn, error) {
				t.Fatal("did not expect TCP dial when non-expiring 0xffffffff lease uniquely matched")
				return nil, nil
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ip != "192.168.65.4" {
			t.Fatalf("expected 192.168.65.4, got %q", ip)
		}
	})
}

func TestParseDHCPDLeases_MultilineSingleLineCommentsQuotesAndMalformed(t *testing.T) {
	t.Run("single-line blocks with commas, semicolons, quotes, and comments", func(t *testing.T) {
		content := `
# { name=commented-out ip_address=192.168.65.99 lease=0x70000000 }
{ name=host-a ip_address=192.168.65.4 hw_address=ff,f1:f5:dd:7f:0:2 identifier=ff,f1:f5:dd:7f:0:2 lease=0x6747a1b2 }
{name="host-b"; ip_address='192.168.65.5'; lease="1800000000";} # inline comment
{ name = "host-c"
  ip_address = 192.168.65.6 # trailing comment
  lease = 0xffffffff }
`
		got := parseDHCPDLeases(content)
		want := []dhcpdLease{
			{Name: "host-a", IPAddress: "192.168.65.4", HasExpiry: true, ExpiresAt: 0x6747a1b2},
			{Name: "host-b", IPAddress: "192.168.65.5", HasExpiry: true, ExpiresAt: 1800000000},
			{Name: "host-c", IPAddress: "192.168.65.6", HasExpiry: false, ExpiresAt: 0},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("parseDHCPDLeases = %+v, want %+v", got, want)
		}
	})

	t.Run("malformed and unclosed blocks are discarded without breaking valid blocks", func(t *testing.T) {
		content := `
}
stray garbage line
{
	name=unclosed-before-valid
	ip_address=192.168.65.2
{
	malformed-token-without-equals
	name="valid-host"
	ip_address=192.168.65.4
	lease=not-a-number
}
{
	name=loopback-only
	ip_address=127.0.0.1
}
{
	name=unclosed-at-eof
	ip_address=192.168.65.9
`
		got := parseDHCPDLeases(content)
		want := []dhcpdLease{
			{Name: "valid-host", IPAddress: "192.168.65.4", HasExpiry: false, ExpiresAt: 0},
			{Name: "loopback-only", IPAddress: "", HasExpiry: false, ExpiresAt: 0},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("parseDHCPDLeases = %+v, want %+v", got, want)
		}
	})
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
	t.Run("matching lease", func(t *testing.T) {
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
			now:            func() time.Time { return recordedM2Now },
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
	})

	t.Run("fallback to port 22 reachability when lease only has image hostname ubuntu-server", func(t *testing.T) {
		vm := alchemy_build.VirtualMachineConfig{
			Name:                 "",
			OS:                   "ubuntu",
			UbuntuType:           "server",
			Arch:                 "arm64",
			HostOs:               alchemy_build.HostOsDarwin,
			VirtualizationEngine: alchemy_build.VirtualizationEngineUtm,
		}
		writeTestUtmConfigPlist(t, vm, "A6:E3:1E:AD:7B:67")

		// Real unnamed default Ubuntu guests keep the image hostname "ubuntu-server"
		// in dhcpd_leases, which does not match InstanceHostname(vm).
		leasesPath := filepath.Join(t.TempDir(), "dhcpd_leases")
		leases := "{\n\tname=ubuntu-server\n\tip_address=192.168.65.2\n\tlease=0x6747a001\n}\n"
		if err := os.WriteFile(leasesPath, []byte(leases), 0o644); err != nil {
			t.Fatalf("write dhcpd_leases: %v", err)
		}

		var dialed []string
		acceptedConn := &spyNetConn{}
		ip, err := discoverUtmVMIPv4WithOptions(t.TempDir(), vm, utmIPv4DiscoveryOptions{
			readFile:       os.ReadFile,
			dhcpLeasesPath: leasesPath,
			now:            func() time.Time { return recordedM2Now },
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
			t.Fatalf("expected unnamed default multiple ARP matches to fall back to port 22 reachability, got: %v", err)
		}
		if ip != "192.168.65.4" {
			t.Fatalf("expected 192.168.65.4, got %q", ip)
		}
		if !reflect.DeepEqual(dialed, []string{"192.168.65.2:22", "192.168.65.4:22"}) {
			t.Fatalf("dialed = %v, want [192.168.65.2:22 192.168.65.4:22]", dialed)
		}
		if !acceptedConn.closed {
			t.Fatal("expected accepted TCP probe connection to be closed immediately")
		}
	})
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
	var (
		arpCalls           int
		wallBeforeAttempt2 time.Time
		dialCalls          int
	)

	_, err := discoverUtmVMIPv4WithOptions(t.TempDir(), vm, utmIPv4DiscoveryOptions{
		readFile:              os.ReadFile,
		dhcpLeasesPath:        filepath.Join(t.TempDir(), "missing-dhcpd_leases"),
		discoveryBudget:       2 * time.Second,
		retryInterval:         500 * time.Millisecond,
		arpCommandTimer:       time.Second,
		candidateProbeTimeout: 5 * time.Second,
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
			wallBeforeAttempt2 = time.Now()
			return recordedM2ARPWithMultipleMatches, nil
		},
		primeARPCache: func(context.Context) ([]string, error) {
			// Leaves 200ms after the 500ms sleep (300 + 1000 + 500 = 1800ms elapsed of 2000ms budget).
			nowTime = nowTime.Add(1000 * time.Millisecond)
			return []string{"bridge101"}, nil
		},
		dial: func(ctx context.Context, _ string, address string) (net.Conn, error) {
			dialCalls++
			dl, ok := ctx.Deadline()
			if !ok {
				t.Fatalf("expected dial context for %s to have a deadline", address)
			}
			// Monotonic invariants that hold under arbitrary CI scheduling delays:
			// 1. time.Until(dl) <= 200ms proves the 5s probe timeout was clamped to <= 200ms.
			// 2. dl.Sub(wallBeforeAttempt2) >= 200ms proves the timeout was not collapsed to 0.
			if until := time.Until(dl); until > 200*time.Millisecond {
				t.Fatalf("expected dial timeout clamped to <= 200ms remaining budget, got %s remaining", until)
			}
			if sinceStart := dl.Sub(wallBeforeAttempt2); sinceStart < 200*time.Millisecond {
				t.Fatalf("expected dial deadline at least 200ms after attempt start, got %s", sinceStart)
			}
			nowTime = nowTime.Add(200 * time.Millisecond)
			return nil, errors.New("i/o timeout")
		},
	})
	if err == nil {
		t.Fatal("expected budget-exhausted error when dial consumes remaining budget")
	}
	if dialCalls != 1 {
		t.Fatalf("expected probing to stop after budget ran out on first candidate, got %d dial calls", dialCalls)
	}
	for _, wantSubstr := range []string{"192.168.65.2", "192.168.65.4", "a6:e3:1e:ad:7b:67", "port 22", "budget 2s"} {
		if !strings.Contains(err.Error(), wantSubstr) {
			t.Fatalf("expected error to include %q, got: %v", wantSubstr, err)
		}
	}
}

func TestProbeUtmCandidateServicePort_FinalCandidateBudgetExhaustion(t *testing.T) {
	t.Run("direct probe reports budgetExhausted when final candidate consumes remaining budget", func(t *testing.T) {
		nowTime := time.Date(2026, 11, 27, 20, 0, 0, 0, time.UTC)
		deadline := nowTime.Add(600 * time.Millisecond)
		var dialed []string

		options := withDefaultUtmIPv4DiscoveryOptions(utmIPv4DiscoveryOptions{
			candidateProbeTimeout: 500 * time.Millisecond,
			now:                   func() time.Time { return nowTime },
			dial: func(_ context.Context, _ string, address string) (net.Conn, error) {
				dialed = append(dialed, address)
				if len(dialed) == 1 {
					nowTime = nowTime.Add(500 * time.Millisecond)
				} else {
					nowTime = nowTime.Add(100 * time.Millisecond)
				}
				return nil, errors.New("i/o timeout")
			},
		})

		ip, exhausted := probeUtmCandidateServicePort([]string{"192.168.65.2", "192.168.65.4"}, 22, deadline, options)
		if ip != "" || !exhausted {
			t.Fatalf("probeUtmCandidateServicePort = (%q, %v), want (%q, true)", ip, exhausted, "")
		}
		if !reflect.DeepEqual(dialed, []string{"192.168.65.2:22", "192.168.65.4:22"}) {
			t.Fatalf("dialed = %v, want both candidates probed", dialed)
		}
	})

	t.Run("discovery error reports budget exhaustion on final candidate", func(t *testing.T) {
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
		var dialCalls int

		_, err := discoverUtmVMIPv4WithOptions(t.TempDir(), vm, utmIPv4DiscoveryOptions{
			readFile:              os.ReadFile,
			dhcpLeasesPath:        filepath.Join(t.TempDir(), "missing-dhcpd_leases"),
			discoveryBudget:       600 * time.Millisecond,
			retryInterval:         500 * time.Millisecond,
			arpCommandTimer:       500 * time.Millisecond,
			candidateProbeTimeout: 500 * time.Millisecond,
			now:                   func() time.Time { return nowTime },
			sleep:                 func(d time.Duration) { nowTime = nowTime.Add(d) },
			runCommand: func(_ string, _ time.Duration, _ string, _ []string) (string, error) {
				return recordedM2ARPWithMultipleMatches, nil
			},
			primeARPCache: func(context.Context) ([]string, error) {
				t.Fatal("did not expect primeARPCache after discovery budget expired on final candidate")
				return nil, nil
			},
			dial: func(context.Context, string, string) (net.Conn, error) {
				dialCalls++
				if dialCalls == 1 {
					nowTime = nowTime.Add(500 * time.Millisecond)
				} else {
					nowTime = nowTime.Add(100 * time.Millisecond)
				}
				return nil, errors.New("i/o timeout")
			},
		})
		if err == nil {
			t.Fatal("expected budget-exhausted error")
		}
		if dialCalls != 2 {
			t.Fatalf("expected 2 dial calls across both candidates, got %d", dialCalls)
		}
		for _, wantSubstr := range []string{"before discovery budget 600ms expired", "192.168.65.2", "192.168.65.4", "port 22"} {
			if !strings.Contains(err.Error(), wantSubstr) {
				t.Fatalf("expected error to contain %q, got: %v", wantSubstr, err)
			}
		}
	})
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

package deploy

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeClock advances only when the checker sleeps or a probe consumes time.
type fakeClock struct {
	current time.Time
	sleeps  int
}

func newFakeClock() *fakeClock {
	return &fakeClock{current: time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) clock() guestNetworkClock {
	return guestNetworkClock{
		now: func() time.Time { return c.current },
		sleep: func(d time.Duration) {
			c.sleeps++
			c.current = c.current.Add(d)
		},
	}
}

type fakeGuestProbe struct {
	clock *fakeClock
	// probeCost is the simulated duration of every probe call.
	probeCost time.Duration

	accessErrs []error
	dnsErrs    []error
	httpsErrs  []error
	prepare    bool
	prepareErr error

	accessCalls, dnsCalls, httpsCalls, prepareCalls, diagnoseCalls int
	diagnoseResolvers                                              []string
	diagnoseHint                                                   string
}

func (p *fakeGuestProbe) spend() {
	if p.clock != nil {
		p.clock.current = p.clock.current.Add(p.probeCost)
	}
}

func nextErr(errs []error, call int) error {
	if len(errs) == 0 {
		return nil
	}
	if call-1 < len(errs) {
		return errs[call-1]
	}
	return errs[len(errs)-1]
}

func (p *fakeGuestProbe) Access(time.Duration) error {
	p.accessCalls++
	p.spend()
	return nextErr(p.accessErrs, p.accessCalls)
}

func (p *fakeGuestProbe) NeedsPrepare() bool { return p.prepare }

func (p *fakeGuestProbe) Prepare(time.Duration) (string, error) {
	p.prepareCalls++
	p.spend()
	if p.prepareErr != nil {
		return "", p.prepareErr
	}
	return "set DNS servers 192.0.2.53", nil
}

func (p *fakeGuestProbe) SystemDNS(string, time.Duration) error {
	p.dnsCalls++
	p.spend()
	return nextErr(p.dnsErrs, p.dnsCalls)
}

func (p *fakeGuestProbe) HTTPS(string, time.Duration) error {
	p.httpsCalls++
	p.spend()
	return nextErr(p.httpsErrs, p.httpsCalls)
}

func (p *fakeGuestProbe) DiagnoseDNS(string, time.Duration) ([]string, string) {
	p.diagnoseCalls++
	return p.diagnoseResolvers, p.diagnoseHint
}

func testCheckConfig(mode GuestNetworkCheckMode) guestNetworkCheckConfig {
	return guestNetworkCheckConfig{
		Mode:          mode,
		DNSNames:      []string{"example.com"},
		HTTPSURLs:     []string{"https://example.com/"},
		Timeout:       60 * time.Second,
		RetryInterval: 5 * time.Second,
		ProbeTimeout:  10 * time.Second,
	}
}

func TestGuestNetworkCheck_DNSFailureWithReachableGuest(t *testing.T) {
	clock := newFakeClock()
	probe := &fakeGuestProbe{
		clock:             clock,
		probeCost:         time.Second,
		dnsErrs:           []error{errors.New("system resolver did not answer for example.com within 10s")},
		diagnoseResolvers: []string{"192.0.2.1 (timeout)"},
		diagnoseHint:      "the guest uses the DHCP-provided resolver 192.0.2.1",
	}

	report := runGuestNetworkCheck("vm-a", testCheckConfig(GuestNetworkCheckFull), probe, clock.clock())

	if report.Status != GuestNetworkFailed || report.Stage != GuestNetworkStageSystemDNS {
		t.Fatalf("expected system-dns failure, got %+v", report)
	}
	if probe.httpsCalls != 0 {
		t.Fatalf("expected no HTTPS probe after DNS failure, got %d", probe.httpsCalls)
	}
	if probe.diagnoseCalls != 1 {
		t.Fatalf("expected one DNS diagnosis, got %d", probe.diagnoseCalls)
	}
	message := (&GuestNetworkError{Report: report}).Error()
	for _, want := range []string{`"vm-a"`, "stage system-dns", "192.0.2.1 (timeout)", "hint: the guest uses the DHCP-provided resolver"} {
		if !strings.Contains(message, want) {
			t.Fatalf("expected error to contain %q, got %q", want, message)
		}
	}
	if strings.Contains(message, "ready") {
		t.Fatalf("a failed check must not claim readiness: %q", message)
	}
}

func TestGuestNetworkCheck_HTTPSTLSFailure(t *testing.T) {
	clock := newFakeClock()
	tlsErr := classifyCurlProbe("https://example.com/", "curl: (60) SSL certificate problem: unable to get local issuer certificate\nhttp_code=000\ncurl_exit=60\n")
	probe := &fakeGuestProbe{clock: clock, probeCost: time.Second, httpsErrs: []error{tlsErr}}

	report := runGuestNetworkCheck("vm-a", testCheckConfig(GuestNetworkCheckFull), probe, clock.clock())

	if report.Status != GuestNetworkFailed || report.Stage != GuestNetworkStageHTTPS {
		t.Fatalf("expected https failure, got %+v", report)
	}
	if !strings.Contains(report.Hint, "TLS verification failed") || !strings.Contains(report.Hint, "never disables certificate verification") {
		t.Fatalf("expected TLS hint, got %q", report.Hint)
	}
	if !strings.Contains(report.Detail, "curl exit 60") {
		t.Fatalf("expected curl exit in detail, got %q", report.Detail)
	}
	if probe.diagnoseCalls != 0 {
		t.Fatal("DNS diagnosis must only run for DNS failures")
	}
}

func TestGuestNetworkCheck_DelayedGuestAccessBecomesReady(t *testing.T) {
	clock := newFakeClock()
	agentDown := errors.New("guest agent not reachable")
	probe := &fakeGuestProbe{clock: clock, probeCost: time.Second, accessErrs: []error{agentDown, agentDown, nil}}

	report := runGuestNetworkCheck("vm-a", testCheckConfig(GuestNetworkCheckFull), probe, clock.clock())

	if report.Status != GuestNetworkReady {
		t.Fatalf("expected ready after delayed guest access, got %+v", report)
	}
	if report.Attempts != 3 || clock.sleeps != 2 {
		t.Fatalf("expected 3 attempts with 2 sleeps, got attempts=%d sleeps=%d", report.Attempts, clock.sleeps)
	}
	if !slices.Contains(report.Verified, "https https://example.com/ ok") {
		t.Fatalf("expected verified HTTPS probe, got %v", report.Verified)
	}
}

func TestGuestNetworkCheck_SuccessAfterDNSRetry(t *testing.T) {
	clock := newFakeClock()
	probe := &fakeGuestProbe{clock: clock, probeCost: time.Second, dnsErrs: []error{errors.New("no answer"), nil}}

	report := runGuestNetworkCheck("vm-a", testCheckConfig(GuestNetworkCheckFull), probe, clock.clock())

	if report.Status != GuestNetworkReady || report.Attempts != 2 {
		t.Fatalf("expected ready on second attempt, got %+v", report)
	}
	if probe.diagnoseCalls != 0 {
		t.Fatal("expected no diagnosis after a successful retry")
	}
}

func TestGuestNetworkCheck_TimeoutIsBounded(t *testing.T) {
	clock := newFakeClock()
	probe := &fakeGuestProbe{clock: clock, probeCost: 3 * time.Second, accessErrs: []error{errors.New("guest agent not reachable")}}
	cfg := testCheckConfig(GuestNetworkCheckFull)

	report := runGuestNetworkCheck("vm-a", cfg, probe, clock.clock())

	if report.Status != GuestNetworkFailed || report.Stage != GuestNetworkStageAccess {
		t.Fatalf("expected guest-access failure, got %+v", report)
	}
	// Each attempt costs 3s plus a 5s pause; the loop must stop before the
	// next pause would cross the 60s deadline.
	if report.Elapsed > cfg.Timeout+cfg.ProbeTimeout {
		t.Fatalf("expected bounded elapsed time, got %s", report.Elapsed)
	}
	if report.Attempts < 2 || report.Attempts > 8 {
		t.Fatalf("expected a bounded number of attempts, got %d", report.Attempts)
	}
}

func TestGuestNetworkCheck_PermanentErrorStopsRetries(t *testing.T) {
	clock := newFakeClock()
	probe := &fakeGuestProbe{
		clock:      clock,
		prepare:    true,
		prepareErr: &guestProbeError{err: errors.New("sudo failed"), hint: "needs passwordless sudo", permanent: true},
	}

	report := runGuestNetworkCheck("vm-a", testCheckConfig(GuestNetworkCheckFull), probe, clock.clock())

	if report.Status != GuestNetworkFailed || report.Stage != GuestNetworkStageDNSConfig || report.Attempts != 1 {
		t.Fatalf("expected immediate dns-config failure, got %+v", report)
	}
	if report.Hint != "needs passwordless sudo" {
		t.Fatalf("expected probe hint, got %q", report.Hint)
	}
}

func TestGuestNetworkCheck_PrepareRunsOnce(t *testing.T) {
	clock := newFakeClock()
	probe := &fakeGuestProbe{clock: clock, prepare: true, dnsErrs: []error{errors.New("no answer"), nil}}

	report := runGuestNetworkCheck("vm-a", testCheckConfig(GuestNetworkCheckFull), probe, clock.clock())

	if report.Status != GuestNetworkReady || probe.prepareCalls != 1 {
		t.Fatalf("expected one DNS configuration across retries, got status=%s prepareCalls=%d", report.Status, probe.prepareCalls)
	}
	if !strings.Contains(report.String(), "dns-config: set DNS servers") {
		t.Fatalf("expected DNS config summary in report, got %q", report.String())
	}
}

func TestGuestNetworkCheck_OfflineModeIsReportedAsUnverified(t *testing.T) {
	clock := newFakeClock()
	probe := &fakeGuestProbe{clock: clock}

	report := runGuestNetworkCheck("vm-a", testCheckConfig(GuestNetworkCheckOffline), probe, clock.clock())

	if report.Status != GuestNetworkOffline {
		t.Fatalf("expected offline status, got %s", report.Status)
	}
	if probe.dnsCalls != 0 || probe.httpsCalls != 0 || probe.accessCalls != 1 {
		t.Fatalf("expected only guest access in offline mode, got access=%d dns=%d https=%d", probe.accessCalls, probe.dnsCalls, probe.httpsCalls)
	}
	if !strings.Contains(report.String(), "DNS and HTTPS NOT verified") {
		t.Fatalf("expected explicit offline marker, got %q", report.String())
	}
}

func TestGuestNetworkCheck_OffModeSkipsWithoutGuestCalls(t *testing.T) {
	probe := &fakeGuestProbe{}

	report := runGuestNetworkCheck("vm-a", testCheckConfig(GuestNetworkCheckOff), probe, newFakeClock().clock())

	if report.Status != GuestNetworkSkipped || probe.accessCalls != 0 {
		t.Fatalf("expected skipped without guest calls, got %+v (access=%d)", report, probe.accessCalls)
	}
	if !strings.Contains(report.String(), "NOT verified") {
		t.Fatalf("expected skipped report to say readiness is not verified, got %q", report.String())
	}
}

func TestGuestNetworkCheck_OffModeStillAppliesDNS(t *testing.T) {
	probe := &fakeGuestProbe{prepare: true}

	report := runGuestNetworkCheck("vm-a", testCheckConfig(GuestNetworkCheckOff), probe, newFakeClock().clock())

	if report.Status != GuestNetworkSkipped || probe.prepareCalls != 1 || probe.dnsCalls != 0 {
		t.Fatalf("expected DNS config without checks, got %+v (prepare=%d dns=%d)", report, probe.prepareCalls, probe.dnsCalls)
	}
}

func TestClassifyCurlProbe(t *testing.T) {
	cases := []struct {
		name     string
		output   string
		wantErr  bool
		wantHint string
	}{
		{name: "redirect is reachable", output: "http_code=302\ncurl_exit=0\n"},
		{name: "client error still proves TLS", output: "http_code=404\ncurl_exit=0\n"},
		{name: "dns", output: "curl: (6) Could not resolve host: example.com\nhttp_code=000\ncurl_exit=6\n", wantErr: true, wantHint: "could not resolve"},
		{name: "resolve timeout", output: "curl: (28) Resolving timed out after 15000 milliseconds\nhttp_code=000\ncurl_exit=28\n", wantErr: true, wantHint: "could not resolve"},
		{name: "tls", output: "curl: (60) SSL certificate problem\nhttp_code=000\ncurl_exit=60\n", wantErr: true, wantHint: "TLS verification failed"},
		{name: "connect", output: "curl: (7) Failed to connect\nhttp_code=000\ncurl_exit=7\n", wantErr: true, wantHint: "not reachable"},
		{name: "missing output", output: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := classifyCurlProbe("https://example.com/", tc.output)
			if (err != nil) != tc.wantErr {
				t.Fatalf("expected error=%v, got %v", tc.wantErr, err)
			}
			if tc.wantHint != "" {
				hint, _ := probeErrorHint(err)
				if !strings.Contains(hint, tc.wantHint) {
					t.Fatalf("expected hint containing %q, got %q", tc.wantHint, hint)
				}
			}
		})
	}
}

func envMap(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestLoadTartGuestNetworkConfig_Defaults(t *testing.T) {
	cfg, err := loadTartGuestNetworkConfig(envMap(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.dns.mode != tartDNSUnmanaged {
		t.Fatalf("expected DNS to stay unmanaged by default, got %v", cfg.dns.mode)
	}
	if cfg.check.Mode != GuestNetworkCheckFull || cfg.check.Timeout != defaultTartNetworkCheckTimeout {
		t.Fatalf("unexpected defaults: %+v", cfg.check)
	}
	if !slices.Equal(cfg.check.HTTPSURLs, []string{defaultTartNetworkCheckURL}) || !slices.Equal(cfg.check.DNSNames, []string{"github.com"}) {
		t.Fatalf("expected DNS names derived from default URL, got %+v", cfg.check)
	}
}

func TestLoadTartGuestNetworkConfig_ExplicitValues(t *testing.T) {
	cfg, err := loadTartGuestNetworkConfig(envMap(map[string]string{
		tartMacOSDNSServersEnvVar:           "192.0.2.53, 2001:db8::53",
		tartMacOSNetworkCheckEnvVar:         "OFFLINE",
		tartMacOSNetworkCheckURLsEnvVar:     "https://a.example/x https://b.example",
		tartMacOSNetworkCheckDNSNamesEnvVar: "intranet.example.",
		tartMacOSNetworkCheckTimeoutEnvVar:  "3m",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.dns.mode != tartDNSServers || !slices.Equal(cfg.dns.servers, []string{"192.0.2.53", "2001:db8::53"}) {
		t.Fatalf("unexpected DNS setting: %+v", cfg.dns)
	}
	if cfg.check.Mode != GuestNetworkCheckOffline || cfg.check.Timeout != 3*time.Minute {
		t.Fatalf("unexpected check config: %+v", cfg.check)
	}
	if !slices.Equal(cfg.check.DNSNames, []string{"intranet.example."}) || len(cfg.check.HTTPSURLs) != 2 {
		t.Fatalf("unexpected targets: %+v", cfg.check)
	}

	cfg, err = loadTartGuestNetworkConfig(envMap(map[string]string{tartMacOSDNSServersEnvVar: "DHCP"}))
	if err != nil || cfg.dns.mode != tartDNSDHCP {
		t.Fatalf("expected dhcp mode, got %+v, %v", cfg.dns, err)
	}
}

func TestLoadTartGuestNetworkConfig_RejectsInvalidValues(t *testing.T) {
	cases := map[string]map[string]string{
		"dns hostname":       {tartMacOSDNSServersEnvVar: "dns.example"},
		"mode":               {tartMacOSNetworkCheckEnvVar: "maybe"},
		"http url":           {tartMacOSNetworkCheckURLsEnvVar: "http://example.com"},
		"url with userinfo":  {tartMacOSNetworkCheckURLsEnvVar: "https://user:pw@example.com"},
		"shell in dns name":  {tartMacOSNetworkCheckDNSNamesEnvVar: "example.com$(reboot)"},
		"zero timeout":       {tartMacOSNetworkCheckTimeoutEnvVar: "0s"},
		"unparsable timeout": {tartMacOSNetworkCheckTimeoutEnvVar: "soon"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loadTartGuestNetworkConfig(envMap(env)); err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
}

func TestPlanTartGuestDNS(t *testing.T) {
	desired := tartDNSSetting{mode: tartDNSServers, servers: []string{"192.0.2.53"}}
	dhcp := tartDNSSetting{mode: tartDNSDHCP}
	managed := tartGuestDNSState{service: "Ethernet", current: []string{"192.0.2.53"}, markerPresent: true, markerService: "Ethernet", markerServers: []string{"192.0.2.53"}}
	oldManaged := tartGuestDNSState{service: "Ethernet", current: []string{"198.51.100.1"}, markerPresent: true, markerService: "Ethernet", markerServers: []string{"198.51.100.1"}}
	user := tartGuestDNSState{service: "Ethernet", current: []string{"203.0.113.9"}}
	empty := tartGuestDNSState{service: "Ethernet"}

	cases := []struct {
		name    string
		setting tartDNSSetting
		state   tartGuestDNSState
		want    tartDNSAction
	}{
		{"unmanaged never touches guest", tartDNSSetting{}, user, tartDNSActionNone},
		{"dhcp guest gets servers", desired, empty, tartDNSActionSet},
		{"already applied is idempotent", desired, managed, tartDNSActionNone},
		{"user set same servers stays", desired, tartGuestDNSState{service: "Ethernet", current: []string{"192.0.2.53"}}, tartDNSActionNone},
		{"sailwright servers are updated", desired, oldManaged, tartDNSActionSet},
		{"user servers are kept", desired, user, tartDNSActionKeepUser},
		{"marker for other service does not own servers", desired, tartGuestDNSState{service: "Ethernet", current: []string{"203.0.113.9"}, markerPresent: true, markerService: "Wi-Fi", markerServers: []string{"203.0.113.9"}}, tartDNSActionKeepUser},
		{"dhcp reverts sailwright servers", dhcp, managed, tartDNSActionReset},
		{"dhcp keeps user servers", dhcp, user, tartDNSActionKeepUser},
		{"dhcp is idempotent", dhcp, empty, tartDNSActionNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, summary := planTartGuestDNS(tc.setting, tc.state)
			if got != tc.want {
				t.Fatalf("expected %s, got %s (%s)", tc.want, got, summary)
			}
		})
	}
}

// fakeTartGuest emulates the guest commands the Tart probe sends via `tart exec`.
type fakeTartGuest struct {
	service  string
	servers  []string
	marker   string
	setCalls int
	scripts  []string
}

func (g *fakeTartGuest) run(_ string, _ time.Duration, executable string, args []string) (string, error) {
	if executable != "tart" || len(args) != 5 || args[0] != "exec" || args[2] != "/bin/sh" || args[3] != "-c" {
		return "", fmt.Errorf("unexpected command %s %v", executable, args)
	}
	script := args[4]
	g.scripts = append(g.scripts, script)
	switch {
	case strings.Contains(script, "-setdnsservers"):
		g.setCalls++
		if strings.Contains(script, " Empty ") {
			g.servers = nil
			g.marker = ""
		} else {
			g.servers = []string{"192.0.2.53"}
			g.marker = "service=" + g.service + "\nservers=192.0.2.53\n"
		}
		var out strings.Builder
		if len(g.servers) == 0 {
			out.WriteString("after=There aren't any DNS Servers set on " + g.service + ".\n")
		}
		for _, server := range g.servers {
			out.WriteString("after=" + server + "\n")
		}
		return out.String(), nil
	case strings.Contains(script, "-getdnsservers"):
		var out strings.Builder
		fmt.Fprintf(&out, "device=en0\nservice=%s\n", g.service)
		if len(g.servers) == 0 {
			fmt.Fprintf(&out, "current=There aren't any DNS Servers set on %s.\n", g.service)
		}
		for _, server := range g.servers {
			fmt.Fprintf(&out, "current=%s\n", server)
		}
		if g.marker != "" {
			out.WriteString("marker=present\n")
			for _, line := range strings.Split(strings.TrimSpace(g.marker), "\n") {
				out.WriteString("marker_" + line + "\n")
			}
		}
		return out.String(), nil
	}
	return "", fmt.Errorf("unexpected script %q", script)
}

func TestTartGuestProbePrepare_IsIdempotentAndRevertsToDHCP(t *testing.T) {
	guest := &fakeTartGuest{service: "Ethernet"}
	probe := &tartGuestProbe{vmName: "vm-a", dns: tartDNSSetting{mode: tartDNSServers, servers: []string{"192.0.2.53"}}, run: guest.run}

	if _, err := probe.Prepare(time.Second); err != nil {
		t.Fatalf("first prepare failed: %v", err)
	}
	summary, err := probe.Prepare(time.Second)
	if err != nil {
		t.Fatalf("second prepare failed: %v", err)
	}
	if guest.setCalls != 1 {
		t.Fatalf("expected DNS servers to be set once, got %d", guest.setCalls)
	}
	if !strings.Contains(summary, "already uses DNS servers 192.0.2.53") {
		t.Fatalf("expected idempotent summary, got %q", summary)
	}
	if !strings.Contains(guest.marker, "servers=192.0.2.53") {
		t.Fatalf("expected sailwright marker, got %q", guest.marker)
	}

	probe.dns = tartDNSSetting{mode: tartDNSDHCP}
	if _, err := probe.Prepare(time.Second); err != nil {
		t.Fatalf("dhcp revert failed: %v", err)
	}
	if len(guest.servers) != 0 || guest.marker != "" {
		t.Fatalf("expected DHCP DNS and no marker, got servers=%v marker=%q", guest.servers, guest.marker)
	}
	if _, err := probe.Prepare(time.Second); err != nil || guest.setCalls != 2 {
		t.Fatalf("expected idempotent DHCP revert, got err=%v setCalls=%d", err, guest.setCalls)
	}
}

func TestTartGuestProbePrepare_KeepsUserServers(t *testing.T) {
	guest := &fakeTartGuest{service: "Ethernet", servers: []string{"203.0.113.9"}}
	probe := &tartGuestProbe{vmName: "vm-a", dns: tartDNSSetting{mode: tartDNSServers, servers: []string{"192.0.2.53"}}, run: guest.run}

	summary, err := probe.Prepare(time.Second)
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	if guest.setCalls != 0 || !slices.Equal(guest.servers, []string{"203.0.113.9"}) {
		t.Fatalf("expected user DNS servers to be untouched, got %v", guest.servers)
	}
	if !strings.Contains(summary, "kept user-managed DNS servers 203.0.113.9") {
		t.Fatalf("unexpected summary %q", summary)
	}
}

func TestTartGuestDNSApplyScript_QuotesServiceNames(t *testing.T) {
	script := tartGuestDNSApplyScript(tartDNSActionSet, "USB 10/100 LAN's", []string{"192.0.2.53"})
	if !strings.Contains(script, `-setdnsservers 'USB 10/100 LAN'"'"'s' '192.0.2.53'`) {
		t.Fatalf("expected quoted service name, got:\n%s", script)
	}
}

func TestParseTartGuestDNSState_ReportsMissingRoute(t *testing.T) {
	if _, err := parseTartGuestDNSState("error=no default route\n"); err == nil || !strings.Contains(err.Error(), "no default route") {
		t.Fatalf("expected default route error, got %v", err)
	}
}

func TestTartDNSFailureHint(t *testing.T) {
	router, resolvers := parseTartDNSDiagnosis("device=en0\nrouter=192.0.2.1\nresolver=192.0.2.1 timeout\n")
	hint := tartDNSFailureHint(tartDNSSetting{}, router, resolvers)
	if !strings.Contains(hint, "Tart NAT (vmnet) DNS forwarder") || !strings.Contains(hint, tartMacOSDNSServersEnvVar) {
		t.Fatalf("expected NAT forwarder hint, got %q", hint)
	}
	if got := formatTartResolverProbes(resolvers); !slices.Equal(got, []string{"192.0.2.1 (timeout)"}) {
		t.Fatalf("unexpected resolver probes %v", got)
	}

	configured := tartDNSFailureHint(tartDNSSetting{mode: tartDNSServers, servers: []string{"198.51.100.53"}}, router, []tartResolverProbe{{address: "198.51.100.53", result: "timeout"}})
	if !strings.Contains(configured, "resolvers from "+tartMacOSDNSServersEnvVar) {
		t.Fatalf("expected configured-resolver hint, got %q", configured)
	}

	answered := tartDNSFailureHint(tartDNSSetting{}, router, []tartResolverProbe{{address: "192.0.2.1", result: "answered"}})
	if !strings.Contains(answered, "answers direct queries") {
		t.Fatalf("expected system-resolution hint, got %q", answered)
	}

	if none := tartDNSFailureHint(tartDNSSetting{}, "", nil); !strings.Contains(none, "no DNS resolver") {
		t.Fatalf("expected no-resolver hint, got %q", none)
	}
}

func TestTartGuestProbeSystemDNS(t *testing.T) {
	cases := map[string]struct {
		output  string
		wantErr string
	}{
		"resolved": {output: "dns_address=192.0.2.10\ndns_status=done\n"},
		"timeout":  {output: "dns_status=timeout\n", wantErr: "did not answer"},
		"empty":    {output: "dns_status=done\n", wantErr: "no address"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			probe := &tartGuestProbe{vmName: "vm-a", run: func(string, time.Duration, string, []string) (string, error) { return tc.output, nil }}
			err := probe.SystemDNS("example.com", 5*time.Second)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestTartGuestProbeAccess_UnsupportedTartIsPermanent(t *testing.T) {
	probe := &tartGuestProbe{vmName: "vm-a", run: func(string, time.Duration, string, []string) (string, error) {
		return "Error: Unexpected argument 'exec'", errors.New("exit status 64")
	}}
	err := probe.Access(time.Second)
	if _, permanent := probeErrorHint(err); !permanent {
		t.Fatalf("expected permanent error, got %v", err)
	}
}

func TestEnsureTartGuestNetworkAfterStart_WrapsReadinessFailure(t *testing.T) {
	previous := ensureTartGuestNetworkFunc
	t.Cleanup(func() { ensureTartGuestNetworkFunc = previous })
	ensureTartGuestNetworkFunc = func(string, string) error {
		return &GuestNetworkError{Report: GuestNetworkReport{Guest: "vm-a", Status: GuestNetworkFailed, Stage: GuestNetworkStageSystemDNS}}
	}

	err := ensureTartGuestNetworkAfterStart("/project", "vm-a", "192.0.2.2")
	var netErr *GuestNetworkError
	if !errors.As(err, &netErr) || netErr.Report.Stage != GuestNetworkStageSystemDNS {
		t.Fatalf("expected wrapped GuestNetworkError, got %v", err)
	}
	if !strings.Contains(err.Error(), "running at 192.0.2.2 but not network-ready") {
		t.Fatalf("expected IP reachability to be distinguished from readiness, got %q", err)
	}
}

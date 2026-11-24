package deploy

import (
	"errors"
	"fmt"
	"log"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	tartMacOSDNSServersEnvVar           = "TART_MACOS_DNS_SERVERS"
	tartMacOSDNSServersForceEnvVar      = "TART_MACOS_DNS_SERVERS_FORCE"
	tartMacOSNetworkCheckEnvVar         = "TART_MACOS_NETWORK_CHECK"
	tartMacOSNetworkCheckURLsEnvVar     = "TART_MACOS_NETWORK_CHECK_URLS"
	tartMacOSNetworkCheckDNSNamesEnvVar = "TART_MACOS_NETWORK_CHECK_DNS_NAMES"
	tartMacOSNetworkCheckTimeoutEnvVar  = "TART_MACOS_NETWORK_CHECK_TIMEOUT"

	tartDNSServersDHCPValue         = "dhcp"
	tartGuestDNSMarkerDir           = "/var/db/sailwright"
	tartGuestDNSMarkerPath          = tartGuestDNSMarkerDir + "/tart-dns"
	tartGuestNetworkDocsHint        = "see docs/tart-guest-network.md"
	defaultTartNetworkCheckURL      = "https://github.com/"
	defaultTartNetworkCheckTimeout  = 90 * time.Second
	maxTartNetworkCheckTimeout      = 30 * time.Minute
	defaultTartNetworkRetryInterval = 5 * time.Second
	defaultTartNetworkProbeTimeout  = 15 * time.Second
	// tartGuestExecGracePeriod covers `tart exec` start-up on top of a
	// guest-side time limit.
	tartGuestExecGracePeriod = 10 * time.Second
	// tartGuestAgentConnectTimeout lets `tart exec` finish its own wait for
	// the guest agent (about 30 seconds in Tart 2.30) and report why it
	// failed instead of being killed first.
	tartGuestAgentConnectTimeout = 45 * time.Second
	tartGuestAccessToken         = "sailwright-guest-ok"
	tartGuestMaxDiagnosedServers = 3
	maxReportDetailLength        = 300
)

// TartMacOSNetworkEnvVars lists the Tart guest network variables: guest
// DNS, the readiness check and the bridged interface. Gateway jobs receive
// them only through env_passthrough, and the gateway does not redact their
// values (they hold resolver addresses and URLs, not credentials).
var TartMacOSNetworkEnvVars = []string{
	tartMacOSDNSServersEnvVar,
	tartMacOSDNSServersForceEnvVar,
	tartMacOSNetworkCheckEnvVar,
	tartMacOSNetworkCheckURLsEnvVar,
	tartMacOSNetworkCheckDNSNamesEnvVar,
	tartMacOSNetworkCheckTimeoutEnvVar,
	tartMacOSBridgedInterfaceEnvVar,
}

var (
	dnsNamePattern         = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*\.?$`)
	plainShellWordPattern  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	whitespaceRunsPattern  = regexp.MustCompile(`\s+`)
	errTartGuestNotRunning = errors.New("the Tart VM is not running")
)

type tartDNSMode int

const (
	// tartDNSUnmanaged leaves guest DNS untouched (default).
	tartDNSUnmanaged tartDNSMode = iota
	// tartDNSDHCP reverts DNS servers previously set by Sailwright.
	tartDNSDHCP
	// tartDNSServers sets explicit DNS servers.
	tartDNSServers
)

type tartDNSSetting struct {
	mode    tartDNSMode
	servers []string
	// force replaces manual DNS servers that Sailwright did not set.
	force bool
}

type tartGuestNetworkConfig struct {
	dns   tartDNSSetting
	check guestNetworkCheckConfig
}

// ValidateTartGuestNetworkConfig reports invalid Tart guest network
// settings before any VM work starts.
func ValidateTartGuestNetworkConfig() error {
	if _, err := loadTartGuestNetworkConfig(os.Getenv); err != nil {
		return fmt.Errorf("invalid Tart guest network configuration: %w", err)
	}
	return nil
}

func loadTartGuestNetworkConfig(getenv func(string) string) (tartGuestNetworkConfig, error) {
	var cfg tartGuestNetworkConfig

	dns, err := parseTartDNSSetting(getenv(tartMacOSDNSServersEnvVar), getenv(tartMacOSDNSServersForceEnvVar))
	if err != nil {
		return cfg, err
	}
	cfg.dns = dns

	mode := GuestNetworkCheckMode(strings.ToLower(strings.TrimSpace(getenv(tartMacOSNetworkCheckEnvVar))))
	switch mode {
	case "":
		mode = GuestNetworkCheckFull
	case GuestNetworkCheckFull, GuestNetworkCheckOffline, GuestNetworkCheckOff:
	default:
		return cfg, fmt.Errorf("%s must be one of %q, %q or %q: %q", tartMacOSNetworkCheckEnvVar, GuestNetworkCheckFull, GuestNetworkCheckOffline, GuestNetworkCheckOff, mode)
	}

	urls := splitConfigList(getenv(tartMacOSNetworkCheckURLsEnvVar))
	if len(urls) == 0 {
		urls = []string{defaultTartNetworkCheckURL}
	}
	var hostsFromURLs []string
	for _, raw := range urls {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
			return cfg, fmt.Errorf("%s entries must be https URLs without credentials: %q", tartMacOSNetworkCheckURLsEnvVar, raw)
		}
		host := parsed.Hostname()
		// An IP literal needs no name resolution, so it adds no DNS check.
		if _, ipErr := netip.ParseAddr(host); ipErr == nil {
			continue
		}
		if !dnsNamePattern.MatchString(host) {
			return cfg, fmt.Errorf("%s entries must use a DNS host name or an IP address: %q", tartMacOSNetworkCheckURLsEnvVar, raw)
		}
		if !slices.Contains(hostsFromURLs, host) {
			hostsFromURLs = append(hostsFromURLs, host)
		}
	}

	names := splitConfigList(getenv(tartMacOSNetworkCheckDNSNamesEnvVar))
	if len(names) == 0 {
		names = hostsFromURLs
	} else {
		for _, name := range names {
			if _, ipErr := netip.ParseAddr(name); ipErr == nil || !dnsNamePattern.MatchString(name) {
				return cfg, fmt.Errorf("%s entries must be DNS host names: %q", tartMacOSNetworkCheckDNSNamesEnvVar, name)
			}
		}
	}

	timeout := defaultTartNetworkCheckTimeout
	if raw := strings.TrimSpace(getenv(tartMacOSNetworkCheckTimeoutEnvVar)); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed < time.Second || parsed > maxTartNetworkCheckTimeout {
			return cfg, fmt.Errorf("%s must be a duration between 1s and %s, for example 90s: %q", tartMacOSNetworkCheckTimeoutEnvVar, maxTartNetworkCheckTimeout, raw)
		}
		timeout = parsed
	}

	cfg.check = guestNetworkCheckConfig{
		Mode:          mode,
		DNSNames:      names,
		HTTPSURLs:     urls,
		Timeout:       timeout,
		RetryInterval: defaultTartNetworkRetryInterval,
		ProbeTimeout:  defaultTartNetworkProbeTimeout,
	}
	return cfg, nil
}

func parseTartDNSSetting(raw string, rawForce string) (tartDNSSetting, error) {
	force := false
	if trimmed := strings.TrimSpace(rawForce); trimmed != "" {
		parsed, err := strconv.ParseBool(trimmed)
		if err != nil {
			return tartDNSSetting{}, fmt.Errorf("%s must be true or false: %q", tartMacOSDNSServersForceEnvVar, rawForce)
		}
		force = parsed
	}

	trimmed := strings.TrimSpace(raw)
	switch {
	case trimmed == "":
		if force {
			return tartDNSSetting{}, fmt.Errorf("%s=true needs %s set to a list of IP addresses", tartMacOSDNSServersForceEnvVar, tartMacOSDNSServersEnvVar)
		}
		return tartDNSSetting{mode: tartDNSUnmanaged}, nil
	case strings.EqualFold(trimmed, tartDNSServersDHCPValue):
		if force {
			return tartDNSSetting{}, fmt.Errorf("%s=true only applies to a list of IP addresses; %s=%s never resets DNS servers that sailwright did not set", tartMacOSDNSServersForceEnvVar, tartMacOSDNSServersEnvVar, tartDNSServersDHCPValue)
		}
		return tartDNSSetting{mode: tartDNSDHCP}, nil
	}

	var servers []string
	for _, server := range splitConfigList(trimmed) {
		normalized, ok := normalizeDNSServer(server)
		if !ok {
			return tartDNSSetting{}, fmt.Errorf("%s must be %q or a comma-separated list of IP addresses: %q is not an IP address", tartMacOSDNSServersEnvVar, tartDNSServersDHCPValue, server)
		}
		servers = append(servers, normalized)
	}
	return tartDNSSetting{mode: tartDNSServers, servers: servers, force: force}, nil
}

// normalizeDNSServer returns the canonical text form of an IP address, so
// that equivalent IPv6 spellings compare equal.
func normalizeDNSServer(value string) (string, bool) {
	addr, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil {
		return "", false
	}
	return addr.Unmap().String(), true
}

func normalizeDNSServers(values []string) []string {
	var out []string
	for _, value := range values {
		if normalized, ok := normalizeDNSServer(value); ok {
			out = append(out, normalized)
		}
	}
	return out
}

func splitConfigList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == ';'
	})
	var out []string
	for _, field := range fields {
		if field = strings.TrimSpace(field); field != "" {
			out = append(out, field)
		}
	}
	return out
}

// tartGuestDNSState is the guest's DNS configuration for its primary
// network service, plus Sailwright's marker of what it applied earlier.
// Server lists are normalized.
type tartGuestDNSState struct {
	device        string
	service       string
	current       []string
	markerPresent bool
	markerService string
	markerServers []string
}

type tartDNSAction string

const (
	tartDNSActionNone     tartDNSAction = "none"
	tartDNSActionSet      tartDNSAction = "set"
	tartDNSActionReset    tartDNSAction = "reset"
	tartDNSActionKeepUser tartDNSAction = "keep-user"
)

// managedBySailwright reports whether the current manual DNS servers are the
// ones Sailwright recorded when it last applied TART_MACOS_DNS_SERVERS.
func (s tartGuestDNSState) managedBySailwright() bool {
	return s.markerPresent && s.markerService == s.service && slices.Equal(s.markerServers, s.current)
}

// planTartGuestDNS decides how to reconcile the guest with the setting.
// Manual DNS servers that Sailwright did not set are only replaced when the
// setting forces it.
func planTartGuestDNS(setting tartDNSSetting, state tartGuestDNSState) (tartDNSAction, string) {
	service := strconv.Quote(state.service)
	desired := strings.Join(setting.servers, " ")
	current := strings.Join(state.current, " ")
	revertNote := fmt.Sprintf("managed by sailwright; %s=%s reverts to DHCP", tartMacOSDNSServersEnvVar, tartDNSServersDHCPValue)
	switch setting.mode {
	case tartDNSServers:
		managed := state.managedBySailwright()
		switch {
		case slices.Equal(state.current, setting.servers) && managed:
			return tartDNSActionNone, fmt.Sprintf("guest service %s already uses DNS servers %s", service, desired)
		case slices.Equal(state.current, setting.servers) && !setting.force && !state.markerPresent:
			return tartDNSActionNone, fmt.Sprintf("guest service %s already uses DNS servers %s (set outside sailwright)", service, desired)
		case slices.Equal(state.current, setting.servers):
			// Same servers but a stale marker, or forced adoption: record
			// them so a later revert works.
			return tartDNSActionSet, fmt.Sprintf("recorded DNS servers %s on guest service %s (%s)", desired, service, revertNote)
		case len(state.current) == 0 || managed:
			return tartDNSActionSet, fmt.Sprintf("set DNS servers %s on guest service %s (%s)", desired, service, revertNote)
		case setting.force:
			return tartDNSActionSet, fmt.Sprintf("replaced user-managed DNS servers %s with %s on guest service %s (%s=true; %s)", current, desired, service, tartMacOSDNSServersForceEnvVar, revertNote)
		default:
			return tartDNSActionKeepUser, fmt.Sprintf("kept user-managed DNS servers %s on guest service %s; %s was not applied (set %s=true to replace them)", current, service, tartMacOSDNSServersEnvVar, tartMacOSDNSServersForceEnvVar)
		}
	case tartDNSDHCP:
		switch {
		case state.managedBySailwright():
			return tartDNSActionReset, fmt.Sprintf("reverted guest service %s to DHCP-provided DNS", service)
		case len(state.current) == 0:
			if state.markerPresent {
				return tartDNSActionReset, fmt.Sprintf("guest service %s already uses DHCP-provided DNS; removed stale sailwright marker", service)
			}
			return tartDNSActionNone, fmt.Sprintf("guest service %s already uses DHCP-provided DNS", service)
		default:
			return tartDNSActionKeepUser, fmt.Sprintf("kept user-managed DNS servers %s on guest service %s; reset them manually with `sudo networksetup -setdnsservers %s Empty`", current, service, shellQuoteForDisplay(state.service))
		}
	default:
		return tartDNSActionNone, ""
	}
}

func shellQuoteForDisplay(value string) string {
	if plainShellWordPattern.MatchString(value) {
		return value
	}
	return bashSingleQuote(value)
}

// tartGuestProbe runs readiness probes through `tart exec`, which talks to
// the Tart guest agent and does not need SSH credentials.
type tartGuestProbe struct {
	projectDir string
	vmName     string
	dns        tartDNSSetting
	// readOnly reports planned DNS changes without applying them.
	readOnly bool
	run      func(string, time.Duration, string, []string) (string, error)
}

// exec runs script in the guest. The returned error is a short, single-line
// description; it never contains the script.
func (p *tartGuestProbe) exec(script string, hostTimeout time.Duration) (string, error) {
	output, err := p.run(p.projectDir, hostTimeout, "tart", []string{"exec", p.vmName, "/bin/sh", "-c", script})
	if err == nil {
		return output, nil
	}
	var exitErr *exec.ExitError
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return output, errors.New("`tart` was not found on PATH")
	case errors.As(err, &exitErr) && exitErr.ExitCode() < 0:
		return output, fmt.Errorf("`tart exec` did not finish within %s", hostTimeout.Round(time.Second))
	case errors.As(err, &exitErr):
		return output, fmt.Errorf("`tart exec` exited with status %d", exitErr.ExitCode())
	default:
		return output, errors.New("`tart exec` failed")
	}
}

// oneLine collapses output into a short single line for reports.
func oneLine(value string) string {
	value = strings.TrimSpace(whitespaceRunsPattern.ReplaceAllString(value, " "))
	if len(value) > maxReportDetailLength {
		value = value[:maxReportDetailLength] + "..."
	}
	return value
}

func lastLines(output string, count int) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return oneLine(strings.Join(lines, " | "))
}

const tartGuestAccessHint = "install the Tart guest agent (tart-guest-agent) in the image or upgrade Tart. To use the VM without verifying its network, set TART_MACOS_NETWORK_CHECK=off (TART_MACOS_NETWORK_CHECK=offline also needs the guest agent); sailwright never downgrades the check on its own"

func (p *tartGuestProbe) Access(timeout time.Duration) error {
	output, err := p.exec("echo "+tartGuestAccessToken, max(timeout, tartGuestAgentConnectTimeout))
	if err == nil && strings.Contains(output, tartGuestAccessToken) {
		return nil
	}
	detail := lastLines(output, 2)
	lower := strings.ToLower(detail)
	switch {
	case strings.Contains(lower, "unexpected argument") || strings.Contains(lower, "unknown subcommand") || strings.Contains(lower, "unknown command"):
		// Tart predates `tart exec`; retrying cannot help.
		return &guestProbeError{
			err:       fmt.Errorf("this Tart version does not support `tart exec` (%s)", detail),
			hint:      tartGuestAccessHint,
			permanent: true,
		}
	case strings.Contains(lower, "guest agent") || strings.Contains(lower, "control socket"):
		// `tart exec` already waited for the agent itself.
		return &guestProbeError{
			err:       fmt.Errorf("the Tart guest agent is not answering in the guest (%s)", detail),
			hint:      tartGuestAccessHint,
			permanent: true,
		}
	case strings.Contains(lower, "is not running"):
		// Right after `tart run`, `tart ip` can already report the previous
		// DHCP lease while `tart exec` still says the VM is not running, so
		// this is retried within the window.
		return &guestProbeError{err: errTartGuestNotRunning, hint: "the Tart VM process did not come up; check the tart run log in .dev-alchemy/tart"}
	}
	if err == nil {
		err = errors.New("unexpected output")
	}
	return &guestProbeError{
		err:  fmt.Errorf("could not run a command in the guest: %v (%s)", err, detail),
		hint: tartGuestAccessHint,
	}
}

func (p *tartGuestProbe) NeedsPrepare() bool {
	return p.dns.mode != tartDNSUnmanaged
}

const tartGuestPrimaryServiceScript = `dev=$(/sbin/route -n get default 2>/dev/null | /usr/bin/awk '/interface:/{print $2}')
if [ -z "$dev" ]; then echo "error=no default route"; exit 0; fi
svc=$(/usr/sbin/networksetup -listnetworkserviceorder | /usr/bin/awk -v d="$dev" '/^\(([0-9]+|\*)\) /{sub(/^\(([0-9]+|\*)\) /, ""); name=$0} index($0, "Device: " d ")") {print name; exit}')
if [ -z "$svc" ]; then echo "error=no network service for $dev"; exit 0; fi
echo "device=$dev"
echo "service=$svc"
`

func tartGuestDNSInspectScript() string {
	return tartGuestPrimaryServiceScript + `/usr/sbin/networksetup -getdnsservers "$svc" | while IFS= read -r line; do echo "current=$line"; done
if [ -f ` + tartGuestDNSMarkerPath + ` ]; then echo "marker=present"; /usr/bin/sed 's/^/marker_/' ` + tartGuestDNSMarkerPath + `; fi
`
}

func parseTartGuestDNSState(output string) (tartGuestDNSState, error) {
	var state tartGuestDNSState
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "error":
			return state, errors.New(value)
		case "device":
			state.device = value
		case "service":
			state.service = value
		case "current":
			state.current = append(state.current, normalizeDNSServers([]string{value})...)
		case "marker":
			state.markerPresent = true
		case "marker_service":
			state.markerService = value
		case "marker_servers":
			state.markerServers = normalizeDNSServers(strings.Fields(value))
		}
	}
	if state.service == "" {
		return state, errors.New("could not determine the guest's primary network service")
	}
	return state, nil
}

func (p *tartGuestProbe) Prepare(timeout time.Duration) (string, error) {
	output, err := p.exec(tartGuestDNSInspectScript(), timeout+tartGuestExecGracePeriod)
	if err != nil {
		return "", fmt.Errorf("failed to inspect guest DNS configuration: %v (%s)", err, lastLines(output, 2))
	}
	state, err := parseTartGuestDNSState(output)
	if err != nil {
		return "", fmt.Errorf("failed to inspect guest DNS configuration: %w", err)
	}

	action, summary := planTartGuestDNS(p.dns, state)
	switch action {
	case tartDNSActionNone:
		return summary, nil
	case tartDNSActionKeepUser:
		log.Printf("Warning: Tart VM %q: %s", p.vmName, summary)
		return summary, nil
	}
	if p.readOnly {
		return fmt.Sprintf("check mode, guest not changed; would have %s", summary), nil
	}

	output, err = p.exec(tartGuestDNSApplyScript(action, state.service, p.dns.servers), timeout+tartGuestExecGracePeriod)
	if err != nil {
		var hint string
		permanent := false
		switch {
		case strings.Contains(output, "sudo-failed"):
			hint = "the guest agent user needs passwordless sudo to change DNS servers; set them manually with networksetup or unset " + tartMacOSDNSServersEnvVar
			permanent = true
		case strings.Contains(output, "marker-failed"):
			hint = "sailwright could not record its DNS marker " + tartGuestDNSMarkerPath + " in the guest, so it did not change DNS servers; check free space and permissions of " + tartGuestDNSMarkerDir
			permanent = true
		}
		return "", &guestProbeError{
			err:       fmt.Errorf("failed to apply guest DNS configuration: %v (%s)", err, lastLines(output, 2)),
			hint:      hint,
			permanent: permanent,
		}
	}

	want := p.dns.servers
	if action == tartDNSActionReset {
		want = nil
	}
	var after []string
	for _, line := range strings.Split(output, "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "after="); ok {
			after = append(after, normalizeDNSServers([]string{value})...)
		}
	}
	if !slices.Equal(after, want) {
		return "", &guestProbeError{
			err:       fmt.Errorf("guest DNS servers are %q after applying %q", strings.Join(after, " "), strings.Join(want, " ")),
			permanent: true,
		}
	}
	log.Printf("Tart VM %q: %s", p.vmName, summary)
	return summary, nil
}

// tartGuestDNSApplyScript records the marker before it changes DNS servers,
// so servers set by sailwright are always covered by a marker and a later
// `dhcp` can revert them. On revert the marker is removed last.
func tartGuestDNSApplyScript(action tartDNSAction, service string, servers []string) string {
	quotedService := bashSingleQuote(service)
	var b strings.Builder
	b.WriteString("sudo -n /usr/bin/true || { echo sudo-failed; exit 10; }\n")
	switch action {
	case tartDNSActionSet:
		quotedServers := make([]string, 0, len(servers))
		for _, server := range servers {
			quotedServers = append(quotedServers, bashSingleQuote(server))
		}
		fmt.Fprintf(&b, "sudo -n /bin/mkdir -p %s || { echo marker-failed; exit 11; }\n", tartGuestDNSMarkerDir)
		fmt.Fprintf(&b, "printf 'service=%%s\\nservers=%%s\\n' %s %s | sudo -n /usr/bin/tee %s >/dev/null || { echo marker-failed; exit 11; }\n", quotedService, bashSingleQuote(strings.Join(servers, " ")), tartGuestDNSMarkerPath)
		fmt.Fprintf(&b, "sudo -n /bin/chmod 0644 %s || { echo marker-failed; exit 11; }\n", tartGuestDNSMarkerPath)
		fmt.Fprintf(&b, "sudo -n /usr/sbin/networksetup -setdnsservers %s %s || { echo sudo-failed; exit 10; }\n", quotedService, strings.Join(quotedServers, " "))
	case tartDNSActionReset:
		fmt.Fprintf(&b, "sudo -n /usr/sbin/networksetup -setdnsservers %s Empty || { echo sudo-failed; exit 10; }\n", quotedService)
		fmt.Fprintf(&b, "sudo -n /bin/rm -f %s || { echo marker-failed; exit 11; }\n", tartGuestDNSMarkerPath)
	}
	b.WriteString("sudo -n /usr/bin/dscacheutil -flushcache 2>/dev/null || true\n")
	fmt.Fprintf(&b, "/usr/sbin/networksetup -getdnsservers %s | while IFS= read -r line; do echo \"after=$line\"; done\n", quotedService)
	return b.String()
}

// tartGuestSystemDNSScript resolves name through the guest's system resolver
// (mDNSResponder) with a guest-side watchdog: a broken resolver otherwise
// blocks dscacheutil for about a minute.
func tartGuestSystemDNSScript(name string, timeout time.Duration) string {
	limit := max(int(timeout/time.Second), 1)
	return fmt.Sprintf(`out=$(/usr/bin/mktemp -t sailwright-dns) || exit 1
/usr/bin/dscacheutil -q host -a name %s >"$out" 2>&1 &
pid=$!
n=0
while /bin/kill -0 "$pid" 2>/dev/null; do
  if [ "$n" -ge %d ]; then /bin/kill "$pid" 2>/dev/null; /bin/rm -f "$out"; echo "dns_status=timeout"; exit 0; fi
  /bin/sleep 1
  n=$((n+1))
done
/usr/bin/awk '/^ip(v6)?_address:/{print "dns_address=" $2}' "$out"
/bin/rm -f "$out"
echo "dns_status=done"
`, bashSingleQuote(name), limit)
}

func (p *tartGuestProbe) SystemDNS(name string, timeout time.Duration) error {
	output, err := p.exec(tartGuestSystemDNSScript(name, timeout), timeout+tartGuestExecGracePeriod)
	// Trust the probe's own result when it got that far; `tart exec` can
	// report a transport error after the guest command finished.
	if err != nil && !strings.Contains(output, "dns_status=") {
		return fmt.Errorf("system DNS probe for %s could not run: %v (%s)", name, err, lastLines(output, 2))
	}
	switch {
	case strings.Contains(output, "dns_status=timeout"):
		return fmt.Errorf("system resolver did not answer for %s within %s", name, timeout.Round(time.Second))
	case strings.Contains(output, "dns_address="):
		return nil
	case strings.Contains(output, "dns_status=done"):
		return fmt.Errorf("system resolver returned no address for %s", name)
	default:
		return fmt.Errorf("system DNS probe for %s returned unexpected output (%s)", name, lastLines(output, 2))
	}
}

func tartGuestHTTPSScript(target string, timeout time.Duration) string {
	limit := max(int(timeout/time.Second), 1)
	return fmt.Sprintf(`/usr/bin/curl --silent --show-error --output /dev/null --proto '=https' --max-time %d --write-out 'http_code=%%{http_code}\n' %s 2>&1
echo "curl_exit=$?"
`, limit, bashSingleQuote(target))
}

var curlTLSExitCodes = []int{35, 51, 53, 54, 58, 59, 60, 64, 66, 77, 80, 82, 83, 90, 91}

func (p *tartGuestProbe) HTTPS(target string, timeout time.Duration) error {
	output, err := p.exec(tartGuestHTTPSScript(target, timeout), timeout+tartGuestExecGracePeriod)
	if err != nil && !strings.Contains(output, "curl_exit=") {
		return fmt.Errorf("HTTPS probe for %s could not run: %v (%s)", target, err, lastLines(output, 2))
	}
	return classifyCurlProbe(target, output)
}

func classifyCurlProbe(target string, output string) error {
	exitCode := -1
	httpCode := ""
	var curlMessage string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "curl_exit="):
			exitCode, _ = strconv.Atoi(strings.TrimPrefix(line, "curl_exit="))
		case strings.HasPrefix(line, "http_code="):
			httpCode = strings.TrimPrefix(line, "http_code=")
		case strings.HasPrefix(line, "curl: "):
			curlMessage = oneLine(line)
		}
	}
	if exitCode == 0 && httpCode != "" && httpCode != "000" {
		return nil
	}
	detail := fmt.Sprintf("HTTPS request to %s failed (curl exit %d)", target, exitCode)
	if curlMessage != "" {
		detail += ": " + curlMessage
	}
	lowerMessage := strings.ToLower(curlMessage)
	switch {
	case exitCode == 6 || (exitCode == 28 && strings.Contains(lowerMessage, "resolving timed out")):
		return &guestProbeError{err: errors.New(detail), hint: "the guest could not resolve the HTTPS host; fix guest DNS (" + tartMacOSDNSServersEnvVar + ", " + tartGuestNetworkDocsHint + ")"}
	case slices.Contains(curlTLSExitCodes, exitCode):
		return &guestProbeError{err: errors.New(detail), hint: "TLS verification failed inside the guest; if your network uses a TLS-inspecting proxy, install its root CA in the guest trust store. Sailwright never disables certificate verification"}
	case exitCode == 7 || exitCode == 28:
		return &guestProbeError{err: errors.New(detail), hint: "the HTTPS endpoint is not reachable from the guest; check guest routing, proxy settings and host firewall, or point " + tartMacOSNetworkCheckURLsEnvVar + " at an endpoint your network allows"}
	default:
		return &guestProbeError{err: errors.New(detail)}
	}
}

func tartGuestDNSDiagnosisScript(name string) string {
	return fmt.Sprintf(`dev=$(/sbin/route -n get default 2>/dev/null | /usr/bin/awk '/interface:/{print $2}')
echo "device=$dev"
if [ -n "$dev" ]; then echo "router=$(/usr/sbin/ipconfig getoption "$dev" router 2>/dev/null)"; fi
for ns in $(/usr/sbin/scutil --dns | /usr/bin/awk '/^resolver #/{f=0} /^resolver #1$/{f=1} f && /nameserver\[[0-9]+\]/{print $3}' | /usr/bin/sort -u | /usr/bin/head -n %d); do
  out=$(/usr/bin/dig @"$ns" +time=2 +tries=1 +short %s A 2>&1)
  if printf '%%s\n' "$out" | /usr/bin/grep -Eq '^[0-9]+(\.[0-9]+){3}$'; then st=answered
  elif printf '%%s' "$out" | /usr/bin/grep -q 'timed out'; then st=timeout
  elif printf '%%s' "$out" | /usr/bin/grep -Eq 'reset|refused|unreachable'; then st=refused
  else st=no-answer; fi
  echo "resolver=$ns $st"
done
`, tartGuestMaxDiagnosedServers, bashSingleQuote(name))
}

type tartResolverProbe struct {
	address string
	result  string
}

func parseTartDNSDiagnosis(output string) (string, []tartResolverProbe) {
	router := ""
	var resolvers []tartResolverProbe
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "router":
			router = strings.TrimSpace(value)
		case "resolver":
			fields := strings.Fields(value)
			if len(fields) == 2 {
				resolvers = append(resolvers, tartResolverProbe{address: fields[0], result: fields[1]})
			}
		}
	}
	return router, resolvers
}

func (p *tartGuestProbe) DiagnoseDNS(name string, timeout time.Duration) ([]string, string) {
	// The host timeout is the whole budget: diagnosis stays in the window.
	output, err := p.exec(tartGuestDNSDiagnosisScript(name), timeout)
	if err != nil {
		return nil, fmt.Sprintf("could not collect guest resolver diagnostics: %v", err)
	}
	router, resolvers := parseTartDNSDiagnosis(output)
	return formatTartResolverProbes(resolvers), tartDNSFailureHint(p.dns, router, resolvers)
}

func formatTartResolverProbes(resolvers []tartResolverProbe) []string {
	formatted := make([]string, 0, len(resolvers))
	for _, resolver := range resolvers {
		formatted = append(formatted, fmt.Sprintf("%s (%s)", resolver.address, resolver.result))
	}
	return formatted
}

func tartDNSFailureHint(setting tartDNSSetting, router string, resolvers []tartResolverProbe) string {
	if len(resolvers) == 0 {
		return "the guest has no DNS resolver configured; check DHCP on the Tart network or set " + tartMacOSDNSServersEnvVar + " (" + tartGuestNetworkDocsHint + ")"
	}
	var failing []string
	routerFails := false
	for _, resolver := range resolvers {
		if resolver.result == "answered" {
			return "at least one guest resolver answers direct queries, but system resolution failed; check resolver order, search domains and /etc/resolver overrides in the guest"
		}
		failing = append(failing, resolver.address)
		if router != "" && resolver.address == router {
			routerFails = true
		}
	}
	// Blame TART_MACOS_DNS_SERVERS only when the guest actually uses those
	// resolvers; in check mode or with kept user servers it may not.
	if setting.mode == tartDNSServers && slices.ContainsFunc(failing, func(address string) bool {
		normalized, _ := normalizeDNSServer(address)
		return slices.Contains(setting.servers, normalized)
	}) {
		return fmt.Sprintf("the resolvers from %s (%s) do not answer from inside the guest; choose resolvers the guest can reach (%s)", tartMacOSDNSServersEnvVar, strings.Join(failing, ", "), tartGuestNetworkDocsHint)
	}
	if routerFails {
		return fmt.Sprintf("the guest uses the DHCP-provided resolver %s, which is the Tart NAT (vmnet) DNS forwarder on the host, and it does not answer; set %s to a resolver reachable from the guest, or use TART_MACOS_BRIDGED_INTERFACE (%s)", router, tartMacOSDNSServersEnvVar, tartGuestNetworkDocsHint)
	}
	return fmt.Sprintf("none of the guest resolvers (%s) answer from inside the guest; set %s to a reachable resolver (%s)", strings.Join(failing, ", "), tartMacOSDNSServersEnvVar, tartGuestNetworkDocsHint)
}

var tartGuestNetworkCommandRunner = runCommandWithCombinedOutput

// TartGuestNetworkOptions adjusts EnsureTartGuestNetworkWithOptions.
type TartGuestNetworkOptions struct {
	// ReadOnly checks the network but does not change guest DNS; planned
	// changes are only reported. Used for `provision --check`.
	ReadOnly bool
}

// EnsureTartGuestNetwork applies the configured guest DNS servers and checks
// guest access, system DNS and HTTPS from inside the running Tart VM. It
// returns a *GuestNetworkError when the guest is reachable by IP but not
// ready for networked provisioning.
func EnsureTartGuestNetwork(projectDir string, vmName string) error {
	return EnsureTartGuestNetworkWithOptions(projectDir, vmName, TartGuestNetworkOptions{})
}

// EnsureTartGuestNetworkWithOptions is EnsureTartGuestNetwork with options.
func EnsureTartGuestNetworkWithOptions(projectDir string, vmName string, options TartGuestNetworkOptions) error {
	cfg, err := loadTartGuestNetworkConfig(os.Getenv)
	if err != nil {
		return fmt.Errorf("invalid Tart guest network configuration: %w", err)
	}
	probe := &tartGuestProbe{projectDir: projectDir, vmName: vmName, dns: cfg.dns, readOnly: options.ReadOnly, run: tartGuestNetworkCommandRunner}
	if cfg.check.Mode != GuestNetworkCheckOff || probe.NeedsPrepare() {
		log.Printf("Checking guest network readiness for Tart VM %q (mode=%s, timeout=%s)", vmName, cfg.check.Mode, cfg.check.Timeout)
	}
	report := runGuestNetworkCheck(vmName, cfg.check, probe, guestNetworkClock{})
	if report.Status == GuestNetworkFailed {
		return &GuestNetworkError{Report: report}
	}
	log.Print(report.String())
	return nil
}

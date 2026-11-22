package deploy

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	tartMacOSDNSServersEnvVar           = "TART_MACOS_DNS_SERVERS"
	tartMacOSNetworkCheckEnvVar         = "TART_MACOS_NETWORK_CHECK"
	tartMacOSNetworkCheckURLsEnvVar     = "TART_MACOS_NETWORK_CHECK_URLS"
	tartMacOSNetworkCheckDNSNamesEnvVar = "TART_MACOS_NETWORK_CHECK_DNS_NAMES"
	tartMacOSNetworkCheckTimeoutEnvVar  = "TART_MACOS_NETWORK_CHECK_TIMEOUT"

	tartDNSServersDHCPValue           = "dhcp"
	tartGuestDNSMarkerDir             = "/var/db/sailwright"
	tartGuestDNSMarkerPath            = tartGuestDNSMarkerDir + "/tart-dns"
	tartGuestNetworkDocsHint          = "see docs/tart-guest-network.md"
	defaultTartNetworkCheckURL        = "https://github.com/"
	defaultTartNetworkCheckTimeout    = 90 * time.Second
	maxTartNetworkCheckTimeout        = 30 * time.Minute
	defaultTartNetworkRetryInterval   = 5 * time.Second
	defaultTartNetworkProbeTimeout    = 15 * time.Second
	tartGuestExecGracePeriod          = 15 * time.Second
	tartGuestAccessToken              = "sailwright-guest-ok"
	tartGuestDNSSudoFailureExitCode   = 10
	tartGuestDNSMarkerFailureExitCode = 11
)

// TartMacOSNetworkEnvVars lists the variables that configure Tart guest DNS
// and the post-start network readiness check. Gateway jobs only receive them
// through env_passthrough.
var TartMacOSNetworkEnvVars = []string{
	tartMacOSDNSServersEnvVar,
	tartMacOSNetworkCheckEnvVar,
	tartMacOSNetworkCheckURLsEnvVar,
	tartMacOSNetworkCheckDNSNamesEnvVar,
	tartMacOSNetworkCheckTimeoutEnvVar,
}

var dnsNamePattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*\.?$`)

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
}

type tartGuestNetworkConfig struct {
	dns   tartDNSSetting
	check guestNetworkCheckConfig
}

func loadTartGuestNetworkConfig(getenv func(string) string) (tartGuestNetworkConfig, error) {
	var cfg tartGuestNetworkConfig

	dns, err := parseTartDNSSetting(getenv(tartMacOSDNSServersEnvVar))
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
		if !slices.Contains(hostsFromURLs, parsed.Hostname()) {
			hostsFromURLs = append(hostsFromURLs, parsed.Hostname())
		}
	}

	names := splitConfigList(getenv(tartMacOSNetworkCheckDNSNamesEnvVar))
	if len(names) == 0 {
		names = hostsFromURLs
	}
	for _, name := range names {
		if !dnsNamePattern.MatchString(name) || net.ParseIP(name) != nil {
			return cfg, fmt.Errorf("%s entries must be DNS host names: %q", tartMacOSNetworkCheckDNSNamesEnvVar, name)
		}
	}

	timeout := defaultTartNetworkCheckTimeout
	if raw := strings.TrimSpace(getenv(tartMacOSNetworkCheckTimeoutEnvVar)); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 || parsed > maxTartNetworkCheckTimeout {
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

func parseTartDNSSetting(raw string) (tartDNSSetting, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return tartDNSSetting{mode: tartDNSUnmanaged}, nil
	}
	if strings.EqualFold(trimmed, tartDNSServersDHCPValue) {
		return tartDNSSetting{mode: tartDNSDHCP}, nil
	}
	servers := splitConfigList(trimmed)
	for _, server := range servers {
		if net.ParseIP(server) == nil {
			return tartDNSSetting{}, fmt.Errorf("%s must be %q or a comma-separated list of IP addresses: %q is not an IP address", tartMacOSDNSServersEnvVar, tartDNSServersDHCPValue, server)
		}
	}
	return tartDNSSetting{mode: tartDNSServers, servers: servers}, nil
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
// Manual DNS servers that Sailwright did not set are never overwritten.
func planTartGuestDNS(setting tartDNSSetting, state tartGuestDNSState) (tartDNSAction, string) {
	service := strconv.Quote(state.service)
	switch setting.mode {
	case tartDNSServers:
		switch {
		case slices.Equal(state.current, setting.servers):
			if state.markerPresent && !state.managedBySailwright() {
				// Same servers, stale marker; rewrite marker for later reverts.
				return tartDNSActionSet, fmt.Sprintf("set DNS servers %s on guest service %s", strings.Join(setting.servers, " "), service)
			}
			return tartDNSActionNone, fmt.Sprintf("guest service %s already uses DNS servers %s", service, strings.Join(setting.servers, " "))
		case len(state.current) == 0, state.managedBySailwright():
			return tartDNSActionSet, fmt.Sprintf("set DNS servers %s on guest service %s (managed by sailwright; %s=%s reverts to DHCP)", strings.Join(setting.servers, " "), service, tartMacOSDNSServersEnvVar, tartDNSServersDHCPValue)
		default:
			return tartDNSActionKeepUser, fmt.Sprintf("kept user-managed DNS servers %s on guest service %s; %s was not applied", strings.Join(state.current, " "), service, tartMacOSDNSServersEnvVar)
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
			return tartDNSActionKeepUser, fmt.Sprintf("kept user-managed DNS servers %s on guest service %s; reset them manually with `sudo networksetup -setdnsservers %s Empty`", strings.Join(state.current, " "), service, shellQuoteForDisplay(state.service))
		}
	default:
		return tartDNSActionNone, ""
	}
}

var plainShellWordPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

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
	run        func(string, time.Duration, string, []string) (string, error)
}

func (p *tartGuestProbe) exec(script string, timeout time.Duration) (string, error) {
	return p.run(p.projectDir, timeout+tartGuestExecGracePeriod, "tart", []string{"exec", p.vmName, "/bin/sh", "-c", script})
}

func (p *tartGuestProbe) Access(timeout time.Duration) error {
	output, err := p.exec("echo "+tartGuestAccessToken, timeout)
	if err == nil && strings.Contains(output, tartGuestAccessToken) {
		return nil
	}
	detail := strings.TrimSpace(lastLines(output, 3))
	lower := strings.ToLower(detail)
	if strings.Contains(lower, "unexpected argument") || strings.Contains(lower, "unknown subcommand") {
		return &guestProbeError{
			err:       fmt.Errorf("this Tart version does not support `tart exec`: %s", detail),
			hint:      "upgrade Tart, or set TART_MACOS_NETWORK_CHECK=off to skip the check (network readiness is then not verified)",
			permanent: true,
		}
	}
	if err == nil {
		err = errors.New("unexpected output")
	}
	return &guestProbeError{
		err:  fmt.Errorf("`tart exec` could not run a command in the guest: %v; output: %s", err, detail),
		hint: "the Tart guest agent is not reachable yet or not installed in this image; use an image with tart-guest-agent or set TART_MACOS_NETWORK_CHECK=off (network readiness is then not verified)",
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
			if net.ParseIP(strings.TrimSpace(value)) != nil {
				state.current = append(state.current, strings.TrimSpace(value))
			}
		case "marker":
			state.markerPresent = true
		case "marker_service":
			state.markerService = value
		case "marker_servers":
			state.markerServers = strings.Fields(value)
		}
	}
	if state.service == "" {
		return state, errors.New("could not determine the guest's primary network service")
	}
	return state, nil
}

func (p *tartGuestProbe) Prepare(timeout time.Duration) (string, error) {
	output, err := p.exec(tartGuestDNSInspectScript(), timeout)
	if err != nil {
		return "", fmt.Errorf("failed to inspect guest DNS configuration: %v; output: %s", err, lastLines(output, 3))
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

	script := tartGuestDNSApplyScript(action, state.service, p.dns.servers)
	output, err = p.exec(script, timeout)
	if err != nil {
		hint := ""
		permanent := false
		if strings.Contains(output, "sudo-failed") {
			hint = "the guest agent user needs passwordless sudo to change DNS servers; set them manually with networksetup or unset " + tartMacOSDNSServersEnvVar
			permanent = true
		}
		return "", &guestProbeError{
			err:       fmt.Errorf("failed to apply guest DNS configuration: %v; output: %s", err, lastLines(output, 3)),
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
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "after="); ok && net.ParseIP(value) != nil {
			after = append(after, value)
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

func tartGuestDNSApplyScript(action tartDNSAction, service string, servers []string) string {
	quotedService := bashSingleQuote(service)
	var b strings.Builder
	switch action {
	case tartDNSActionSet:
		quotedServers := make([]string, 0, len(servers))
		for _, server := range servers {
			quotedServers = append(quotedServers, bashSingleQuote(server))
		}
		fmt.Fprintf(&b, "sudo -n /usr/sbin/networksetup -setdnsservers %s %s || { echo sudo-failed; exit %d; }\n", quotedService, strings.Join(quotedServers, " "), tartGuestDNSSudoFailureExitCode)
		fmt.Fprintf(&b, "sudo -n /bin/mkdir -p %s || exit %d\n", tartGuestDNSMarkerDir, tartGuestDNSMarkerFailureExitCode)
		fmt.Fprintf(&b, "printf 'service=%%s\\nservers=%%s\\n' %s %s | sudo -n /usr/bin/tee %s >/dev/null || exit %d\n", quotedService, bashSingleQuote(strings.Join(servers, " ")), tartGuestDNSMarkerPath, tartGuestDNSMarkerFailureExitCode)
		fmt.Fprintf(&b, "sudo -n /bin/chmod 0644 %s || exit %d\n", tartGuestDNSMarkerPath, tartGuestDNSMarkerFailureExitCode)
	case tartDNSActionReset:
		fmt.Fprintf(&b, "sudo -n /usr/sbin/networksetup -setdnsservers %s Empty || { echo sudo-failed; exit %d; }\n", quotedService, tartGuestDNSSudoFailureExitCode)
		fmt.Fprintf(&b, "sudo -n /bin/rm -f %s || exit %d\n", tartGuestDNSMarkerPath, tartGuestDNSMarkerFailureExitCode)
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
	output, err := p.exec(tartGuestSystemDNSScript(name, timeout), timeout)
	if err != nil {
		return fmt.Errorf("system DNS probe for %s could not run: %v; output: %s", name, err, lastLines(output, 3))
	}
	switch {
	case strings.Contains(output, "dns_status=timeout"):
		return fmt.Errorf("system resolver did not answer for %s within %s", name, timeout.Round(time.Second))
	case strings.Contains(output, "dns_address="):
		return nil
	case strings.Contains(output, "dns_status=done"):
		return fmt.Errorf("system resolver returned no address for %s", name)
	default:
		return fmt.Errorf("system DNS probe for %s returned unexpected output: %s", name, lastLines(output, 3))
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
	output, err := p.exec(tartGuestHTTPSScript(target, timeout), timeout)
	if err != nil {
		return fmt.Errorf("HTTPS probe for %s could not run: %v; output: %s", target, err, lastLines(output, 3))
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
			curlMessage = line
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
	return `dev=$(/sbin/route -n get default 2>/dev/null | /usr/bin/awk '/interface:/{print $2}')
echo "device=$dev"
if [ -n "$dev" ]; then echo "router=$(/usr/sbin/ipconfig getoption "$dev" router 2>/dev/null)"; fi
for ns in $(/usr/sbin/scutil --dns | /usr/bin/awk '/^resolver #/{f=0} /^resolver #1$/{f=1} f && /nameserver\[[0-9]+\]/{print $3}' | /usr/bin/sort -u); do
  out=$(/usr/bin/dig @"$ns" +time=2 +tries=1 +short ` + bashSingleQuote(name) + ` A 2>&1)
  if printf '%s\n' "$out" | /usr/bin/grep -Eq '^[0-9]+(\.[0-9]+){3}$'; then st=answered
  elif printf '%s' "$out" | /usr/bin/grep -q 'timed out'; then st=timeout
  elif printf '%s' "$out" | /usr/bin/grep -Eq 'reset|refused|unreachable'; then st=refused
  else st=no-answer; fi
  echo "resolver=$ns $st"
done
`
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
	output, err := p.exec(tartGuestDNSDiagnosisScript(name), timeout)
	if err != nil {
		return nil, "could not collect guest resolver diagnostics: " + lastLines(output, 2)
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
	if setting.mode == tartDNSServers {
		return fmt.Sprintf("the resolvers from %s (%s) do not answer from inside the guest; choose resolvers the guest can reach (%s)", tartMacOSDNSServersEnvVar, strings.Join(failing, ", "), tartGuestNetworkDocsHint)
	}
	if routerFails {
		return fmt.Sprintf("the guest uses the DHCP-provided resolver %s, which is the Tart NAT (vmnet) DNS forwarder on the host, and it does not answer; set %s to a resolver reachable from the guest, or use TART_MACOS_BRIDGED_INTERFACE (%s)", router, tartMacOSDNSServersEnvVar, tartGuestNetworkDocsHint)
	}
	return fmt.Sprintf("none of the guest resolvers (%s) answer from inside the guest; set %s to a reachable resolver (%s)", strings.Join(failing, ", "), tartMacOSDNSServersEnvVar, tartGuestNetworkDocsHint)
}

func lastLines(output string, count int) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return strings.Join(lines, " | ")
}

var tartGuestNetworkCommandRunner = runCommandWithCombinedOutput

// EnsureTartGuestNetwork applies the configured guest DNS servers and checks
// guest access, system DNS and HTTPS from inside the running Tart VM. It
// returns a *GuestNetworkError when the guest is reachable by IP but not
// ready for networked provisioning.
func EnsureTartGuestNetwork(projectDir string, vmName string) error {
	cfg, err := loadTartGuestNetworkConfig(os.Getenv)
	if err != nil {
		return fmt.Errorf("invalid Tart guest network configuration: %w", err)
	}
	probe := &tartGuestProbe{projectDir: projectDir, vmName: vmName, dns: cfg.dns, run: tartGuestNetworkCommandRunner}
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

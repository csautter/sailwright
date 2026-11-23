package deploy

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// GuestNetworkCheckMode selects how much of the guest network is verified
// after a VM start.
type GuestNetworkCheckMode string

const (
	// GuestNetworkCheckFull verifies guest access, system DNS and HTTPS.
	GuestNetworkCheckFull GuestNetworkCheckMode = "full"
	// GuestNetworkCheckOffline verifies guest access only and reports that
	// internet access was not verified.
	GuestNetworkCheckOffline GuestNetworkCheckMode = "offline"
	// GuestNetworkCheckOff disables the readiness check.
	GuestNetworkCheckOff GuestNetworkCheckMode = "off"
)

// GuestNetworkStage names one step of the readiness check.
type GuestNetworkStage string

const (
	GuestNetworkStageAccess    GuestNetworkStage = "guest-access"
	GuestNetworkStageDNSConfig GuestNetworkStage = "dns-config"
	GuestNetworkStageSystemDNS GuestNetworkStage = "system-dns"
	GuestNetworkStageHTTPS     GuestNetworkStage = "https"
)

// GuestNetworkStatus is the overall result of a readiness check.
type GuestNetworkStatus string

const (
	GuestNetworkReady   GuestNetworkStatus = "ready"
	GuestNetworkOffline GuestNetworkStatus = "offline"
	GuestNetworkSkipped GuestNetworkStatus = "skipped"
	GuestNetworkFailed  GuestNetworkStatus = "failed"
)

// GuestNetworkReport describes a readiness check result without secrets.
type GuestNetworkReport struct {
	Guest  string
	Status GuestNetworkStatus
	// Stage is the failed stage; empty unless Status is failed.
	Stage GuestNetworkStage
	// Detail is the last probe error of the failed stage.
	Detail string
	// Hint is a concrete next step for the failed stage.
	Hint string
	// Resolvers lists the guest's system resolvers with a direct probe
	// result, collected when system DNS fails.
	Resolvers []string
	// DNSConfig summarizes what the DNS configuration step did.
	DNSConfig string
	// Verified lists the probes that passed.
	Verified []string
	Attempts int
	Elapsed  time.Duration
}

// String renders a single-line summary suitable for CLI and gateway logs.
func (r GuestNetworkReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "guest network readiness for %q: %s", r.Guest, r.Status)
	switch r.Status {
	case GuestNetworkFailed:
		fmt.Fprintf(&b, " at stage %s after %d attempt(s) in %s", r.Stage, r.Attempts, r.Elapsed.Round(time.Second))
		if r.Detail != "" {
			fmt.Fprintf(&b, ": %s", oneLine(r.Detail))
		}
		if len(r.Resolvers) > 0 {
			fmt.Fprintf(&b, "; guest resolvers: %s", strings.Join(r.Resolvers, ", "))
		}
	case GuestNetworkOffline:
		b.WriteString(" (guest reachable; DNS and HTTPS NOT verified: offline mode)")
	case GuestNetworkSkipped:
		b.WriteString(" (network readiness NOT verified: check disabled)")
	case GuestNetworkReady:
		if len(r.Verified) > 0 {
			fmt.Fprintf(&b, " (%s)", strings.Join(r.Verified, ", "))
		}
	}
	if r.DNSConfig != "" {
		fmt.Fprintf(&b, "; dns-config: %s", oneLine(r.DNSConfig))
	}
	if r.Status == GuestNetworkFailed && r.Hint != "" {
		fmt.Fprintf(&b, "; hint: %s", oneLine(r.Hint))
	}
	return b.String()
}

// GuestNetworkError is returned when a guest fails its readiness check.
type GuestNetworkError struct {
	Report GuestNetworkReport
}

func (e *GuestNetworkError) Error() string {
	return e.Report.String()
}

// guestNetworkCheckConfig holds the resolved check settings.
type guestNetworkCheckConfig struct {
	Mode          GuestNetworkCheckMode
	DNSNames      []string
	HTTPSURLs     []string
	Timeout       time.Duration
	RetryInterval time.Duration
	ProbeTimeout  time.Duration
}

// guestNetworkProbe runs the individual checks inside one guest.
type guestNetworkProbe interface {
	Access(timeout time.Duration) error
	// NeedsPrepare reports whether Prepare changes guest configuration.
	NeedsPrepare() bool
	// Prepare applies guest network configuration and returns a summary.
	Prepare(timeout time.Duration) (string, error)
	SystemDNS(name string, timeout time.Duration) error
	HTTPS(url string, timeout time.Duration) error
	// DiagnoseDNS probes each system resolver directly and returns a list
	// of "<resolver> (<result>)" entries plus a hint.
	// The timeout is the whole budget for the diagnosis.
	DiagnoseDNS(name string, timeout time.Duration) ([]string, string)
}

type guestNetworkClock struct {
	now   func() time.Time
	sleep func(time.Duration)
}

// guestProbeError carries a hint and whether retrying can help.
type guestProbeError struct {
	err       error
	hint      string
	permanent bool
}

func (e *guestProbeError) Error() string { return e.err.Error() }
func (e *guestProbeError) Unwrap() error { return e.err }

func probeErrorHint(err error) (string, bool) {
	var probeErr *guestProbeError
	if errors.As(err, &probeErr) {
		return probeErr.hint, probeErr.permanent
	}
	return "", false
}

// guestDNSDiagnosisBudget is reserved at the end of the check window for
// probing the guest resolvers after a DNS failure.
const guestDNSDiagnosisBudget = 15 * time.Second

// minGuestDNSDiagnosisTime is the least time worth spending on diagnosis.
const minGuestDNSDiagnosisTime = 3 * time.Second

// runGuestNetworkCheck verifies guest access, DNS configuration, system DNS
// and HTTPS with bounded retries. Retries stop early enough to leave a DNS
// diagnosis budget inside cfg.Timeout; a single probe is additionally capped
// at cfg.ProbeTimeout and the remaining retry window.
func runGuestNetworkCheck(guest string, cfg guestNetworkCheckConfig, probe guestNetworkProbe, clock guestNetworkClock) GuestNetworkReport {
	if clock.now == nil {
		clock.now = time.Now
	}
	if clock.sleep == nil {
		clock.sleep = time.Sleep
	}
	report := GuestNetworkReport{Guest: guest}
	if cfg.Mode == GuestNetworkCheckOff && !probe.NeedsPrepare() {
		report.Status = GuestNetworkSkipped
		return report
	}

	start := clock.now()
	deadline := start.Add(cfg.Timeout)
	diagnosisBudget := time.Duration(0)
	if cfg.Mode == GuestNetworkCheckFull && len(cfg.DNSNames) > 0 {
		diagnosisBudget = min(guestDNSDiagnosisBudget, cfg.Timeout/4)
	}
	retryDeadline := deadline.Add(-diagnosisBudget)
	probeTimeout := func() time.Duration {
		remaining := retryDeadline.Sub(clock.now())
		if remaining < time.Second {
			remaining = time.Second
		}
		if cfg.ProbeTimeout > 0 && cfg.ProbeTimeout < remaining {
			return cfg.ProbeTimeout
		}
		return remaining
	}

	prepared := false
	for attempt := 1; ; attempt++ {
		report.Attempts = attempt
		report.Verified = nil
		stage, target, err := runGuestNetworkAttempt(cfg, probe, probeTimeout, &prepared, &report)
		report.Elapsed = clock.now().Sub(start)
		if err == nil {
			switch cfg.Mode {
			case GuestNetworkCheckOff:
				report.Status = GuestNetworkSkipped
			case GuestNetworkCheckOffline:
				report.Status = GuestNetworkOffline
			default:
				report.Status = GuestNetworkReady
			}
			return report
		}

		hint, permanent := probeErrorHint(err)
		if permanent || !clock.now().Add(cfg.RetryInterval).Before(retryDeadline) {
			report.Status = GuestNetworkFailed
			report.Stage = stage
			report.Detail = err.Error()
			report.Hint = hint
			if stage == GuestNetworkStageSystemDNS {
				if remaining := deadline.Sub(clock.now()); remaining >= minGuestDNSDiagnosisTime {
					resolvers, dnsHint := probe.DiagnoseDNS(target, remaining)
					report.Resolvers = resolvers
					if dnsHint != "" {
						report.Hint = dnsHint
					}
				}
			}
			report.Elapsed = clock.now().Sub(start)
			return report
		}
		clock.sleep(cfg.RetryInterval)
	}
}

func runGuestNetworkAttempt(
	cfg guestNetworkCheckConfig,
	probe guestNetworkProbe,
	probeTimeout func() time.Duration,
	prepared *bool,
	report *GuestNetworkReport,
) (GuestNetworkStage, string, error) {
	if err := probe.Access(probeTimeout()); err != nil {
		return GuestNetworkStageAccess, "", err
	}
	report.Verified = append(report.Verified, string(GuestNetworkStageAccess)+" ok")

	if !*prepared && probe.NeedsPrepare() {
		summary, err := probe.Prepare(probeTimeout())
		if err != nil {
			return GuestNetworkStageDNSConfig, "", err
		}
		report.DNSConfig = summary
		*prepared = true
	}

	if cfg.Mode != GuestNetworkCheckFull {
		return "", "", nil
	}

	for _, name := range cfg.DNSNames {
		if err := probe.SystemDNS(name, probeTimeout()); err != nil {
			return GuestNetworkStageSystemDNS, name, err
		}
		report.Verified = append(report.Verified, fmt.Sprintf("%s %s ok", GuestNetworkStageSystemDNS, name))
	}
	for _, url := range cfg.HTTPSURLs {
		if err := probe.HTTPS(url, probeTimeout()); err != nil {
			return GuestNetworkStageHTTPS, url, err
		}
		report.Verified = append(report.Verified, fmt.Sprintf("%s %s ok", GuestNetworkStageHTTPS, url))
	}
	return "", "", nil
}

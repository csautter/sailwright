// Command win11iso is a CI helper for the "Refresh Windows 11 ISO" workflow.
// Every subcommand prints key=value lines suitable for $GITHUB_OUTPUT.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/csautter/dev-alchemy/pkg/build"
)

// Network calls, replaced in tests.
var (
	resolveWin11Link = build.ResolveWindows11Link
	probeWin11ISO    = build.ProbeWin11ISO
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	// cmd is a literal, not os.Args[1], so the log line cannot carry arbitrary input.
	var cmd string
	var run func([]string, io.Writer, time.Time) error
	switch os.Args[1] {
	case "check":
		cmd, run = "check", runCheck
	case "resolve":
		cmd, run = "resolve", runResolve
	case "fetch":
		cmd, run = "fetch", runFetch
	case "publish":
		cmd, run = "publish", runPublish
	default:
		usage()
		os.Exit(2)
	}
	if err := run(os.Args[2:], os.Stdout, time.Now()); err != nil {
		log.Fatalf("win11iso %s: %v", cmd, err)
	}
}

func usage() {
	_, _ = fmt.Fprintln(os.Stderr, "usage: win11iso <check|resolve|fetch|publish> [flags]")
}

func validArch(arch string) error {
	if arch != "amd64" && arch != "arm64" {
		return fmt.Errorf("--arch must be amd64 or arm64, got %q", arch)
	}
	return nil
}

func emit(w io.Writer, pairs ...string) {
	for i := 0; i+1 < len(pairs); i += 2 {
		_, _ = fmt.Fprintf(w, "%s=%s\n", pairs[i], pairs[i+1])
	}
}

func runCheck(args []string, out io.Writer, now time.Time) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	arch := fs.String("arch", "", "amd64 or arm64")
	manifestPath := fs.String("manifest", "", "local manifest file (may not exist)")
	force := fs.Bool("force", false, "treat the refresh as due")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validArch(*arch); err != nil {
		return err
	}
	m, err := build.LoadWin11Manifest(*manifestPath)
	if err != nil {
		return err
	}
	latest, err := build.FetchLatestWin11Release()
	if err != nil {
		log.Printf("Release notes lookup failed, relying on the age rule: %v", err)
		latest = ""
	}
	due, reason := build.Win11RefreshDue(m, now, latest, *force)
	emit(out, "due", fmt.Sprint(due), "reason", reason, "latest_release", latest)
	return nil
}

func runResolve(args []string, out io.Writer, now time.Time) error {
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	arch := fs.String("arch", "", "amd64 or arm64")
	manifestPath := fs.String("manifest", "", "local manifest file (may not exist)")
	announced := fs.String("announced-release", "", "latest_release printed by check; recorded when the ISO is unchanged")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validArch(*arch); err != nil {
		return err
	}
	m, err := build.LoadWin11Manifest(*manifestPath)
	if err != nil {
		return err
	}

	info, err := resolveWin11Link(*arch)
	if errors.Is(err, build.ErrMicrosoftRefused) {
		log.Printf("Microsoft refused the request: %v", err)
		emit(out, "outcome", "refused")
		return nil
	}
	if err != nil {
		return err
	}

	if m != nil && unchangedISO(m, info) {
		m.CheckedAt = now.UTC().Format(time.RFC3339)
		if *announced != "" {
			m.AnnouncedRelease = *announced
		}
		if err := m.Save(*manifestPath); err != nil {
			return err
		}
		emit(out, "outcome", "unchanged", "source_name", info.SourceName, "object", m.Object,
			"url", info.URL, "sha256", m.SHA256)
		return nil
	}

	emit(out, "outcome", "new",
		"url", info.URL,
		"source_name", info.SourceName,
		"sha256", info.SHA256,
		"object", build.Win11ObjectName(info.SourceName, *arch, now))
	return nil
}

// unchangedISO reports whether the resolved link serves the ISO the manifest already points to.
// Microsoft's published sha256 is decisive when available; otherwise the file name and size must match.
// When the size cannot be probed, the ISO is downloaded and writeManifest dedupes it by sha256.
func unchangedISO(m *build.Win11Manifest, info *build.Win11LinkInfo) bool {
	if info.SHA256 != "" {
		return strings.EqualFold(info.SHA256, m.SHA256)
	}
	if m.SourceName != info.SourceName {
		return false
	}
	size, err := probeWin11ISO(info.URL)
	if err != nil {
		log.Printf("Size probe failed; downloading to compare sha256: %v", err)
		return false
	}
	return size == m.Size
}

func runFetch(args []string, out io.Writer, now time.Time) error {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	arch := fs.String("arch", "", "amd64 or arm64")
	url := fs.String("url", "", "ISO download URL")
	outDir := fs.String("out", "", "directory to download into")
	manifestPath := fs.String("manifest", "", "local manifest file (updated)")
	sourceName := fs.String("source-name", "", "Microsoft's file name, e.g. Win11_25H2_English_x64.iso")
	sha := fs.String("sha256", "", "expected sha256 (optional)")
	announced := fs.String("announced-release", "", "latest_release printed by check")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validArch(*arch); err != nil {
		return err
	}
	if *url == "" || *outDir == "" || *manifestPath == "" || *sourceName == "" {
		return errors.New("--url, --out, --manifest and --source-name are required")
	}
	object := build.Win11ObjectName(*sourceName, *arch, now)
	local := filepath.Join(*outDir, object)
	if err := build.DownloadWin11ISO(*url, local, strings.ToLower(*sha)); err != nil {
		return err
	}
	return writeManifest(out, *arch, *manifestPath, *sourceName, object, local, *announced, now)
}

func runPublish(args []string, out io.Writer, now time.Time) error {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	arch := fs.String("arch", "", "amd64 or arm64")
	iso := fs.String("iso", "", "ISO file downloaded manually")
	sourceName := fs.String("source-name", "", "Microsoft's file name, e.g. Win11_25H2_English_x64.iso")
	manifestPath := fs.String("manifest", "", "local manifest file (updated)")
	markDue := fs.Bool("mark-due", false, "backdate checked_at so the next scheduled run checks Microsoft")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validArch(*arch); err != nil {
		return err
	}
	if *iso == "" || *sourceName == "" || *manifestPath == "" {
		return errors.New("--iso, --source-name and --manifest are required")
	}
	object := build.Win11ObjectName(*sourceName, *arch, now)
	if err := writeManifest(out, *arch, *manifestPath, *sourceName, object, *iso, "", now); err != nil {
		return err
	}
	if *markDue {
		m, err := build.LoadWin11Manifest(*manifestPath)
		if err != nil {
			return err
		}
		m.MarkRefreshDue(now)
		return m.Save(*manifestPath)
	}
	return nil
}

// writeManifest hashes the ISO, promotes it in the manifest and prints the outputs:
//   - object:     the object the manifest now points to
//   - existing:   true when that object is already on the remote (listed in the manifest history
//     with the same sha256), so it must not be uploaded again
//   - unchanged:  true when the ISO is byte-identical to the current object; only checked_at changed
//   - pruned:     objects that fell out of the retention window
//
// A history entry with the same object name but different bytes (same-day re-publish of changed
// media) is an error: the immutable object must never be replaced.
func writeManifest(out io.Writer, arch, manifestPath, sourceName, object, local, announced string, now time.Time) error {
	sum, size, err := build.HashFile(local)
	if err != nil {
		return err
	}
	m, err := build.LoadWin11Manifest(manifestPath)
	if err != nil {
		return err
	}
	if m == nil {
		m = &build.Win11Manifest{}
	}
	m.Arch = arch
	if announced != "" {
		m.AnnouncedRelease = announced
	}

	entry := build.Win11HistoryEntry{Object: object, SHA256: sum, PublishedAt: now.UTC().Format(time.RFC3339)}
	existing := false
	for _, h := range m.History {
		if h.SHA256 == sum {
			// Same bytes as an already published object: reuse it instead of storing a duplicate.
			entry, existing = h, true
			break
		}
		if h.Object == object {
			return fmt.Errorf("object %s is already published with sha256 %s, but %s has sha256 %s; retry tomorrow or publish under a different date", object, h.SHA256, local, sum)
		}
	}

	unchanged := existing && entry.Object == m.Object
	var pruned []string
	if unchanged {
		// Keep the name Microsoft serves these bytes under, so the next resolve compares like with like.
		m.SourceName = sourceName
		m.Release = build.ParseWin11Release(sourceName)
		m.CheckedAt = now.UTC().Format(time.RFC3339)
	} else {
		pruned = m.Promote(entry, sourceName, size, now)
	}
	if err := m.Save(manifestPath); err != nil {
		return err
	}
	emit(out,
		"object", entry.Object,
		"local_path", local,
		"existing", fmt.Sprint(existing),
		"unchanged", fmt.Sprint(unchanged),
		"pruned", strings.Join(pruned, " "))
	return nil
}

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

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "check":
		err = runCheck(os.Args[2:], os.Stdout, time.Now())
	case "resolve":
		err = runResolve(os.Args[2:], os.Stdout, time.Now())
	case "fetch":
		err = runFetch(os.Args[2:], os.Stdout, time.Now())
	case "publish":
		err = runPublish(os.Args[2:], os.Stdout, time.Now())
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		log.Fatalf("win11iso %s: %v", os.Args[1], err)
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
	emit(out, "due", fmt.Sprint(due), "reason", reason)
	return nil
}

func runResolve(args []string, out io.Writer, now time.Time) error {
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	arch := fs.String("arch", "", "amd64 or arm64")
	manifestPath := fs.String("manifest", "", "local manifest file (may not exist)")
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

	info, err := build.ResolveWindows11Link(*arch)
	if errors.Is(err, build.ErrMicrosoftRefused) {
		log.Printf("Microsoft refused the request: %v", err)
		emit(out, "outcome", "refused")
		return nil
	}
	if err != nil {
		return err
	}

	if m != nil && m.SourceName == info.SourceName {
		size, headErr := build.ProbeWin11ISO(info.URL)
		if headErr != nil {
			log.Printf("HEAD request failed, treating the ISO as new: %v", headErr)
		} else if size == m.Size {
			m.CheckedAt = now.UTC().Format(time.RFC3339)
			if err := m.Save(*manifestPath); err != nil {
				return err
			}
			emit(out, "outcome", "unchanged", "source_name", info.SourceName)
			return nil
		}
	}

	emit(out, "outcome", "new",
		"url", info.URL,
		"source_name", info.SourceName,
		"sha256", info.SHA256,
		"object", build.Win11ObjectName(info.SourceName, now))
	return nil
}

func runFetch(args []string, out io.Writer, now time.Time) error {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	arch := fs.String("arch", "", "amd64 or arm64")
	url := fs.String("url", "", "ISO download URL")
	outDir := fs.String("out", "", "directory to download into")
	manifestPath := fs.String("manifest", "", "local manifest file (updated)")
	sourceName := fs.String("source-name", "", "Microsoft's file name, e.g. Win11_25H2_English_x64.iso")
	sha := fs.String("sha256", "", "expected sha256 (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validArch(*arch); err != nil {
		return err
	}
	if *url == "" || *outDir == "" || *manifestPath == "" || *sourceName == "" {
		return errors.New("--url, --out, --manifest and --source-name are required")
	}
	object := build.Win11ObjectName(*sourceName, now)
	local := filepath.Join(*outDir, object)
	if err := build.DownloadWin11ISO(*url, local, strings.ToLower(*sha)); err != nil {
		return err
	}
	return writeManifest(out, *arch, *manifestPath, *sourceName, object, local, now)
}

func runPublish(args []string, out io.Writer, now time.Time) error {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	arch := fs.String("arch", "", "amd64 or arm64")
	iso := fs.String("iso", "", "ISO file downloaded manually")
	sourceName := fs.String("source-name", "", "Microsoft's file name, e.g. Win11_25H2_English_x64.iso")
	manifestPath := fs.String("manifest", "", "local manifest file (updated)")
	epoch := fs.Bool("epoch-checked-at", false, "set checked_at to the Unix epoch so the next scheduled run checks Microsoft")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validArch(*arch); err != nil {
		return err
	}
	if *iso == "" || *sourceName == "" || *manifestPath == "" {
		return errors.New("--iso, --source-name and --manifest are required")
	}
	object := build.Win11ObjectName(*sourceName, now)
	if err := writeManifest(out, *arch, *manifestPath, *sourceName, object, *iso, now); err != nil {
		return err
	}
	if *epoch {
		m, err := build.LoadWin11Manifest(*manifestPath)
		if err != nil {
			return err
		}
		m.CheckedAt = time.Unix(0, 0).UTC().Format(time.RFC3339)
		return m.Save(*manifestPath)
	}
	return nil
}

// writeManifest hashes the ISO, promotes it in the manifest and prints the outputs.
func writeManifest(out io.Writer, arch, manifestPath, sourceName, object, local string, now time.Time) error {
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
	pruned := m.Promote(build.Win11HistoryEntry{
		Object:      object,
		SHA256:      sum,
		PublishedAt: now.UTC().Format(time.RFC3339),
	}, sourceName, size, now)
	if err := m.Save(manifestPath); err != nil {
		return err
	}
	emit(out, "object", object, "local_path", local, "pruned", strings.Join(pruned, " "))
	return nil
}

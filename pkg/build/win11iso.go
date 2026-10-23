package build

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vbauerster/mpb/v8"
)

const (
	win11ManifestSchema = 1
	// win11HistoryLimit is the number of ISO objects kept on the remote: the current one plus two previous ones.
	win11HistoryLimit = 3
	// win11RefreshMaxAge is the age of the last check after which Microsoft is contacted again,
	// even when the release notes announce nothing new (catches media refreshes such as "v2").
	// The 90-day staleness limit lives in the staleness-guard job of refresh-windows-iso.yml.
	win11RefreshMaxAge = 28 * 24 * time.Hour

	win11ReleaseNotesURL = "https://learn.microsoft.com/en-us/windows/release-health/windows11-release-information"

	// win11RefusedExitCode is the exit code of playwright_win11_iso.py when Microsoft refuses the request.
	win11RefusedExitCode = 3
	// Win11OfflineEnv disables every attempt to reach Microsoft (CI build jobs).
	Win11OfflineEnv = "SAILWRIGHT_WIN11_ISO_OFFLINE"
)

// ErrMicrosoftRefused signals that Microsoft rejected or challenged the request (e.g. datacenter IP).
// It is a neutral outcome, not a code defect.
var ErrMicrosoftRefused = errors.New("microsoft refused the Windows 11 download request")

var (
	win11SourceNamePattern = regexp.MustCompile(`^Win11_\d{2}H[12]_[A-Za-z]+_(x64|Arm64)(v\d+)?\.iso$`)
	win11ReleasePattern    = regexp.MustCompile(`^Win11_(\d{2}H[12])_`)
	win11NotesPattern      = regexp.MustCompile(`Version\s+(\d{2})H([12])`)
	// win11ObjectPattern guards object names before they reach FTP commands (DELE) or local paths.
	win11ObjectPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.iso$`)
	win11SHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Win11HistoryEntry describes one published ISO object.
type Win11HistoryEntry struct {
	Object      string `json:"object"`
	SHA256      string `json:"sha256"`
	PublishedAt string `json:"published_at"`
}

// Win11Manifest is the pointer file (win11-<arch>.json) to the current ISO object on the remote cache.
type Win11Manifest struct {
	Schema      int    `json:"schema"`
	Arch        string `json:"arch"`
	Object      string `json:"object"`
	SourceName  string `json:"source_name"`
	Release     string `json:"release"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	Build       string `json:"build"`
	PublishedAt string `json:"published_at"`
	CheckedAt   string `json:"checked_at"`
	// AnnouncedRelease is the newest release the release notes announced at the last successful check.
	// The release-notes trigger fires only once per announcement, see Win11RefreshDue.
	AnnouncedRelease string              `json:"announced_release,omitempty"`
	History          []Win11HistoryEntry `json:"history"`
}

// Win11ObjectName builds the immutable remote object name from Microsoft's file name and the first-seen date.
// Unexpected source names use the requested architecture (amd64 or arm64) in the fallback.
func Win11ObjectName(sourceName, arch string, firstSeen time.Time) string {
	date := firstSeen.UTC().Format("20060102")
	if win11SourceNamePattern.MatchString(sourceName) {
		base := strings.TrimSuffix(strings.ToLower(sourceName), ".iso")
		return base + "_" + date + ".iso"
	}
	log.Printf("Warning: unexpected Windows 11 ISO name %q; using fallback object name", sourceName)
	return "win11_unknown_english_" + arch + "_" + date + ".iso"
}

// ParseWin11Release extracts the release (e.g. "25H2") from Microsoft's ISO file name; "" if unknown.
func ParseWin11Release(sourceName string) string {
	m := win11ReleasePattern.FindStringSubmatch(sourceName)
	if m == nil {
		return ""
	}
	return m[1]
}

// Win11SourceNameFromURL returns Microsoft's file name (the URL path basename, ignoring the query string).
func Win11SourceNameFromURL(rawURL string) string {
	trimmed := rawURL
	if i := strings.IndexAny(trimmed, "?#"); i >= 0 {
		trimmed = trimmed[:i]
	}
	return path.Base(trimmed)
}

// LoadWin11Manifest reads a manifest file. A missing file yields (nil, nil).
func LoadWin11Manifest(file string) (*Win11Manifest, error) {
	// #nosec G304 -- the manifest path is supplied by the CI helper operator.
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m Win11Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", file, err)
	}
	if m.Schema != win11ManifestSchema {
		return nil, fmt.Errorf("unsupported manifest schema %d in %s", m.Schema, file)
	}
	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("invalid manifest %s: %w", file, err)
	}
	return &m, nil
}

// validate rejects manifests whose names could escape the ISO directory or whose digests are malformed.
func (m *Win11Manifest) validate() error {
	if m.Arch != "amd64" && m.Arch != "arm64" {
		return fmt.Errorf("unsupported arch %q", m.Arch)
	}
	entries := append([]Win11HistoryEntry{{Object: m.Object, SHA256: m.SHA256}}, m.History...)
	for _, e := range entries {
		if !win11ObjectPattern.MatchString(e.Object) {
			return fmt.Errorf("invalid object name %q", e.Object)
		}
		if !win11SHA256Pattern.MatchString(e.SHA256) {
			return fmt.Errorf("invalid sha256 %q for %s", e.SHA256, e.Object)
		}
	}
	return nil
}

// Save writes the manifest as indented JSON.
func (m *Win11Manifest) Save(file string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		return err
	}
	return os.WriteFile(file, append(data, '\n'), 0o600)
}

// CheckedTime parses CheckedAt; the zero time is returned when unset or invalid.
func (m *Win11Manifest) CheckedTime() time.Time {
	t, err := time.Parse(time.RFC3339, m.CheckedAt)
	if err != nil {
		return time.Time{}
	}
	return t
}

// MarkRefreshDue backdates CheckedAt so the next scheduled run contacts Microsoft. Unlike the
// epoch, the result stays well inside the staleness limit, so a refused first run does not fail.
func (m *Win11Manifest) MarkRefreshDue(now time.Time) {
	m.CheckedAt = now.Add(-win11RefreshMaxAge).UTC().Format(time.RFC3339)
}

// Promote makes entry the current ISO and returns the objects that fell out of the retention window.
// The object the manifest points to is never part of the returned list.
func (m *Win11Manifest) Promote(entry Win11HistoryEntry, sourceName string, size int64, now time.Time) []string {
	m.Schema = win11ManifestSchema
	m.Object = entry.Object
	m.SourceName = sourceName
	m.Release = ParseWin11Release(sourceName)
	m.SHA256 = entry.SHA256
	m.Size = size
	m.PublishedAt = entry.PublishedAt
	m.CheckedAt = now.UTC().Format(time.RFC3339)

	history := []Win11HistoryEntry{entry}
	for _, old := range m.History {
		if old.Object != entry.Object {
			history = append(history, old)
		}
	}
	var pruned []string
	if len(history) > win11HistoryLimit {
		for _, dropped := range history[win11HistoryLimit:] {
			if dropped.Object != entry.Object {
				pruned = append(pruned, dropped.Object)
			}
		}
		history = history[:win11HistoryLimit]
	}
	m.History = history
	return pruned
}

// compareWin11Release compares releases like "24H2" and "25H1"; returns -1, 0 or 1. Unparseable values sort lowest.
func compareWin11Release(a, b string) int {
	rank := func(r string) int {
		if len(r) != 4 || r[2] != 'H' {
			return -1
		}
		year, err1 := strconv.Atoi(r[:2])
		half, err2 := strconv.Atoi(r[3:])
		if err1 != nil || err2 != nil {
			return -1
		}
		return year*10 + half
	}
	ra, rb := rank(strings.ToUpper(a)), rank(strings.ToUpper(b))
	switch {
	case ra < rb:
		return -1
	case ra > rb:
		return 1
	}
	return 0
}

// Win11RefreshDue decides offline whether contacting Microsoft is worthwhile.
// latestRelease is the newest release announced by the release notes ("" if unknown).
// A newer announcement triggers one check only: announcements without general ISO media
// (e.g. device-specific releases) would otherwise contact Microsoft on every run. After that
// check, the age rule picks up media that Microsoft publishes later.
func Win11RefreshDue(m *Win11Manifest, now time.Time, latestRelease string, force bool) (bool, string) {
	if m == nil {
		return true, "no manifest present"
	}
	if force {
		return true, "forced by workflow input"
	}
	if latestRelease != "" && compareWin11Release(latestRelease, m.Release) > 0 &&
		!strings.EqualFold(latestRelease, m.AnnouncedRelease) {
		return true, fmt.Sprintf("release notes announce %s, manifest has %q", latestRelease, m.Release)
	}
	checked := m.CheckedTime()
	if checked.IsZero() || now.Sub(checked) >= win11RefreshMaxAge {
		return true, fmt.Sprintf("last check %s is at least %d days old", m.CheckedAt, int(win11RefreshMaxAge.Hours()/24))
	}
	return false, fmt.Sprintf("checked %s ago, no new release announced since", now.Sub(checked).Truncate(time.Hour))
}

// latestWin11ReleaseFromHTML returns the highest "Version NNH[12]" mentioned in the release notes page.
func latestWin11ReleaseFromHTML(html string) string {
	latest := ""
	for _, m := range win11NotesPattern.FindAllStringSubmatch(html, -1) {
		candidate := m[1] + "H" + m[2]
		if latest == "" || compareWin11Release(candidate, latest) > 0 {
			latest = candidate
		}
	}
	return latest
}

// fetchLatestWin11Release reads the release notes page. Errors are not fatal for callers.
func fetchLatestWin11Release(client *http.Client, url string) (string, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if url == "" {
		url = win11ReleaseNotesURL
	}
	resp, err := client.Get(url) // #nosec G107 -- URL is the fixed release-notes page or a test server.
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("release notes returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return "", err
	}
	latest := latestWin11ReleaseFromHTML(string(body))
	if latest == "" {
		return "", errors.New("no Windows 11 version found in release notes")
	}
	return latest, nil
}

// FetchLatestWin11Release queries the public release notes for the newest Windows 11 release.
func FetchLatestWin11Release() (string, error) { return fetchLatestWin11Release(nil, "") }

// Win11ISOOffline reports whether Microsoft access is disabled (CI build jobs).
func Win11ISOOffline() bool {
	v := os.Getenv(Win11OfflineEnv)
	return v == "1" || strings.EqualFold(v, "true")
}

// windows11ISOPath returns the stable, release-neutral local ISO path.
func windows11ISOPath(arch string) string {
	return GetDirectoriesInstance().CachePath("windows11", "iso", "win11_english_"+arch+".iso")
}

// migrateLegacyWindows11ISO renames a legacy "win11_25h2_english_<arch>.iso" to windows11ISOPath
// when only the legacy file exists. It runs during dependency reconciliation, before the ISO is
// looked up. build/packer/windows/windows11-qemu.sh and scripts/macos/create-win11-autounattend-iso.sh
// do the same for direct script use.
func migrateLegacyWindows11ISO(arch string) error {
	current := windows11ISOPath(arch)
	legacy := GetDirectoriesInstance().CachePath("windows11", "iso", "win11_25h2_english_"+arch+".iso")
	if _, err := os.Stat(current); err == nil {
		return nil
	}
	if _, err := os.Stat(legacy); err != nil {
		return nil
	}
	if err := os.Rename(legacy, current); err != nil {
		return fmt.Errorf("rename legacy Windows 11 ISO %s to %s: %w", legacy, current, err)
	}
	log.Printf("Renamed legacy Windows 11 ISO %s to %s", legacy, current)
	return nil
}

// Win11LinkInfo is written by playwright_win11_iso.py next to the URL file.
type Win11LinkInfo struct {
	URL        string `json:"url"`
	SourceName string `json:"source_name"`
	SHA256     string `json:"sha256"`
}

// ReadWin11LinkInfo reads the link description of the last resolution for an architecture.
func ReadWin11LinkInfo(arch string) (*Win11LinkInfo, error) {
	if arch != "amd64" && arch != "arm64" {
		return nil, fmt.Errorf("unsupported arch %q", arch)
	}
	// #nosec G304 -- arch is allowlisted above.
	data, err := os.ReadFile(GetDirectoriesInstance().CachePath("windows", "win11_"+arch+"_iso_link.json"))
	if err != nil {
		return nil, err
	}
	var info Win11LinkInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// ResolveWindows11Link asks Microsoft (via Playwright) for the current ISO link. It returns
// ErrMicrosoftRefused when Microsoft blocks the request.
func ResolveWindows11Link(arch string) (*Win11LinkInfo, error) {
	return resolveWindows11Link(arch, getWindows11Download)
}

func resolveWindows11Link(arch string, resolve func(string, string, bool) (string, error)) (*Win11LinkInfo, error) {
	if arch != "amd64" && arch != "arm64" {
		return nil, fmt.Errorf("unsupported arch %q", arch)
	}
	tmp, err := os.MkdirTemp("", "win11iso-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	// The path must not exist, otherwise getWindows11Download short-circuits.
	link, err := resolve(arch, filepath.Join(tmp, "missing.iso"), false)
	if err != nil {
		return nil, err
	}
	link = strings.TrimSpace(link)
	info, infoErr := ReadWin11LinkInfo(arch)
	if infoErr != nil || strings.TrimSpace(info.URL) != link {
		info = &Win11LinkInfo{}
	}
	info.URL = link
	if info.SourceName == "" {
		info.SourceName = Win11SourceNameFromURL(link)
	}
	return info, nil
}

// ProbeWin11ISO returns the size of the ISO behind url without downloading it. It tries HEAD
// first and falls back to a one-byte ranged GET, because signed CDN URLs sometimes reject HEAD
// or omit Content-Length.
func ProbeWin11ISO(url string) (int64, error) {
	return probeWin11ISO(&http.Client{Timeout: time.Minute}, url)
}

func probeWin11ISO(client *http.Client, url string) (int64, error) {
	size, headErr := probeWin11ISOHead(client, url)
	if headErr == nil {
		return size, nil
	}
	size, getErr := probeWin11ISORange(client, url)
	if getErr != nil {
		return 0, fmt.Errorf("HEAD: %v; ranged GET: %w", headErr, getErr)
	}
	return size, nil
}

func probeWin11ISOHead(client *http.Client, url string) (int64, error) {
	req, err := http.NewRequest(http.MethodHead, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req) // #nosec G107 -- URL comes from Microsoft's download page.
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength < 0 {
		return 0, errors.New("no Content-Length")
	}
	return resp.ContentLength, nil
}

func probeWin11ISORange(client *http.Client, url string) (int64, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", "bytes=0-0")
	resp, err := client.Do(req) // #nosec G107 -- URL comes from Microsoft's download page.
	if err != nil {
		return 0, err
	}
	// The body is closed unread: a server ignoring Range would otherwise stream the whole ISO.
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusPartialContent:
		// Content-Range: bytes 0-0/<total>
		cr := resp.Header.Get("Content-Range")
		i := strings.LastIndex(cr, "/")
		if i < 0 {
			return 0, fmt.Errorf("unexpected Content-Range %q", cr)
		}
		total, err := strconv.ParseInt(cr[i+1:], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("unexpected Content-Range %q", cr)
		}
		return total, nil
	case http.StatusOK:
		if resp.ContentLength < 0 {
			return 0, errors.New("no Content-Length")
		}
		return resp.ContentLength, nil
	}
	return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
}

// DownloadWin11ISO downloads the ISO to dst (go-getter, progress, checksum retry) and verifies sha256 when given.
func DownloadWin11ISO(url, dst, expectedSHA256 string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	dep := WebFileDependency{LocalPath: dst, Source: url}
	if expectedSHA256 != "" {
		dep.Checksum = "sha256:" + expectedSHA256
	}
	progress := mpb.New(mpb.WithOutput(os.Stderr))
	defer progress.Wait()
	return downloadWebFileDependency(progress, dep)
}

// HashFile returns the sha256 hex digest and size of a file.
func HashFile(file string) (string, int64, error) {
	// #nosec G304 -- caller-provided ISO path.
	f, err := os.Open(file)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// isWin11RefusedExit reports whether err is the playwright script's "Microsoft refused" exit code.
func isWin11RefusedExit(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == win11RefusedExitCode
}

func win11OfflineError(savePath string) error {
	return fmt.Errorf("missing Windows 11 ISO at %s; Microsoft download disabled in CI. Run the 'Refresh Windows 11 ISO' workflow or publish one manually (docs/windows-iso-cache.md)", savePath)
}

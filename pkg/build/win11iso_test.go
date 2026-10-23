package build

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWin11ObjectName(t *testing.T) {
	day := time.Date(2026, 10, 23, 5, 0, 0, 0, time.UTC)
	tests := []struct {
		source, arch, want string
	}{
		{"Win11_25H2_English_x64.iso", "amd64", "win11_25h2_english_x64_20261023.iso"},
		{"Win11_25H2_English_x64v2.iso", "amd64", "win11_25h2_english_x64v2_20261023.iso"},
		{"Win11_25H2_English_Arm64.iso", "arm64", "win11_25h2_english_arm64_20261023.iso"},
		{"Weird.iso", "amd64", "win11_unknown_english_amd64_20261023.iso"},
		{"Weird.iso", "arm64", "win11_unknown_english_arm64_20261023.iso"},
		{"Win11_26H1_English_ARM.iso", "arm64", "win11_unknown_english_arm64_20261023.iso"},
		{"Win11_26H1_English_x64_v2.iso", "amd64", "win11_unknown_english_amd64_20261023.iso"},
	}
	for _, tt := range tests {
		if got := Win11ObjectName(tt.source, tt.arch, day); got != tt.want {
			t.Errorf("Win11ObjectName(%q, %q) = %q, want %q", tt.source, tt.arch, got, tt.want)
		}
	}
}

func TestParseWin11ReleaseAndSourceName(t *testing.T) {
	if got := ParseWin11Release("Win11_25H2_English_x64v2.iso"); got != "25H2" {
		t.Errorf("release = %q", got)
	}
	if got := ParseWin11Release("nonsense.iso"); got != "" {
		t.Errorf("release = %q, want empty", got)
	}
	got := Win11SourceNameFromURL("https://software.download.prss.microsoft.com/dbazure/Win11_25H2_English_x64v2.iso?t=abc&P1=1")
	if got != "Win11_25H2_English_x64v2.iso" {
		t.Errorf("source name = %q", got)
	}
}

func TestWin11RefreshDue(t *testing.T) {
	now := time.Date(2027, 2, 11, 4, 0, 0, 0, time.UTC)
	fresh := &Win11Manifest{Release: "25H2", CheckedAt: now.Add(-24 * time.Hour).Format(time.RFC3339)}
	old := &Win11Manifest{Release: "25H2", CheckedAt: now.Add(-29 * 24 * time.Hour).Format(time.RFC3339)}
	announced := &Win11Manifest{Release: "25H2", AnnouncedRelease: "26H1", CheckedAt: fresh.CheckedAt}
	unknown := &Win11Manifest{CheckedAt: fresh.CheckedAt}
	unknownAnnounced := &Win11Manifest{AnnouncedRelease: "25H2", CheckedAt: fresh.CheckedAt}

	tests := []struct {
		name   string
		m      *Win11Manifest
		latest string
		force  bool
		due    bool
	}{
		{"no manifest", nil, "", false, true},
		{"force", fresh, "", true, true},
		{"newer release", fresh, "26H1", false, true},
		{"same release", fresh, "25H2", false, false},
		{"older release notes", fresh, "24H2", false, false},
		{"age", old, "25H2", false, true},
		{"age with failed lookup", old, "", false, true},
		{"skip", fresh, "", false, false},
		{"invalid checked_at", &Win11Manifest{Release: "25H2"}, "", false, true},
		{"announcement already checked", announced, "26H1", false, false},
		{"announcement already checked is case-insensitive", announced, "26h1", false, false},
		{"next announcement", announced, "26H2", false, true},
		{"announcement already checked but old", &Win11Manifest{Release: "25H2", AnnouncedRelease: "26H1", CheckedAt: old.CheckedAt}, "26H1", false, true},
		{"unknown release, new announcement", unknown, "25H2", false, true},
		{"unknown release, announcement already checked", unknownAnnounced, "25H2", false, false},
		{"exactly 28 days", &Win11Manifest{Release: "25H2", CheckedAt: now.Add(-win11RefreshMaxAge).Format(time.RFC3339)}, "", false, true},
	}
	for _, tt := range tests {
		due, reason := Win11RefreshDue(tt.m, now, tt.latest, tt.force)
		if due != tt.due || reason == "" {
			t.Errorf("%s: due=%v reason=%q, want due=%v", tt.name, due, reason, tt.due)
		}
	}
}

func TestLatestWin11ReleaseFromFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "win11_release_notes.html"))
	if err != nil {
		t.Fatal(err)
	}
	if got := latestWin11ReleaseFromHTML(string(data)); got != "25H2" {
		t.Errorf("latest = %q, want 25H2", got)
	}
	if got := latestWin11ReleaseFromHTML("<html>nothing</html>"); got != "" {
		t.Errorf("latest = %q, want empty", got)
	}
}

func TestFetchLatestWin11Release(t *testing.T) {
	data, _ := os.ReadFile(filepath.Join("testdata", "win11_release_notes.html"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			http.Error(w, "no", http.StatusForbidden)
			return
		}
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	got, err := fetchLatestWin11Release(srv.Client(), srv.URL)
	if err != nil || got != "25H2" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := fetchLatestWin11Release(srv.Client(), srv.URL+"/bad"); err == nil {
		t.Fatal("expected error for HTTP 403")
	}
}

func TestWin11ManifestPromoteTruncatesHistory(t *testing.T) {
	now := time.Date(2027, 1, 14, 4, 0, 0, 0, time.UTC)
	m := &Win11Manifest{}
	var pruned []string
	for i, obj := range []string{"a.iso", "b.iso", "c.iso", "d.iso"} {
		pruned = m.Promote(Win11HistoryEntry{Object: obj, SHA256: "s" + obj, PublishedAt: now.Format(time.RFC3339)}, "Win11_25H2_English_x64.iso", int64(i), now)
	}
	if m.Object != "d.iso" || len(m.History) != 3 {
		t.Fatalf("object=%q history=%v", m.Object, m.History)
	}
	if m.History[0].Object != "d.iso" || m.History[2].Object != "b.iso" {
		t.Errorf("unexpected history order: %v", m.History)
	}
	if len(pruned) != 1 || pruned[0] != "a.iso" {
		t.Errorf("pruned = %v, want [a.iso]", pruned)
	}
	if m.Release != "25H2" {
		t.Errorf("release = %q", m.Release)
	}

	// Re-promoting the current object neither duplicates nor prunes it.
	pruned = m.Promote(m.History[0], "Win11_25H2_English_x64.iso", 3, now)
	if len(pruned) != 0 || len(m.History) != 3 || m.History[0].Object != "d.iso" || m.History[1].Object != "c.iso" {
		t.Errorf("re-promote: pruned=%v history=%v", pruned, m.History)
	}
	// Promoting an older history entry moves it to the front without pruning.
	pruned = m.Promote(m.History[2], "Win11_25H2_English_x64.iso", 1, now)
	if len(pruned) != 0 || m.Object != "b.iso" || m.History[1].Object != "d.iso" || m.History[2].Object != "c.iso" {
		t.Errorf("promote older: pruned=%v history=%v", pruned, m.History)
	}
}

func TestWin11ManifestRoundTrip(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sub", "win11-amd64.json")
	if m, err := LoadWin11Manifest(file); m != nil || err != nil {
		t.Fatalf("missing manifest: %v, %v", m, err)
	}
	m := &Win11Manifest{Arch: "amd64", AnnouncedRelease: "26H1"}
	m.Promote(Win11HistoryEntry{Object: "x.iso", SHA256: testSHA256}, "Win11_25H2_English_x64.iso", 1, time.Now())
	if err := m.Save(file); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadWin11Manifest(file)
	if err != nil || loaded.Object != "x.iso" || loaded.Schema != 1 || loaded.AnnouncedRelease != "26H1" {
		t.Fatalf("loaded %+v, %v", loaded, err)
	}
}

const testSHA256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestLoadWin11ManifestRejectsInvalidContent(t *testing.T) {
	valid := `"arch":"amd64","object":"x.iso","sha256":"` + testSHA256 + `"`
	tests := map[string]string{
		"schema":    `{"schema":2,` + valid + `}`,
		"arch":      `{"schema":1,"arch":"x86","object":"x.iso","sha256":"` + testSHA256 + `"}`,
		"traversal": `{"schema":1,"arch":"amd64","object":"../x.iso","sha256":"` + testSHA256 + `"}`,
		"separator": `{"schema":1,"arch":"amd64","object":"a/x.iso","sha256":"` + testSHA256 + `"}`,
		"control character": `{"schema":1,"arch":"amd64","object":"x
DELE y.iso","sha256":"` + testSHA256 + `"}`,
		"short sha256":   `{"schema":1,"arch":"amd64","object":"x.iso","sha256":"abc"}`,
		"history object": `{"schema":1,` + valid + `,"history":[{"object":"/etc/x.iso","sha256":"` + testSHA256 + `"}]}`,
		"not json":       `{`,
	}
	for name, content := range tests {
		file := filepath.Join(t.TempDir(), "m.json")
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadWin11Manifest(file); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestMarkRefreshDue(t *testing.T) {
	now := time.Date(2027, 2, 11, 4, 0, 0, 0, time.UTC)
	m := &Win11Manifest{Release: "25H2"}
	m.MarkRefreshDue(now)
	if due, reason := Win11RefreshDue(m, now, "", false); !due {
		t.Errorf("expected due after MarkRefreshDue: %s", reason)
	}
	// Well inside the 90-day staleness limit of the refresh workflow.
	if age := now.Sub(m.CheckedTime()); age > 30*24*time.Hour {
		t.Errorf("checked_at is %s old", age)
	}
}

func TestProbeWin11ISO(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/head":
			w.Header().Set("Content-Length", "1234")
		case "/range":
			if r.Method == http.MethodHead {
				http.Error(w, "no", http.StatusForbidden)
				return
			}
			if r.Header.Get("Range") != "bytes=0-0" {
				t.Errorf("unexpected Range %q", r.Header.Get("Range"))
			}
			w.Header().Set("Content-Range", "bytes 0-0/5678")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("x"))
		default:
			http.Error(w, "no", http.StatusForbidden)
		}
	}))
	defer srv.Close()

	if size, err := probeWin11ISO(srv.Client(), srv.URL+"/head"); err != nil || size != 1234 {
		t.Errorf("HEAD: size=%d err=%v", size, err)
	}
	if size, err := probeWin11ISO(srv.Client(), srv.URL+"/range"); err != nil || size != 5678 {
		t.Errorf("ranged GET fallback: size=%d err=%v", size, err)
	}
	if _, err := probeWin11ISO(srv.Client(), srv.URL+"/forbidden"); err == nil {
		t.Error("expected an error when HEAD and GET are refused")
	}
}

func TestMigrateLegacyWindows11ISO(t *testing.T) {
	previous := directoriesInstance
	directoriesInstance = &Directories{CacheDir: t.TempDir()}
	t.Cleanup(func() { directoriesInstance = previous })
	isoDir := GetDirectoriesInstance().CachePath("windows11", "iso")
	if err := os.MkdirAll(isoDir, 0o750); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(isoDir, "win11_25h2_english_amd64.iso")
	current := windows11ISOPath("amd64")

	// Nothing present: nothing to do.
	if err := migrateLegacyWindows11ISO("amd64"); err != nil {
		t.Fatal(err)
	}

	// Only the legacy file: renamed.
	if err := os.WriteFile(legacy, []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacyWindows11ISO("amd64"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(current); err != nil || string(data) != "legacy" {
		t.Fatalf("current = %q, %v", data, err)
	}
	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("legacy file still present: %v", err)
	}

	// Both present: the current file wins and the legacy file is left alone.
	if err := os.WriteFile(legacy, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacyWindows11ISO("amd64"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(current); string(data) != "legacy" {
		t.Errorf("current was overwritten: %q", data)
	}

	// The path getter has no side effects.
	_ = os.Remove(current)
	if windows11ISOPath("amd64") != current {
		t.Error("unexpected path")
	}
	if _, err := os.Stat(current); !errors.Is(err, os.ErrNotExist) {
		t.Error("windows11ISOPath must not rename files")
	}
}

func TestWin11RefusedExitCodeMapsToSentinel(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 3")
	err := cmd.Run()
	if !isWin11RefusedExit(err) {
		t.Fatalf("exit 3 not recognised: %v", err)
	}
	if isWin11RefusedExit(exec.Command("sh", "-c", "exit 1").Run()) {
		t.Fatal("exit 1 must not be treated as a refusal")
	}
	wrapped := errors.Join(ErrMicrosoftRefused, err)
	if !errors.Is(wrapped, ErrMicrosoftRefused) {
		t.Fatal("sentinel not matchable")
	}
}

func TestGetWindows11DownloadOffline(t *testing.T) {
	t.Setenv(Win11OfflineEnv, "1")
	savePath := filepath.Join(t.TempDir(), "win11_english_amd64.iso")
	_, err := getWindows11Download("amd64", savePath, false)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"missing Windows 11 ISO at " + savePath, "Microsoft download disabled in CI", "docs/windows-iso-cache.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestResolveWindows11LinkMetadata(t *testing.T) {
	previous := directoriesInstance
	directoriesInstance = &Directories{CacheDir: t.TempDir()}
	t.Cleanup(func() { directoriesInstance = previous })
	link := "https://example.invalid/Win11_26H1_English_Arm64.iso?token=new"
	for _, tt := range []struct {
		name, data   string
		keepMetadata bool
	}{
		{"matching", `{"url":"` + link + `","source_name":"published.iso","sha256":"abc"}`, true},
		{"matching with whitespace", `{"url":" ` + link + ` ","source_name":"published.iso","sha256":"abc"}`, true},
		{"stale", `{"url":"https://example.invalid/old.iso","source_name":"old.iso","sha256":"abc"}`, false},
		{"missing URL", `{"source_name":"old.iso","sha256":"abc"}`, false},
		{"invalid JSON", `{`, false},
		{"missing file", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			info, err := resolveWindows11Link("arm64", func(arch, dst string, download bool) (string, error) {
				if arch != "arm64" || download {
					t.Fatal("unexpected resolver arguments")
				}
				if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("destination must not exist: %v", err)
				}
				file := GetDirectoriesInstance().CachePath("windows", "win11_arm64_iso_link.json")
				if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
					t.Fatal(err)
				}
				if tt.data == "" {
					if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(file, []byte(tt.data), 0o600); err != nil {
					t.Fatal(err)
				}
				return " " + link + "\n", nil
			})
			if err != nil {
				t.Fatal(err)
			}
			want := &Win11LinkInfo{URL: link, SourceName: "Win11_26H1_English_Arm64.iso"}
			if tt.keepMetadata {
				want.SourceName, want.SHA256 = "published.iso", "abc"
			}
			if *info != *want {
				t.Errorf("info = %+v, want %+v", info, want)
			}
		})
	}
}

func TestReadWin11LinkInfoRejectsUnsupportedArch(t *testing.T) {
	for _, arch := range []string{"", "x64", "../../other"} {
		if _, err := ReadWin11LinkInfo(arch); err == nil {
			t.Errorf("accepted unsupported arch %q", arch)
		}
	}
}

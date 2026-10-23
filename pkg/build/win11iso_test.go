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
	tests := map[string]string{
		"Win11_25H2_English_x64.iso":   "win11_25h2_english_x64_20261023.iso",
		"Win11_25H2_English_x64v2.iso": "win11_25h2_english_x64v2_20261023.iso",
		"Win11_25H2_English_Arm64.iso": "win11_25h2_english_arm64_20261023.iso",
		"Weird.iso":                    "win11_unknown_english_x64_20261023.iso",
		"Weird_Arm64.iso":              "win11_unknown_english_arm64_20261023.iso",
	}
	for in, want := range tests {
		if got := win11ObjectName(in, day); got != want {
			t.Errorf("win11ObjectName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseWin11ReleaseAndSourceName(t *testing.T) {
	if got := parseWin11Release("Win11_25H2_English_x64v2.iso"); got != "25H2" {
		t.Errorf("release = %q", got)
	}
	if got := parseWin11Release("nonsense.iso"); got != "" {
		t.Errorf("release = %q, want empty", got)
	}
	got := win11SourceNameFromURL("https://software.download.prss.microsoft.com/dbazure/Win11_25H2_English_x64v2.iso?t=abc&P1=1")
	if got != "Win11_25H2_English_x64v2.iso" {
		t.Errorf("source name = %q", got)
	}
	if win11ManifestKey("arm64") != "windows11/iso/win11-arm64.json" {
		t.Errorf("unexpected manifest key %q", win11ManifestKey("arm64"))
	}
}

func TestWin11RefreshDue(t *testing.T) {
	now := time.Date(2027, 2, 11, 4, 0, 0, 0, time.UTC)
	fresh := &Win11Manifest{Release: "25H2", CheckedAt: now.Add(-24 * time.Hour).Format(time.RFC3339)}
	old := &Win11Manifest{Release: "25H2", CheckedAt: now.Add(-29 * 24 * time.Hour).Format(time.RFC3339)}

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
	}
	for _, tt := range tests {
		due, reason := win11RefreshDue(tt.m, now, tt.latest, tt.force)
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
}

func TestWin11ManifestRoundTrip(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sub", "win11-amd64.json")
	if m, err := LoadWin11Manifest(file); m != nil || err != nil {
		t.Fatalf("missing manifest: %v, %v", m, err)
	}
	m := &Win11Manifest{Arch: "amd64"}
	m.Promote(Win11HistoryEntry{Object: "x.iso"}, "Win11_25H2_English_x64.iso", 1, time.Now())
	if err := m.Save(file); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadWin11Manifest(file)
	if err != nil || loaded.Object != "x.iso" || loaded.Schema != 1 {
		t.Fatalf("loaded %+v, %v", loaded, err)
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
	for _, want := range []string{"Windows 11 ISO missing at " + savePath, "Microsoft download disabled in CI", "docs/windows-iso-cache.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

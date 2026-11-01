package selfupdate

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestSettingsAndEligibility(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config", "update.json")
	s, err := LoadSettings(file)
	if err != nil || s.Enabled || s.Interval != time.Hour {
		t.Fatalf("default: %+v %v", s, err)
	}
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	if Enabled(s, get) {
		t.Fatal("enabled by default")
	}
	env["SAILWRIGHT_UPDATE_CHECK"] = "1"
	if !Enabled(s, get) {
		t.Fatal("opt-in ignored")
	}
	env["SAILWRIGHT_NO_UPDATE_CHECK"] = "1"
	if Enabled(s, get) {
		t.Fatal("disable must win")
	}
	for _, command := range []string{"update", "version", "help", "completion", "__complete"} {
		if Eligible(command, "v1.0.0", true, get) {
			t.Errorf("eligible: %s", command)
		}
	}
	if Eligible("build", "dev", true, get) || Eligible("build", "v1.0.0", false, get) {
		t.Fatal("ineligible run accepted")
	}
	env["CI"] = "1"
	if Eligible("build", "v1.0.0", true, get) {
		t.Fatal("CI accepted")
	}
	delete(env, "CI")
	if !Eligible("build", "v1.0.0", true, get) {
		t.Fatal("TTY release rejected")
	}
	s.Enabled = true
	s.Interval = 20 * time.Minute
	if err := SaveSettings(file, s); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSettings(file)
	if err != nil || got != s {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	s.Interval = time.Second
	if SaveSettings(file, s) == nil {
		t.Fatal("short interval accepted")
	}
}
func TestSettingsFileFormatAndInvalidFiles(t *testing.T) {
	file := filepath.Join(t.TempDir(), "update.json")
	if err := SaveSettings(file, Settings{Enabled: true, Interval: 90 * time.Minute}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(file)
	if err != nil || string(b) != `{"enabled":true,"interval":"1h30m0s"}` {
		t.Fatalf("file format: %s %v", b, err)
	}
	for _, content := range []string{`{`, `{"enabled":true,"interval":"soon"}`, `{"enabled":true,"interval":"1m"}`, `{"enabled":true,"interval":3600}`} {
		if err := os.WriteFile(file, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		s, err := LoadSettings(file)
		if err == nil || s != DefaultSettings() {
			t.Errorf("%s: want defaults and error, got %+v %v", content, s, err)
		}
	}
	if err := os.WriteFile(file, []byte(`{"enabled":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := LoadSettings(file); err != nil || !s.Enabled || s.Interval != DefaultInterval {
		t.Fatalf("missing interval: %+v %v", s, err)
	}
}
func TestCheckerIntervalAndNotice(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, "/releases/tag/v2.0.0", 302)
	}))
	defer server.Close()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := Checker{Releases: Releases{BaseURL: server.URL, Version: "v1.0.0"}, Settings: Settings{Interval: time.Hour}, CacheFile: filepath.Join(t.TempDir(), "cache.json"), Now: func() time.Time { return now }}
	if c.Start()() != "" || requests.Load() != 0 {
		t.Fatal("default queried")
	}
	c.Settings.Enabled = true
	finish := c.Start()
	deadline := time.Now().Add(time.Second)
	for {
		if v := finish(); v != "" {
			if v != "v2.0.0" {
				t.Fatal(v)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("notice missing")
		}
		time.Sleep(time.Millisecond)
	}
	if c.Start()() != "" || requests.Load() != 1 {
		t.Fatal("interval ignored")
	}
	now = now.Add(time.Hour)
	finish = c.Start()
	deadline = time.Now().Add(time.Second)
	for finish() == "" {
		if time.Now().After(deadline) {
			t.Fatal("new interval notice missing")
		}
		time.Sleep(time.Millisecond)
	}
	if requests.Load() != 2 {
		t.Fatal("missing next check")
	}
}
func TestCheckerNeverWaitsAndCachesFailure(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; http.Error(w, "offline", 503) }))
	defer server.Close()
	defer close(release)
	now := time.Now()
	file := filepath.Join(t.TempDir(), "cache.json")
	c := Checker{Releases: Releases{BaseURL: server.URL, Version: "v1.0.0"}, Settings: Settings{Enabled: true, Interval: time.Hour}, CacheFile: file, Now: func() time.Time { return now }}
	start := time.Now()
	finish := c.Start()
	if finish() != "" || time.Since(start) > noticeGrace+100*time.Millisecond {
		t.Fatal("check delayed command beyond the grace period")
	}
	<-entered
	if c.Start()() != "" {
		t.Fatal("unexpected notice")
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var cache checkCache
	if err := json.Unmarshal(b, &cache); err != nil {
		t.Fatal(err)
	}
	if !cache.LastCheck.Equal(now) {
		t.Fatal("failed check not throttled")
	}
}
func TestCheckerGracePeriodForQuickCommands(t *testing.T) {
	// A generous grace period keeps this deterministic on slow CI runners.
	defer func(previous time.Duration) { noticeGrace = previous }(noticeGrace)
	noticeGrace = 5 * time.Second
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		http.Redirect(w, r, "/releases/tag/v2.0.0", 302)
	}))
	defer server.Close()
	file := filepath.Join(t.TempDir(), "cache.json")
	c := Checker{Releases: Releases{BaseURL: server.URL, Version: "v1.0.0"}, Settings: Settings{Enabled: true, Interval: time.Hour}, CacheFile: file}
	// finish is called immediately, as for a command that ends at once.
	if got := c.Start()(); got != "v2.0.0" {
		t.Fatalf("quick command lost the result: %q", got)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var cache checkCache
	if err := json.Unmarshal(b, &cache); err != nil || cache.Latest != "v2.0.0" {
		t.Fatalf("result not cached: %+v %v", cache, err)
	}
}

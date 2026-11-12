package selfupdate

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
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
func readCache(t *testing.T, file string) checkCache {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var cache checkCache
	if err := json.Unmarshal(b, &cache); err != nil {
		t.Fatal(err)
	}
	return cache
}
func TestCheckerNeverWaitsAndCachesFailure(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		close(entered)
		<-release
		http.Error(w, "offline", 503)
	}))
	defer server.Close()
	// server.Close waits for the blocked handler: unblock it first, also when the
	// test fails early, or a failure turns into a hang until the test timeout.
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	now := time.Now()
	file := filepath.Join(t.TempDir(), "cache.json")
	c := Checker{Releases: Releases{BaseURL: server.URL, Version: "v1.0.0"}, Settings: Settings{Enabled: true, Interval: time.Hour}, CacheFile: file, Now: func() time.Time { return now }}
	start := time.Now()
	finish := c.Start()
	// The request stays blocked, so returning at all shows finish does not wait
	// for it; the bound only catches waits far beyond the grace period, with
	// headroom for loaded hosts.
	if finish() != "" || time.Since(start) > noticeGrace+2*time.Second {
		t.Fatalf("check delayed command beyond the grace period (%s)", time.Since(start))
	}
	<-entered
	if c.Start()() != "" || requests.Load() != 1 {
		t.Fatal("pending check not throttled")
	}
	if cache := readCache(t, file); !cache.LastAttempt.Equal(now) || !cache.LastCheck.IsZero() {
		t.Fatalf("pending check: %+v", cache)
	}
	unblock()
	// Wait for the background writer before reading the cache. Polling the file
	// can overlap its replacement, which Windows rejects with a sharing violation.
	defer func(previous time.Duration) { noticeGrace = previous }(noticeGrace)
	noticeGrace = 5 * time.Second
	if got := finish(); got != "" {
		t.Fatalf("failed check produced a notice: %q", got)
	}
	if cache := readCache(t, file); !cache.LastCheck.Equal(now) {
		t.Fatalf("failed check did not start the interval: %+v", cache)
	}
	// Past the retry window but within the interval: a failure still counts.
	now = now.Add(retryAfter + time.Minute)
	if c.Start()() != "" || requests.Load() != 1 {
		t.Fatal("failed check retried within the interval")
	}
}
func TestCheckerRetriesUnfinishedCheck(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, "/releases/tag/v2.0.0", 302)
	}))
	defer server.Close()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	file := filepath.Join(t.TempDir(), "cache.json")
	// A previous CLI reserved an attempt and exited before the response arrived.
	if err := writeJSON(file, checkCache{LastAttempt: now}); err != nil {
		t.Fatal(err)
	}
	c := Checker{Releases: Releases{BaseURL: server.URL, Version: "v1.0.0"}, Settings: Settings{Enabled: true, Interval: time.Hour}, CacheFile: file, Now: func() time.Time { return now }}
	if c.Start()() != "" || requests.Load() != 0 {
		t.Fatal("retried inside the retry window")
	}
	now = now.Add(retryAfter)
	finish := c.Start()
	deadline := time.Now().Add(5 * time.Second)
	for finish() == "" {
		if time.Now().After(deadline) {
			t.Fatal("unfinished check was not retried")
		}
		time.Sleep(time.Millisecond)
	}
	if cache := readCache(t, file); !cache.LastCheck.Equal(now) || cache.Latest != "v2.0.0" {
		t.Fatalf("retried check not recorded: %+v", cache)
	}
}
func TestCheckLockReleasedOnClose(t *testing.T) {
	file := filepath.Join(t.TempDir(), "cache.json")
	unlock, ok := checkLock(file)
	if !ok {
		t.Fatal("lock failed")
	}
	if _, ok := checkLock(file); ok {
		t.Fatal("second lock acquired while held")
	}
	unlock()
	unlock, ok = checkLock(file)
	if !ok {
		t.Fatal("lock not released")
	}
	unlock()
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

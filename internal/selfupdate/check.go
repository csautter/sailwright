package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const DefaultInterval = time.Hour
const MinimumInterval = 15 * time.Minute

// noticeGrace is how long finish waits for a pending check after the command ends,
// so quick commands still record (and can report) the result.
var noticeGrace = 150 * time.Millisecond

type Settings struct {
	Enabled  bool
	Interval time.Duration
}

// settingsFile is the on-disk form; the interval uses Go duration syntax ("1h").
type settingsFile struct {
	Enabled  bool   `json:"enabled"`
	Interval string `json:"interval,omitempty"`
}

func (s Settings) MarshalJSON() ([]byte, error) {
	return json.Marshal(settingsFile{Enabled: s.Enabled, Interval: s.Interval.String()})
}

func (s *Settings) UnmarshalJSON(b []byte) error {
	var f settingsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	s.Enabled = f.Enabled
	if f.Interval != "" {
		d, err := time.ParseDuration(f.Interval)
		if err != nil {
			return fmt.Errorf("invalid interval %q: %w", f.Interval, err)
		}
		s.Interval = d
	}
	return nil
}

// DefaultSettings are used when no settings file exists or it cannot be used.
func DefaultSettings() Settings { return Settings{Interval: DefaultInterval} }

// LoadSettings always returns usable settings: on error it returns the defaults
// together with the error, so callers can warn and overwrite a broken file.
func LoadSettings(file string) (Settings, error) {
	b, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return DefaultSettings(), nil
	}
	if err != nil {
		return DefaultSettings(), err
	}
	s := DefaultSettings()
	if err := json.Unmarshal(b, &s); err != nil {
		return DefaultSettings(), fmt.Errorf("%s: %w", file, err)
	}
	if s.Interval < MinimumInterval {
		return DefaultSettings(), fmt.Errorf("%s: update interval must be at least %s", file, MinimumInterval)
	}
	return s, nil
}
func SaveSettings(file string, s Settings) error {
	if s.Interval < MinimumInterval {
		return fmt.Errorf("update interval must be at least %s", MinimumInterval)
	}
	return writeJSON(file, s)
}
func writeJSON(file string, v any) error {
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(file), ".update-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), file)
}
func Enabled(s Settings, getenv func(string) string) bool {
	return getenv("SAILWRIGHT_NO_UPDATE_CHECK") != "1" && (s.Enabled || getenv("SAILWRIGHT_UPDATE_CHECK") == "1")
}
func Eligible(command, version string, tty bool, getenv func(string) string) bool {
	if !tty || getenv("CI") != "" || !ValidVersion(version) {
		return false
	}
	switch command {
	case "update", "version", "completion", "help", "__complete", "__completeNoDesc":
		return false
	}
	return true
}

type checkCache struct {
	LastCheck  time.Time `json:"last_check"`
	Latest     string    `json:"latest"`
	LastNotice time.Time `json:"last_notice"`
}

type Checker struct {
	Releases  Releases
	Settings  Settings
	CacheFile string
	Now       func() time.Time
}

// Start reserves the interval before launching a bounded request. Finish waits at
// most noticeGrace for a pending request; a later result is used on the next run.
func (c Checker) Start() func() string {
	empty := func() string { return "" }
	if !c.Settings.Enabled || c.Settings.Interval < MinimumInterval {
		return empty
	}
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	if err := os.MkdirAll(filepath.Dir(c.CacheFile), 0700); err != nil {
		return empty
	}
	unlock, ok := checkLock(c.CacheFile)
	if !ok {
		return empty
	}
	defer unlock()
	var cache checkCache
	if b, err := os.ReadFile(c.CacheFile); err == nil {
		_ = json.Unmarshal(b, &cache)
	}
	notice := func(state *checkCache) string {
		if !ValidVersion(state.Latest) || Compare(state.Latest, c.Releases.Version) <= 0 || (!state.LastNotice.IsZero() && now.Sub(state.LastNotice) < c.Settings.Interval) {
			return ""
		}
		state.LastNotice = now
		return state.Latest
	}
	if now.Sub(cache.LastCheck) < c.Settings.Interval {
		latest := notice(&cache)
		if latest != "" {
			if writeJSON(c.CacheFile, cache) != nil {
				return empty
			}
		}
		return func() string { return latest }
	}
	cache.LastCheck = now
	if err := writeJSON(c.CacheFile, cache); err != nil {
		return empty
	}
	done := make(chan string, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		latest, err := c.Releases.Latest(ctx)
		if err != nil {
			done <- ""
			return
		}
		// Read and merge under the same cross-process lock as interval reservation.
		if unlock, ok := checkLock(c.CacheFile); ok {
			var state checkCache
			if b, err := os.ReadFile(c.CacheFile); err == nil && json.Unmarshal(b, &state) == nil {
				state.Latest = latest
				_ = writeJSON(c.CacheFile, state)
			}
			unlock()
		}
		done <- latest
	}()
	return func() string {
		select {
		case latest := <-done:
			if !ValidVersion(latest) {
				return ""
			}
			unlock, ok := checkLock(c.CacheFile)
			if !ok {
				return ""
			}
			defer unlock()
			var state checkCache
			b, err := os.ReadFile(c.CacheFile)
			if err != nil || json.Unmarshal(b, &state) != nil {
				return ""
			}
			state.Latest = latest
			result := notice(&state)
			if writeJSON(c.CacheFile, state) != nil {
				return ""
			}
			return result
		case <-time.After(noticeGrace):
			return ""
		}
	}
}

// Locks are held only for local cache I/O. Recover a lock left by a killed CLI.
func checkLock(file string) (func(), bool) {
	lock := file + ".lock"
	if info, err := os.Stat(lock); err == nil && time.Since(info.ModTime()) > time.Minute {
		_ = os.Remove(lock)
	}
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, false
	}
	_ = f.Close()
	return func() { _ = os.Remove(lock) }, true
}

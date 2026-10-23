package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/csautter/dev-alchemy/pkg/build"
)

func TestPublishWritesManifestAndPrunes(t *testing.T) {
	dir := t.TempDir()
	iso := filepath.Join(dir, "x.iso")
	manifest := filepath.Join(dir, "win11-amd64.json")
	day := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)

	var out bytes.Buffer
	for i := 0; i < 4; i++ {
		// Different bytes each day; identical bytes would be deduplicated.
		if err := os.WriteFile(iso, []byte(fmt.Sprintf("iso%d", i)), 0o600); err != nil {
			t.Fatal(err)
		}
		out.Reset()
		args := []string{"--arch", "amd64", "--iso", iso, "--source-name", "Win11_25H2_English_x64.iso", "--manifest", manifest}
		if err := runPublish(args, &out, day.AddDate(0, 0, i)); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(out.String(), "object=win11_25h2_english_x64_20261026.iso") {
		t.Errorf("output: %s", out.String())
	}
	if !strings.Contains(out.String(), "pruned=win11_25h2_english_x64_20261023.iso") {
		t.Errorf("expected oldest object pruned: %s", out.String())
	}
	m, err := build.LoadWin11Manifest(manifest)
	if err != nil || len(m.History) != 3 || m.Arch != "amd64" || m.Size != 4 {
		t.Fatalf("manifest %+v, %v", m, err)
	}
}

func TestPublishMarkDue(t *testing.T) {
	dir := t.TempDir()
	iso := filepath.Join(dir, "x.iso")
	_ = os.WriteFile(iso, []byte("iso"), 0o600)
	manifest := filepath.Join(dir, "m.json")
	now := time.Date(2026, 10, 23, 4, 0, 0, 0, time.UTC)
	args := []string{"--arch", "arm64", "--iso", iso, "--source-name", "Win11_25H2_English_Arm64.iso", "--manifest", manifest, "--mark-due"}
	if err := runPublish(args, &bytes.Buffer{}, now); err != nil {
		t.Fatal(err)
	}
	m, _ := build.LoadWin11Manifest(manifest)
	// Due on the next run, but far from the 90-day staleness limit.
	if due, reason := build.Win11RefreshDue(m, now, "", false); !due {
		t.Errorf("expected due: %s", reason)
	}
	if m.CheckedAt != "2026-09-25T04:00:00Z" {
		t.Errorf("checked_at = %s", m.CheckedAt)
	}
}

// publishBytes publishes content as the ISO of the given day and returns the helper output.
func publishBytes(t *testing.T, manifest, content string, day time.Time) (string, error) {
	t.Helper()
	iso := filepath.Join(t.TempDir(), "x.iso")
	if err := os.WriteFile(iso, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := runPublish([]string{"--arch", "amd64", "--iso", iso, "--source-name", "Win11_25H2_English_x64.iso", "--manifest", manifest}, &out, day)
	return out.String(), err
}

func TestPublishDeduplicatesIdenticalBytes(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "m.json")
	day := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	if _, err := publishBytes(t, manifest, "v1", day); err != nil {
		t.Fatal(err)
	}
	if _, err := publishBytes(t, manifest, "v2", day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}

	// Same bytes as the current object: nothing new is published, only checked_at moves.
	out, err := publishBytes(t, manifest, "v2", day.AddDate(0, 0, 30))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"object=win11_25h2_english_x64_20261024.iso", "existing=true", "unchanged=true", "pruned=\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q: %s", want, out)
		}
	}
	m, _ := build.LoadWin11Manifest(manifest)
	if len(m.History) != 2 || m.CheckedAt != "2026-11-22T00:00:00Z" || m.PublishedAt != "2026-10-24T00:00:00Z" {
		t.Errorf("manifest %+v", m)
	}

	// Same bytes as an older object: that object becomes current again, no upload needed.
	out, err = publishBytes(t, manifest, "v1", day.AddDate(0, 0, 31))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"object=win11_25h2_english_x64_20261023.iso", "existing=true", "unchanged=false"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q: %s", want, out)
		}
	}
	m, _ = build.LoadWin11Manifest(manifest)
	if m.Object != "win11_25h2_english_x64_20261023.iso" || len(m.History) != 2 {
		t.Errorf("manifest %+v", m)
	}
}

func TestPublishRejectsChangedBytesUnderPublishedName(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "m.json")
	day := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	if _, err := publishBytes(t, manifest, "v1", day); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(manifest)
	_, err := publishBytes(t, manifest, "v1-changed", day.Add(time.Hour))
	if err == nil || !strings.Contains(err.Error(), "already published") {
		t.Fatalf("expected a conflict error, got %v", err)
	}
	after, _ := os.ReadFile(manifest)
	if !bytes.Equal(before, after) {
		t.Error("manifest must not change on conflict")
	}
}

func stubNetwork(t *testing.T, info *build.Win11LinkInfo, resolveErr error, size int64, probeErr error) *int {
	t.Helper()
	probes := 0
	prevResolve, prevProbe := resolveWin11Link, probeWin11ISO
	resolveWin11Link = func(string) (*build.Win11LinkInfo, error) { return info, resolveErr }
	probeWin11ISO = func(string) (int64, error) { probes++; return size, probeErr }
	t.Cleanup(func() { resolveWin11Link, probeWin11ISO = prevResolve, prevProbe })
	return &probes
}

func TestResolve(t *testing.T) {
	now := time.Date(2026, 11, 30, 4, 0, 0, 0, time.UTC)
	const currentSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const otherSHA = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	link := func(name, sha string) *build.Win11LinkInfo {
		return &build.Win11LinkInfo{URL: "https://example.invalid/" + name, SourceName: name, SHA256: sha}
	}
	tests := []struct {
		name       string
		info       *build.Win11LinkInfo
		resolveErr error
		size       int64
		probeErr   error
		noManifest bool
		want       string
		wantProbes int
	}{
		{name: "no manifest", info: link("Win11_25H2_English_x64.iso", ""), noManifest: true, want: "outcome=new"},
		{name: "refused", resolveErr: fmt.Errorf("%w: exit 3", build.ErrMicrosoftRefused), want: "outcome=refused"},
		{name: "same sha256", info: link("Win11_25H2_English_x64v2.iso", strings.ToUpper(currentSHA)), want: "outcome=unchanged"},
		{name: "different sha256", info: link("Win11_25H2_English_x64.iso", otherSHA), want: "outcome=new"},
		{name: "same name and size", info: link("Win11_25H2_English_x64.iso", ""), size: 42, want: "outcome=unchanged", wantProbes: 1},
		{name: "same name, other size", info: link("Win11_25H2_English_x64.iso", ""), size: 43, want: "outcome=new", wantProbes: 1},
		{name: "size probe failed", info: link("Win11_25H2_English_x64.iso", ""), probeErr: errors.New("HTTP 403"), want: "outcome=new", wantProbes: 1},
		{name: "new name", info: link("Win11_25H2_English_x64v2.iso", ""), want: "outcome=new"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := filepath.Join(t.TempDir(), "m.json")
			if !tt.noManifest {
				m := &build.Win11Manifest{Arch: "amd64"}
				m.Promote(build.Win11HistoryEntry{Object: "win11_25h2_english_x64_20261023.iso", SHA256: currentSHA}, "Win11_25H2_English_x64.iso", 42, now.AddDate(0, 0, -38))
				if err := m.Save(manifest); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(manifest)
			probes := stubNetwork(t, tt.info, tt.resolveErr, tt.size, tt.probeErr)

			var out bytes.Buffer
			if err := runResolve([]string{"--arch", "amd64", "--manifest", manifest, "--announced-release", "26H1"}, &out, now); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tt.want+"\n") {
				t.Errorf("output %q lacks %q", out.String(), tt.want)
			}
			if *probes != tt.wantProbes {
				t.Errorf("probes = %d, want %d", *probes, tt.wantProbes)
			}
			after, _ := os.ReadFile(manifest)
			if tt.want == "outcome=unchanged" {
				m, _ := build.LoadWin11Manifest(manifest)
				if m.CheckedAt != "2026-11-30T04:00:00Z" || m.AnnouncedRelease != "26H1" {
					t.Errorf("unchanged must record the check: %+v", m)
				}
			} else if !bytes.Equal(before, after) {
				t.Error("manifest must only change when the ISO is unchanged")
			}
		})
	}
}

func TestRejectsBadArch(t *testing.T) {
	if err := runPublish([]string{"--arch", "x86"}, &bytes.Buffer{}, time.Now()); err == nil {
		t.Fatal("expected error")
	}
}

package main

import (
	"bytes"
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
	if err := os.WriteFile(iso, []byte("iso"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "win11-amd64.json")
	day := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)

	var out bytes.Buffer
	for i := 0; i < 4; i++ {
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
	if err != nil || len(m.History) != 3 || m.Arch != "amd64" || m.Size != 3 {
		t.Fatalf("manifest %+v, %v", m, err)
	}
}

func TestPublishEpochCheckedAt(t *testing.T) {
	dir := t.TempDir()
	iso := filepath.Join(dir, "x.iso")
	_ = os.WriteFile(iso, []byte("iso"), 0o600)
	manifest := filepath.Join(dir, "m.json")
	args := []string{"--arch", "arm64", "--iso", iso, "--source-name", "Win11_25H2_English_Arm64.iso", "--manifest", manifest, "--epoch-checked-at"}
	if err := runPublish(args, &bytes.Buffer{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	m, _ := build.LoadWin11Manifest(manifest)
	if !strings.HasPrefix(m.CheckedAt, "1970-01-01") {
		t.Errorf("checked_at = %s", m.CheckedAt)
	}
}

func TestRejectsBadArch(t *testing.T) {
	if err := runPublish([]string{"--arch", "x86"}, &bytes.Buffer{}, time.Now()); err == nil {
		t.Fatal("expected error")
	}
}

package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOutputBufferKeepsNewestLimitBytesAcrossWraparound(t *testing.T) {
	buf := newOutputBuffer(100)
	for i := 0; i < 50; i++ {
		_, _ = buf.Write([]byte("0123456789"))
	}
	if buf.Len() != 500 {
		t.Fatalf("Len = %d, want 500", buf.Len())
	}
	chunk := buf.Read(0, 1000)
	if !chunk.TruncatedBefore || chunk.Offset != 400 || chunk.NextOffset != 500 || len(chunk.Data) != 100 {
		t.Fatalf("unexpected chunk %+v", chunk)
	}
	if tail := buf.Read(450, 1000); tail.TruncatedBefore || tail.Offset != 450 || len(tail.Data) != 50 {
		t.Fatalf("unexpected tail %+v", tail)
	}
}

func BenchmarkOutputBufferFull(b *testing.B) {
	buf := newOutputBuffer(8 << 20)
	_, _ = buf.Write(make([]byte, 8<<20))
	line := make([]byte, 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = buf.Write(line)
	}
}

func TestCappedWriterTruncatesWithMarker(t *testing.T) {
	var out strings.Builder
	w := &cappedWriter{w: &out, limit: 10}
	for _, chunk := range []string{"12345", "6789012345", "more"} {
		if n, err := w.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("write %q: %d, %v", chunk, n, err)
		}
	}
	if got := out.String(); !strings.HasPrefix(got, "1234567890\n[log truncated") || strings.Contains(got, "more") || strings.Count(got, "truncated") != 1 {
		t.Fatalf("unexpected log %q", got)
	}
}

func TestPruneOldLogsRemovesOnlyStaleLogs(t *testing.T) {
	dir := t.TempDir()
	old, fresh, other := filepath.Join(dir, "old.log"), filepath.Join(dir, "fresh.log"), filepath.Join(dir, "keep.txt")
	for _, p := range []string{old, fresh, other} {
		writeFile(t, p, "x")
	}
	stale := time.Now().Add(-60 * 24 * time.Hour)
	for _, p := range []string{old, other} {
		if err := os.Chtimes(p, stale, stale); err != nil {
			t.Fatal(err)
		}
	}
	pruneOldLogs(dir, maxJobLogAge)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("stale log must be removed")
	}
	for _, p := range []string{fresh, other} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s must stay: %v", p, err)
		}
	}
}

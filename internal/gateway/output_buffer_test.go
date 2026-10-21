package gateway

import "testing"

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

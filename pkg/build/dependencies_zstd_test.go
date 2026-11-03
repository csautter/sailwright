package build

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestDownloadWebFileDependencyZstd(t *testing.T) {
	expected := []byte("dependency payload\n")
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	valid := encoder.EncodeAll(expected, nil)
	encoder.Close()
	// Upstream regression frame for GO-2026-6865; see testdata/README.md.
	malformed, err := os.ReadFile("testdata/zstd-two-block-panic.zst")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		body    []byte
		wantErr bool
	}{
		{name: "valid", body: valid},
		{name: "two-block-panic", body: malformed, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(tc.body)
			}))
			defer server.Close()
			dest := filepath.Join(t.TempDir(), "dependency")
			err := downloadWebFileDependency(context.Background(), nil, WebFileDependency{
				LocalPath: dest,
				Source:    server.URL + "/dependency.zst",
			})
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected malformed zstd download to fail")
				}
				if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
					t.Fatalf("failed download left destination behind: %v", statErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(dest)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, expected) {
				t.Fatalf("downloaded payload = %q, want %q", got, expected)
			}
		})
	}
}

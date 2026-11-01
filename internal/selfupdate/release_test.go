package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func archive(t *testing.T, goos, name string) []byte {
	t.Helper()
	var b bytes.Buffer
	if goos == "windows" {
		w := zip.NewWriter(&b)
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write([]byte("new binary"))
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		gz := gzip.NewWriter(&b)
		w := tar.NewWriter(gz)
		if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: 10}); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("new binary"))
		_ = w.Close()
		_ = gz.Close()
	}
	return b.Bytes()
}
func TestReleaseVerification(t *testing.T) {
	// These fixtures intentionally have no signature. Signature behavior is tested separately.
	t.Setenv("PATH", t.TempDir())
	for _, goos := range []string{"linux", "darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			ext, exe := ".tar.gz", ""
			if goos == "windows" {
				ext, exe = ".zip", ".exe"
			}
			asset := "sailwright_1.2.3_" + goos + "_amd64" + ext
			data := archive(t, goos, "sailwright"+exe)
			sum := sha256.Sum256(data)
			corrupt, missing := false, false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/releases/latest":
					if r.UserAgent() != "sailwright/v1.0.0" {
						t.Error("missing user agent")
					}
					http.Redirect(w, r, "/releases/tag/v1.2.3", 302)
				case strings.HasSuffix(r.URL.Path, "checksums.txt"):
					fmt.Fprintf(w, "%x  dist/%s\n", sum, asset)
				case strings.HasSuffix(r.URL.Path, asset):
					if missing {
						http.NotFound(w, r)
					} else if corrupt {
						_, _ = w.Write([]byte("tampered"))
					} else {
						_, _ = w.Write(data)
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			r := Releases{BaseURL: server.URL, Version: "v1.0.0"}
			ctx := context.Background()
			tag, err := r.Latest(ctx)
			if err != nil || tag != "v1.2.3" {
				t.Fatalf("%s %v", tag, err)
			}
			sums, signed, err := r.Checksums(ctx, tag)
			if err != nil || signed {
				t.Fatalf("signed=%t %v", signed, err)
			}
			b, err := r.Binary(ctx, tag, "sailwright", goos, "amd64", sums)
			if err != nil || string(b) != "new binary" {
				t.Fatalf("%s %v", b, err)
			}
			corrupt = true
			if _, err := r.Binary(ctx, tag, "sailwright", goos, "amd64", sums); err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
				t.Fatalf("expected mismatch: %v", err)
			}
			corrupt = false
			missing = true
			if _, err := r.Binary(ctx, tag, "sailwright", goos, "amd64", sums); err == nil {
				t.Fatal("missing asset accepted")
			}
			if _, err := r.Binary(ctx, tag, "sailwright", goos, "amd64", nil); err == nil {
				t.Fatal("missing checksum accepted")
			}
		})
	}
}
func TestSignatureVerification(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture; Windows checksum integration runs in CI")
	}
	for _, tc := range []struct {
		name                string
		fail, missing       bool
		wantErr, wantSigned bool
	}{{name: "verified", wantSigned: true}, {name: "rejected", fail: true, wantErr: true}, {name: "signature missing", missing: true, wantErr: true}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			code := "0"
			if tc.fail {
				code = "1"
			}
			script := "#!/bin/sh\n[ \"$1\" = verify-blob ] || exit 3\ncase \"$*\" in *'" + Repository + "/.github/workflows/release-binaries.yml@refs/heads/main'*'https://token.actions.githubusercontent.com'*) exit " + code + ";; *) exit 4;; esac\n"
			if err := os.WriteFile(filepath.Join(dir, "cosign"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.missing && strings.HasSuffix(r.URL.Path, ".sig") {
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write([]byte("signed fixture"))
			}))
			defer s.Close()
			data, signed, err := (Releases{BaseURL: s.URL}).Checksums(context.Background(), "v1.0.0")
			if (err != nil) != tc.wantErr || signed != tc.wantSigned {
				t.Fatalf("signed=%t err=%v", signed, err)
			}
			if tc.wantErr && data != nil {
				t.Fatal("manifest returned despite failed verification")
			}
			if tc.missing && !strings.Contains(err.Error(), "signature is unavailable") {
				t.Fatalf("unclear error: %v", err)
			}
		})
	}
}
func TestVersionComparison(t *testing.T) {
	for _, v := range []string{"dev", "pr-42", "v1.2.3/../bad", "v1.2.3-beta.1", "01.2.3"} {
		if ValidVersion(v) {
			t.Errorf("accepted %s", v)
		}
	}
	for _, v := range []string{"v1.2.3", "0.0.0", "v999999999999999999999.0.0"} {
		if !ValidVersion(v) {
			t.Errorf("rejected %s", v)
		}
	}
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"v1.10.0", "1.9.9", 1}, {"v1.0.0", "2.0.0", -1}, {"v1.0.0", "1.0.0", 0},
		{"v10.0.0", "v9.99.99", 1}, {"v999999999999999999999.0.0", "v999999999999999999998.0.0", 1},
		{"v1.2.3", "dev", 1}, {"dev", "v0.0.0", -1}, {"dev", "pr-42", 0}, {"v1.2", "v1.2.3", -1}, {"", "", 0},
	} {
		if got := Compare(tc.a, tc.b); got != tc.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
func TestValidateBaseURL(t *testing.T) {
	for _, ok := range []string{"", "http://127.0.0.1:8765", "http://localhost:1/", "https://[::1]:8443", "http://127.0.0.2"} {
		if err := ValidateBaseURL(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://example.com", "https://github.com/someone/else", "http://127.0.0.1.evil.test", "http://user@127.0.0.1", "file:///tmp/releases", "ftp://127.0.0.1", "127.0.0.1:8765", "http://127.0.0.1/?x=1"} {
		if ValidateBaseURL(bad) == nil {
			t.Errorf("accepted %s", bad)
		}
		if _, err := (Releases{BaseURL: bad}).Latest(context.Background()); err == nil {
			t.Errorf("Latest used %s", bad)
		}
		if _, _, err := (Releases{BaseURL: bad}).Checksums(context.Background(), "v1.0.0"); err == nil {
			t.Errorf("Checksums used %s", bad)
		}
	}
}
func TestInvalidRedirect(t *testing.T) {
	for _, location := range []string{"/login", "/releases/tag/dev", "/releases/tag/v1.0.0-beta.1"} {
		t.Run(location, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, location, 302) }))
			defer s.Close()
			if _, err := (Releases{BaseURL: s.URL}).Latest(context.Background()); err == nil {
				t.Fatal("invalid redirect accepted")
			}
		})
	}
}

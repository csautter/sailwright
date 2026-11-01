// Package selfupdate implements explicit, verified release updates and optional notices.
package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const Repository = "https://github.com/csautter/sailwright"
const maxArchive = 256 << 20

var releaseVersion = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// ValidVersion accepts stable release versions only; development builds cannot update.
func ValidVersion(v string) bool { return releaseVersion.MatchString(v) }

// Compare compares stable release versions without integer overflow. Invalid
// versions (such as "dev") sort below every valid version and equal each other.
func Compare(a, b string) int {
	switch va, vb := ValidVersion(a), ValidVersion(b); {
	case !va && !vb:
		return 0
	case !va:
		return -1
	case !vb:
		return 1
	}
	aa, bb := strings.Split(strings.TrimPrefix(a, "v"), "."), strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := range aa {
		if len(aa[i]) < len(bb[i]) {
			return -1
		}
		if len(aa[i]) > len(bb[i]) {
			return 1
		}
		if c := strings.Compare(aa[i], bb[i]); c != 0 {
			return c
		}
	}
	return 0
}

type Releases struct {
	BaseURL string // Repository URL; override only for local release fixtures.
	Client  *http.Client
	Version string
}

func (r Releases) base() (string, error) {
	if r.BaseURL == "" {
		return Repository, nil
	}
	if err := ValidateBaseURL(r.BaseURL); err != nil {
		return "", err
	}
	return strings.TrimRight(r.BaseURL, "/"), nil
}

// ValidateBaseURL limits the release source override to local test fixtures, so a
// leaked environment variable cannot redirect updates to a remote server.
func ValidateBaseURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.User == nil && u.RawQuery == "" && u.Fragment == "" {
		host := u.Hostname()
		if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
			return nil
		}
	}
	return fmt.Errorf("SAILWRIGHT_UPDATE_BASE_URL must be an http(s) URL on a loopback address for local test fixtures, got %q; unset it to use %s", raw, Repository)
}
func (r Releases) client() *http.Client {
	if r.Client != nil {
		return r.Client
	}
	return &http.Client{Timeout: 2 * time.Minute}
}

// Latest reads just the redirect, without downloading the release HTML or calling the API.
func (r Releases) Latest(ctx context.Context) (string, error) {
	client := *r.client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	base, err := r.base()
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "sailwright/"+r.Version)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 || resp.StatusCode > 399 {
		return "", fmt.Errorf("latest release: HTTP %d", resp.StatusCode)
	}
	location, err := resp.Location()
	if err != nil {
		return "", err
	}
	tag := path.Base(location.Path)
	if !strings.HasSuffix(location.Path, "/releases/tag/"+tag) || !ValidVersion(tag) {
		return "", fmt.Errorf("invalid release redirect")
	}
	return tag, nil
}
func (r Releases) download(ctx context.Context, tag, name string, limit int64) ([]byte, error) {
	base, err := r.base()
	if err != nil {
		return nil, err
	}
	u := base + "/releases/download/" + url.PathEscape(tag) + "/" + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "sailwright/"+r.Version)
	resp, err := r.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", name, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s exceeds size limit", name)
	}
	return data, nil
}

// Checksums downloads the checksum manifest and verifies its signature whenever
// cosign is installed. signed reports whether the signature was verified.
func (r Releases) Checksums(ctx context.Context, tag string) (data []byte, signed bool, err error) {
	name := "sailwright_" + strings.TrimPrefix(tag, "v") + "_checksums.txt"
	data, err = r.download(ctx, tag, name, 1<<20)
	if err != nil {
		return nil, false, err
	}
	cosign, lookErr := exec.LookPath("cosign")
	if lookErr != nil {
		return data, false, nil
	}
	dir, err := os.MkdirTemp("", "sailwright-signature-*")
	if err != nil {
		return nil, false, err
	}
	defer os.RemoveAll(dir)
	manifest := filepath.Join(dir, name)
	if err := os.WriteFile(manifest, data, 0600); err != nil {
		return nil, false, err
	}
	for _, suffix := range []string{".sig", ".pem"} {
		signature, err := r.download(ctx, tag, name+suffix, 1<<20)
		if err != nil {
			return nil, false, fmt.Errorf("cosign is installed but the signature is unavailable: %w", err)
		}
		if err := os.WriteFile(manifest+suffix, signature, 0600); err != nil {
			return nil, false, err
		}
	}
	cmd := exec.CommandContext(ctx, cosign, "verify-blob", "--certificate", manifest+".pem", "--signature", manifest+".sig", "--certificate-identity", Repository+"/.github/workflows/release-binaries.yml@refs/heads/main", "--certificate-oidc-issuer", "https://token.actions.githubusercontent.com", manifest)
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, false, fmt.Errorf("cosign verification failed: %w: %s", err, output)
	}
	return data, true, nil
}

func (r Releases) Binary(ctx context.Context, tag, name, goos, arch string, checksums []byte) ([]byte, error) {
	ext, exe := ".tar.gz", ""
	if goos == "windows" {
		ext, exe = ".zip", ".exe"
	}
	asset := name + "_" + strings.TrimPrefix(tag, "v") + "_" + goos + "_" + arch + ext
	var expected string
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && path.Base(strings.TrimPrefix(fields[1], "*")) == asset {
			if expected != "" {
				return nil, fmt.Errorf("duplicate checksum for %s", asset)
			}
			expected = fields[0]
		}
	}
	if len(expected) != 64 {
		return nil, fmt.Errorf("missing SHA-256 for %s", asset)
	}
	data, err := r.download(ctx, tag, asset, maxArchive)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), expected) {
		return nil, fmt.Errorf("SHA-256 mismatch for %s; nothing replaced", asset)
	}
	return extract(data, name+exe, goos)
}
func extract(data []byte, name, goos string) ([]byte, error) {
	read := func(reader io.Reader) ([]byte, error) {
		b, err := io.ReadAll(io.LimitReader(reader, maxArchive+1))
		if len(b) > maxArchive || len(b) == 0 {
			return nil, fmt.Errorf("invalid binary size")
		}
		return b, err
	}
	if goos == "windows" {
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range z.File {
			if f.Name == name && f.Mode().IsRegular() {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return read(rc)
			}
		}
	} else {
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if h.Name == name && h.Typeflag == tar.TypeReg {
				return read(tr)
			}
		}
	}
	return nil, fmt.Errorf("archive does not contain regular file %s", name)
}

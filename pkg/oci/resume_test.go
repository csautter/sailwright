package oci

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"oras.land/oras-go/v2/content"
)

func TestResumableTargetRecoversInterruptedBlob(t *testing.T) {
	for _, tc := range []struct{ ranges, corrupt bool }{{true, false}, {false, false}, {true, true}} {
		t.Run(fmt.Sprintf("ranges=%v/corrupt=%v", tc.ranges, tc.corrupt), func(t *testing.T) {
			t.Parallel()
			payload := strings.Repeat("blob data\n", 1000)
			desc := content.NewDescriptorFromBytes(MediaTypeQCOW2, []byte(payload))
			var requests, resumed atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempt := requests.Add(1)
				if tc.ranges {
					w.Header().Set("Accept-Ranges", "bytes")
				}
				offset := 0
				if h := r.Header.Get("Range"); h != "" {
					if _, err := fmt.Sscanf(h, "bytes=%d-", &offset); err != nil || offset != len(payload)/2 {
						t.Errorf("unexpected resume range %q", h)
						w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
						return
					}
					resumed.Add(1)
					w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, len(payload)-1, len(payload)))
				}
				w.Header().Set("Content-Length", fmt.Sprint(len(payload)-offset))
				if offset > 0 {
					w.WriteHeader(http.StatusPartialContent)
				}
				if attempt == 1 {
					_, _ = io.WriteString(w, payload[:len(payload)/2])
					w.(http.Flusher).Flush()
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					conn.Close()
					return
				}
				suffix := payload[offset:]
				if tc.corrupt {
					suffix = "X" + suffix[1:]
				}
				_, _ = io.WriteString(w, suffix)
			}))
			defer server.Close()
			ref, err := parsePullReference(strings.TrimPrefix(server.URL, "http://") + "/test:blob")
			if err != nil {
				t.Fatal(err)
			}
			repo, err := newRepository(ref, RegistryOptions{PlainHTTP: true, DisableDockerCredentials: true})
			if err != nil {
				t.Fatal(err)
			}
			source := resumableTarget{ReadOnlyTarget: repo}
			progress := &recordingProgress{}
			tracked := progressReadOnlyTarget{ReadOnlyTarget: source, progress: progress}
			body, err := tracked.Fetch(context.Background(), desc)
			if err != nil {
				t.Fatal(err)
			}
			defer body.Close()
			got, err := content.ReadAll(body, desc)
			if tc.corrupt {
				if err == nil {
					t.Fatal("corrupted resumed data must fail digest verification")
				}
				return
			}
			if err != nil || string(got) != payload {
				t.Fatalf("resumed blob failed size/digest verification: %v", err)
			}
			if got := atomic.LoadInt64(&progress.added); got != int64(len(payload)) {
				t.Fatalf("progress counted %d bytes, want %d", got, len(payload))
			}
			if tc.ranges && resumed.Load() != 1 {
				t.Fatalf("expected one range request, got %d", resumed.Load())
			}
			if !tc.ranges && requests.Load() != 2 {
				t.Fatalf("expected two full requests without range support, got %d", requests.Load())
			}
		})
	}
}

func TestResumableBlobCancellationDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	body := &resumableBlob{ctx: ctx, body: io.NopCloser(strings.NewReader("")), size: 100,
		fetch: func() (io.ReadCloser, error) {
			t.Fatal("must not fetch again after cancellation")
			return nil, nil
		},
	}
	defer body.Close()
	_, err := io.ReadAll(body)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want cancellation, got %v", err)
	}
}

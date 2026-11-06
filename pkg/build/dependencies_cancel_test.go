package build

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stallingFileServer sends the first part of a file and then stalls until the client
// goes away, like a slow mirror during Ctrl+C.
func stallingFileServer(t *testing.T, started chan<- struct{}) *httptest.Server {
	t.Helper()
	const size = 4 << 20
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(size))
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(make([]byte, 64<<10))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	return server
}

func TestCancelledDownloadLeavesNoFileBehind(t *testing.T) {
	started := make(chan struct{}, 1)
	server := stallingFileServer(t, started)
	localPath := filepath.Join(t.TempDir(), "guest-tools.iso")
	// No checksum, like the Windows 11 ISO and the UTM guest tools: a partial file at
	// the final path would be accepted by the next run.
	dep := WebFileDependency{LocalPath: localPath, Source: server.URL + "/guest-tools.iso"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- downloadWebFileDependency(ctx, nil, dep) }()

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("download did not start")
	}
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected the cancelled download to fail")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("download did not stop after cancellation")
	}

	for _, path := range []string{localPath, localPath + partialDownloadSuffix} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected no file at %s after a cancelled download, got err=%v", path, err)
		}
	}
	if checkIfWebFileDependencyExists(dep) {
		t.Fatal("a cancelled download must not count as present")
	}
}

func TestInterruptedDependencyReconciliationStopsBeforeDownloads(t *testing.T) {
	h, ctx := newInterruptHandler(context.Background(), nil, func(int) {})
	h.escalate(os.Interrupt)
	err := DependencyReconciliation(ctx, VirtualMachineConfig{OS: "windows11", Arch: "amd64", HostOs: HostOsLinux, VirtualizationEngine: VirtualizationEngineQemu})
	if err == nil || !Interrupted(ctx) {
		t.Fatalf("expected an interrupted reconciliation to fail, got %v", err)
	}
}

func TestCompletedDownloadIsMovedToFinalPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("payload"))
	}))
	defer server.Close()
	localPath := filepath.Join(t.TempDir(), "file.iso")
	// A leftover partial file from a killed run must not be resumed.
	writeTestFile(t, localPath+partialDownloadSuffix, "stale prefix of an older file version")

	dep := WebFileDependency{LocalPath: localPath, Source: server.URL + "/file.iso"}
	if err := downloadWebFileDependency(context.Background(), nil, dep); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(localPath)
	if err != nil || string(content) != "payload" {
		t.Fatalf("expected downloaded payload at final path, got %q, %v", content, err)
	}
	if _, err := os.Stat(localPath + partialDownloadSuffix); !os.IsNotExist(err) {
		t.Fatalf("expected the partial file to be gone, got err=%v", err)
	}
}

// Parallel builds share dependencies such as ISOs; concurrent reconciliations of the
// same file must download it once instead of replacing each other's partial file.
func TestConcurrentReconciliationsDownloadSharedDependencyOnce(t *testing.T) {
	var requests atomic.Int32
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			return
		}
		requests.Add(1)
		<-release
		_, _ = w.Write([]byte("payload"))
	}))
	defer server.Close()
	dep := WebFileDependency{LocalPath: filepath.Join(t.TempDir(), "shared.iso"), Source: server.URL + "/shared.iso"}

	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for range 3 {
		wg.Go(func() { errs <- reconcileWebFileDependency(context.Background(), nil, dep) })
	}
	time.Sleep(200 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("expected one download of the shared dependency, got %d", got)
	}
	content, err := os.ReadFile(dep.LocalPath)
	if err != nil || string(content) != "payload" {
		t.Fatalf("expected downloaded payload, got %q, %v", content, err)
	}
}

func TestWaitingForDependencyLockStopsOnCancel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locked.iso")
	unlock, err := lockWebFileDependency(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := lockWebFileDependency(ctx, path); err == nil {
		t.Fatal("expected waiting for a held lock to stop on cancellation")
	}
}

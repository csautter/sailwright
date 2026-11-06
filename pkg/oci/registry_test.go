package oci

import (
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"
)

func TestRepositoryUsesHTTP1ForRedirectedBlobs(t *testing.T) {
	payload := strings.Repeat("registry blob\n", 1024)
	blobs := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Received-Protocol", r.Proto)
		_, _ = io.WriteString(w, payload)
	}))
	blobs.EnableHTTP2 = true
	blobs.StartTLS()
	defer blobs.Close()

	registry := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 1 {
			t.Errorf("registry request used %s, want HTTP/1.1", r.Proto)
		}
		http.Redirect(w, r, blobs.URL, http.StatusTemporaryRedirect)
	}))
	registry.EnableHTTP2 = true
	registry.StartTLS()
	defer registry.Close()

	// Confirm the blob endpoint offers HTTP/2, so this catches negotiation regressions.
	resp, err := blobs.Client().Get(blobs.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.ProtoMajor != 2 {
		t.Fatalf("test endpoint used %s, want HTTP/2", resp.Proto)
	}

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: registry.Certificate().Raw})
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		opts RegistryOptions
	}{
		{name: "default"},
		{name: "anonymous", opts: RegistryOptions{DisableDockerCredentials: true}},
		{name: "credentials", opts: RegistryOptions{Username: "test", Password: "test"}},
		{name: "custom CA", opts: RegistryOptions{CAFile: caPath}},
		{name: "insecure TLS", opts: RegistryOptions{InsecureSkipTLSVerify: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, err := newRepository(remoteReference{repository: "example.com/test", registry: "example.com"}, tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			client := repo.Client.(*auth.Client).Client
			transport := client.Transport.(*retry.Transport).Base.(*http.Transport)
			defer transport.CloseIdleConnections()
			if tc.opts.CAFile == "" && !tc.opts.InsecureSkipTLSVerify {
				// Trust the test certificate without changing the client's protocol settings.
				pool := x509.NewCertPool()
				pool.AddCert(registry.Certificate())
				transport.TLSClientConfig.RootCAs = pool
			}
			resp, err := client.Get(registry.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil || string(body) != payload {
				t.Fatalf("blob transfer failed: %v, received %d bytes", err, len(body))
			}
			if got := resp.Header.Get("X-Received-Protocol"); got != "HTTP/1.1" {
				t.Errorf("redirected blob request used %s, want HTTP/1.1", got)
			}
		})
	}
}

func TestRegistryHTTPClientRetriesTransientResponses(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, err := registryHTTPClient(RegistryOptions{PlainHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || requests.Load() != 2 {
		t.Fatalf("status %d after %d requests, want 204 after 2", resp.StatusCode, requests.Load())
	}
}

func TestRegistryHTTPClientSkipsTLSVerification(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := registryHTTPClient(RegistryOptions{InsecureSkipTLSVerify: true})
	if err != nil {
		t.Fatalf("expected registry HTTP client: %v", err)
	}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("expected insecure registry client to reach self-signed server: %v", err)
	}
	resp.Body.Close()
}

func TestRegistryHTTPClientTrustsCAFile(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatalf("failed to write test CA file: %v", err)
	}

	client, err := registryHTTPClient(RegistryOptions{CAFile: caPath})
	if err != nil {
		t.Fatalf("expected registry HTTP client with CA file: %v", err)
	}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("expected registry client to trust CA file: %v", err)
	}
	resp.Body.Close()
}

func TestRegistryHTTPClientRejectsInvalidCAFile(t *testing.T) {
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("failed to write invalid test CA file: %v", err)
	}

	_, err := registryHTTPClient(RegistryOptions{CAFile: caPath})
	if err == nil {
		t.Fatal("expected invalid CA file to fail")
	}
	if !strings.Contains(err.Error(), "no PEM certificates") {
		t.Fatalf("expected invalid CA error, got %q", err.Error())
	}
}

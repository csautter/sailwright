package client

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDiscoveryKeepsContainerCredentialsAheadOfLocalGateway(t *testing.T) {
	mounted, local := t.TempDir(), t.TempDir()
	for dir, token := range map[string]string{mounted: "mounted-token", local: "local-token"} {
		if err := os.WriteFile(filepath.Join(dir, tokenName), []byte(token), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	endpoint, err := discover(func(string) string { return "" }, mounted, local)
	if err != nil || endpoint.URL != DefaultTCPURL || endpoint.Token != "mounted-token" {
		t.Fatalf("Docker Desktop fallback changed: %+v %v", endpoint, err)
	}
	if err := os.Remove(filepath.Join(mounted, tokenName)); err != nil {
		t.Fatal(err)
	}
	endpoint, err = discover(func(string) string { return "" }, mounted, local)
	if err != nil || endpoint.URL != LocalTCPURL || endpoint.Token != "local-token" {
		t.Fatalf("local TCP discovery: %+v %v", endpoint, err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	listener, err := net.Listen("unix", filepath.Join(local, socketName))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	endpoint, err = discover(func(string) string { return "" }, mounted, local)
	if err != nil || endpoint.Socket != filepath.Join(local, socketName) || endpoint.Token != "local-token" {
		t.Fatalf("local socket discovery: %+v %v", endpoint, err)
	}
	// A live local socket must not take over a Docker Desktop mount.
	if err := os.WriteFile(filepath.Join(mounted, tokenName), []byte("mounted-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	endpoint, err = discover(func(string) string { return "" }, mounted, local)
	if err != nil || endpoint.Socket != "" || endpoint.URL != DefaultTCPURL || endpoint.Token != "mounted-token" {
		t.Fatalf("live local gateway displaced mounted host: %+v %v", endpoint, err)
	}
	if err := os.Remove(filepath.Join(mounted, tokenName)); err != nil {
		t.Fatal(err)
	}
	// Explicit container/custom configuration must not silently switch to local.
	explicit := func(name string) string {
		if name == EnvDir {
			return mounted
		}
		return ""
	}
	if _, err := discover(explicit, mounted, local); err == nil {
		t.Fatal("explicit missing directory silently used local credentials")
	}
}

func TestDiscoveryExplicitEndpointAndTokenWinOverLocal(t *testing.T) {
	mounted, local := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(local, tokenName), []byte("local-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{EnvURL: "http://127.0.0.1:12345/mcp", EnvToken: "explicit-token"}
	endpoint, err := discover(func(name string) string { return values[name] }, mounted, local)
	if err != nil || endpoint.URL != values[EnvURL] || endpoint.Token != values[EnvToken] {
		t.Fatalf("explicit settings changed: %+v %v", endpoint, err)
	}
}

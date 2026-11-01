package gateway_test

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csautter/dev-alchemy/internal/gateway"
	"github.com/csautter/dev-alchemy/internal/gateway/gatewaytest"
)

func TestCLIResolverSharesConcurrentLookupsPerTarget(t *testing.T) {
	fake := gatewaytest.BuildFakeCLI(t)
	work, state := t.TempDir(), t.TempDir()
	pinned, err := gateway.PinBinary(fake, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"ubuntu-server-amd64", "windows11--amd64"} {
		if err := os.WriteFile(filepath.Join(state, id), []byte("running"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	record := filepath.Join(t.TempDir(), "record.jsonl")
	env := append(os.Environ(), "FAKE_CLI_STATE="+state, "FAKE_CLI_RECORD="+record, "FAKE_VM_INFO_DELAY=300ms", "FAKE_VM_PASSWORD=guest-pass")
	redactor := gateway.NewRedactor()
	resolver := &gateway.CLIManagedVMResolver{Runner: &gateway.Runner{Binary: pinned, Env: env}, Work: work, Redactor: redactor}

	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 6; i++ {
		for _, target := range [][3]string{{"ubuntu", "server", "amd64"}, {"windows11", "", "amd64"}} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				vm, err := resolver.ResolveFresh(context.Background(), target[0], target[1], target[2])
				if err == nil && vm.Password != "guest-pass" {
					err = os.ErrInvalid
				}
				errs <- err
			}()
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := countVMInfo(t, record); got > 4 {
		t.Fatalf("concurrent lookups were not shared: %d CLI calls for 12 requests", got)
	}
	if got := redactor.String("guest-pass"); got != "***" {
		t.Fatalf("validated guest password not registered: %q", got)
	}

	// Cancelling one in-flight caller must not cancel another caller's shared CLI.
	before := countVMInfo(t, record)
	ctx, cancel := context.WithCancel(context.Background())
	cancelled := make(chan error, 1)
	go func() { _, err := resolver.ResolveFresh(ctx, "ubuntu", "server", "amd64"); cancelled <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for countVMInfo(t, record) == before {
		if time.Now().After(deadline) {
			t.Fatal("lookup did not start")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-cancelled; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled caller: %v", err)
	}
	if _, err := resolver.ResolveFresh(context.Background(), "ubuntu", "server", "amd64"); err != nil {
		t.Fatalf("shared lookup: %v", err)
	}
	if got := countVMInfo(t, record); got != before+1 {
		t.Fatalf("shared lookup ran %d CLI calls", got-before)
	}

}

func TestCLIResolverDoesNotLearnSecretsFromInvalidResponses(t *testing.T) {
	fake := gatewaytest.BuildFakeCLI(t)
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "ubuntu-server-amd64"), []byte("running"), 0o600); err != nil {
		t.Fatal(err)
	}
	pinned, err := gateway.PinBinary(fake, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	// The fake reports a non-IP address, so the response is rejected.
	env := append(os.Environ(), "FAKE_CLI_STATE="+state, "FAKE_VM_ADDRESS=not-an-ip", "FAKE_VM_PASSWORD=x")
	redactor := gateway.NewRedactor()
	resolver := &gateway.CLIManagedVMResolver{Runner: &gateway.Runner{Binary: pinned, Env: env}, Work: t.TempDir(), Redactor: redactor}
	if _, err := resolver.Resolve(context.Background(), "ubuntu", "server", "amd64"); err == nil {
		t.Fatal("invalid address accepted")
	}
	if got := redactor.String("x marks"); got != "x marks" {
		t.Fatalf("secret learned from rejected response: %q", got)
	}
}

func countVMInfo(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path) // #nosec G304 -- test temp file.
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), `"argv":["vm","info"`) {
			count++
		}
	}
	return count
}

func TestCLIResolverReportsLookupFailures(t *testing.T) {
	fake := gatewaytest.BuildFakeCLI(t)
	state := t.TempDir()
	pinned, err := gateway.PinBinary(fake, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	var console gatewaytest.SyncBuffer
	resolver := &gateway.CLIManagedVMResolver{Runner: &gateway.Runner{Binary: pinned, Env: append(os.Environ(), "FAKE_CLI_STATE="+state, "FAKE_VM_INFO_WARNING=warning: guest agent slow")}, Work: t.TempDir(), Redactor: gateway.NewRedactor(), Console: &console}

	// The CLI's own reason reaches the agent and the operator console.
	_, err = resolver.ResolveFresh(context.Background(), "ubuntu", "server", "amd64")
	if !errors.Is(err, gateway.ErrVMAccessUnavailable) || !strings.Contains(err.Error(), "exited with 1: VM is not running") {
		t.Fatalf("lookup error: %v", err)
	}
	if !strings.Contains(console.String(), "vm info ubuntu/server/amd64 failed: ") || !strings.Contains(console.String(), "VM is not running") {
		t.Fatalf("console: %q", console.String())
	}

	// Warnings printed before the JSON document do not break the lookup.
	if err := os.WriteFile(filepath.Join(state, "ubuntu-server-amd64"), []byte("running"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveFresh(context.Background(), "ubuntu", "server", "amd64"); err != nil {
		t.Fatalf("lookup with a leading warning: %v", err)
	}
}

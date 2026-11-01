package gateway_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/csautter/dev-alchemy/internal/gateway"
	"github.com/csautter/dev-alchemy/internal/gateway/gatewaytest"
)

func TestSharedStateCompatibility(t *testing.T) {
	fake := gatewaytest.BuildFakeCLI(t)
	state := t.TempDir()
	pinned, err := gateway.PinBinary(fake, state, "")
	if err != nil {
		t.Fatal(err)
	}
	dirs := gateway.NewManagedDirs(gateway.Config{StateDir: state, DataDir: t.TempDir()})
	if err := dirs.Ensure(); err != nil {
		t.Fatal(err)
	}
	// Keep discovery independent of the developer's installed CLI.
	t.Setenv("PATH", t.TempDir())
	for _, tc := range []struct {
		metadata     string
		shared, fail bool
	}{
		{`{"schema_version":1,"lock_version":1,"version":"test"}`, true, false},
		{`{"schema_version":2,"lock_version":1,"version":"test"}`, true, true},
		{`{"schema_version":1,"version":"old"}`, true, true},
		{`not json`, true, true},
		{`not json`, false, false},
	} {
		env := append(os.Environ(), "FAKE_CLI_METADATA="+tc.metadata)
		err := gateway.CheckCompatibility(context.Background(), pinned, dirs, env, env, tc.shared, io.Discard)
		if (err != nil) != tc.fail {
			t.Fatalf("metadata %s, shared %v: %v", tc.metadata, tc.shared, err)
		}
	}
}

func TestInstalledCLIContract(t *testing.T) {
	fake := gatewaytest.BuildFakeCLI(t)
	state := t.TempDir()
	pinned, err := gateway.PinBinary(fake, state, "")
	if err != nil {
		t.Fatal(err)
	}
	dirs := gateway.NewManagedDirs(gateway.Config{StateDir: state, DataDir: t.TempDir()})
	if err := dirs.Ensure(); err != nil {
		t.Fatal(err)
	}
	// Put a copy of the fake on PATH as the "installed" sailwright.
	bin := t.TempDir()
	name := "sailwright"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	data, err := os.ReadFile(fake) // #nosec G304 -- test binary.
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, name), data, 0o700); err != nil { // #nosec G306 -- test executable.
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	good := append(os.Environ(), `FAKE_CLI_METADATA={"schema_version":1,"lock_version":1,"version":"pinned"}`)
	for _, tc := range []struct {
		name, installed string
		fail            bool
		warning         string
	}{
		{"old CLI without contract only warns", `not json`, false, "predates cross-process locking"},
		{"incompatible reported schema fails", `{"schema_version":2,"lock_version":1,"version":"new"}`, true, ""},
		{"version difference warns", `{"schema_version":1,"lock_version":1,"version":"other"}`, false, "differs from installed"},
	} {
		var out strings.Builder
		installedEnv := append(os.Environ(), "FAKE_CLI_METADATA="+tc.installed)
		err := gateway.CheckCompatibility(context.Background(), pinned, dirs, good, installedEnv, true, &out)
		if (err != nil) != tc.fail || !strings.Contains(out.String(), tc.warning) {
			t.Fatalf("%s: err=%v output=%q", tc.name, err, out.String())
		}
	}
}

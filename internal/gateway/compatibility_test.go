package gateway_test

import (
	"context"
	"io"
	"os"
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
		err := gateway.CheckCompatibility(context.Background(), pinned, dirs, env, tc.shared, io.Discard)
		if (err != nil) != tc.fail {
			t.Fatalf("metadata %s, shared %v: %v", tc.metadata, tc.shared, err)
		}
	}
}

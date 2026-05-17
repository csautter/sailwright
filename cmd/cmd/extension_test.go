package cmd

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	alchemy_extension "github.com/csautter/dev-alchemy/pkg/extension"
	"github.com/spf13/cobra"
)

func TestPrintAvailableExtensions(t *testing.T) {
	var output bytes.Buffer

	err := printAvailableExtensions(&output, []alchemy_extension.Executable{
		{Name: "analyzer", Path: "/usr/local/bin/alchemy-analyzer"},
	})
	if err != nil {
		t.Fatalf("expected extension listing to succeed, got %v", err)
	}

	got := output.String()
	for _, want := range []string{"NAME", "EXECUTABLE", "analyzer", "/usr/local/bin/alchemy-analyzer"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, got)
		}
	}
}

func TestPrintAvailableExtensionsHandlesEmptyPath(t *testing.T) {
	var output bytes.Buffer

	if err := printAvailableExtensions(&output, nil); err != nil {
		t.Fatalf("expected empty extension listing to succeed, got %v", err)
	}
	if !strings.Contains(output.String(), "No Dev Alchemy extensions found") {
		t.Fatalf("expected empty extension message, got %q", output.String())
	}
}

func TestExtensionRunCommandPassesArgumentsAfterDash(t *testing.T) {
	previousRunFunc := extensionRunFunc
	previousRootOut := rootCmd.OutOrStdout()
	previousRootErr := rootCmd.ErrOrStderr()
	t.Cleanup(func() {
		extensionRunFunc = previousRunFunc
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(previousRootOut)
		rootCmd.SetErr(previousRootErr)
	})

	var capturedOptions alchemy_extension.RunOptions
	extensionRunFunc = func(ctx context.Context, options alchemy_extension.RunOptions) error {
		capturedOptions = options
		return nil
	}

	rootCmd.SetArgs([]string{
		"extension",
		"run",
		"analyzer",
		"--",
		"scan",
		"--out",
		"snapshot.json",
	})
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected extension run to execute successfully, got %v", err)
	}
	if capturedOptions.Name != "analyzer" {
		t.Fatalf("expected analyzer extension, got %q", capturedOptions.Name)
	}
	if got := strings.Join(capturedOptions.Args, " "); got != "scan --out snapshot.json" {
		t.Fatalf("expected args after -- to pass through, got %q", got)
	}
}

func TestExtensionRunCommandPassesPositionalArgumentsWithoutDash(t *testing.T) {
	name, args := splitExtensionRunArgs(&cobra.Command{}, []string{"analyzer", "manifest"})
	if name != "analyzer" {
		t.Fatalf("expected analyzer extension, got %q", name)
	}
	if got := strings.Join(args, " "); got != "manifest" {
		t.Fatalf("expected positional extension args to pass through, got %q", got)
	}
}

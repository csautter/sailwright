package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	alchemy_extension "github.com/csautter/dev-alchemy/pkg/extension"
	"github.com/spf13/cobra"
)

func TestPrintAvailableExtensions(t *testing.T) {
	var output bytes.Buffer

	err := printAvailableExtensions(&output, []alchemy_extension.Executable{
		{Name: "analyzer", Path: "/usr/local/bin/sailwright-analyzer"},
	})
	if err != nil {
		t.Fatalf("expected extension listing to succeed, got %v", err)
	}

	got := output.String()
	for _, want := range []string{"NAME", "EXECUTABLE", "analyzer", "/usr/local/bin/sailwright-analyzer"} {
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
	if !strings.Contains(output.String(), "No Sailwright extensions found") {
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

func TestExtensionRunCommandForwardsExtensionExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell to produce a process exit code")
	}

	previousRunFunc := extensionRunFunc
	previousRootOut := rootCmd.OutOrStdout()
	previousRootErr := rootCmd.ErrOrStderr()
	t.Cleanup(func() {
		extensionRunFunc = previousRunFunc
		extensionRunCmd.SilenceUsage = false
		extensionRunCmd.SilenceErrors = false
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(previousRootOut)
		rootCmd.SetErr(previousRootErr)
	})

	extensionRunFunc = func(ctx context.Context, options alchemy_extension.RunOptions) error {
		return fmt.Errorf("run extension %q: %w", options.Name, exec.Command("sh", "-c", "exit 4").Run())
	}

	var output bytes.Buffer
	rootCmd.SetArgs([]string{"extension", "run", "analyzer"})
	rootCmd.SetOut(&output)
	rootCmd.SetErr(&output)

	err := rootCmd.Execute()
	var exitErr *exitCodeError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected exitCodeError, got %v", err)
	}
	if exitErr.code != 4 {
		t.Fatalf("expected exit code 4, got %d", exitErr.code)
	}
	if output.Len() != 0 {
		t.Fatalf("expected no usage or error output for extension failures, got:\n%s", output.String())
	}
}

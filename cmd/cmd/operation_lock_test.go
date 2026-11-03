package cmd

import (
	"errors"
	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	"path/filepath"
	"testing"
	"time"

	"github.com/csautter/dev-alchemy/internal/operationlock"
	"github.com/spf13/cobra"
)

func TestOperationLockTarget(t *testing.T) {
	newCommand := func(typ, arch string) *cobra.Command {
		command := &cobra.Command{Use: "test"}
		command.Flags().String("type", typ, "")
		command.Flags().String("arch", arch, "")
		return command
	}
	for _, tc := range []struct {
		args     []string
		typ      string
		transfer bool
		want     string
		locked   bool
	}{
		{nil, "server", false, "", false},
		{[]string{"local"}, "server", false, "", false},
		{[]string{"all"}, "server", false, "", true},
		{[]string{"ubuntu"}, "server", false, "ubuntu/server/amd64", true},
		{[]string{"ubuntu"}, "desktop", false, "ubuntu/desktop/amd64", true},
		// The type flag is irrelevant outside Ubuntu, so create and start share a key.
		{[]string{"windows11"}, "server", false, "windows11//amd64", true},
		{[]string{"ghcr.io/example/image:1"}, "server", true, "", true},
	} {
		got, locked := operationLockTarget(newCommand(tc.typ, "amd64"), tc.args, tc.transfer)
		if got != tc.want || locked != tc.locked {
			t.Errorf("%v transfer=%v: got %q locked=%v", tc.args, tc.transfer, got, locked)
		}
	}
}

func TestOperationLockTimeout(t *testing.T) {
	if got, err := operationLockTimeout(""); err != nil || got != 0 {
		t.Fatal(got, err)
	}
	if got, err := operationLockTimeout(" 30s "); err != nil || got != 30*time.Second {
		t.Fatal(got, err)
	}
	for _, bad := range []string{"soon", "-1s"} {
		if _, err := operationLockTimeout(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestWrappedOperationReportsBusyTarget(t *testing.T) {
	appData := t.TempDir()
	ran := false
	command := &cobra.Command{Use: "start", RunE: func(*cobra.Command, []string) error { ran = true; return nil }}
	command.Flags().String("type", "server", "")
	command.Flags().String("arch", "amd64", "")
	wrapOperationLocks([]*cobra.Command{command}, func() string { return appData })

	release, err := operationlock.Acquire(appData, "ubuntu/server/amd64", "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(lockTimeoutEnvVar, "250ms")
	started := time.Now()
	err = command.RunE(command, []string{"ubuntu"})
	if !errors.Is(err, operationlock.ErrBusy) || ran {
		t.Fatalf("expected busy error, got %v (ran=%v)", err, ran)
	}
	if time.Since(started) < 200*time.Millisecond {
		t.Fatal("did not wait for the configured lock timeout")
	}
	if err := command.RunE(command, []string{"windows11"}); err != nil || !ran {
		t.Fatalf("other target blocked: %v", err)
	}
	release()
	ran = false
	if err := command.RunE(command, []string{"ubuntu"}); err != nil || !ran {
		t.Fatalf("released target still busy: %v", err)
	}
}

func TestSharedCacheLocksSpanAppDataDirectories(t *testing.T) {
	oldBuild, oldCreate := buildCmd, createCmd
	dirs := alchemy_build.GetDirectoriesInstance()
	oldCache := dirs.CacheDir
	dirs.CacheDir = t.TempDir()
	defer func() { buildCmd = oldBuild; createCmd = oldCreate; dirs.CacheDir = oldCache }()
	t.Setenv(lockTimeoutEnvVar, "0s")
	command := func(verb string, run func(*cobra.Command, []string) error) *cobra.Command {
		c := &cobra.Command{Use: verb, RunE: run}
		c.Flags().String("type", "server", "")
		c.Flags().String("arch", "amd64", "")
		return c
	}
	built, cloned := false, false
	buildCmd = command("build", func(*cobra.Command, []string) error { built = true; return nil })
	createCmd = command("create", func(*cobra.Command, []string) error { cloned = true; return nil })
	addInstanceFlag(createCmd)
	createCmd.Flags().Set("name", "b")
	appA, appB := t.TempDir(), t.TempDir()
	wrapOperationLocks([]*cobra.Command{buildCmd}, func() string { return appA })
	wrapOperationLocks([]*cobra.Command{createCmd}, func() string { return appB })
	release, err := operationlock.AcquireShared(filepath.Join(dirs.CacheDir, "image-operation-locks"), "ubuntu/server/amd64", "clone a")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := buildCmd.RunE(buildCmd, []string{"ubuntu"}); !errors.Is(err, operationlock.ErrBusy) || built {
		t.Fatalf("build ignored shared cache reader: %v", err)
	}
	if err := createCmd.RunE(createCmd, []string{"ubuntu"}); err != nil || !cloned {
		t.Fatalf("independent clone blocked: %v", err)
	}
}

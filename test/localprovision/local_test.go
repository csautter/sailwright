package localprovision_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLocalProvisionIntegration(t *testing.T) {
	if os.Getenv("SAILWRIGHT_LOCAL_INTEGRATION") != "1" {
		t.Skip("set SAILWRIGHT_LOCAL_INTEGRATION=1 to run real local Ansible tests")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("local Ansible integration requires Linux or macOS")
	}
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-playbook is not installed")
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	bin := os.Getenv("SAILWRIGHT_LOCAL_TEST_BIN")
	if bin == "" {
		bin = filepath.Join(work, "sailwright")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		build := exec.CommandContext(ctx, "go", "build", "-o", bin, "./cmd/main.go")
		build.Dir = repo
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build CLI: %v\n%s", err, output)
		}
	}
	bin, err = filepath.Abs(bin)
	if err != nil {
		t.Fatal(err)
	}
	// Run outside the checkout to exercise the embedded localhost inventory and
	// role-source setup used by a release binary, with isolated configuration.
	env := append(os.Environ(),
		"DEV_ALCHEMY_APP_DATA_DIR="+filepath.Join(work, "data"),
		"DEV_ALCHEMY_CONFIG_DIR="+filepath.Join(work, "config"),
		"ANSIBLE_CONFIG="+filepath.Join(repo, "ansible.cfg"),
		"ANSIBLE_STDOUT_CALLBACK=default",
	)
	fixtureFile := filepath.Join(work, "fixture.txt")
	ansi := regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	run := func(playbook string, check bool) (string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		args := []string{"provision", "local", "--yes", "--verbosity", "0", "--playbook", filepath.Join(repo, "test/localprovision/testdata", playbook)}
		if check {
			args = append(args, "--check")
		}
		args = append(args, "--", "-e", "fixture_file="+fixtureFile)
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir = work
		cmd.Env = env
		output, err := cmd.CombinedOutput()
		t.Logf("%s check=%t:\n%s", playbook, check, output)
		return ansi.ReplaceAllString(string(output), ""), err
	}
	recap := regexp.MustCompile(`ok=\d+\s+changed=(\d+)\s+unreachable=0\s+failed=0`)
	for _, phase := range []struct {
		name    string
		check   bool
		changed string
	}{{"check", true, "1"}, {"apply", false, "1"}, {"idempotency", false, "0"}} {
		t.Run(phase.name, func(t *testing.T) {
			output, err := run("setup.yml", phase.check)
			if err != nil {
				t.Fatalf("local provisioning failed: %v", err)
			}
			match := recap.FindStringSubmatch(output)
			if len(match) != 2 || match[1] != phase.changed {
				t.Fatalf("expected successful recap with changed=%s:\n%s", phase.changed, output)
			}
			if !strings.Contains(output, "local-provision-stdout-marker") {
				t.Fatal("Ansible stdout did not reach CLI output")
			}
			content, err := os.ReadFile(fixtureFile)
			if phase.check {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("check mode wrote the fixture: %v", err)
				}
			} else if err != nil || string(content) != "local provisioning fixture\n" {
				t.Fatalf("fixture content=%q, err=%v", content, err)
			}
		})
		if t.Failed() {
			t.FailNow()
		}
	}
	t.Run("failure", func(t *testing.T) {
		output, err := run("failure.yml", false)
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			t.Fatalf("expected CLI exit 1 for Ansible failure, got %v", err)
		}
		for _, expected := range []string{"local-provision-failure-marker", "failed=1", "exit status 2", "failed provisioning local host"} {
			if !strings.Contains(output, expected) {
				t.Errorf("missing %q in failure output:\n%s", expected, output)
			}
		}
	})
}

//go:build unix

package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeBuildConfig returns a build without dependencies whose script writes the staged
// artifact, records its process group and then runs body.
func fakeBuildConfig(t *testing.T, root string, body string) (VirtualMachineConfig, []string, string) {
	t.Helper()
	artifact := filepath.Join(root, "cache", "fake", "disk.qcow2")
	staged := stagedBuildArtifactPath(artifact, time.Now().UnixNano())
	pgidFile := filepath.Join(root, "build.pgid")
	script := `mkdir -p "$(dirname "$1")"; printf partial > "$1"; ps -o pgid= $$ > "$2"; ` + body
	config := VirtualMachineConfig{
		OS:                     "fake-os",
		Arch:                   "amd64",
		VncPort:                5990,
		ExpectedBuildArtifacts: []string{artifact},
		StagedBuildArtifacts:   []string{staged},
		DetachedFromTerminal:   true,
	}
	return config, []string{"-c", script, "fake-build", staged, pgidFile}, pgidFile
}

func readProcessGroupID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			if id, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && id > 0 {
				return id
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("build did not write its process group to %s", path)
	return 0
}

func TestRunBuildScriptCancelStopsBuildAndDiscardsArtifact(t *testing.T) {
	root := useTestDirectories(t)
	stubStdinIsTerminal(t, false)
	// The trap mimics Packer, which ignores the first SIGTERM while it cleans up.
	config, args, pgidFile := fakeBuildConfig(t, root, `trap '' TERM; sleep 300 & wait`)

	h, ctx := newInterruptHandler(context.Background(), nil, func(int) {})
	done := make(chan error, 1)
	go func() { done <- RunBuildScript(ctx, config, "sh", args) }()

	pgid := readProcessGroupID(t, pgidFile)
	if state, _ := artifactsState(config.ExpectedBuildArtifacts); state.Usable() {
		t.Fatalf("a running build must not look usable, got %q", state)
	}
	h.escalate(os.Interrupt)

	select {
	case err := <-done:
		if !errors.Is(err, ErrInterrupted) {
			t.Fatalf("expected ErrInterrupted, got %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("RunBuildScript did not return after the interrupt")
	}

	if err := syscall.Kill(-pgid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("expected the build's process group %d to be gone, got %v", pgid, err)
	}
	assertArtifactState(t, ArtifactStateMissing, config.ExpectedBuildArtifacts...)
}

func TestRunBuildScriptPromotesArtifactOnlyAfterSuccess(t *testing.T) {
	root := useTestDirectories(t)
	config, args, _ := fakeBuildConfig(t, root, `exit 0`)

	if err := RunBuildScript(context.Background(), config, "sh", args); err != nil {
		t.Fatal(err)
	}
	assertArtifactState(t, ArtifactStateExists, config.ExpectedBuildArtifacts...)
}

func TestRunBuildScriptStartOnlyProbe(t *testing.T) {
	for _, cached := range []bool{false, true} {
		for _, exitCode := range []int{0, 3} {
			t.Run(fmt.Sprintf("cached=%t/exit=%d", cached, exitCode), func(t *testing.T) {
				root := useTestDirectories(t)
				t.Setenv("DEV_ALCHEMY_PACKER_START_ONLY", "true")
				config, _, _ := fakeBuildConfig(t, root, "")
				artifact := config.ExpectedBuildArtifacts[0]
				if cached {
					if err := os.MkdirAll(filepath.Dir(artifact), 0755); err != nil {
						t.Fatal(err)
					}
					writeTestFile(t, artifact, "cached image")
					if err := MarkBuildArtifactsComplete(config.ExpectedBuildArtifacts); err != nil {
						t.Fatal(err)
					}
				}
				// Like the real wrapper, this probe exits without producing an image.
				ran := filepath.Join(root, "probe-ran")
				args := []string{"-c", fmt.Sprintf(`touch "$1"; exit %d`, exitCode), "probe", ran}
				err := RunBuildScript(context.Background(), config, "sh", args)
				if (err != nil) != (exitCode != 0) {
					t.Fatalf("probe exit %d: got %v", exitCode, err)
				}
				if _, err := os.Stat(ran); err != nil {
					t.Fatalf("probe must run even with a cached image: %v", err)
				}
				if cached {
					content, err := os.ReadFile(artifact)
					if err != nil || string(content) != "cached image" {
						t.Fatalf("cached image changed: %q, %v", content, err)
					}
					assertArtifactState(t, ArtifactStateExists, artifact)
				} else {
					assertArtifactState(t, ArtifactStateMissing, artifact)
				}
			})
		}
	}
}

func TestRunBuildScriptStartOnlyOverride(t *testing.T) {
	root := useTestDirectories(t)
	t.Setenv("DEV_ALCHEMY_PACKER_START_ONLY", "true")
	config, args, _ := fakeBuildConfig(t, root, `exit 0`)
	if err := RunBuildScript(context.Background(), config, "sh", args, "DEV_ALCHEMY_PACKER_START_ONLY=false"); err != nil {
		t.Fatal(err)
	}
	assertArtifactState(t, ArtifactStateExists, config.ExpectedBuildArtifacts...)
}

func TestRunBuildScriptFailureLeavesNoArtifact(t *testing.T) {
	root := useTestDirectories(t)
	config, args, _ := fakeBuildConfig(t, root, `exit 3`)

	if err := RunBuildScript(context.Background(), config, "sh", args); err == nil {
		t.Fatal("expected the failed build to return an error")
	}
	assertArtifactState(t, ArtifactStateMissing, config.ExpectedBuildArtifacts...)
}

func TestRunBuildScriptDoesNotStartAfterInterrupt(t *testing.T) {
	root := useTestDirectories(t)
	config, args, pgidFile := fakeBuildConfig(t, root, `exit 0`)

	h, ctx := newInterruptHandler(context.Background(), nil, func(int) {})
	h.escalate(os.Interrupt)
	if err := RunBuildScript(ctx, config, "sh", args); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("expected ErrInterrupted, got %v", err)
	}
	if _, err := os.Stat(pgidFile); !os.IsNotExist(err) {
		t.Fatal("the build command must not start after an interrupt")
	}
}

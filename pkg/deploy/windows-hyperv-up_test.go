package deploy

import (
	"errors"
	"strings"
	"testing"
	"time"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

func hypervUpTestFixture() (alchemy_build.VirtualMachineConfig, hypervVagrantDeploySettings) {
	config := alchemy_build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "amd64"}
	settings := hypervVagrantDeploySettings{
		VagrantDir: "vagrant-dir",
		VagrantEnv: []string{hypervVagrantVMNameEnvVar + "=linux-ubuntu-server-packer"},
	}
	return config, settings
}

func TestRunHypervVagrantUpSucceedsWithoutProbe(t *testing.T) {
	restore := stubHypervStopDependencies(t)
	defer restore()
	ups := 0
	runHypervVagrantCommandWithEnv = func(_ string, _ time.Duration, _ string, _ []string, _ []string, _ string) error {
		ups++
		return nil
	}
	runHypervCommandWithCombinedOutput = func(string, time.Duration, string, []string) (string, error) {
		t.Fatal("did not expect a nested virtualization probe after a successful start")
		return "", nil
	}
	config, settings := hypervUpTestFixture()
	if err := runHypervVagrantUp(config, settings); err != nil {
		t.Fatal(err)
	}
	if ups != 1 {
		t.Fatalf("expected one vagrant up, got %d", ups)
	}
}

func TestRunHypervVagrantUpRetriesWithoutNestedVirtualization(t *testing.T) {
	restore := stubHypervStopDependencies(t)
	defer restore()
	var envs [][]string
	runHypervVagrantCommandWithEnv = func(_ string, _ time.Duration, executable string, args []string, env []string, _ string) error {
		if executable != "vagrant" || strings.Join(args, " ") != "up --provider hyperv" {
			t.Fatalf("unexpected command %s %v", executable, args)
		}
		envs = append(envs, env)
		if len(envs) == 1 {
			return errors.New("platform does not support nested virtualization")
		}
		return nil
	}
	runHypervCommandWithCombinedOutput = func(_ string, _ time.Duration, executable string, args []string) (string, error) {
		script := args[len(args)-1]
		if executable != "powershell" || !strings.Contains(script, "'linux-ubuntu-server-packer'") || !strings.Contains(script, "Id=3108") {
			t.Fatalf("unexpected probe %s %v", executable, args)
		}
		return "disabled\r\n", nil
	}
	config, settings := hypervUpTestFixture()
	if err := runHypervVagrantUp(config, settings); err != nil {
		t.Fatal(err)
	}
	if len(envs) != 2 {
		t.Fatalf("expected a retry, got %d vagrant up calls", len(envs))
	}
	assertEnvContainsEntry(t, envs[1], hypervNestedVirtualizationEnvVar+"=false")
	for _, entry := range envs[0] {
		if strings.HasPrefix(entry, hypervNestedVirtualizationEnvVar+"=") {
			t.Fatalf("first attempt must keep the Vagrantfile default, got %q", entry)
		}
	}
	if len(settings.VagrantEnv) != 1 {
		t.Fatalf("retry must not modify the shared settings env: %v", settings.VagrantEnv)
	}
}

func TestRunHypervVagrantUpKeepsOtherFailures(t *testing.T) {
	restore := stubHypervStopDependencies(t)
	defer restore()
	ups := 0
	upErr := errors.New("box is broken")
	runHypervVagrantCommandWithEnv = func(_ string, _ time.Duration, _ string, _ []string, _ []string, _ string) error {
		ups++
		return upErr
	}
	runHypervCommandWithCombinedOutput = func(string, time.Duration, string, []string) (string, error) {
		return "unchanged\r\n", nil
	}
	config, settings := hypervUpTestFixture()
	if err := runHypervVagrantUp(config, settings); !errors.Is(err, upErr) {
		t.Fatalf("expected the original error, got %v", err)
	}
	if ups != 1 {
		t.Fatalf("did not expect a retry, got %d vagrant up calls", ups)
	}
}

func TestRunHypervVagrantUpReportsFailedRetry(t *testing.T) {
	restore := stubHypervStopDependencies(t)
	defer restore()
	upErr := errors.New("nested refused")
	ups := 0
	runHypervVagrantCommandWithEnv = func(_ string, _ time.Duration, _ string, _ []string, _ []string, _ string) error {
		ups++
		if ups == 1 {
			return upErr
		}
		return errors.New("still broken")
	}
	runHypervCommandWithCombinedOutput = func(string, time.Duration, string, []string) (string, error) {
		return "disabled", nil
	}
	config, settings := hypervUpTestFixture()
	err := runHypervVagrantUp(config, settings)
	if !errors.Is(err, upErr) || !strings.Contains(err.Error(), "still broken") {
		t.Fatalf("expected both errors, got %v", err)
	}
}

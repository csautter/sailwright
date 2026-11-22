package provision

import (
	"errors"
	"strings"
	"testing"
	"time"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	alchemy_deploy "github.com/csautter/dev-alchemy/pkg/deploy"
)

func stubTartProvisionPath(t *testing.T, readiness func(string, string) error) *int {
	t.Helper()
	previousInspect := inspectProvisionTarget
	previousDiscover := discoverTartProvisionAddress
	previousWait := waitForTartProvisionSSHPort
	previousReadiness := ensureTartGuestNetworkForProvision
	previousAnsible := runAnsibleProvisionCommandFunc
	previousLookPath := lookPathProvisionCommand
	t.Cleanup(func() {
		inspectProvisionTarget = previousInspect
		discoverTartProvisionAddress = previousDiscover
		waitForTartProvisionSSHPort = previousWait
		ensureTartGuestNetworkForProvision = previousReadiness
		runAnsibleProvisionCommandFunc = previousAnsible
		lookPathProvisionCommand = previousLookPath
	})

	inspectProvisionTarget = func(alchemy_build.VirtualMachineConfig) (alchemy_deploy.StartTargetState, error) {
		return alchemy_deploy.StartTargetState{Exists: true, Running: true, State: "running"}, nil
	}
	discoverTartProvisionAddress = func(string, alchemy_build.VirtualMachineConfig) (string, error) {
		return "192.0.2.2", nil
	}
	waitForTartProvisionSSHPort = func(string) error { return nil }
	ensureTartGuestNetworkForProvision = readiness
	lookPathProvisionCommand = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	ansibleCalls := 0
	runAnsibleProvisionCommandFunc = func(string, []string, time.Duration, string) error {
		ansibleCalls++
		return nil
	}
	return &ansibleCalls
}

func tartProvisionVM() alchemy_build.VirtualMachineConfig {
	return alchemy_build.VirtualMachineConfig{
		OS:                   "macos",
		Arch:                 "arm64",
		HostOs:               alchemy_build.HostOsDarwin,
		VirtualizationEngine: alchemy_build.VirtualizationEngineTart,
	}
}

func TestRunTartMacOSProvision_NetworkFailureRunsNoRoles(t *testing.T) {
	readinessErr := &alchemy_deploy.GuestNetworkError{Report: alchemy_deploy.GuestNetworkReport{
		Guest:  "tahoe-base-alchemy",
		Status: alchemy_deploy.GuestNetworkFailed,
		Stage:  alchemy_deploy.GuestNetworkStageSystemDNS,
	}}
	ansibleCalls := stubTartProvisionPath(t, func(string, string) error { return readinessErr })

	err := runTartMacOSProvision(tartProvisionVM(), ProvisionOptions{Verbosity: defaultAnsibleVerbosity})

	if *ansibleCalls != 0 {
		t.Fatalf("expected no Ansible run after a readiness failure, got %d", *ansibleCalls)
	}
	var netErr *alchemy_deploy.GuestNetworkError
	if !errors.As(err, &netErr) || !strings.Contains(err.Error(), "no Ansible roles were run") {
		t.Fatalf("expected wrapped readiness error, got %v", err)
	}
}

func TestRunTartMacOSProvision_ChecksNetworkBeforeAnsible(t *testing.T) {
	var order []string
	ansibleCalls := stubTartProvisionPath(t, func(string, string) error {
		order = append(order, "readiness")
		return nil
	})
	runAnsibleProvisionCommandFunc = func(string, []string, time.Duration, string) error {
		order = append(order, "ansible")
		*ansibleCalls++
		return nil
	}

	if err := runTartMacOSProvision(tartProvisionVM(), ProvisionOptions{Verbosity: defaultAnsibleVerbosity, Check: true}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(order, ",") != "readiness,ansible" {
		t.Fatalf("expected readiness before Ansible, got %v", order)
	}
}

func TestRunTartMacOSProvision_InternalPlaysSkipNetworkCheck(t *testing.T) {
	readinessCalls := 0
	ansibleCalls := stubTartProvisionPath(t, func(string, string) error {
		readinessCalls++
		return errors.New("must not be called")
	})

	err := runTartMacOSProvision(tartProvisionVM(), ProvisionOptions{Verbosity: defaultAnsibleVerbosity, skipGuestNetworkReadiness: true})
	if err != nil || readinessCalls != 0 || *ansibleCalls != 1 {
		t.Fatalf("expected internal play without readiness check, got err=%v readiness=%d ansible=%d", err, readinessCalls, *ansibleCalls)
	}
}

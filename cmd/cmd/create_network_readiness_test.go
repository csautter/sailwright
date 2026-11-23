package cmd

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	alchemy_deploy "github.com/csautter/dev-alchemy/pkg/deploy"
)

func stubTartCreate(t *testing.T, deployErr error) (*int, *int) {
	t.Helper()
	fakeGuestSecurity(t, "nat", true, false)
	t.Setenv(createStartedFileEnvVar, filepath.Join(t.TempDir(), "started"))
	oldInspector, oldDeploy, oldConfigure, oldRecord := inspectCreateTargetExists, runTartDeployFunc, configureNamedGuestFunc, recordInstanceFunc
	t.Cleanup(func() {
		inspectCreateTargetExists, runTartDeployFunc, configureNamedGuestFunc, recordInstanceFunc = oldInspector, oldDeploy, oldConfigure, oldRecord
	})
	inspectCreateTargetExists = func(alchemy_build.VirtualMachineConfig) (bool, error) { return false, nil }
	runTartDeployFunc = func(alchemy_build.VirtualMachineConfig) error { return deployErr }
	configured, recorded := 0, 0
	configureNamedGuestFunc = func(alchemy_build.VirtualMachineConfig) error { configured++; return nil }
	recordInstanceFunc = func(alchemy_build.VirtualMachineConfig) error { recorded++; return nil }
	return &configured, &recorded
}

func tartCreateVM() alchemy_build.VirtualMachineConfig {
	return alchemy_build.VirtualMachineConfig{OS: "macos", Arch: "arm64", Name: "dev1", HostOs: alchemy_build.HostOsDarwin, VirtualizationEngine: alchemy_build.VirtualizationEngineTart}
}

func TestRunDeployRecordsVMThatFailedNetworkReadiness(t *testing.T) {
	networkErr := fmt.Errorf("Tart VM is running but not network-ready: %w", &alchemy_deploy.GuestNetworkError{Report: alchemy_deploy.GuestNetworkReport{Status: alchemy_deploy.GuestNetworkFailed}})
	configured, recorded := stubTartCreate(t, networkErr)

	err := runDeploy(tartCreateVM())

	var readinessErr *alchemy_deploy.GuestNetworkError
	if !errors.As(err, &readinessErr) {
		t.Fatalf("expected the readiness error to be returned, got %v", err)
	}
	if *configured != 1 || *recorded != 1 {
		t.Fatalf("expected the running VM to be named and recorded, got configured=%d recorded=%d", *configured, *recorded)
	}
}

func TestRunDeployDoesNotRecordVMAfterOtherFailures(t *testing.T) {
	configured, recorded := stubTartCreate(t, errors.New("tart clone failed"))

	if err := runDeploy(tartCreateVM()); err == nil {
		t.Fatal("expected deploy error")
	}
	if *configured != 0 || *recorded != 0 {
		t.Fatalf("expected no naming or record after a clone failure, got configured=%d recorded=%d", *configured, *recorded)
	}
}

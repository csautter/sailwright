package cmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	alchemy_oci "github.com/csautter/dev-alchemy/pkg/oci"
	"github.com/spf13/cobra"
)

func TestResolveOCIVirtualMachineRequiresEngineForAmbiguousTarget(t *testing.T) {
	_, err := resolveOCIVirtualMachine("windows", "windows11", "", "amd64", "")
	if err == nil {
		t.Fatal("expected ambiguous Windows artifact target to require --engine")
	}
	if !strings.Contains(err.Error(), "--engine") {
		t.Fatalf("expected error to mention --engine, got %q", err.Error())
	}
}

func TestResolveOCIVirtualMachineSelectsRequestedEngine(t *testing.T) {
	vm, err := resolveOCIVirtualMachine("windows", "windows11", "", "amd64", "virtualbox")
	if err != nil {
		t.Fatalf("expected virtualbox target to resolve: %v", err)
	}
	if vm.VirtualizationEngine != alchemy_build.VirtualizationEngineVirtualBox {
		t.Fatalf("expected virtualbox engine, got %q", vm.VirtualizationEngine)
	}
}

func TestResolveOCIVirtualMachineRequiresOS(t *testing.T) {
	_, err := resolveOCIVirtualMachine("linux", "", "server", "amd64", "")
	if err == nil {
		t.Fatal("expected missing OS to fail")
	}
	if !strings.Contains(err.Error(), "--os") {
		t.Fatalf("expected error to mention --os, got %q", err.Error())
	}
}

func TestOCIRegistryOptionsReadsPasswordStdin(t *testing.T) {
	previousPassword := ociPassword
	previousPasswordStdin := ociPasswordStdin
	previousUsername := ociUsername
	t.Cleanup(func() {
		ociPassword = previousPassword
		ociPasswordStdin = previousPasswordStdin
		ociUsername = previousUsername
	})

	ociUsername = "user"
	ociPassword = ""
	ociPasswordStdin = true

	command := &cobra.Command{}
	command.SetIn(bytes.NewBufferString("secret\n"))

	options, err := ociRegistryOptions(command)
	if err != nil {
		t.Fatalf("expected password stdin to parse: %v", err)
	}
	if options.Username != "user" {
		t.Fatalf("expected username, got %q", options.Username)
	}
	if options.Password != "secret" {
		t.Fatalf("expected trimmed password, got %q", options.Password)
	}
}

func TestOCIRegistryOptionsIncludeTLSSettings(t *testing.T) {
	previousInsecureSkipTLSVerify := ociInsecureSkipTLSVerify
	previousCAFile := ociCAFile
	t.Cleanup(func() {
		ociInsecureSkipTLSVerify = previousInsecureSkipTLSVerify
		ociCAFile = previousCAFile
	})

	ociInsecureSkipTLSVerify = true
	ociCAFile = "/tmp/dev-alchemy-test-ca.pem"

	options, err := ociRegistryOptions(&cobra.Command{})
	if err != nil {
		t.Fatalf("expected OCI registry options to parse: %v", err)
	}
	if !options.InsecureSkipTLSVerify {
		t.Fatal("expected insecure skip TLS verify option")
	}
	if options.CAFile != "/tmp/dev-alchemy-test-ca.pem" {
		t.Fatalf("expected CA file option, got %q", options.CAFile)
	}
}

func TestConfirmOCIForeignArtifactUsePromptsForConfirmation(t *testing.T) {
	previousAssumeYes := ociAssumeYes
	t.Cleanup(func() {
		ociAssumeYes = previousAssumeYes
	})
	ociAssumeYes = false

	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetIn(bytes.NewBufferString("yes\n"))
	command.SetOut(&output)

	confirmed, err := confirmOCIForeignArtifactUse(command)(context.Background(), testForeignOCIArtifact())
	if err != nil {
		t.Fatalf("expected foreign artifact confirmation to succeed: %v", err)
	}
	if !confirmed {
		t.Fatal("expected foreign artifact confirmation")
	}
	for _, want := range []string{"foreign build artifact", "host_os=darwin", "engine=utm", "host_os=debian", "engine=qemu", "Continue? [y/N]:"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("expected confirmation output to contain %q, got %q", want, output.String())
		}
	}
}

func TestConfirmOCIForeignArtifactUseSkipsPromptWhenYesFlagIsSet(t *testing.T) {
	previousAssumeYes := ociAssumeYes
	previousPrompt := promptForConfirmationFunc
	t.Cleanup(func() {
		ociAssumeYes = previousAssumeYes
		promptForConfirmationFunc = previousPrompt
	})
	ociAssumeYes = true
	promptForConfirmationFunc = func(input io.Reader, output io.Writer, prompt string) (bool, error) {
		t.Fatal("did not expect confirmation prompt when --yes is set")
		return false, nil
	}

	var stderr bytes.Buffer
	command := &cobra.Command{}
	command.SetErr(&stderr)

	confirmed, err := confirmOCIForeignArtifactUse(command)(context.Background(), testForeignOCIArtifact())
	if err != nil {
		t.Fatalf("expected --yes to confirm foreign artifact: %v", err)
	}
	if !confirmed {
		t.Fatal("expected --yes to confirm foreign artifact")
	}
	if !strings.Contains(stderr.String(), "Continuing because --yes is set") {
		t.Fatalf("expected --yes warning output, got %q", stderr.String())
	}
}

func TestOCIProgressReporterWritesStatusToOutput(t *testing.T) {
	var output bytes.Buffer
	reporter := newOCIProgressReporter("pushing", &output)

	reporter.Status("Hashing local artifact /tmp/artifact.qcow2")

	if got := output.String(); !strings.Contains(got, "Hashing local artifact /tmp/artifact.qcow2") {
		t.Fatalf("expected status output, got %q", got)
	}
}

func TestLocalOCIArtifactStateDetectsMissingPartialAndExistingArtifacts(t *testing.T) {
	tempDir := t.TempDir()
	artifactA := filepath.Join(tempDir, "artifact-a.qcow2")
	artifactB := filepath.Join(tempDir, "artifact-b.qcow2")
	vm := alchemy_build.VirtualMachineConfig{
		ExpectedBuildArtifacts: []string{artifactA, artifactB},
	}

	state, err := localOCIArtifactState(vm)
	if err != nil {
		t.Fatalf("expected missing artifact state to inspect cleanly: %v", err)
	}
	if state != "missing" {
		t.Fatalf("expected missing state, got %q", state)
	}

	if err := os.WriteFile(artifactA, []byte("a"), 0o600); err != nil {
		t.Fatalf("failed to write test artifact: %v", err)
	}
	state, err = localOCIArtifactState(vm)
	if err != nil {
		t.Fatalf("expected partial artifact state to inspect cleanly: %v", err)
	}
	if state != "partial" {
		t.Fatalf("expected partial state, got %q", state)
	}

	if err := os.WriteFile(artifactB, []byte("b"), 0o600); err != nil {
		t.Fatalf("failed to write test artifact: %v", err)
	}
	state, err = localOCIArtifactState(vm)
	if err != nil {
		t.Fatalf("expected existing artifact state to inspect cleanly: %v", err)
	}
	if state != "exists" {
		t.Fatalf("expected exists state, got %q", state)
	}

	// A completion marker that no longer matches the artifact (e.g. an interrupted
	// rewrite) makes the set incomplete, which `oci push` refuses.
	if err := alchemy_build.MarkBuildArtifactsComplete([]string{artifactA, artifactB}); err != nil {
		t.Fatalf("failed to mark test artifacts complete: %v", err)
	}
	if err := os.WriteFile(artifactB, []byte("bb"), 0o600); err != nil {
		t.Fatalf("failed to rewrite test artifact: %v", err)
	}
	state, err = localOCIArtifactState(vm)
	if err != nil {
		t.Fatalf("expected incomplete artifact state to inspect cleanly: %v", err)
	}
	if state != "incomplete" {
		t.Fatalf("expected incomplete state, got %q", state)
	}
	row, err := pushListRow(vm)
	if err != nil {
		t.Fatal(err)
	}
	if row[len(row)-1] != "build required" {
		t.Fatalf("expected an incomplete artifact to require a build before push, got %q", row[len(row)-1])
	}
}

func TestOCIListRowsIncludePushAndPullReadiness(t *testing.T) {
	previousInspector := inspectOCIArtifactState
	t.Cleanup(func() {
		inspectOCIArtifactState = previousInspector
	})

	artifactStates := map[string]string{
		"windows11/":     "exists",
		"ubuntu/server":  "missing",
		"ubuntu/desktop": "partial",
	}
	inspectOCIArtifactState = func(vm alchemy_build.VirtualMachineConfig) (string, error) {
		return artifactStates[vm.OS+"/"+vm.UbuntuType], nil
	}

	vms := []alchemy_build.VirtualMachineConfig{
		{
			OS:                   "windows11",
			Arch:                 "amd64",
			HostOs:               alchemy_build.HostOsLinux,
			VirtualizationEngine: alchemy_build.VirtualizationEngineQemu,
		},
		{
			OS:                   "ubuntu",
			UbuntuType:           "server",
			Arch:                 "amd64",
			HostOs:               alchemy_build.HostOsLinux,
			VirtualizationEngine: alchemy_build.VirtualizationEngineQemu,
		},
		{
			OS:                   "ubuntu",
			UbuntuType:           "desktop",
			Arch:                 "amd64",
			HostOs:               alchemy_build.HostOsLinux,
			VirtualizationEngine: alchemy_build.VirtualizationEngineQemu,
		},
	}

	var pushOutput bytes.Buffer
	if err := printVirtualMachineCombinationTable(
		&pushOutput,
		"Available push combinations for host OS: debian",
		"No push combinations are available for host OS debian.",
		vms,
		[]string{"OS", "Type", "Arch", "Artifact", "Push"},
		pushListRow,
	); err != nil {
		t.Fatalf("expected push list table to print: %v", err)
	}
	push := pushOutput.String()
	for _, want := range []string{"Push", "ready to push", "build required", "incomplete"} {
		if !strings.Contains(push, want) {
			t.Fatalf("expected push list output to contain %q, got %q", want, push)
		}
	}

	var pullOutput bytes.Buffer
	if err := printVirtualMachineCombinationTable(
		&pullOutput,
		"Available pull combinations for host OS: debian",
		"No pull combinations are available for host OS debian.",
		vms,
		[]string{"OS", "Type", "Arch", "Artifact", "Pull"},
		pullListRow,
	); err != nil {
		t.Fatalf("expected pull list table to print: %v", err)
	}
	pull := pullOutput.String()
	for _, want := range []string{"Pull", "will replace", "ready to pull", "will replace partial"} {
		if !strings.Contains(pull, want) {
			t.Fatalf("expected pull list output to contain %q, got %q", want, pull)
		}
	}
}

func TestOCICommandsIncludeListSubcommands(t *testing.T) {
	for _, command := range []*cobra.Command{pushCmd, pullCmd} {
		found := false
		for _, child := range command.Commands() {
			if child.Name() == "list" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected %s command to include list subcommand", command.Name())
		}
	}
}

func TestOCICommandsIncludeTLSFlags(t *testing.T) {
	for _, command := range []*cobra.Command{pushCmd, pullCmd} {
		for _, flagName := range []string{"insecure-skip-tls-verify", "ca-file"} {
			if command.Flags().Lookup(flagName) == nil {
				t.Fatalf("expected %s command to include --%s", command.Name(), flagName)
			}
		}
	}
}

func TestPullCommandIncludesForeignArtifactYesFlag(t *testing.T) {
	if pullCmd.Flags().Lookup("yes") == nil {
		t.Fatal("expected pull command to include --yes")
	}
}

func testForeignOCIArtifact() alchemy_oci.ForeignArtifact {
	return alchemy_oci.ForeignArtifact{
		OS:                             "ubuntu",
		UbuntuType:                     "server",
		Arch:                           "arm64",
		SourceHostOS:                   alchemy_build.HostOsDarwin,
		SourceVirtualizationEngine:     alchemy_build.VirtualizationEngineUtm,
		TargetHostOS:                   alchemy_build.HostOsLinux,
		TargetVirtualizationEngine:     alchemy_build.VirtualizationEngineQemu,
		SourceHostOSAnnotation:         "darwin",
		SourceVirtualizationAnnotation: "utm",
	}
}

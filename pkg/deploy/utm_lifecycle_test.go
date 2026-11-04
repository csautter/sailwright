package deploy

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

func TestUtmStartStopUseOverriddenBundleUUID(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("UTM command stubs require a Unix shell")
	}
	root := t.TempDir()
	t.Setenv("SAILWRIGHT_UTM_VM_DIR", filepath.Join(root, "custom bundles"))
	t.Setenv("UTM_TEST_LOG", filepath.Join(root, "commands"))
	t.Setenv("UTM_TEST_STATE", filepath.Join(root, "state"))
	if err := os.WriteFile(filepath.Join(root, "state"), []byte("stopped\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	vm := alchemy_build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "desktop", Arch: "arm64"}
	bundle, err := utmVirtualMachinePath(vm)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bundle, 0o700); err != nil {
		t.Fatal(err)
	}
	stubs := map[string]string{
		"plutil": `#!/bin/sh
printf '%s\n' "$@" >> "$UTM_TEST_LOG"
echo custom-bundle-uuid
`,
		"osascript": `#!/bin/sh
printf '%s\n' "$@" >> "$UTM_TEST_LOG"
for arg in "$@"; do
  case "$arg" in
    'start targetVM') echo started > "$UTM_TEST_STATE";;
    'stop targetVM by request'|'stop targetVM by force') echo stopped > "$UTM_TEST_STATE";;
    'return (status of targetVM) as text') cat "$UTM_TEST_STATE";;
  esac
done
`,
	}
	for name, source := range stubs {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, operation := range []func(alchemy_build.VirtualMachineConfig) error{RunUtmStartOnMacOS, RunUtmStopOnMacOS} {
		if err := operation(vm); err != nil {
			t.Fatal(err)
		}
	}
	commands, err := os.ReadFile(filepath.Join(root, "commands"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{filepath.Join(bundle, "config.plist"), bundle, "custom-bundle-uuid", "virtual machine id (item 2 of argv)"} {
		if !strings.Contains(string(commands), want) {
			t.Errorf("commands must select %q, got %s", want, commands)
		}
	}
	if strings.Contains(string(commands), "virtual machine named") {
		t.Fatal("same-named VMs must not determine the target")
	}
}

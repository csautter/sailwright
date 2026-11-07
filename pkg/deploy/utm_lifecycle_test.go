package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUtmStartStopUseOverriddenBundleUUID(t *testing.T) {
	t.Setenv("SAILWRIGHT_UTM_VM_DIR", filepath.Join(t.TempDir(), "custom bundles"))
	vm, bundle, state := mockMacOSLifecycle(t)
	writeMacFixture(t, filepath.Join(bundle, "config.plist"), newUtmID)
	writeMacFixture(t, filepath.Join(state, "status"), "stopped")
	if err := RunUtmStartOnMacOS(vm); err != nil {
		t.Fatal(err)
	}
	if err := RunUtmStopOnMacOS(vm); err != nil {
		t.Fatal(err)
	}
	commands, err := os.ReadFile(filepath.Join(state, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{filepath.Join(bundle, "config.plist"), bundle, newUtmID, `set targetVM to virtual machine id "` + newUtmID + `"`, "stop targetVM by request"} {
		if !strings.Contains(string(commands), want) {
			t.Errorf("commands must select %q, got %s", want, commands)
		}
	}
	if strings.Contains(string(commands), "virtual machine named") {
		t.Fatal("same-named VMs must not determine the target")
	}
}

package deploy

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

const oldUtmID = "aaaaaaaa-1111-2222-3333-444444444444"
const newUtmID = "bbbbbbbb-1111-2222-3333-444444444444"

// Model the destructive behavior of UTM's delete command: it removes whatever
// currently occupies its registered path, even if the bundle UUID has changed.
func mockMacOSLifecycle(t *testing.T) (alchemy_build.VirtualMachineConfig, string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("mock commands use POSIX shell")
	}
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("TART_HOME", filepath.Join(root, "tart"))
	t.Setenv("TART_MACOS_VM_NAME", "custom-test-vm")
	vm := alchemy_build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "arm64", HostOs: alchemy_build.HostOsDarwin, VirtualizationEngine: alchemy_build.VirtualizationEngineUtm}
	bundle, err := utmVirtualMachinePath(vm)
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(state, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOCK_STATE", state)
	t.Setenv("MOCK_BUNDLE", bundle)
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := `#!/bin/sh
set -eu
printf '%s %s\n' "${0##*/}" "$*" >> "$MOCK_STATE/calls"
case "${0##*/}" in
 plutil)
  [ "$1" = '-extract' ] && [ "$2" = 'Information.UUID' ] && [ "$3" = 'raw' ] && [ "$4" = '-o' ] && [ "$5" = '-' ]
  cat "$6"; exit;;
 bash)
  [ "$1" != '-lc' ]
  mkdir -p "$MOCK_BUNDLE"
  printf 'bbbbbbbb-1111-2222-3333-444444444444' > "$MOCK_BUNDLE/config.plist"
  exit;;
 stat) cat "$MOCK_STATE/stat"; exit;;
esac
case "$*" in
 *'every virtual machine whose name'*) cat "$MOCK_STATE/ids" 2>/dev/null || true;;
 *'sailwright: inspect persistent UTM registrations'*)
  [ ! -f "$MOCK_STATE/registry-error" ] || exit 1
  if [ -f "$MOCK_STATE/persisted" ]; then cat "$MOCK_STATE/persisted"; else printf '[]\n'; fi;;
 *'POSIX path of (file of virtual machine'*)
  [ ! -f "$MOCK_STATE/legacy" ] || exit 1
  if [ -f "$MOCK_STATE/path" ]; then cat "$MOCK_STATE/path"; else printf '%s\n' "$MOCK_BUNDLE"; fi;;
 *'JavaScript'*)
  [ ! -f "$MOCK_STATE/bookmark-error" ] || exit 1
  if [ -f "$MOCK_STATE/bookmark-delays" ]; then
   delays=$(cat "$MOCK_STATE/bookmark-delays")
   if [ "$delays" -gt 0 ]; then
    printf '%s' "$((delays - 1))" > "$MOCK_STATE/bookmark-delays"
    exit 1
   fi
  fi
  printf '%s\n' "$MOCK_BUNDLE";;
 *'is not stopped then stop'*)
  [ ! -f "$MOCK_STATE/stop-error" ] || exit 1
  printf 'stopped\n' > "$MOCK_STATE/status";;
 *'return (status of virtual machine id'*|*'return (status of targetVM)'*) cat "$MOCK_STATE/status";;
 *'delete virtual machine id'*)
  [ ! -f "$MOCK_STATE/delete-error" ] || exit 1
  # Fail if the replacement bundle was not protected by the implementation.
  if [ -f "$MOCK_STATE/preserve" ] && [ -f "$MOCK_BUNDLE/disk" ]; then exit 12; fi
  rm -rf "$MOCK_BUNDLE"
  : > "$MOCK_STATE/ids";;
 *'open POSIX file'*) cat "$MOCK_BUNDLE/config.plist" > "$MOCK_STATE/ids";;
 *'stop targetVM by request'*) printf 'stopped\n' > "$MOCK_STATE/status";;
 *'start targetVM'*)
  grep -q 'bbbbbbbb-1111-2222-3333-444444444444' "$MOCK_STATE/ids"
  printf 'started\n' > "$MOCK_STATE/status";;
 *) echo 'unexpected command' >&2; exit 2;;
esac
`
	for _, name := range []string{"osascript", "plutil", "stat", "bash"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	return vm, bundle, state
}

func writeMacFixture(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUtmStartRepairsStaleRegistrationWithoutDeletingReplacement(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "file property", true: "legacy bookmark"}[legacy], func(t *testing.T) {
			vm, bundle, state := mockMacOSLifecycle(t)
			writeMacFixture(t, filepath.Join(bundle, "config.plist"), newUtmID)
			writeMacFixture(t, filepath.Join(bundle, "disk"), "valuable guest disk")
			writeMacFixture(t, filepath.Join(state, "ids"), oldUtmID)
			// Preferences may still contain this ID after live deletion. Repair
			// must poll the scripting collection rather than the stale snapshot.
			writeMacFixture(t, filepath.Join(state, "persisted"), `["`+oldUtmID+`"]`)
			writeMacFixture(t, filepath.Join(state, "status"), "started")
			writeMacFixture(t, filepath.Join(state, "preserve"), "")
			if legacy {
				writeMacFixture(t, filepath.Join(state, "legacy"), "")
			}
			if err := RunUtmStartOnMacOS(vm); err != nil {
				t.Fatal(err)
			}
			disk, err := os.ReadFile(filepath.Join(bundle, "disk"))
			if err != nil || string(disk) != "valuable guest disk" {
				t.Fatalf("replacement damaged: %q %v", disk, err)
			}
			calls, _ := os.ReadFile(filepath.Join(state, "calls"))
			if strings.Contains(string(calls), `set targetVM to virtual machine named`) {
				t.Fatal("selected by name")
			}
			if !strings.Contains(string(calls), `set targetVM to virtual machine id "`+newUtmID+`"`) {
				t.Fatal("did not select new UUID")
			}
			// A repeated start must not delete or reopen the already running instance.
			writeMacFixture(t, filepath.Join(state, "persisted"), `["`+newUtmID+`"]`)
			writeMacFixture(t, filepath.Join(state, "calls"), "")
			if err := RunUtmStartOnMacOS(vm); err != nil {
				t.Fatal(err)
			}
			calls, _ = os.ReadFile(filepath.Join(state, "calls"))
			if strings.Contains(string(calls), "delete virtual machine") || strings.Contains(string(calls), "open POSIX") {
				t.Fatalf("non-idempotent start: %s", calls)
			}
		})
	}
}

func TestUtmStartWaitsForNewRegistrationBookmark(t *testing.T) {
	vm, bundle, state := mockMacOSLifecycle(t)
	writeMacFixture(t, filepath.Join(bundle, "config.plist"), newUtmID)
	writeMacFixture(t, filepath.Join(state, "ids"), "")
	writeMacFixture(t, filepath.Join(state, "status"), "stopped")
	writeMacFixture(t, filepath.Join(state, "legacy"), "")
	writeMacFixture(t, filepath.Join(state, "bookmark-delays"), "2")
	if err := RunUtmStartOnMacOS(vm); err != nil {
		t.Fatal(err)
	}
	remaining, err := os.ReadFile(filepath.Join(state, "bookmark-delays"))
	if err != nil || string(remaining) != "0" {
		t.Fatalf("bookmark retries remaining: %q: %v", remaining, err)
	}
}

func TestUtmMatchingUUIDRequiresManagedPath(t *testing.T) {
	for _, operation := range []string{"start", "stop", "inspect"} {
		for _, validPath := range []bool{false, true} {
			t.Run(operation+map[bool]string{false: "/other path", true: "/managed path"}[validPath], func(t *testing.T) {
				vm, bundle, state := mockMacOSLifecycle(t)
				writeMacFixture(t, filepath.Join(bundle, "config.plist"), newUtmID)
				writeMacFixture(t, filepath.Join(bundle, "disk"), "valuable guest disk")
				writeMacFixture(t, filepath.Join(state, "ids"), newUtmID)
				writeMacFixture(t, filepath.Join(state, "persisted"), `["`+strings.ToUpper(newUtmID)+`"]`)
				writeMacFixture(t, filepath.Join(state, "status"), "stopped")
				if !validPath {
					writeMacFixture(t, filepath.Join(state, "path"), "/unrelated/copied.utm")
				}
				var err error
				switch operation {
				case "start":
					err = RunUtmStartOnMacOS(vm)
				case "stop":
					err = RunUtmStopOnMacOS(vm)
				case "inspect":
					_, err = inspectUtmStartTarget(vm)
				}
				if validPath {
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				if err == nil || !strings.Contains(err.Error(), "refusing operation") {
					t.Fatalf("expected path refusal, got %v", err)
				}
				calls, _ := os.ReadFile(filepath.Join(state, "calls"))
				for _, mutation := range []string{"start targetVM", "stop targetVM", "delete virtual machine", "open POSIX"} {
					if strings.Contains(string(calls), mutation) {
						t.Fatalf("mutated wrong-path VM: %s", calls)
					}
				}
				if disk, err := os.ReadFile(filepath.Join(bundle, "disk")); err != nil || string(disk) != "valuable guest disk" {
					t.Fatalf("managed bundle damaged: %q %v", disk, err)
				}
			})
		}
	}
}

func TestUtmMatchingUUIDAcceptsBundlePathAlias(t *testing.T) {
	vm, bundle, state := mockMacOSLifecycle(t)
	writeMacFixture(t, filepath.Join(bundle, "config.plist"), newUtmID)
	writeMacFixture(t, filepath.Join(state, "ids"), newUtmID)
	writeMacFixture(t, filepath.Join(state, "status"), "started")
	alias := filepath.Join(t.TempDir(), "alias.utm")
	if err := os.Symlink(bundle, alias); err != nil {
		t.Fatal(err)
	}
	writeMacFixture(t, filepath.Join(state, "path"), alias)
	if err := RunUtmStartOnMacOS(vm); err != nil {
		t.Fatal(err)
	}
}

func TestUtmDestroyUnregistersRunningAndOrphanVMs(t *testing.T) {
	for _, bundleExists := range []bool{true, false} {
		t.Run(map[bool]string{true: "running bundle", false: "orphan registration"}[bundleExists], func(t *testing.T) {
			vm, bundle, state := mockMacOSLifecycle(t)
			if bundleExists {
				writeMacFixture(t, filepath.Join(bundle, "config.plist"), oldUtmID)
			}
			writeMacFixture(t, filepath.Join(state, "ids"), oldUtmID)
			writeMacFixture(t, filepath.Join(state, "status"), "started")
			if err := RunUtmDestroyOnMacOS(vm); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(bundle); !os.IsNotExist(err) {
				t.Fatalf("bundle remains: %v", err)
			}
			ids, _ := os.ReadFile(filepath.Join(state, "ids"))
			if len(ids) != 0 {
				t.Fatalf("stale registration: %s", ids)
			}
			if err := RunUtmDestroyOnMacOS(vm); err != nil {
				t.Fatalf("repeat destroy: %v", err)
			}
		})
	}
}

func TestUtmRepairFailuresPreserveBundle(t *testing.T) {
	for _, failure := range []string{"delete-error", "stop-error", "path", "bookmark-error"} {
		t.Run(failure, func(t *testing.T) {
			vm, bundle, state := mockMacOSLifecycle(t)
			writeMacFixture(t, filepath.Join(bundle, "config.plist"), newUtmID)
			writeMacFixture(t, filepath.Join(bundle, "disk"), "disk")
			writeMacFixture(t, filepath.Join(state, "ids"), oldUtmID)
			writeMacFixture(t, filepath.Join(state, "status"), "started")
			writeMacFixture(t, filepath.Join(state, failure), "/unrelated/VM.utm")
			if failure == "bookmark-error" {
				writeMacFixture(t, filepath.Join(state, "legacy"), "")
			}
			if err := RunUtmStartOnMacOS(vm); err == nil {
				t.Fatal("expected repair failure")
			}
			if _, err := os.Stat(filepath.Join(bundle, "disk")); err != nil {
				t.Fatal("lost bundle after failed repair", err)
			}
			calls, _ := os.ReadFile(filepath.Join(state, "calls"))
			if strings.Contains(string(calls), "start targetVM") {
				t.Fatal("started after failed repair")
			}
		})
	}
}

func TestMacOSInstanceIdentity(t *testing.T) {
	vm, bundle, state := mockMacOSLifecycle(t)
	if id, exists, err := InspectInstanceID(vm); err != nil || exists || id != "" {
		t.Fatalf("absent UTM: %s %v %v", id, exists, err)
	}
	writeMacFixture(t, filepath.Join(bundle, "config.plist"), strings.ToUpper(oldUtmID))
	if id, exists, err := InspectInstanceID(vm); err != nil || !exists || id != oldUtmID {
		t.Fatalf("UTM: %s %v %v", id, exists, err)
	}
	writeMacFixture(t, filepath.Join(bundle, "config.plist"), newUtmID)
	if id, _, err := InspectInstanceID(vm); err != nil || id != newUtmID {
		t.Fatalf("replacement UTM: %s %v", id, err)
	}
	writeMacFixture(t, filepath.Join(bundle, "config.plist"), "missing")
	if _, _, err := InspectInstanceID(vm); err == nil {
		t.Fatal("malformed UUID accepted")
	}

	vm.OS, vm.UbuntuType, vm.VirtualizationEngine = "macos", "", alchemy_build.VirtualizationEngineTart
	tartPath := filepath.Join(os.Getenv("TART_HOME"), "vms", tartMacOSVMName(vm))
	if id, exists, err := InspectInstanceID(vm); err != nil || exists || id != "" {
		t.Fatalf("absent Tart: %s %v %v", id, exists, err)
	}
	if err := os.MkdirAll(tartPath, 0o750); err != nil {
		t.Fatal(err)
	}
	writeMacFixture(t, filepath.Join(state, "stat"), "123:456:1700000000")
	first, exists, err := InspectInstanceID(vm)
	if err != nil || !exists || !strings.HasPrefix(first, "tart:") {
		t.Fatalf("Tart: %s %v %v", first, exists, err)
	}
	// Identical cloned config/hardware IDs do not make a recreated directory the
	// same instance. Also cover inode reuse with a different birth time.
	for _, tuple := range []string{"123:789:1700000000", "123:456:1700000001"} {
		writeMacFixture(t, filepath.Join(state, "stat"), tuple)
		next, _, err := InspectInstanceID(vm)
		if err != nil || next == first {
			t.Fatalf("Tart replacement retained identity: %s %v", next, err)
		}
	}
	writeMacFixture(t, filepath.Join(state, "stat"), "123:456:0")
	if _, _, err := InspectInstanceID(vm); err == nil {
		t.Fatal("invalid filesystem identity accepted")
	}
	calls, _ := os.ReadFile(filepath.Join(state, "calls"))
	if strings.Contains(string(calls), "osascript") {
		t.Fatal("identity lookup mutated or queried registration")
	}
}

func TestUtmCreateDefersOrphanRepairUntilStart(t *testing.T) {
	vm, bundle, state := mockMacOSLifecycle(t)
	writeMacFixture(t, filepath.Join(state, "ids"), oldUtmID)
	writeMacFixture(t, filepath.Join(state, "status"), "stopped")
	if err := RunUtmDeployOnMacOS(vm); err != nil {
		t.Fatal(err)
	}
	id, exists, err := InspectInstanceID(vm)
	if err != nil || !exists || id != newUtmID {
		t.Fatalf("created bundle %s: %s %v %v", bundle, id, exists, err)
	}
	ids, _ := os.ReadFile(filepath.Join(state, "ids"))
	if string(ids) != oldUtmID {
		t.Fatalf("create changed registration: %s", ids)
	}
	calls, _ := os.ReadFile(filepath.Join(state, "calls"))
	if strings.Contains(string(calls), "osascript") {
		t.Fatalf("bundle creation requires GUI automation: %s", calls)
	}
	writeMacFixture(t, filepath.Join(bundle, "disk"), "valuable guest disk")
	writeMacFixture(t, filepath.Join(state, "preserve"), "")
	if err := RunUtmStartOnMacOS(vm); err != nil {
		t.Fatal(err)
	}
	ids, _ = os.ReadFile(filepath.Join(state, "ids"))
	if string(ids) != newUtmID {
		t.Fatalf("start did not repair orphan registration: %s", ids)
	}
	if disk, err := os.ReadFile(filepath.Join(bundle, "disk")); err != nil || string(disk) != "valuable guest disk" {
		t.Fatalf("start damaged created disk: %q %v", disk, err)
	}
}

func TestTartInstanceIdentityRejectsEscapingPaths(t *testing.T) {
	vm, _, state := mockMacOSLifecycle(t)
	vm.OS, vm.UbuntuType, vm.VirtualizationEngine = "macos", "", alchemy_build.VirtualizationEngineTart
	for _, name := range []string{".", "..", "../outside", "/outside", "nested/vm"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("TART_MACOS_VM_NAME", name)
			if _, _, err := InspectInstanceID(vm); err == nil {
				t.Fatal("accepted invalid VM name")
			}
		})
	}
	vms := filepath.Join(os.Getenv("TART_HOME"), "vms")
	if err := os.MkdirAll(vms, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(vms, tartMacOSVMName(vm))); err != nil {
		t.Fatal(err)
	}
	if _, _, err := InspectInstanceID(vm); err == nil {
		t.Fatal("accepted symlink outside Tart home")
	}
	if calls, _ := os.ReadFile(filepath.Join(state, "calls")); len(calls) != 0 {
		t.Fatalf("queried identity for unsafe path: %s", calls)
	}
}

func TestUtmIdentityAndInspectionDoNotRegisterOrStart(t *testing.T) {
	vm, bundle, state := mockMacOSLifecycle(t)
	writeMacFixture(t, filepath.Join(bundle, "config.plist"), newUtmID)
	writeMacFixture(t, filepath.Join(state, "ids"), oldUtmID)
	got, err := inspectUtmStartTarget(vm)
	if err != nil || !got.Exists || got.Running || got.State != "unregistered" {
		t.Fatalf("inspection: %+v %v", got, err)
	}
	calls, _ := os.ReadFile(filepath.Join(state, "calls"))
	for _, mutation := range []string{"delete virtual machine", "open POSIX", "start targetVM", "stop targetVM"} {
		if strings.Contains(string(calls), mutation) {
			t.Fatalf("inspection mutated VM: %s", calls)
		}
	}
}

func TestUtmDestroyListingIncludesOrphanRegistration(t *testing.T) {
	vm, _, state := mockMacOSLifecycle(t)
	writeMacFixture(t, filepath.Join(state, "ids"), oldUtmID)
	exists, err := DestroyTargetExists(vm)
	if err != nil || !exists {
		t.Fatalf("orphan not destroyable: %v %v", exists, err)
	}
	writeMacFixture(t, filepath.Join(state, "ids"), "")
	exists, err = DestroyTargetExists(vm)
	if err != nil || exists {
		t.Fatalf("absent target: %v %v", exists, err)
	}
}

func TestUtmUnloadedRegistrationRefusesCleanup(t *testing.T) {
	for _, bundleExists := range []bool{false, true} {
		for _, operation := range []string{"destroy", "listing", "start"} {
			t.Run(operation+map[bool]string{false: "/missing bundle", true: "/replacement bundle"}[bundleExists], func(t *testing.T) {
				vm, bundle, state := mockMacOSLifecycle(t)
				if bundleExists {
					writeMacFixture(t, filepath.Join(bundle, "config.plist"), newUtmID)
					writeMacFixture(t, filepath.Join(bundle, "disk"), "valuable guest disk")
				}
				// After UTM restarts with a missing bundle, the entry remains in
				// preferences but is absent from the scriptable collection.
				writeMacFixture(t, filepath.Join(state, "ids"), "")
				writeMacFixture(t, filepath.Join(state, "persisted"), `["`+strings.ToUpper(oldUtmID)+`"]`)
				var err error
				switch operation {
				case "destroy":
					err = RunUtmDestroyOnMacOS(vm)
				case "listing":
					_, err = DestroyTargetExists(vm)
					if bundleExists {
						// A present bundle is already a destroy target; cleanup itself
						// must still refuse the unavailable registration.
						if err != nil {
							t.Fatal(err)
						}
						return
					}
				case "start":
					err = RunUtmStartOnMacOS(vm)
					if !bundleExists {
						if err == nil {
							t.Fatal("started absent VM")
						}
						return
					}
				}
				if err == nil || !strings.Contains(err.Error(), "remove the unavailable entry in the UTM library manually") {
					t.Fatalf("expected manual cleanup error, got %v", err)
				}
				calls, _ := os.ReadFile(filepath.Join(state, "calls"))
				for _, mutation := range []string{"delete virtual machine", "stop targetVM", "start targetVM", "open POSIX"} {
					if strings.Contains(string(calls), mutation) {
						t.Fatalf("mutated VM after incomplete registry lookup: %s", calls)
					}
				}
				if bundleExists {
					if disk, err := os.ReadFile(filepath.Join(bundle, "disk")); err != nil || string(disk) != "valuable guest disk" {
						t.Fatalf("replacement damaged: %q %v", disk, err)
					}
				} else if _, err := os.Stat(bundle); !os.IsNotExist(err) {
					t.Fatalf("created placeholder for unscriptable entry: %v", err)
				}
			})
		}
	}
}

func TestUtmPersistentRegistrationLookupFailsClosed(t *testing.T) {
	for _, output := range []string{"command failure", "invalid json", "null", `["not-a-uuid"]`} {
		t.Run(output, func(t *testing.T) {
			vm, bundle, state := mockMacOSLifecycle(t)
			writeMacFixture(t, filepath.Join(bundle, "config.plist"), newUtmID)
			if output == "command failure" {
				writeMacFixture(t, filepath.Join(state, "registry-error"), "")
			} else {
				writeMacFixture(t, filepath.Join(state, "persisted"), output)
			}
			if err := RunUtmDestroyOnMacOS(vm); err == nil {
				t.Fatal("destroy accepted an unverifiable persistent registry")
			}
			if _, err := os.Stat(filepath.Join(bundle, "config.plist")); err != nil {
				t.Fatalf("bundle damaged: %v", err)
			}
		})
	}
}

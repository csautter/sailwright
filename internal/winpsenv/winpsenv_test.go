package winpsenv

import (
	"reflect"
	"strings"
	"testing"
)

const (
	programFilesEnv    = `ProgramFiles=C:\Program Files`
	programFilesX86Env = `ProgramFiles(x86)=C:\Program Files (x86)`
	userProfileEnv     = `USERPROFILE=C:\Users\runneradmin`
)

func stubFileExists(t *testing.T, existing ...string) {
	t.Helper()
	original := fileExists
	t.Cleanup(func() { fileExists = original })
	fileExists = func(path string) bool {
		for _, candidate := range existing {
			if strings.EqualFold(candidate, path) {
				return true
			}
		}
		return false
	}
}

func moduleEnv(paths ...string) []string {
	return []string{programFilesEnv, programFilesX86Env, userProfileEnv, "PSModulePath=" + strings.Join(paths, ";")}
}

func TestSanitizeRemovesPowerShellCoreModulePaths(t *testing.T) {
	stubFileExists(t)
	environ := moduleEnv(
		`C:\Users\runneradmin\Documents\PowerShell\Modules`,
		`C:\Program Files\PowerShell\Modules`,
		`c:\program files\powershell\7\Modules`,
		`C:\Program Files\WindowsPowerShell\Modules`,
		`C:\Windows\system32\WindowsPowerShell\v1.0\Modules`,
		`C:\Program Files (x86)\Microsoft SDKs\Azure\PowerShell\ServiceManagement`,
	)

	got, removed := Sanitize(`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, environ)

	want := moduleEnv(
		`C:\Program Files\WindowsPowerShell\Modules`,
		`C:\Windows\system32\WindowsPowerShell\v1.0\Modules`,
		`C:\Program Files (x86)\Microsoft SDKs\Azure\PowerShell\ServiceManagement`,
	)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected environment %v, got %v", want, got)
	}
	wantRemoved := []string{
		`C:\Users\runneradmin\Documents\PowerShell\Modules`,
		`C:\Program Files\PowerShell\Modules`,
		`c:\program files\powershell\7\Modules`,
	}
	if !reflect.DeepEqual(removed, wantRemoved) {
		t.Fatalf("expected removed entries %v, got %v", wantRemoved, removed)
	}
}

func TestIsPowerShellCoreModulePath(t *testing.T) {
	stubFileExists(t,
		`D:\tools\pwsh\pwsh.exe`,
		`C:\Program Files\WindowsApps\Microsoft.PowerShell_7.5.0.0_x64__8wekyb3d8bbwe\pwsh.exe`,
	)
	locations := newModuleLocations([]string{programFilesEnv, programFilesX86Env, userProfileEnv})

	cases := map[string]bool{
		// PowerShell 7 locations.
		`C:\Program Files\PowerShell\Modules`:                                                  true,
		`C:\Program Files\PowerShell\7\Modules`:                                                true,
		`C:\Program Files\PowerShell\7-preview\Modules\`:                                       true,
		`C:/Program Files/PowerShell/7/Modules`:                                                true,
		`C:\Program Files (x86)\PowerShell\Modules`:                                            true,
		`C:\Users\runneradmin\Documents\PowerShell\Modules`:                                    true,
		`C:\Users\runneradmin\OneDrive - Contoso\Dokumente\PowerShell\Modules`:                 true,
		`E:\Redirected\Documents\PowerShell\Modules`:                                           true,
		`D:\tools\pwsh\Modules`:                                                                true,
		`C:\Program Files\WindowsApps\Microsoft.PowerShell_7.5.0.0_x64__8wekyb3d8bbwe\Modules`: true,
		// Windows PowerShell and third-party locations.
		`C:\Program Files\WindowsPowerShell\Modules`:                               false,
		`C:\Windows\system32\WindowsPowerShell\v1.0\Modules`:                       false,
		`C:\Users\runneradmin\Documents\WindowsPowerShell\Modules`:                 false,
		`C:\Program Files\Vendor\PowerShell\Modules`:                               false,
		`C:\Program Files\Vendor\PowerShell\2\Modules`:                             false,
		`C:\Program Files\PowerShell\Scripts`:                                      false,
		`C:\Program Files (x86)\Microsoft SDKs\Azure\PowerShell\ServiceManagement`: false,
		`Modules`:                false,
		`C:\\Modules`:            false,
		`C:\PowerShell\\Modules`: false,
		``:                       false,
	}
	for path, want := range cases {
		if got := locations.isPowerShellCoreModulePath(path); got != want {
			t.Errorf("isPowerShellCoreModulePath(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestSanitizeDropsPSModulePathWithOnlyPowerShellCoreEntries(t *testing.T) {
	stubFileExists(t)
	environ := []string{programFilesEnv, `PSMODULEPATH=C:\Program Files\PowerShell\7-preview\Modules;;C:\Program Files\PowerShell\Modules`}

	got, removed := Sanitize("powershell", environ)

	if !reflect.DeepEqual(got, []string{programFilesEnv}) || len(removed) != 2 {
		t.Fatalf("expected PSModulePath to be removed, got env=%v removed=%v", got, removed)
	}
}

func TestSanitizeKeepsUnaffectedEnvironment(t *testing.T) {
	stubFileExists(t)
	cases := map[string][]string{
		"only Windows PowerShell paths": moduleEnv(`C:\Program Files\WindowsPowerShell\Modules`, `C:\Windows\system32\WindowsPowerShell\v1.0\Modules`),
		"empty PSModulePath":            moduleEnv(),
		"no PSModulePath":               {programFilesEnv},
	}
	for name, environ := range cases {
		got, removed := Sanitize("powershell.exe", environ)
		if removed != nil || !reflect.DeepEqual(got, environ) {
			t.Errorf("%s: expected environment to be unchanged, got env=%v removed=%v", name, got, removed)
		}
	}
}

func TestSanitizeIgnoresOtherExecutables(t *testing.T) {
	stubFileExists(t)
	environ := moduleEnv(`C:\Program Files\PowerShell\7\Modules`)

	for _, executable := range []string{"pwsh", "pwsh.exe", `C:\Program Files\PowerShell\7\pwsh.exe`, "ansible-playbook", ""} {
		got, removed := Sanitize(executable, environ)
		if removed != nil || !reflect.DeepEqual(got, environ) {
			t.Fatalf("expected environment to be unchanged for %q, got env=%v removed=%v", executable, got, removed)
		}
	}
}

func TestIsWindowsPowerShell(t *testing.T) {
	cases := map[string]bool{
		"powershell":     true,
		"powershell.exe": true,
		`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`: true,
		`C:/Windows/System32/WindowsPowerShell/v1.0/POWERSHELL.EXE`: true,
		"pwsh":           false,
		"pwsh.exe":       false,
		"powershell_ise": false,
		"":               false,
	}
	for executable, want := range cases {
		if got := IsWindowsPowerShell(executable); got != want {
			t.Errorf("IsWindowsPowerShell(%q) = %v, want %v", executable, got, want)
		}
	}
}

func TestCommandEnvInheritsEnvironmentWhenNothingChanges(t *testing.T) {
	stubFileExists(t)
	t.Setenv("ProgramFiles", `C:\Program Files`)
	t.Setenv("PSModulePath", `C:\Program Files\PowerShell\7\Modules`)

	if got := CommandEnv("ansible-playbook", nil); got != nil {
		t.Fatalf("expected nil environment to inherit the current one, got %v", got)
	}

	t.Setenv("PSModulePath", `C:\Program Files\WindowsPowerShell\Modules`)
	if got := CommandEnv("powershell.exe", nil); got != nil {
		t.Fatalf("expected nil environment for clean PSModulePath, got %v", got)
	}
}

func TestCommandEnvSanitizesWindowsPowerShellAndAppendsExtraEnv(t *testing.T) {
	stubFileExists(t)
	t.Setenv("ProgramFiles", `C:\Program Files`)
	t.Setenv("PSModulePath", `C:\Program Files\PowerShell\7\Modules;C:\Program Files\WindowsPowerShell\Modules`)

	got := CommandEnv("powershell.exe", []string{"EXTRA=1"})

	var modulePath string
	for _, entry := range got {
		if strings.HasPrefix(strings.ToUpper(entry), "PSMODULEPATH=") {
			modulePath = entry
		}
	}
	if modulePath != `PSModulePath=C:\Program Files\WindowsPowerShell\Modules` {
		t.Fatalf("expected sanitized PSModulePath, got %q", modulePath)
	}
	if got[len(got)-1] != "EXTRA=1" {
		t.Fatalf("expected extra environment to be appended last, got %v", got)
	}
}

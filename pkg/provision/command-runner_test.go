package provision

import (
	"reflect"
	"strings"
	"testing"
)

func TestSanitizeCommandArgsForLogsRedactsURLCredentials(t *testing.T) {
	args := []string{
		"clone",
		"https://token@example.test/org/private.git",
		"ssh://git:secret@example.test/org/private.git",
		"https://example.test/org/public.git",
	}

	got := sanitizeCommandArgsForLogs(args)
	want := []string{
		"clone",
		"https://***REDACTED***@example.test/org/private.git",
		"ssh://***REDACTED***@example.test/org/private.git",
		"https://example.test/org/public.git",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected sanitized args %v, got %v", want, got)
	}
}

func TestSanitizeWindowsPowerShellEnvRemovesPowerShellCoreModulePaths(t *testing.T) {
	environ := []string{
		"PATH=C:\\Windows\\System32",
		`PSModulePath=C:\Users\runneradmin\Documents\PowerShell\Modules;C:\Program Files\PowerShell\Modules;c:\program files\powershell\7\Modules;C:\Program Files\WindowsPowerShell\Modules;C:\Windows\system32\WindowsPowerShell\v1.0\Modules;C:\Program Files (x86)\Microsoft SDKs\Azure\PowerShell\ServiceManagement`,
	}

	got, changed := sanitizeWindowsPowerShellEnv(`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, environ)

	want := []string{
		"PATH=C:\\Windows\\System32",
		`PSModulePath=C:\Program Files\WindowsPowerShell\Modules;C:\Windows\system32\WindowsPowerShell\v1.0\Modules;C:\Program Files (x86)\Microsoft SDKs\Azure\PowerShell\ServiceManagement`,
	}
	if !changed {
		t.Fatal("expected PSModulePath to be changed")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected environment %v, got %v", want, got)
	}
}

func TestSanitizeWindowsPowerShellEnvDropsPSModulePathWithOnlyPowerShellCoreEntries(t *testing.T) {
	environ := []string{`PSMODULEPATH=C:\Program Files\PowerShell\7-preview\Modules;C:\Program Files\PowerShell\Modules`}

	got, changed := sanitizeWindowsPowerShellEnv("powershell", environ)

	if !changed || len(got) != 0 {
		t.Fatalf("expected PSModulePath to be removed, got changed=%v env=%v", changed, got)
	}
}

func TestSanitizeWindowsPowerShellEnvKeepsWindowsPowerShellModulePaths(t *testing.T) {
	environ := []string{`PSModulePath=C:\Program Files\WindowsPowerShell\Modules;C:\Windows\system32\WindowsPowerShell\v1.0\Modules`}

	got, changed := sanitizeWindowsPowerShellEnv("powershell.exe", environ)

	if changed || !reflect.DeepEqual(got, environ) {
		t.Fatalf("expected environment to be unchanged, got changed=%v env=%v", changed, got)
	}
}

func TestSanitizeWindowsPowerShellEnvIgnoresOtherExecutables(t *testing.T) {
	environ := []string{`PSModulePath=C:\Program Files\PowerShell\7\Modules`}

	for _, executable := range []string{"pwsh", "pwsh.exe", `C:\Program Files\PowerShell\7\pwsh.exe`, "ansible-playbook"} {
		got, changed := sanitizeWindowsPowerShellEnv(executable, environ)
		if changed || !reflect.DeepEqual(got, environ) {
			t.Fatalf("expected environment to be unchanged for %q, got changed=%v env=%v", executable, changed, got)
		}
	}
}

func TestCommandEnvInheritsEnvironmentWhenNothingChanges(t *testing.T) {
	t.Setenv("PSModulePath", `C:\Program Files\PowerShell\7\Modules`)

	if got := commandEnv("ansible-playbook", nil); got != nil {
		t.Fatalf("expected nil environment to inherit the current one, got %v", got)
	}
}

func TestCommandEnvSanitizesWindowsPowerShellAndAppendsExtraEnv(t *testing.T) {
	t.Setenv("PSModulePath", `C:\Program Files\PowerShell\7\Modules;C:\Program Files\WindowsPowerShell\Modules`)

	got := commandEnv("powershell.exe", []string{"EXTRA=1"})

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

// Package winpsenv prepares the environment for Windows PowerShell child
// processes.
//
// When sailwright is started from PowerShell 7 (pwsh), for example from the
// default shell of GitHub Actions Windows runners, child processes inherit
// pwsh's PSModulePath. Windows PowerShell 5.1 then loads incompatible
// PowerShell 7 core modules: Microsoft.PowerShell.Security fails to load and
// the Cert: drive is missing, among other breakage. pwsh applies a similar
// mitigation (removing its own module paths) when it starts powershell.exe
// directly; this package does the same for processes sailwright starts.
package winpsenv

import (
	"log"
	"os"
	"strings"
	"sync"
)

const psModulePathKey = "PSModulePath"

// fileExists reports whether path is an existing regular file. It is a
// variable so tests can simulate pwsh installations.
var fileExists = func(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

var (
	loggedRemovalsMu sync.Mutex
	loggedRemovals   = map[string]struct{}{}
)

// CommandEnv returns the environment for a child process started with
// executable, or nil to inherit the current environment unchanged. extraEnv is
// appended last so it overrides inherited values.
func CommandEnv(executable string, extraEnv []string) []string {
	environ, removed := Sanitize(executable, os.Environ())
	if len(removed) == 0 && len(extraEnv) == 0 {
		return nil
	}

	return append(environ, extraEnv...)
}

// Sanitize returns environ with PowerShell 7 module directories removed from
// PSModulePath when executable is Windows PowerShell, plus the removed
// entries. For any other executable, or when nothing is removed, environ is
// returned unchanged. If PSModulePath only held PowerShell 7 entries, the
// variable is dropped so Windows PowerShell computes its default.
func Sanitize(executable string, environ []string) ([]string, []string) {
	if !IsWindowsPowerShell(executable) {
		return environ, nil
	}

	locations := newModuleLocations(environ)
	sanitized := make([]string, 0, len(environ))
	var removed []string
	for _, entry := range environ {
		key, value, found := strings.Cut(entry, "=")
		if !found || !strings.EqualFold(key, psModulePathKey) {
			sanitized = append(sanitized, entry)
			continue
		}

		kept, removedHere := locations.filter(value)
		if len(removedHere) == 0 {
			sanitized = append(sanitized, entry)
			continue
		}

		removed = append(removed, removedHere...)
		if strings.Trim(kept, ";") != "" {
			sanitized = append(sanitized, key+"="+kept)
		}
	}

	if len(removed) == 0 {
		return environ, nil
	}

	logRemovalOnce(removed)
	return sanitized, removed
}

// IsWindowsPowerShell reports whether executable names Windows PowerShell
// (powershell or powershell.exe, optionally with a directory), as opposed to
// PowerShell 7 (pwsh).
func IsWindowsPowerShell(executable string) bool {
	base := strings.ToLower(executable[strings.LastIndexAny(executable, `\/`)+1:])
	return base == "powershell" || base == "powershell.exe"
}

func logRemovalOnce(removed []string) {
	summary := strings.Join(removed, ";")

	loggedRemovalsMu.Lock()
	defer loggedRemovalsMu.Unlock()
	if _, logged := loggedRemovals[summary]; logged {
		return
	}
	loggedRemovals[summary] = struct{}{}

	log.Printf("Removed PowerShell 7 module paths from PSModulePath for Windows PowerShell child processes: %s", summary)
}

// moduleLocations knows the directories PowerShell 7 adds to PSModulePath:
//
//   - $PSHOME\Modules: the pwsh installation directory, recognised by the
//     pwsh.exe next to Modules or, without file system access, by a
//     <ProgramFiles>\PowerShell\<version>\Modules layout.
//   - %ProgramFiles%\PowerShell\Modules: modules for all users.
//   - <Documents>\PowerShell\Modules: modules for the current user. Documents
//     may be redirected (e.g. OneDrive, localised folder names), so any
//     PowerShell\Modules below the user profile, or below a folder named
//     Documents, counts.
//
// Paths elsewhere that merely end in PowerShell\Modules, such as a vendor's
// C:\Program Files\Vendor\PowerShell\Modules, are kept.
type moduleLocations struct {
	programFilesDirs []string
	userProfileDir   string
}

func newModuleLocations(environ []string) moduleLocations {
	var locations moduleLocations
	for _, entry := range environ {
		key, value, found := strings.Cut(entry, "=")
		if !found || value == "" {
			continue
		}
		switch strings.ToLower(key) {
		case "programfiles", "programw6432", "programfiles(x86)":
			locations.programFilesDirs = append(locations.programFilesDirs, normalizePath(value))
		case "userprofile":
			locations.userProfileDir = normalizePath(value)
		}
	}

	return locations
}

func (l moduleLocations) filter(modulePath string) (string, []string) {
	entries := strings.Split(modulePath, ";")
	kept := make([]string, 0, len(entries))
	var removed []string
	for _, entry := range entries {
		if l.isPowerShellCoreModulePath(entry) {
			removed = append(removed, entry)
			continue
		}
		kept = append(kept, entry)
	}

	return strings.Join(kept, ";"), removed
}

func (l moduleLocations) isPowerShellCoreModulePath(path string) bool {
	normalized := normalizePath(path)
	modulesParent, last := splitLastSegment(normalized)
	if last != "modules" || modulesParent == "" {
		return false
	}

	// Keep the original spelling for the file system lookup.
	trimmed := strings.TrimRight(path, `\/`)
	if fileExists(trimmed[:strings.LastIndexAny(trimmed, `\/`)] + `\pwsh.exe`) {
		return true
	}

	powerShellParent, parentName := splitLastSegment(modulesParent)
	if parentName == "powershell" {
		return l.isProgramFilesDir(powerShellParent) || l.isUserDocumentsDir(powerShellParent)
	}

	installParent, installName := splitLastSegment(powerShellParent)
	return installName == "powershell" &&
		parentName != "" && parentName[0] >= '0' && parentName[0] <= '9' &&
		l.isProgramFilesDir(installParent)
}

func (l moduleLocations) isProgramFilesDir(dir string) bool {
	for _, programFiles := range l.programFilesDirs {
		if dir == programFiles {
			return true
		}
	}

	return false
}

func (l moduleLocations) isUserDocumentsDir(dir string) bool {
	if l.userProfileDir != "" && strings.HasPrefix(dir, l.userProfileDir+`\`) {
		return true
	}

	_, name := splitLastSegment(dir)
	return name == "documents" || name == "my documents"
}

// normalizePath lower-cases path, uses backslashes and trims trailing
// separators, so Windows paths compare case-insensitively.
func normalizePath(path string) string {
	return strings.TrimRight(strings.ToLower(strings.ReplaceAll(path, "/", `\`)), `\`)
}

// splitLastSegment splits a normalized path into its parent and last segment.
func splitLastSegment(path string) (string, string) {
	index := strings.LastIndex(path, `\`)
	if index < 0 {
		return "", path
	}

	return path[:index], path[index+1:]
}

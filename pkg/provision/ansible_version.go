package provision

import (
	"context"
	"fmt"
	"io/fs"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	runtimeassets "github.com/csautter/dev-alchemy"
)

// ansible-core detects a rejected sudo password on the local connection only
// from 2.19 on. Older versions wait forever for sudo's second prompt, so a
// wrong password typed at the --ask-become-pass prompt hangs the run.
var minimumAnsibleCoreVersion = func() string {
	data, err := fs.ReadFile(runtimeassets.FS(), "scripts/common/ansible-core-min-version")
	if err != nil {
		panic(err)
	}
	return strings.TrimSpace(string(data))
}()

var stableCoreVersionRegex = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)$`)

func ansibleCoreMeetsMinimum(version string) bool {
	actual := stableCoreVersionRegex.FindStringSubmatch(version)
	minimum := stableCoreVersionRegex.FindStringSubmatch(minimumAnsibleCoreVersion)
	if actual == nil || minimum == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		a, errA := strconv.Atoi(actual[i])
		b, errB := strconv.Atoi(minimum[i])
		if errA != nil || errB != nil {
			return false
		}
		if a != b {
			return a > b
		}
	}
	return true
}

// CheckInstalledAnsibleCore verifies the executable the CLI will find on PATH.
func CheckInstalledAnsibleCore() error {
	output, err := ansiblePlaybookVersionOutputFunc()
	if err != nil {
		return fmt.Errorf("ansible-playbook could not start: %w; rerun sailwright install and check PATH with command -v ansible-playbook: %s", err, strings.TrimSpace(output))
	}
	version, _, _, ok := parseAnsibleCoreVersion(output)
	if !ok || !ansibleCoreMeetsMinimum(version) {
		return fmt.Errorf("Sailwright requires ansible-core >= %s (found %q); rerun sailwright install, remove an older Ansible from PATH, or activate an environment with a supported ansible-core; check with command -v ansible-playbook", minimumAnsibleCoreVersion, strings.TrimSpace(output))
	}
	return nil
}

var ansibleCoreVersionRegex = regexp.MustCompile(`\[core (([0-9]+)\.([0-9]+)[^\]]*)\]`)

var ansiblePlaybookVersionOutputFunc = func() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// #nosec G204 -- fixed executable and arguments.
	output, err := exec.CommandContext(ctx, "ansible-playbook", "--version").CombinedOutput()
	return string(output), err
}

// parseAnsibleCoreVersion reads the version from `ansible-playbook --version`,
// whose first line looks like `ansible-playbook [core 2.16.3]`.
func parseAnsibleCoreVersion(output string) (version string, major int, minor int, ok bool) {
	match := ansibleCoreVersionRegex.FindStringSubmatch(output)
	if match == nil {
		return "", 0, 0, false
	}
	major, majorErr := strconv.Atoi(match[2])
	minor, minorErr := strconv.Atoi(match[3])
	if majorErr != nil || minorErr != nil {
		return "", 0, 0, false
	}
	return match[1], major, minor, true
}

// askBecomePassVersionWarning returns a warning when the ansible-core in
// versionOutput hangs on a wrong become password, or "" otherwise.
func askBecomePassVersionWarning(versionOutput string) string {
	version, major, minor, ok := parseAnsibleCoreVersion(versionOutput)
	if !ok {
		return ""
	}
	_, minimumMajor, minimumMinor, _ := parseAnsibleCoreVersion("[core " + minimumAnsibleCoreVersion + "]")
	if major > minimumMajor || (major == minimumMajor && minor >= minimumMinor) {
		return ""
	}
	return fmt.Sprintf(
		"⚠️ ansible-core %s does not detect a wrong sudo password: the run then hangs at the first task that uses become. "+
			"If that happens, press Ctrl+C and run again. ansible-core %s and newer fail with a clear error instead.",
		version, minimumAnsibleCoreVersion,
	)
}

// warnIfAnsibleCoreHangsOnWrongBecomePassword prints askBecomePassVersionWarning
// for the installed ansible-playbook. It stays silent if the version is unknown.
func warnIfAnsibleCoreHangsOnWrongBecomePassword() {
	output, err := ansiblePlaybookVersionOutputFunc()
	if err != nil {
		return
	}
	if warning := askBecomePassVersionWarning(output); warning != "" {
		fmt.Println(warning)
	}
}

package provision

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"time"
)

// ansible-core detects a rejected sudo password on the local connection only
// from 2.19 on. Older versions wait forever for sudo's second prompt, so a
// wrong password typed at the --ask-become-pass prompt hangs the run.
const (
	becomePasswordCheckMajor = 2
	becomePasswordCheckMinor = 19
)

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
	if major > becomePasswordCheckMajor || (major == becomePasswordCheckMajor && minor >= becomePasswordCheckMinor) {
		return ""
	}
	return fmt.Sprintf(
		"⚠️ ansible-core %s does not detect a wrong sudo password: the run then hangs at the first task that uses become. "+
			"If that happens, press Ctrl+C and run again. ansible-core %d.%d and newer fail with a clear error instead.",
		version, becomePasswordCheckMajor, becomePasswordCheckMinor,
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

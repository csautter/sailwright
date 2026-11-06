package deploy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

func utmRegistrationIDs(config alchemy_build.VirtualMachineConfig) ([]string, error) {
	output, err := runUtmAppleScript([]string{
		`tell application "UTM"`,
		fmt.Sprintf(`set matches to every virtual machine whose name is %q`, utmVirtualMachineName(config)),
		`set resultText to ""`,
		`repeat with targetVM in matches`,
		`set resultText to resultText & (id of targetVM) & linefeed`,
		`end repeat`,
		`return resultText`,
		`end tell`,
	})
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, line := range strings.Fields(output) {
		id, exists, err := parseInstanceID(line)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("invalid UTM registration UUID %q", line)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func runUtmBundleAppleScript(config alchemy_build.VirtualMachineConfig, lines []string) (string, error) {
	id, exists, err := inspectUtmInstanceID(config)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", fmt.Errorf("UTM VM %q does not exist; run sailwright create first", utmVirtualMachineName(config))
	}
	for i := range lines {
		lines[i] = strings.ReplaceAll(lines[i], "__SAILWRIGHT_UUID__", id)
	}
	return runUtmAppleScript(lines)
}

// removeUtmRegistrations removes only registrations at our managed bundle path.
// keepID is the UUID of a bundle we are retaining. Never delete that bundle via
// a stale registration: UTM's delete command removes both data and registration.
func removeUtmRegistrations(config alchemy_build.VirtualMachineConfig, keepID string) error {
	ids, err := utmRegistrationIDs(config)
	if err != nil {
		return err
	}
	vmPath, err := utmVirtualMachinePath(config)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if id == keepID {
			continue
		}
		output, err := utmRegistrationPath(id)
		if err != nil {
			return fmt.Errorf("cannot verify registered UTM VM %s bundle path; refusing deletion: %w", id, err)
		}
		if filepath.Clean(strings.TrimSpace(output)) != filepath.Clean(vmPath) {
			return fmt.Errorf("UTM VM %s has the managed name but a different bundle path; refusing deletion", id)
		}
		if err := stopUtmRegistration(id); err != nil {
			return err
		}
		if err := deleteUtmRegistration(vmPath, id, keepID != ""); err != nil {
			return err
		}
	}
	return nil
}

func stopUtmRegistration(id string) error {
	// Force is intentional for destroy/stale-registration cleanup. Wait for the
	// stopped state before deleting: UTM's stop command may complete asynchronously.
	_, err := runUtmAppleScript([]string{
		`tell application "UTM"`,
		fmt.Sprintf(`set targetVM to virtual machine id %q`, id),
		`if (status of targetVM) is not stopped then stop targetVM by force`,
		`end tell`,
	})
	if err != nil {
		return err
	}
	deadline := time.Now().Add(utmGracefulStopTimeout)
	for time.Now().Before(deadline) {
		output, err := runUtmAppleScript([]string{`tell application "UTM"`, fmt.Sprintf(`return (status of virtual machine id %q) as text`, id), `end tell`})
		if err != nil {
			return err
		}
		if strings.EqualFold(strings.TrimSpace(output), "stopped") {
			return nil
		}
		time.Sleep(utmGracefulStopPollInterval)
	}
	return fmt.Errorf("UTM VM %s did not stop before deletion", id)
}

func deleteUtmRegistration(vmPath, id string, preserve bool) (err error) {
	if preserve {
		// Keep the real bundle in a sibling directory on the same filesystem. An
		// empty placeholder lets UTM unregister even when its old bundle is absent.
		backup, e := os.MkdirTemp(filepath.Dir(vmPath), ".sailwright-utm-")
		if e != nil {
			return e
		}
		saved := filepath.Join(backup, "bundle.utm")
		if e := os.Rename(vmPath, saved); e != nil {
			_ = os.Remove(backup)
			return e
		}
		defer func() {
			// Only remove an empty placeholder. Never recursively delete unexpected data.
			if e := os.Remove(vmPath); e != nil && !os.IsNotExist(e) {
				err = errors.Join(err, e)
			}
			if e := os.Rename(saved, vmPath); e != nil {
				err = errors.Join(err, fmt.Errorf("restore UTM bundle from %q: %w", saved, e))
			} else {
				_ = os.Remove(backup)
			}
		}()
	}
	if _, e := os.Stat(vmPath); os.IsNotExist(e) {
		if e := os.MkdirAll(vmPath, 0o750); e != nil {
			return e
		}
		if !preserve {
			defer func() {
				if e := os.Remove(vmPath); e != nil && !os.IsNotExist(e) {
					err = errors.Join(err, e)
				}
			}()
		}
	} else if e != nil {
		return e
	}
	_, err = runUtmAppleScript([]string{`tell application "UTM"`, fmt.Sprintf(`delete virtual machine id %q`, id), `end tell`})
	return err
}

func ensureUtmRegistration(config alchemy_build.VirtualMachineConfig) error {
	id, exists, err := inspectUtmInstanceID(config)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("UTM VM %q does not exist; run sailwright create first", utmVirtualMachineName(config))
	}
	if err := removeUtmRegistrations(config, id); err != nil {
		return err
	}
	ids, err := utmRegistrationIDs(config)
	if err != nil {
		return err
	}
	if slices.Contains(ids, id) {
		return nil
	}
	vmPath, err := utmVirtualMachinePath(config)
	if err != nil {
		return err
	}
	// Open registers the existing bundle in place. The scripting import command
	// copies it into a different bundle, which would break address discovery.
	if _, err := runUtmAppleScript([]string{`tell application "UTM"`, fmt.Sprintf(`open POSIX file %q`, vmPath), `end tell`}); err != nil {
		return err
	}
	for attempt := 0; attempt < 25; attempt++ {
		ids, err := utmRegistrationIDs(config)
		if err != nil {
			return err
		}
		if slices.Contains(ids, id) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("UTM did not register bundle %q with UUID %s", vmPath, id)
}

// Older UTM releases do not expose the scripting file property. Read their
// registry through Foundation as a compatibility fallback; never edit defaults.
// Resolve bookmarks so a moved VM cannot be deleted based on an outdated Path.
func utmRegistrationPath(id string) (string, error) {
	output, err := runUtmAppleScript([]string{`tell application "UTM"`, fmt.Sprintf(`return POSIX path of (file of virtual machine id %q)`, id), `end tell`})
	if err == nil {
		return strings.TrimSpace(output), nil
	}
	script := fmt.Sprintf(`
 ObjC.import('Foundation');
 var home = ObjC.unwrap($.NSHomeDirectory());
 var prefs = $.NSDictionary.dictionaryWithContentsOfFile(home + '/Library/Containers/com.utmapp.UTM/Data/Library/Preferences/com.utmapp.UTM.plist');
 if (!prefs || prefs.isNil()) { prefs = $.NSDictionary.dictionaryWithContentsOfFile(home + '/Library/Preferences/com.utmapp.UTM.plist'); }
 var registry = prefs.objectForKey('Registry');
 var entry = registry.objectForKey(%q);
 var pkg = entry.objectForKey('Package');
 var bookmark = pkg.objectForKey('Bookmark');
 var url = $.NSURL.URLByResolvingBookmarkDataOptionsRelativeToURLBookmarkDataIsStaleError(bookmark, 768, null, Ref(), Ref());
 if (!url || url.isNil()) { throw new Error('Cannot resolve registered UTM bundle bookmark'); }
 ObjC.unwrap(url.path);
 `, strings.ToUpper(id))
	output, err = runCommandWithCombinedOutput(alchemy_build.GetDirectoriesInstance().ProjectDir, utmAutomationCommandTimeout, "osascript", []string{"-l", "JavaScript", "-e", script})
	if err != nil {
		return "", fmt.Errorf("resolve UTM registry bookmark: %w; output: %s", err, strings.TrimSpace(output))
	}
	result := strings.TrimSpace(output)
	if !filepath.IsAbs(result) {
		return "", fmt.Errorf("invalid UTM registry bundle path %q", result)
	}
	return result, nil
}

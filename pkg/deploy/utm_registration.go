package deploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

// utmRegistrationSettleAttempts and utmRegistrationSettleInterval bound how long
// a persisted registration may outlive its scripting entry. UTM writes its
// preferences asynchronously, so right after a delete the old entry can still
// be listed there even though the scripting collection no longer has it.
var (
	utmRegistrationSettleAttempts = 40
	utmRegistrationSettleInterval = 250 * time.Millisecond
)

func utmRegistrationIDs(config alchemy_build.VirtualMachineConfig) ([]string, error) {
	var lastErr error
	for attempt := 0; attempt < utmRegistrationSettleAttempts; attempt++ {
		ids, unavailable, err := utmRegistrationSnapshotFunc(config)
		if err != nil {
			return nil, err
		}
		if unavailable == "" {
			return ids, nil
		}
		lastErr = fmt.Errorf("UTM registration %s for %q is not available through scripting; remove the unavailable entry in the UTM library manually, then retry (preferences may still be synchronizing)", unavailable, utmVirtualMachineName(config))
		time.Sleep(utmRegistrationSettleInterval)
	}
	if lastErr == nil {
		return utmScriptableRegistrationIDs(config)
	}
	return nil, lastErr
}

var utmRegistrationSnapshotFunc = utmRegistrationSnapshot

// utmRegistrationSnapshot returns the scriptable IDs and the first persisted ID
// that scripting does not know about, or "" when the two views agree.
func utmRegistrationSnapshot(config alchemy_build.VirtualMachineConfig) (ids []string, unavailable string, err error) {
	ids, err = utmScriptableRegistrationIDs(config)
	if err != nil {
		return nil, "", err
	}
	persisted, err := utmPersistentRegistrationIDs(config)
	if err != nil {
		return nil, "", err
	}
	for _, id := range persisted {
		if !slices.Contains(ids, id) {
			return nil, id, nil
		}
	}
	return ids, "", nil
}

// UTM excludes entries whose bundles could not be loaded from its scripting
// collection. Read persistent entries as well so an empty collection cannot be
// mistaken for successful cleanup. Never modify the preferences directly.
func utmPersistentRegistrationIDs(config alchemy_build.VirtualMachineConfig) ([]string, error) {
	vmPath, err := utmVirtualMachinePath(config)
	if err != nil {
		return nil, err
	}
	script := fmt.Sprintf(`
 // sailwright: inspect persistent UTM registrations
 ObjC.import('Foundation');
 var home = ObjC.unwrap($.NSHomeDirectory());
 var paths = [home + '/Library/Containers/com.utmapp.UTM/Data/Library/Preferences/com.utmapp.UTM.plist', home + '/Library/Preferences/com.utmapp.UTM.plist'];
 var matches = [];
 paths.forEach(function(path) {
   if (!$.NSFileManager.defaultManager.fileExistsAtPath(path)) { return; }
   var prefs = $.NSDictionary.dictionaryWithContentsOfFile(path);
   if (!prefs || prefs.isNil()) { throw new Error('Cannot read UTM preferences: ' + path); }
   var registry = prefs.objectForKey('Registry');
   if (!registry || registry.isNil()) { return; }
   ObjC.deepUnwrap(registry.allKeys).forEach(function(id) {
     var entry = registry.objectForKey(id);
     var name = entry.objectForKey('Name');
     var pkg = entry.objectForKey('Package');
     var path = pkg && !pkg.isNil() ? pkg.objectForKey('Path') : null;
     if ((name && !name.isNil() && ObjC.unwrap(name) === %q) || (path && !path.isNil() && ObjC.unwrap(path) === %q)) { matches.push(id); }
   });
 });
 JSON.stringify(matches);
 `, utmVirtualMachineName(config), vmPath)
	output, err := runCommandWithCombinedOutput(alchemy_build.GetDirectoriesInstance().ProjectDir, utmAutomationCommandTimeout, "osascript", []string{"-l", "JavaScript", "-e", script})
	if err != nil {
		return nil, fmt.Errorf("inspect persistent UTM registrations: %w; output: %s", err, strings.TrimSpace(output))
	}
	var ids []string
	if err := json.Unmarshal([]byte(output), &ids); err != nil || ids == nil {
		return nil, fmt.Errorf("invalid persistent UTM registration list %q", truncateForError(output, 128))
	}
	for i, id := range ids {
		if !instanceIDPattern.MatchString(id) {
			return nil, fmt.Errorf("invalid persistent UTM registration UUID %q", truncateForError(id, 64))
		}
		ids[i] = strings.ToLower(id)
	}
	return ids, nil
}

func utmScriptableRegistrationIDs(config alchemy_build.VirtualMachineConfig) ([]string, error) {
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
	if err := verifyUtmRegistrationPath(config, id); err != nil {
		return "", err
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
	for _, id := range ids {
		if err := verifyUtmRegistrationPath(config, id); err != nil {
			return err
		}
	}
	vmPath, err := utmVirtualMachinePath(config)
	if err != nil {
		return err
	}
	// Validate the entire set before changing any registration.
	for _, id := range ids {
		if id == keepID {
			continue
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

// A copied bundle can retain its UUID. Verify location before using that UUID
// to control the registered VM, even when it matches the managed bundle.
func verifyUtmRegistrationPath(config alchemy_build.VirtualMachineConfig, id string) error {
	vmPath, err := utmVirtualMachinePath(config)
	if err != nil {
		return err
	}
	registered, err := utmRegistrationPath(id)
	if err != nil {
		return fmt.Errorf("cannot verify registered UTM VM %s bundle path; refusing operation: %w", id, err)
	}
	if filepath.IsAbs(registered) {
		if filepath.Clean(registered) == filepath.Clean(vmPath) {
			return nil
		}
		// Accept aliases only when both paths resolve to the same bundle.
		actual, actualErr := filepath.EvalSymlinks(registered)
		managed, managedErr := filepath.EvalSymlinks(vmPath)
		if actualErr == nil && managedErr == nil && actual == managed {
			return nil
		}
	}
	return fmt.Errorf("UTM VM %s has a different or unverifiable bundle path; refusing operation", id)
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
	// The initial cleanup checked persistent entries before any mutation. Query
	// the live scripting collection here; preference files can lag a deletion.
	ids, err := utmScriptableRegistrationIDs(config)
	if err != nil {
		return err
	}
	if slices.Contains(ids, id) {
		return verifyUtmRegistrationPath(config, id)
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
	var pathErr error
	for attempt := 0; attempt < 25; attempt++ {
		ids, err := utmScriptableRegistrationIDs(config)
		if err != nil {
			return err
		}
		if slices.Contains(ids, id) {
			// UTM can publish the scripting entry before its bookmark reaches
			// preferences. Keep the path guard closed while that write catches up.
			pathErr = verifyUtmRegistrationPath(config, id)
			if pathErr == nil {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if pathErr != nil {
		return pathErr
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
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home for UTM registry: %w", err)
	}
	script := utmRegistryBookmarkScript(id, []string{
		filepath.Join(home, "Library", "Containers", "com.utmapp.UTM", "Data", "Library", "Preferences", "com.utmapp.UTM.plist"),
		filepath.Join(home, "Library", "Preferences", "com.utmapp.UTM.plist"),
	})
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

func utmRegistryBookmarkScript(id string, paths []string) string {
	encodedPaths, _ := json.Marshal(paths) // A slice of strings cannot fail JSON encoding.
	return fmt.Sprintf(`
 ObjC.import('Foundation');
 (function() {
   var paths = %s;
   for (var i = 0; i < paths.length; i++) {
     var prefs = $.NSDictionary.dictionaryWithContentsOfFile(paths[i]);
     if (!prefs || prefs.isNil()) { continue; }
     var registry = prefs.objectForKey('Registry');
     if (!registry || registry.isNil()) { continue; }
     var entry = registry.objectForKey(%q);
     if (!entry || entry.isNil()) { continue; }
     var pkg = entry.objectForKey('Package');
     if (!pkg || pkg.isNil()) { throw new Error('Missing UTM registry package'); }
     var bookmark = pkg.objectForKey('Bookmark');
     if (!bookmark || bookmark.isNil()) { throw new Error('Missing UTM registry bookmark'); }
     // JavaScript null bridges to NSNull here; NSURL requires Objective-C nil.
     var url = $.NSURL.URLByResolvingBookmarkDataOptionsRelativeToURLBookmarkDataIsStaleError(bookmark, 768, $(), Ref(), Ref());
     if (!url || url.isNil()) { throw new Error('Cannot resolve registered UTM bundle bookmark'); }
     return ObjC.unwrap(url.path);
   }
   throw new Error('UTM registry entry is not persisted yet');
 })();
 `, encodedPaths, strings.ToUpper(id))
}

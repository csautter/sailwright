package deploy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Exercise Foundation itself: mocking osascript cannot detect JavaScript null
// being bridged to NSNull where NSURL expects an Objective-C nil relative URL.
func TestUtmRegistryBookmarkUsesNativeFoundation(t *testing.T) {
	for _, withBookmark := range []bool{true, false} {
		t.Run(fmt.Sprintf("bookmark=%v", withBookmark), func(t *testing.T) {
			root := t.TempDir()
			bundle := filepath.Join(root, "test guest.utm")
			if err := os.Mkdir(bundle, 0o700); err != nil {
				t.Fatal(err)
			}
			prefs := filepath.Join(root, "registry.plist")
			fixture := fmt.Sprintf(`
 ObjC.import('Foundation');
 var pkg = $.NSMutableDictionary.alloc.init;
 pkg.setObjectForKey('/outdated/unrelated.utm', 'Path');
 if (%t) {
   var url = $.NSURL.fileURLWithPath(%q);
   var bookmark = url.bookmarkDataWithOptionsIncludingResourceValuesForKeysRelativeToURLError(0, $(), $(), Ref());
   if (!bookmark || bookmark.isNil()) { throw new Error('Fixture bookmark creation failed'); }
   pkg.setObjectForKey(bookmark, 'Bookmark');
 }
 var entry = $.NSMutableDictionary.alloc.init;
 entry.setObjectForKey(pkg, 'Package');
 var registry = $.NSMutableDictionary.alloc.init;
 registry.setObjectForKey(entry, %q);
 var prefs = $.NSMutableDictionary.alloc.init;
 prefs.setObjectForKey(registry, 'Registry');
 if (!prefs.writeToFileAtomically(%q, true)) { throw new Error('Fixture preferences write failed'); }
 `, withBookmark, bundle, strings.ToUpper(newUtmID), prefs)
			run := func(script string) (string, error) {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				output, err := exec.CommandContext(ctx, "osascript", "-l", "JavaScript", "-e", script).CombinedOutput()
				return strings.TrimSpace(string(output)), err
			}
			if output, err := run(fixture); err != nil {
				t.Fatalf("create Foundation fixture: %v: %s", err, output)
			}
			// An existing container preferences file may not contain this UUID;
			// the non-sandboxed preferences must still be searched.
			empty := filepath.Join(root, "empty.plist")
			if err := os.WriteFile(empty, []byte(`<?xml version="1.0"?><plist version="1.0"><dict/></plist>`), 0o600); err != nil {
				t.Fatal(err)
			}
			output, err := run(utmRegistryBookmarkScript(newUtmID, []string{empty, prefs}))
			if !withBookmark {
				if err == nil || !strings.Contains(output, "Missing UTM registry bookmark") {
					t.Fatalf("must not trust the cached Path without a bookmark: %v: %s", err, output)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolve native Foundation bookmark: %v: %s", err, output)
			}
			got, err := filepath.EvalSymlinks(output)
			if err != nil {
				t.Fatal(err)
			}
			want, err := filepath.EvalSymlinks(bundle)
			if err != nil || got != want {
				t.Fatalf("resolved %q, want %q: %v", got, want, err)
			}
		})
	}
}

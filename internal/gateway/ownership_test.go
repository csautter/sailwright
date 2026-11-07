package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type namedOwnershipResolver struct {
	ManagedVMResolver
	identify func(string) (VMIdentity, error)
}

func (r namedOwnershipResolver) Identify(_ context.Context, osName, typ, arch string, names ...string) (VMIdentity, error) {
	return r.identify(vmKey(osName, typ, arch, names...))
}

func TestUnlistedNamedOwnershipRequiresConfirmedAbsence(t *testing.T) {
	for _, engine := range []string{"tart", "utm"} {
		for _, outcome := range []string{"missing", "present", "failed", "new-record"} {
			t.Run(engine+"/"+outcome, func(t *testing.T) {
				dir := t.TempDir()
				o, err := loadVMOwnership(true, dir)
				if err != nil {
					t.Fatal(err)
				}
				const owned = "macos//arm64/work"
				const granted = "macos//arm64/granted"
				if err := o.created(owned, "job", ""); err != nil {
					t.Fatal(err)
				}
				o.endRequest(granted, true, "")
				listedAt := time.Now().Add(time.Second)
				if outcome == "new-record" {
					listedAt = time.Now().Add(-time.Second)
				}
				calls := 0
				g := &Gateway{ownership: o, console: io.Discard, vmResolver: namedOwnershipResolver{identify: func(key string) (VMIdentity, error) {
					calls++
					if key != owned && key != granted {
						t.Fatal(key)
					}
					if outcome == "failed" {
						return VMIdentity{}, errors.New("provider unavailable")
					}
					return VMIdentity{Exists: outcome == "present"}, nil
				}}}
				// A complete-looking listing still does not itself prove absence.
				no := false
				rows := g.identifyUnlistedNamedVMs(context.Background(), []Target{{OS: "macos", Arch: "arm64", Engine: engine, Exists: &no}}, listedAt)
				if err := o.prune(rows, listedAt); err != nil {
					t.Fatal(err)
				}
				want := outcome != "missing"
				for _, key := range []string{owned, granted} {
					if allowed, err := o.confirm(key, ""); allowed != want || err != nil {
						t.Fatalf("replacement authorization %s = %v, %v", key, allowed, err)
					}
				}
				reloaded, err := loadVMOwnership(true, dir)
				if err != nil || reloaded.allowed(owned) != want {
					t.Fatalf("persisted ownership: %v", err)
				}
				if outcome == "new-record" && calls != 0 {
					t.Fatal("queried a record newer than the listing")
				}
			})
		}
	}
}

func TestUnlistedNamedOwnershipPreservesConcurrentCreate(t *testing.T) {
	o, err := loadVMOwnership(true, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const key = "macos//arm64/work"
	if err := o.created(key, "old", ""); err != nil {
		t.Fatal(err)
	}
	listedAt := time.Now()
	g := &Gateway{ownership: o, console: io.Discard, vmResolver: namedOwnershipResolver{identify: func(string) (VMIdentity, error) {
		// A create completes while an older missing lookup is in flight.
		if err := o.created(key, "new", ""); err != nil {
			t.Fatal(err)
		}
		return VMIdentity{}, nil
	}}}
	rows := g.identifyUnlistedNamedVMs(context.Background(), nil, listedAt)
	if err := o.prune(rows, listedAt); err != nil {
		t.Fatal(err)
	}
	if !o.allowed(key) {
		t.Fatal("older lookup revoked newly created VM")
	}
}

func TestOwnershipConfirmChecksInstance(t *testing.T) {
	dir := t.TempDir()
	o, err := loadVMOwnership(true, dir)
	if err != nil {
		t.Fatal(err)
	}
	const key = "ubuntu/server/amd64"
	if err := o.created(key, "job1", ""); err != nil {
		t.Fatal(err)
	}
	if o.knowsInstance(key) {
		t.Fatal("record without id claims to know its instance")
	}
	// An engine without ids cannot disprove the record.
	if ok, err := o.confirm(key, ""); !ok || err != nil {
		t.Fatalf("empty current id: %v %v", ok, err)
	}
	// A record made without an id adopts the first one seen, persistently.
	if ok, err := o.confirm(key, "uuid-a"); !ok || err != nil {
		t.Fatalf("adopt: %v %v", ok, err)
	}
	reloaded, err := loadVMOwnership(true, dir)
	if err != nil || !reloaded.knowsInstance(key) {
		t.Fatalf("adopted id not persisted: %v", err)
	}
	if ok, _ := reloaded.confirm(key, "uuid-a"); !ok {
		t.Fatal("same instance refused")
	}
	// Another instance under the same name ends ownership.
	if ok, err := reloaded.confirm(key, "uuid-b"); ok || err != nil {
		t.Fatalf("recreated VM still owned: %v %v", ok, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ownershipFileName))
	if err != nil || strings.Contains(string(data), "ubuntu") {
		t.Fatalf("dropped record still on disk: %s %v", data, err)
	}

	// Grants are tied to the instance the operator was asked about.
	reloaded.endRequest(key, true, "uuid-b")
	if ok, _ := reloaded.confirm(key, "uuid-b"); !ok {
		t.Fatal("granted instance refused")
	}
	if ok, _ := reloaded.confirm(key, "uuid-c"); ok {
		t.Fatal("grant carried over to a recreated VM")
	}
}

func TestOwnershipDisabledAllowsEverything(t *testing.T) {
	o, err := loadVMOwnership(false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := o.confirm("ubuntu/server/amd64", "any"); !ok || err != nil || o.knowsInstance("ubuntu/server/amd64") {
		t.Fatalf("isolated mode gated a VM: %v %v", ok, err)
	}
}

// prune trusts the structured `exists` field over the "create" display text, and
// still understands CLIs that only report the text.
func TestOwnershipPruneUsesStructuredExists(t *testing.T) {
	o, err := loadVMOwnership(true, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{"a//amd64", "b//amd64", "c//amd64", "d//amd64", "e//amd64"}
	for _, key := range keys {
		if err := o.created(key, "job", ""); err != nil {
			t.Fatal(err)
		}
	}
	yes, no := true, false
	listing := []Target{
		// Renamed display text must not drop a VM that exists.
		{OS: "a", Arch: "amd64", Exists: &yes, Fields: map[string]string{"create": "exists"}},
		{OS: "b", Arch: "amd64", Exists: &no, Fields: map[string]string{"create": "ready to create"}},
		// Older CLIs without `exists`.
		{OS: "c", Arch: "amd64", Fields: map[string]string{"create": "already created"}},
		{OS: "d", Arch: "amd64", Fields: map[string]string{"create": "ready to create"}},
		// No information at all proves nothing.
		{OS: "e", Arch: "amd64"},
	}
	if err := o.prune(listing, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]bool{"a//amd64": true, "b//amd64": false, "c//amd64": true, "d//amd64": false, "e//amd64": true} {
		if got := o.allowed(key); got != want {
			t.Errorf("%s owned after prune = %v, want %v", key, got, want)
		}
	}
}

// macOS identifiers enter the same ownership check as libvirt/Hyper-V. A target
// listing that still reports an existing VM must not carry ownership or grants
// across replacement, including when no listing observed the absent interval.
func TestMacOSOwnershipReplacement(t *testing.T) {
	for _, engine := range []struct{ name, oldID, newID string }{
		{"utm", "aaaaaaaa-1111-2222-3333-444444444444", "bbbbbbbb-1111-2222-3333-444444444444"},
		{"tart", "tart:old-directory-identity", "tart:new-directory-identity"},
	} {
		for _, listing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/listing=%v", engine.name, listing), func(t *testing.T) {
				o, err := loadVMOwnership(true, t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				key := "ubuntu/server/arm64"
				if engine.name == "tart" {
					key = "macos//arm64"
				}
				if err := o.created(key, "job", engine.oldID); err != nil {
					t.Fatal(err)
				}
				if listing {
					exists := true
					parts := strings.Split(key, "/")
					if err := o.prune([]Target{{OS: parts[0], Type: parts[1], Arch: parts[2], Exists: &exists}}, time.Now().Add(time.Second)); err != nil {
						t.Fatal(err)
					}
				}
				if ok, err := o.confirm(key, engine.newID); ok || err != nil {
					t.Fatalf("replacement still owned: %v %v", ok, err)
				}
				o.endRequest(key, true, engine.oldID)
				if ok, err := o.confirm(key, engine.newID); ok || err != nil {
					t.Fatalf("replacement inherited approval: %v %v", ok, err)
				}
			})
		}
	}
}

package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

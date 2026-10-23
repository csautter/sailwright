package gateway

import (
	"errors"
	"testing"
)

func TestStateLockIsExclusiveUntilReleased(t *testing.T) {
	dir := t.TempDir()
	first, err := AcquireStateLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireStateLock(dir); !errors.Is(err, ErrStateLocked) {
		t.Fatalf("expected ErrStateLocked, got %v", err)
	}
	first.Release()
	again, err := AcquireStateLock(dir)
	if err != nil {
		t.Fatalf("lock should be free after release: %v", err)
	}
	again.Release()
	var nilLock *StateLock
	nilLock.Release()
}

package deploy

import (
	"strings"
	"testing"
	"time"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

func stubUtmRegistrationSettle(t *testing.T, attempts int, snapshot func(alchemy_build.VirtualMachineConfig) ([]string, string, error)) {
	t.Helper()
	oldAttempts, oldInterval, oldSnapshot := utmRegistrationSettleAttempts, utmRegistrationSettleInterval, utmRegistrationSnapshotFunc
	utmRegistrationSettleAttempts, utmRegistrationSettleInterval, utmRegistrationSnapshotFunc = attempts, time.Millisecond, snapshot
	t.Cleanup(func() {
		utmRegistrationSettleAttempts, utmRegistrationSettleInterval, utmRegistrationSnapshotFunc = oldAttempts, oldInterval, oldSnapshot
	})
}

func TestUtmRegistrationIDsWaitsForLaggingPreferencesAfterDelete(t *testing.T) {
	calls := 0
	stubUtmRegistrationSettle(t, 10, func(alchemy_build.VirtualMachineConfig) ([]string, string, error) {
		calls++
		if calls < 4 {
			return nil, "e9f98a83-d620-40d4-8801-15716d330642", nil
		}
		return nil, "", nil
	})
	ids, err := utmRegistrationIDs(alchemy_build.VirtualMachineConfig{})
	if err != nil || len(ids) != 0 || calls != 4 {
		t.Fatalf("ids=%v err=%v calls=%d", ids, err, calls)
	}
}

func TestUtmRegistrationIDsStillReportsPersistentOrphan(t *testing.T) {
	calls := 0
	stubUtmRegistrationSettle(t, 3, func(alchemy_build.VirtualMachineConfig) ([]string, string, error) {
		calls++
		return nil, "e9f98a83-d620-40d4-8801-15716d330642", nil
	})
	_, err := utmRegistrationIDs(alchemy_build.VirtualMachineConfig{})
	if err == nil || !strings.Contains(err.Error(), "not available through scripting") || calls != 3 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

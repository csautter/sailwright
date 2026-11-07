package gateway

import "testing"

func TestInstanceBudgetUsesTypedExistence(t *testing.T) {
	present, absent := true, false
	for _, tt := range []struct {
		name   string
		target Target
		want   bool
	}{
		{"typed existing with new display text", Target{Exists: &present, Fields: map[string]string{"create": "present"}}, true},
		{"typed missing overrides display text", Target{Exists: &absent, Fields: map[string]string{"create": legacyCreatedState}}, false},
		{"legacy existing", Target{Fields: map[string]string{"create": legacyCreatedState}}, true},
		{"legacy missing", Target{Fields: map[string]string{"create": "ready to create"}}, false},
		{"no existence information", Target{}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := listedAsExisting(tt.target); got != tt.want {
				t.Fatalf("listedAsExisting() = %v, want %v", got, tt.want)
			}
		})
	}
}

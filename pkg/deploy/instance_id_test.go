package deploy

import "testing"

func TestParseInstanceID(t *testing.T) {
	for _, tc := range []struct {
		output, id string
		exists, ok bool
	}{
		{"8F0C2A1E-3B4D-4C5E-9F60-718293A4B5C6\r\n", "8f0c2a1e-3b4d-4c5e-9f60-718293a4b5c6", true, true},
		{"WARNING: noise\n8f0c2a1e-3b4d-4c5e-9f60-718293a4b5c6\n", "8f0c2a1e-3b4d-4c5e-9f60-718293a4b5c6", true, true},
		{"missing\n", "", false, true},
		{"not-an-id\n", "", false, false},
		{"", "", false, false},
	} {
		id, exists, err := parseInstanceID(tc.output)
		if id != tc.id || exists != tc.exists || (err == nil) != tc.ok {
			t.Errorf("parseInstanceID(%q) = %q, %v, %v", tc.output, id, exists, err)
		}
	}
}

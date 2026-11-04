package credentials

import "testing"

func TestIsKnownInsecure(t *testing.T) {
	for _, tc := range []struct {
		user, password string
		insecure       bool
	}{
		{"Administrator", "P@ssw0rd!", true}, {"admin", "admin", true},
		{"packer", "packer", true}, {"vagrant", "vagrant", true},
		{"user", "", true}, {"user", "12345678901", true},
		{"user", "123456789012", false}, {"user", "ééééééééééé", true},
		{"user", "Unique-Guest-Passphrase", false},
	} {
		if got := IsKnownInsecure(tc.user, tc.password); got != tc.insecure {
			t.Errorf("classification for user %q: got %t, want %t", tc.user, got, tc.insecure)
		}
	}
}

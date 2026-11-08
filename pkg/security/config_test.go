package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllowInsecureCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if allow, err := AllowInsecureCredentials(path); err != nil || allow {
		t.Fatalf("missing config: %t, %v", allow, err)
	}
	for _, tc := range []struct {
		yaml          string
		want, failure bool
	}{
		{"", false, false}, {"security:\n  allow_insecure_credentials: true\n", true, false},
		{"security:\n  allow_insecure_credentials: false\n", false, false},
		{"security:\n  allow_insecure_credential: true\n", false, true},
		{"security:\n  allow_insecure_credentials: secret-value\n", false, true},
		{"security:\n  allow_insecure_credentials: true\n---\nsecurity: {}\n", false, true},
		{"security: [", false, true},
	} {
		if err := os.WriteFile(path, []byte(tc.yaml), 0o600); err != nil {
			t.Fatal(err)
		}
		allow, err := AllowInsecureCredentials(path)
		if allow != tc.want || (err != nil) != tc.failure {
			t.Errorf("config result: %t, %v", allow, err)
		}
		if err != nil && strings.Contains(err.Error(), "secret-value") {
			t.Fatal("configuration values must not be included in errors")
		}
	}
}

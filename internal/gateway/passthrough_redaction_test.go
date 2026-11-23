package gateway

import (
	"strings"
	"testing"
)

func TestPassthroughSecretsKeepsTartNetworkSettingsVisible(t *testing.T) {
	values := map[string]string{
		"TART_MACOS_DNS_SERVERS":        "192.0.2.53",
		"TART_MACOS_NETWORK_CHECK_URLS": "https://example.com/",
		"TART_MACOS_BRIDGED_INTERFACE":  "en0",
		"OTHER_TOKEN":                   "s3cret-value",
	}
	getenv := func(name string) string { return values[name] }
	passthrough := []string{"TART_MACOS_DNS_SERVERS", "TART_MACOS_NETWORK_CHECK_URLS", "TART_MACOS_BRIDGED_INTERFACE", "OTHER_TOKEN"}

	secrets := PassthroughSecrets(passthrough, getenv)
	if len(secrets) != 1 || secrets[0] != "s3cret-value" {
		t.Fatalf("expected only the non-Tart passthrough value to be redacted, got %q", secrets)
	}

	line := NewRedactor(secrets...).String("guest resolvers: 192.0.2.53 (timeout); token s3cret-value")
	if !strings.Contains(line, "192.0.2.53") || strings.Contains(line, "s3cret-value") {
		t.Fatalf("expected resolver visible and token redacted, got %q", line)
	}
}

func TestPassthroughSecretsStillRedactsOtherTartVariables(t *testing.T) {
	values := map[string]string{"TART_MACOS_ANSIBLE_PASSWORD": "guest-pass"}
	secrets := PassthroughSecrets([]string{"TART_MACOS_ANSIBLE_PASSWORD"}, func(name string) string { return values[name] })
	if len(secrets) != 1 || secrets[0] != "guest-pass" {
		t.Fatalf("expected Tart credentials to stay redacted, got %q", secrets)
	}
}

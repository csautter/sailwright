package gateway

import "testing"

func TestRedactorLearnsGuestPasswordsIncludingShortValues(t *testing.T) {
	redactor := NewRedactor("existing-secret")
	for i := 0; i < 100; i++ {
		redactor.AddRequired("xy", "guest-secret")
	}
	if got := redactor.String("existing-secret guest-secret xy"); got != "*** *** ***" {
		t.Fatal(got)
	}
	if len(redactor.secrets) != 3 {
		t.Fatalf("repeated VM lookups grew secret storage: %d", len(redactor.secrets))
	}
}

func TestRedactorAddRequiredKeepsOptionalThreshold(t *testing.T) {
	redactor := NewRedactor("existing-secret")
	redactor.AddRequired("xy")
	derived := redactor.With("ab", "longer-secret")
	if got := derived.String("xy ab longer-secret"); got != "*** ab ***" {
		t.Fatalf("learned short secret must stay, new short optional value must be ignored: %q", got)
	}
}

func TestGatewayDefaultRedactionKeepsOrdinaryWords(t *testing.T) {
	redactor := NewRedactor("gateway-token", defaultGuestPassword)
	in := "sysadmin /home/admin admin@example.test password P@ssw0rd!"
	if got := redactor.String(in); got != "sysadmin /home/admin admin@example.test password ***" {
		t.Fatal(got)
	}
}

func TestDerivedRedactorSeesSecretsLearnedLater(t *testing.T) {
	base := NewRedactor("existing-secret")
	job := base.WithRequired("pw")
	// Use the derived redactor first, so its merged list is cached before the
	// parent learns more.
	if got := job.String("existing-secret pw guest-learned-later"); got != "*** *** guest-learned-later" {
		t.Fatalf("before learning: %q", got)
	}
	base.AddRequired("guest-learned-later")
	if got := job.String("existing-secret pw guest-learned-later"); got != "*** *** ***" {
		t.Fatalf("derived redactor missed a secret its parent learned later: %q", got)
	}
	if got := base.String("pw"); got != "pw" {
		t.Fatalf("a derived secret leaked into the parent: %q", got)
	}
	if job.maxSecretLen() != len("guest-learned-later") {
		t.Fatalf("max secret length ignores the parent: %d", job.maxSecretLen())
	}
}

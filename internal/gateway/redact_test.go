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

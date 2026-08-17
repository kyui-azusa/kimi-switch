package secret

import (
	"testing"

	keyring "github.com/zalando/go-keyring"
)

func TestSystemCredentialRoundTrip(t *testing.T) {
	keyring.MockInit()
	t.Setenv("USER", "kimi-switch-test")

	const service = "kimi-switch/test"
	const want = "test-api-key"
	if err := Write(service, want); err != nil {
		t.Fatal(err)
	}
	got, err := Read(service)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("credential = %q, want %q", got, want)
	}
}

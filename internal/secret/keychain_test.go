package secret

import (
	"errors"
	"strings"
	"testing"
	"time"

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

func TestReadRetriesTransientBrokenPipe(t *testing.T) {
	t.Setenv("USER", "kimi-switch-test")
	originalGet := keyringGet
	originalSleep := retrySleep
	defer func() {
		keyringGet = originalGet
		retrySleep = originalSleep
	}()

	calls := 0
	sleeps := 0
	keyringGet = func(service, user string) (string, error) {
		calls++
		if calls < 3 {
			return "", errors.New("signal: broken pipe")
		}
		return "restored-key", nil
	}
	retrySleep = func(time.Duration) {
		sleeps++
	}

	got, err := Read("kimi-switch/tyx")
	if err != nil {
		t.Fatal(err)
	}
	if got != "restored-key" {
		t.Fatalf("credential = %q, want restored-key", got)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
	if sleeps != 2 {
		t.Fatalf("sleeps = %d, want 2", sleeps)
	}
}

func TestReadDoesNotRetryNonTransientError(t *testing.T) {
	t.Setenv("USER", "kimi-switch-test")
	originalGet := keyringGet
	originalSleep := retrySleep
	defer func() {
		keyringGet = originalGet
		retrySleep = originalSleep
	}()

	calls := 0
	sleeps := 0
	keyringGet = func(service, user string) (string, error) {
		calls++
		return "", errors.New("permission denied")
	}
	retrySleep = func(time.Duration) {
		sleeps++
	}

	_, err := Read("kimi-switch/tyx")
	if err == nil {
		t.Fatal("expected read error")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if sleeps != 0 {
		t.Fatalf("sleeps = %d, want 0", sleeps)
	}
}

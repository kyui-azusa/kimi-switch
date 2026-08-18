package secret

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"strings"
	"time"

	keyring "github.com/zalando/go-keyring"
)

const transientReadRetryCount = 3

var (
	keyringGet = keyring.Get
	retrySleep = time.Sleep
)

func Read(service string) (string, error) {
	account := currentAccount()
	var value string
	var err error
	for attempt := 1; attempt <= transientReadRetryCount; attempt++ {
		value, err = keyringGet(service, account)
		if err == nil {
			break
		}
		if !isTransientReadError(err) || attempt == transientReadRetryCount {
			return "", fmt.Errorf("read system credential %q: %w", service, err)
		}
		retrySleep(time.Duration(attempt*50) * time.Millisecond)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("system credential %q is empty", service)
	}
	return value, nil
}

func Write(service, value string) error {
	if value == "" {
		return errors.New("keychain value cannot be empty")
	}
	account := currentAccount()
	if err := keyring.Set(service, account, value); err != nil {
		return fmt.Errorf("write system credential %q: %w", service, err)
	}
	return nil
}

func isTransientReadError(err error) bool {
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "broken pipe") ||
		strings.Contains(message, "signal: broken pipe") ||
		strings.Contains(message, "connection reset by peer")
}

func currentAccount() string {
	if account := os.Getenv("USER"); account != "" {
		return account
	}
	if account := os.Getenv("USERNAME"); account != "" {
		return account
	}
	if current, err := user.Current(); err == nil && current.Username != "" {
		return current.Username
	}
	return "kimi-switch"
}

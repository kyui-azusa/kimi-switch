package secret

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"strings"

	keyring "github.com/zalando/go-keyring"
)

func Read(service string) (string, error) {
	account := currentAccount()
	value, err := keyring.Get(service, account)
	if err != nil {
		return "", fmt.Errorf("read system credential %q: %w", service, err)
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

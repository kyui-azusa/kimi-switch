package secret

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func Read(service string) (string, error) {
	if _, err := exec.LookPath("security"); err != nil {
		return "", errors.New("macOS security command is unavailable")
	}
	account := currentAccount()
	out, err := exec.Command("security", "find-generic-password", "-a", account, "-s", service, "-w").Output()
	if err != nil {
		return "", fmt.Errorf("read keychain item %q: %w", service, err)
	}
	value := strings.TrimSpace(string(out))
	if value == "" {
		return "", fmt.Errorf("keychain item %q is empty", service)
	}
	return value, nil
}

func Write(service, value string) error {
	if value == "" {
		return errors.New("keychain value cannot be empty")
	}
	if _, err := exec.LookPath("security"); err != nil {
		return errors.New("macOS security command is unavailable")
	}
	account := currentAccount()
	if err := exec.Command("security", "add-generic-password", "-a", account, "-s", service, "-w", value, "-U").Run(); err != nil {
		return fmt.Errorf("write keychain item %q: %w", service, err)
	}
	return nil
}

func currentAccount() string {
	if account := os.Getenv("USER"); account != "" {
		return account
	}
	return "kimi-switch"
}

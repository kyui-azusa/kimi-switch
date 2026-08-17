package kimiimport

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseProvidersAndEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `[providers.work]
type = "kimi"
api_key = "sk-work"

[providers.personal.env]
KIMI_API_KEY = 'sk-personal'
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	providers, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if providers["work"] != "sk-work" || providers["personal"] != "sk-personal" {
		t.Fatalf("providers = %#v", providers)
	}
}

func TestQuotedHashIsNotAComment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := "[providers.work]\napi_key = \"sk-test#value\" # comment\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	providers, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if providers["work"] != "sk-test#value" {
		t.Fatalf("value = %q", providers["work"])
	}
}

func TestQuotedProviderNameIsUnquoted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := "[providers.\"managed:kimi-code\"]\napi_key = \"sk-managed\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	providers, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if providers["managed:kimi-code"] != "sk-managed" {
		t.Fatalf("providers = %#v", providers)
	}
}

package kimitakeover

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixture = `default_model = "kimi-code/k3-256k"

[providers."managed:kimi-code"]
type = "kimi"
api_key = "secret"
base_url = "https://api.kimi.com/coding/v1"

[models."kimi-code/k3"]
provider = "managed:kimi-code"
model = "k3"
max_context_size = 1048576

[models."kimi-code/k3-256k"]
provider = "managed:kimi-code"
model = "k3-256k"
max_context_size = 262144

[models.other]
provider = "other-provider"
model = "other"
max_context_size = 1000
`

func TestEnableAndDisable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := Set(path, "127.0.0.1:15721", "local-key", true)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || status.AffectedModels != 2 || !status.ReloadRequired {
		t.Fatalf("unexpected status: %+v", status)
	}
	content, _ := os.ReadFile(path)
	text := string(content)
	if strings.Contains(text, `api_key = "secret"`) == false {
		t.Fatal("original provider was modified")
	}
	if got := strings.Count(text, `provider = "kimi-switch-local"`); got != 2 {
		t.Fatalf("managed model count = %d", got)
	}
	if !strings.Contains(text, `provider = "other-provider"`) {
		t.Fatal("unrelated model was modified")
	}
	if _, err := os.Stat(path + ".kimiswitch.bak"); err != nil {
		t.Fatal("backup missing:", err)
	}

	status, err = Set(path, "127.0.0.1:15721", "local-key", false)
	if err != nil {
		t.Fatal(err)
	}
	if status.Enabled || !status.ReloadRequired {
		t.Fatalf("unexpected disabled status: %+v", status)
	}
	restored, _ := os.ReadFile(path)
	if string(restored) != fixture {
		t.Fatalf("config was not restored:\n%s", restored)
	}
}

func TestEnableRequiresDefaultModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[providers.kimi]\ntype = \"kimi\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Set(path, "127.0.0.1:15721", "local", true); err == nil {
		t.Fatal("expected missing default_model error")
	}
}

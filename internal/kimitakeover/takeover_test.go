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

// brokenFixture reproduces the config shape that made Kimi Code reject every model:
// an unmanaged [providers.kimi-switch-local] table next to the managed one.
const brokenFixture = `default_model = "kimi-code/k3-256k"

[providers."managed:kimi-code"]
type = "kimi"
api_key = "secret"
base_url = "https://api.kimi.com/coding/v1"

[providers.kimi-switch-local]
type = "kimi"
api_key = "local-kimi-switch"
base_url = "http://127.0.0.1:15722/v1"

[models."kimi-code/k3"]
provider = "kimi-switch-local"
model = "k3"
max_context_size = 1048576

[models."kimi-code/k3-256k"]
provider = "kimi-switch-local"
model = "k3-256k"
max_context_size = 262144

# >>> KimiSwitch managed provider
# original_provider = "managed:kimi-code"
[providers."kimi-switch-local"]
type = "kimi"
api_key = "local-kimi-switch"
base_url = "http://127.0.0.1:15722/v1"
# <<< KimiSwitch managed provider
`

func TestValidateRejectsDuplicateProviderTable(t *testing.T) {
	if err := validate(brokenFixture); err == nil {
		t.Fatal("expected duplicate table to be rejected")
	}
	if err := validate(fixture); err != nil {
		t.Fatal("clean fixture rejected:", err)
	}
}

func TestInspectReportsDuplicateProviderTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(brokenFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if status.ConfigError == "" {
		t.Fatal("expected Inspect to flag the duplicate table")
	}
}

func TestEnableRepairsDuplicateProviderTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(brokenFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := Set(path, "127.0.0.1:15722", "local-kimi-switch", true)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || status.ConfigError != "" || status.AffectedModels != 2 {
		t.Fatalf("unexpected status: %+v", status)
	}
	content, _ := os.ReadFile(path)
	text := string(content)
	if got := strings.Count(text, "[providers."); got != 2 {
		t.Fatalf("provider table count = %d\n%s", got, text)
	}
	if !strings.Contains(text, `# original_provider = "managed:kimi-code"`) {
		t.Fatalf("lost the original provider:\n%s", text)
	}
}

func TestDisableRecoversFromDuplicateProviderTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(brokenFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Set(path, "127.0.0.1:15722", "local-kimi-switch", false); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(path)
	text := string(content)
	if strings.Contains(text, providerName) {
		t.Fatalf("managed provider survived disable:\n%s", text)
	}
	if got := strings.Count(text, `provider = "managed:kimi-code"`); got != 2 {
		t.Fatalf("restored model count = %d\n%s", got, text)
	}
	if err := validate(text); err != nil {
		t.Fatal(err)
	}
}

// A stray provider table with no managed block at all still has to be recoverable,
// using the pristine backup to learn which provider the models came from.
func TestEnableRecoversOriginalProviderFromBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	stray, _, _ := strings.Cut(brokenFixture, markerBegin)
	if err := os.WriteFile(path, []byte(stray), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backupPath(path), []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := Set(path, "127.0.0.1:15722", "local-kimi-switch", true)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || status.OriginalProvider != "managed:kimi-code" || status.ConfigError != "" {
		t.Fatalf("unexpected status: %+v", status)
	}
	content, _ := os.ReadFile(path)
	if got := strings.Count(string(content), "[providers."); got != 2 {
		t.Fatalf("provider table count = %d\n%s", got, content)
	}
}

func TestEnableIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Set(path, "127.0.0.1:15721", "local-key", true); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	if _, err := Set(path, "127.0.0.1:15721", "local-key", true); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Fatalf("second enable changed the config:\n%s", second)
	}
	if err := validate(string(second)); err != nil {
		t.Fatal(err)
	}
}

package rotation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kyui-azusa/kimi-switch/internal/config"
)

func TestStickyRotationKeepsUsingActiveKeyOnSuccess(t *testing.T) {
	c := config.Default()
	c.Keys = []config.KeyConfig{
		{Name: "work", Enabled: true, APIKey: "work-key"},
		{Name: "personal", Enabled: true, APIKey: "personal-key"},
	}
	c.ActiveKey = "work"
	c.Rotation.Enabled = true
	temp := t.TempDir()
	manager, err := NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		selected, err := manager.Select(100, 1)
		if err != nil {
			t.Fatal(err)
		}
		if selected.Key.Name != "work" {
			t.Fatalf("selection %d used %q, want work", i, selected.Key.Name)
		}
		manager.Finish(selected, 200, Usage{InputTokens: 50, OutputTokens: 50, TotalTokens: 100, Known: true}, nil)
	}
}

func TestStickyRotationDoesNotSpreadConcurrentRequests(t *testing.T) {
	c := config.Default()
	c.Keys = []config.KeyConfig{
		{Name: "a", Enabled: true, APIKey: "a"},
		{Name: "b", Enabled: true, APIKey: "b"},
	}
	c.ActiveKey = "a"
	c.Rotation.Enabled = true
	temp := t.TempDir()
	manager, err := NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	first, err := manager.Select(1000, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Select(1000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if first.Key.Name == second.Key.Name {
		return
	}
	t.Fatalf("concurrent selections should stay sticky, got %s then %s", first.Key.Name, second.Key.Name)
}

func TestStickyRotationFailoverPersistsActiveKey(t *testing.T) {
	c := config.Default()
	c.Keys = []config.KeyConfig{
		{Name: "a", Enabled: true, APIKey: "a"},
		{Name: "b", Enabled: true, APIKey: "b"},
	}
	c.ActiveKey = "a"
	c.Rotation.Enabled = true
	temp := t.TempDir()
	configPath := filepath.Join(temp, "config.json")
	manager, err := NewManager(c, configPath, filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	first, err := manager.Select(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if first.Key.Name != "a" {
		t.Fatalf("first key = %q, want a", first.Key.Name)
	}
	manager.Finish(first, 429, Usage{}, nil)

	second, err := manager.Select(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if second.Key.Name != "b" {
		t.Fatalf("failover key = %q, want b", second.Key.Name)
	}
	if got := manager.Config().ActiveKey; got != "b" {
		t.Fatalf("active key = %q, want b", got)
	}

	saved, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ActiveKey != "b" {
		t.Fatalf("saved active key = %q, want b", saved.ActiveKey)
	}
}

func TestCooldownExcludesRateLimitedKey(t *testing.T) {
	c := config.Default()
	c.Keys = []config.KeyConfig{
		{Name: "a", Enabled: true, APIKey: "a"},
		{Name: "b", Enabled: true, APIKey: "b"},
	}
	c.ActiveKey = "a"
	c.Rotation.Enabled = true
	temp := t.TempDir()
	manager, err := NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	manager.MarkCooldown("a", "rate limited")
	selected, err := manager.Select(10, 1)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Key.Name != "b" {
		t.Fatalf("selected cooled-down key %q", selected.Key.Name)
	}
	if got := manager.Config().ActiveKey; got != "b" {
		t.Fatalf("active key = %q, want b", got)
	}
}

func TestCooldownFallsBackOnlyWhenEveryKeyIsUnavailable(t *testing.T) {
	c := config.Default()
	c.Keys = []config.KeyConfig{
		{Name: "a", Enabled: true, APIKey: "a"},
		{Name: "b", Enabled: true, APIKey: "b"},
	}
	c.ActiveKey = "a"
	c.Rotation.Enabled = true
	temp := t.TempDir()
	manager, err := NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	manager.MarkCooldown("a", "failed")
	manager.MarkCooldown("b", "failed")
	selected, err := manager.Select(10, 1)
	if err != nil {
		t.Fatalf("all-cooled fallback returned error: %v", err)
	}
	if selected.Key.Name != "a" && selected.Key.Name != "b" {
		t.Fatalf("unexpected fallback key %q", selected.Key.Name)
	}
}

func TestAnyFailedRequestStartsTenMinuteCooldown(t *testing.T) {
	c := config.Default()
	c.Keys = []config.KeyConfig{{Name: "a", Enabled: true, APIKey: "a"}}
	c.ActiveKey = "a"
	temp := t.TempDir()
	manager, err := NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := manager.Select(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC().Add(9*time.Minute + 55*time.Second)
	manager.Finish(selected, 500, Usage{}, nil)
	_, state := manager.Snapshot()
	if !state.Keys["a"].CooldownUntil.After(before) {
		t.Fatalf("cooldown too short: %v", state.Keys["a"].CooldownUntil)
	}
}

func TestDeleteActiveKeySelectsNextRecord(t *testing.T) {
	c := config.Default()
	c.Keys = []config.KeyConfig{{Name: "a", Enabled: true, APIKey: "a"}, {Name: "b", Enabled: true, APIKey: "b"}}
	c.ActiveKey = "a"
	temp := t.TempDir()
	manager, err := NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.DeleteKey("a"); err != nil {
		t.Fatal(err)
	}
	if got := manager.Config().ActiveKey; got != "b" {
		t.Fatalf("active key = %q, want b", got)
	}
}

func TestSetActiveImmediatelyChangesStickyKey(t *testing.T) {
	c := config.Default()
	c.Keys = []config.KeyConfig{{Name: "a", Enabled: true, APIKey: "a"}, {Name: "b", Enabled: true, APIKey: "b"}}
	c.ActiveKey = "a"
	c.Rotation.Enabled = true
	temp := t.TempDir()
	manager, err := NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetActive("b"); err != nil {
		t.Fatal(err)
	}
	selected, err := manager.Select(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Key.Name != "b" {
		t.Fatalf("selected %q, want b", selected.Key.Name)
	}
}

func TestEnsureMigratesLegacyRotationModesToSticky(t *testing.T) {
	temp := t.TempDir()
	configPath := filepath.Join(temp, "config.json")
	if err := os.WriteFile(configPath, []byte(`{
  "listen_addr": "127.0.0.1:15722",
  "upstream_base_url": "https://api.kimi.com/coding/v1",
  "local_api_key": "local-kimi-switch",
  "active_key": "a",
  "rotation": {
    "enabled": true,
    "mode": "token",
    "max_attempts": 2,
    "cooldown_seconds": 600,
    "retry_rate_limited": true
  },
  "keys": [{"name":"a","enabled":true,"api_key_env":"KIMI_A"}]
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Ensure(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Rotation.Mode != config.RotationModeSticky {
		t.Fatalf("loaded mode = %q, want sticky", loaded.Rotation.Mode)
	}
	saved, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), `"mode": "sticky"`) {
		t.Fatalf("saved config did not migrate mode: %s", saved)
	}
}

func TestFinishRecordsCCSStyleTokenBreakdownAndDailyUsage(t *testing.T) {
	c := config.Default()
	c.Keys = []config.KeyConfig{{Name: "work", Enabled: true, APIKey: "key"}}
	c.ActiveKey = "work"
	temp := t.TempDir()
	manager, err := NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := manager.Select(25, 1)
	if err != nil {
		t.Fatal(err)
	}
	manager.Finish(selected, 200, Usage{
		InputTokens: 100, OutputTokens: 40, TotalTokens: 140,
		CacheReadTokens: 30, ReasoningTokens: 10, Known: true,
	}, nil)

	_, state := manager.Snapshot()
	stats := state.Keys["work"]
	if stats.InputTokens != 100 || stats.OutputTokens != 40 || stats.CacheReadTokens != 30 || stats.ReasoningTokens != 10 {
		t.Fatalf("unexpected aggregate stats: %+v", stats)
	}
	date := state.Daily[time.Now().Local().Format("2006-01-02")]["work"]
	if date.Requests != 1 || date.TotalTokens != 140 || date.CacheReadTokens != 30 {
		t.Fatalf("unexpected daily stats: %+v", date)
	}
}

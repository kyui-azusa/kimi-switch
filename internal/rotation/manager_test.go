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
	manager.MarkCooldown("b", "rate limited")
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
	_, state := manager.Snapshot()
	if !state.Keys["b"].CooldownUntil.IsZero() || state.Keys["b"].LastError != "" || state.Keys["b"].LastCheckStatus != "" {
		t.Fatalf("manual switch should clear cooldown state: %+v", state.Keys["b"])
	}
}

func TestSetActiveEnablesDisabledKeyImmediately(t *testing.T) {
	c := config.Default()
	c.Keys = []config.KeyConfig{{Name: "a", Enabled: true, APIKey: "a"}, {Name: "b", Enabled: false, APIKey: "b"}}
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
	cfg := manager.Config()
	if !cfg.Keys[1].Enabled {
		t.Fatal("manual set active should enable disabled key")
	}
}

func TestEnableKeyMakesItStickyImmediately(t *testing.T) {
	c := config.Default()
	c.Keys = []config.KeyConfig{{Name: "a", Enabled: true, APIKey: "a"}, {Name: "b", Enabled: false, APIKey: "b"}}
	c.ActiveKey = "a"
	c.Rotation.Enabled = true
	temp := t.TempDir()
	manager, err := NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	manager.MarkCooldown("b", "manual re-enable")
	if err := manager.SetKeyEnabled("b", true); err != nil {
		t.Fatal(err)
	}
	selected, err := manager.Select(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Key.Name != "b" {
		t.Fatalf("selected %q, want b", selected.Key.Name)
	}
	_, state := manager.Snapshot()
	if got := manager.Config().ActiveKey; got != "b" {
		t.Fatalf("active key = %q, want b", got)
	}
	if !state.Keys["b"].CooldownUntil.IsZero() || state.Keys["b"].LastError != "" || state.Keys["b"].LastCheckStatus != "" {
		t.Fatalf("enabled key should clear cooldown state: %+v", state.Keys["b"])
	}
}

func TestEnsureMigratesLegacyRotationModesToSticky(t *testing.T) {
	for _, legacyMode := range []string{"request", "token"} {
		t.Run(legacyMode, func(t *testing.T) {
			temp := t.TempDir()
			configPath := filepath.Join(temp, "config.json")
			if err := os.WriteFile(configPath, []byte(`{
  "listen_addr": "127.0.0.1:15722",
  "upstream_base_url": "https://api.kimi.com/coding/v1",
  "local_api_key": "local-kimi-switch",
  "active_key": "a",
  "rotation": {
    "enabled": true,
    "mode": "`+legacyMode+`",
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
		})
	}
}

func TestStickyFailoverReturnsPersistError(t *testing.T) {
	temp := t.TempDir()
	readonly := filepath.Join(temp, "readonly")
	if err := os.Mkdir(readonly, 0o500); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(readonly, "config.json")
	c := config.Default()
	c.Keys = []config.KeyConfig{
		{Name: "a", Enabled: true, APIKey: "a"},
		{Name: "b", Enabled: true, APIKey: "b"},
	}
	c.ActiveKey = "a"
	c.Rotation.Enabled = true
	manager, err := NewManager(c, configPath, filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	manager.MarkCooldown("a", "failed")
	beforeCfg, beforeState := manager.Snapshot()
	if _, err := manager.Select(0, 1); err == nil {
		t.Fatal("expected persist error when failover cannot save active key")
	}
	afterCfg, afterState := manager.Snapshot()
	if afterCfg.ActiveKey != beforeCfg.ActiveKey {
		t.Fatalf("active key changed in memory after persist error: before=%q after=%q", beforeCfg.ActiveKey, afterCfg.ActiveKey)
	}
	if afterState.Keys["a"].CooldownUntil != beforeState.Keys["a"].CooldownUntil {
		t.Fatalf("cooldown state changed in memory after persist error: before=%v after=%v", beforeState.Keys["a"].CooldownUntil, afterState.Keys["a"].CooldownUntil)
	}
	if afterState.Keys["b"].CooldownUntil != beforeState.Keys["b"].CooldownUntil || afterState.Keys["b"].LastError != beforeState.Keys["b"].LastError || afterState.Keys["b"].LastCheckStatus != beforeState.Keys["b"].LastCheckStatus {
		t.Fatalf("fallback key state changed in memory after persist error: before=%+v after=%+v", beforeState.Keys["b"], afterState.Keys["b"])
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

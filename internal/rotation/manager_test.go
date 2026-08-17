package rotation

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/kyui-azusa/kimi-switch/internal/config"
)

func TestTokenRotationBalancesObservedUsage(t *testing.T) {
	c := config.Default()
	c.Keys = []config.KeyConfig{
		{Name: "work", Enabled: true, APIKey: "work-key"},
		{Name: "personal", Enabled: true, APIKey: "personal-key"},
	}
	c.ActiveKey = "work"
	c.Rotation.Enabled = true
	c.Rotation.Mode = "token"
	temp := t.TempDir()
	manager, err := NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	first, err := manager.Select(100, 1)
	if err != nil || first.Key.Name != "personal" && first.Key.Name != "work" {
		t.Fatalf("first selection = %+v, err=%v", first, err)
	}
	manager.Finish(first, 200, Usage{InputTokens: 50, OutputTokens: 50, TotalTokens: 100, Known: true}, nil)
	second, err := manager.Select(100, 1)
	if err != nil {
		t.Fatal(err)
	}
	if second.Key.Name == first.Key.Name {
		t.Fatalf("expected the next request to use the other key, first=%s second=%s", first.Key.Name, second.Key.Name)
	}
}

func TestTokenRotationCountsInFlightReservations(t *testing.T) {
	c := config.Default()
	c.Keys = []config.KeyConfig{
		{Name: "a", Enabled: true, APIKey: "a"},
		{Name: "b", Enabled: true, APIKey: "b"},
	}
	c.ActiveKey = "a"
	c.Rotation.Enabled = true
	c.Rotation.Mode = "token"
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
		t.Fatalf("in-flight reservation did not move traffic: both used %s", first.Key.Name)
	}
}

func TestRequestRotationAlternatesByCallCount(t *testing.T) {
	c := config.Default()
	c.Keys = []config.KeyConfig{
		{Name: "a", Enabled: true, APIKey: "a"},
		{Name: "b", Enabled: true, APIKey: "b"},
	}
	c.ActiveKey = "a"
	c.Rotation.Enabled = true
	c.Rotation.Mode = "request"
	temp := t.TempDir()
	manager, err := NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"a", "b", "a", "b"} {
		selected, err := manager.Select(0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if selected.Key.Name != want {
			t.Fatalf("selected %q, want %q", selected.Key.Name, want)
		}
		manager.Finish(selected, 200, Usage{}, nil)
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

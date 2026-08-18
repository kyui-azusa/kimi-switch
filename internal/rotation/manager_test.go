package rotation

import (
	"encoding/json"
	"errors"
	"os"
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

func TestCooldownDoesNotFallbackWhenEveryKeyIsUnavailable(t *testing.T) {
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
	if _, err := manager.Select(10, 1); err == nil {
		t.Fatal("selected a quarantined account without a recovery probe")
	}
}

func TestCooldownExpiryStillRequiresRecoveryProbe(t *testing.T) {
	c := config.Default()
	c.Keys = []config.KeyConfig{{Name: "a", Enabled: true, APIKey: "a"}}
	c.ActiveKey = "a"
	temp := t.TempDir()
	manager, err := NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	manager.MarkCooldown("a", "quota exhausted")
	manager.mu.Lock()
	stats := manager.state.Keys["a"]
	stats.NextProbeAt = time.Now().UTC().Add(-time.Second)
	stats.CooldownUntil = stats.NextProbeAt
	manager.state.Keys["a"] = stats
	manager.mu.Unlock()
	if _, err := manager.Select(0, 1); err == nil {
		t.Fatal("selected an account whose cooldown expired but was not probed")
	}
	if _, ok := manager.BeginDueProbe(); !ok {
		t.Fatal("expired quarantine was not eligible for a controlled probe")
	}
}

func TestRecoveryProbeSuccessRestoresRouting(t *testing.T) {
	c := config.Default()
	c.Keys = []config.KeyConfig{{Name: "a", Enabled: true, APIKey: "a"}}
	c.ActiveKey = "a"
	temp := t.TempDir()
	manager, err := NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	manager.MarkCooldown("a", "quota exhausted")
	manager.mu.Lock()
	stats := manager.state.Keys["a"]
	stats.NextProbeAt = time.Now().UTC().Add(-time.Second)
	stats.CooldownUntil = stats.NextProbeAt
	manager.state.Keys["a"] = stats
	manager.mu.Unlock()
	key, ok := manager.BeginDueProbe()
	if !ok {
		t.Fatal("expected a due probe")
	}
	if err := manager.RecordConnectivity(key.Name, "ok", 10*time.Millisecond, ""); err != nil {
		t.Fatal(err)
	}
	selected, err := manager.Select(0, 1)
	if err != nil || selected.Key.Name != "a" {
		t.Fatalf("recovered account was not selectable: %+v, %v", selected, err)
	}
	_, state := manager.Snapshot()
	if state.Keys["a"].RouteStatus != RouteHealthy || state.Keys["a"].LastRecoveredAt.IsZero() {
		t.Fatalf("unexpected recovered state: %+v", state.Keys["a"])
	}
}

func TestRecoveryProbeFailureKeepsQuarantineAndDelaysNextProbe(t *testing.T) {
	c := config.Default()
	c.Keys = []config.KeyConfig{{Name: "a", Enabled: true, APIKey: "a"}}
	c.ActiveKey = "a"
	temp := t.TempDir()
	manager, err := NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	manager.MarkCooldown("a", "quota exhausted")
	manager.mu.Lock()
	stats := manager.state.Keys["a"]
	stats.NextProbeAt = time.Now().UTC().Add(-time.Second)
	stats.CooldownUntil = stats.NextProbeAt
	manager.state.Keys["a"] = stats
	manager.mu.Unlock()
	key, ok := manager.BeginDueProbe()
	if !ok {
		t.Fatal("expected a due probe")
	}
	if err := manager.RecordConnectivity(key.Name, "quota_exhausted", 10*time.Millisecond, "still exhausted"); err != nil {
		t.Fatal(err)
	}
	_, state := manager.Snapshot()
	stats = state.Keys["a"]
	if stats.RouteStatus != RouteQuarantined || !stats.NextProbeAt.After(time.Now().UTC().Add(9*time.Minute+55*time.Second)) {
		t.Fatalf("probe failure did not keep quarantine: %+v", stats)
	}
	if _, err := manager.Select(0, 1); err == nil {
		t.Fatal("failed recovery probe returned account to routing")
	}
}

func TestRecoveryProbeIsSingleFlightPerAccount(t *testing.T) {
	c := config.Default()
	c.Keys = []config.KeyConfig{{Name: "a", Enabled: true, APIKey: "a"}}
	c.ActiveKey = "a"
	temp := t.TempDir()
	manager, err := NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	manager.MarkCooldown("a", "quota exhausted")
	manager.mu.Lock()
	stats := manager.state.Keys["a"]
	stats.NextProbeAt = time.Now().UTC().Add(-time.Second)
	stats.CooldownUntil = stats.NextProbeAt
	manager.state.Keys["a"] = stats
	manager.mu.Unlock()
	if _, ok := manager.BeginDueProbe(); !ok {
		t.Fatal("expected first due probe")
	}
	if _, ok := manager.BeginDueProbe(); ok {
		t.Fatal("reserved account twice for concurrent probes")
	}
	if _, err := manager.BeginProbe("a", false); !errors.Is(err, ErrProbeInProgress) {
		t.Fatalf("manual probe did not observe in-flight recovery probe: %v", err)
	}
}

func TestLegacyCooldownStateMigratesToQuarantineProbeState(t *testing.T) {
	temp := t.TempDir()
	c := config.Default()
	c.Keys = []config.KeyConfig{{Name: "legacy", Enabled: true, APIKey: "legacy"}}
	c.ActiveKey = "legacy"
	past := time.Now().UTC().Add(-time.Second)
	statePath := filepath.Join(temp, "state.json")
	b, err := json.Marshal(State{Keys: map[string]Stats{
		"legacy": {CooldownUntil: past, LastError: "quota exhausted", LastCheckStatus: "quota_exhausted"},
	}, Daily: map[string]map[string]DailyUsage{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(c, filepath.Join(temp, "config.json"), statePath)
	if err != nil {
		t.Fatal(err)
	}
	_, state := manager.Snapshot()
	stats := state.Keys["legacy"]
	if stats.RouteStatus != RouteQuarantined || !stats.NextProbeAt.Equal(past) {
		t.Fatalf("legacy state was not migrated: %+v", stats)
	}
	if _, err := manager.Select(0, 1); err == nil {
		t.Fatal("legacy quarantined account became selectable during migration")
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

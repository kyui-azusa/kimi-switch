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

func TestNextCycleBoundaryUsesNearestUTCBoundaryAcrossCycles(t *testing.T) {
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.FixedZone("PST", -8*60*60))
	got := NextCycleBoundary(anchor, 7*24*60*60, now)
	want := time.Date(2026, 1, 14, 16, 0, 0, 0, time.UTC)
	if !got.Equal(want) || got.Location() != time.UTC {
		t.Fatalf("next boundary = %v, want %v UTC", got, want)
	}
}

func TestQuotaImportWithoutAnchorRemainsUnknown(t *testing.T) {
	c := config.Default()
	c.Keys = []config.KeyConfig{{Name: "work", Enabled: true, APIKey: "key"}}
	temp := t.TempDir()
	manager, err := NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	observed := time.Date(2026, 2, 1, 1, 2, 3, 0, time.UTC)
	if err := manager.RecordImported("work", observed); err != nil {
		t.Fatal(err)
	}
	stats := manager.State().Keys["work"]
	if !stats.NextRefreshAt.IsZero() || stats.RefreshSource != "unknown" || !stats.LastQuotaObservedAt.Equal(observed) {
		t.Fatalf("unexpected imported quota state: %+v", stats)
	}
}

func TestQuotaCalibrationPersistsAndRequestFinishDoesNotMoveAnchor(t *testing.T) {
	c := config.Default()
	c.Keys = []config.KeyConfig{{Name: "work", Enabled: true, APIKey: "key"}}
	temp := t.TempDir()
	manager, err := NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := manager.CalibrateQuota("work", anchor, 24*60*60); err != nil {
		t.Fatal(err)
	}
	before := manager.State().Keys["work"]
	selected, err := manager.Select(10, 1)
	if err != nil {
		t.Fatal(err)
	}
	manager.Finish(selected, 200, Usage{}, nil)
	after := manager.State().Keys["work"]
	if !after.QuotaCycleAnchorAt.Equal(before.QuotaCycleAnchorAt) || after.QuotaCycleSeconds != before.QuotaCycleSeconds || !after.NextRefreshAt.Equal(before.NextRefreshAt) {
		t.Fatalf("request changed quota anchor: before=%+v after=%+v", before, after)
	}
	if after.RefreshSource != "manual" || after.RefreshConfidence != "high" {
		t.Fatalf("manual source not persisted: %+v", after)
	}
}

func TestDueQuotaProbeAdvancesBoundaryWithoutRestoringAccount(t *testing.T) {
	c := config.Default()
	c.Keys = []config.KeyConfig{{Name: "work", Enabled: true, APIKey: "key"}}
	temp := t.TempDir()
	manager, err := NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	anchor := time.Now().UTC().Add(-2 * time.Hour)
	if err := manager.RecordQuotaObservation("work", QuotaObservation{ObservedAt: time.Now(), AnchorAt: anchor, CycleSeconds: 3600, Source: QuotaSourceProvider, Confidence: "high"}); err != nil {
		t.Fatal(err)
	}
	manager.SetAccountStatus("work", "quota_exhausted", "limit")
	old := manager.State().Keys["work"]
	probeNow := time.Now().UTC().Add(2 * time.Hour)
	due := manager.ClaimDueQuotaProbes(probeNow)
	if len(due) != 1 || due[0] != "work" {
		t.Fatalf("due probes = %v", due)
	}
	newStats := manager.State().Keys["work"]
	if !newStats.NextRefreshAt.After(time.Now().UTC()) || newStats.LastCheckStatus != old.LastCheckStatus || newStats.CooldownUntil.IsZero() {
		t.Fatalf("probe claim changed health unexpectedly: before=%+v after=%+v", old, newStats)
	}
	if got := manager.ClaimDueQuotaProbes(probeNow); len(got) != 0 {
		t.Fatalf("duplicate due probe = %v", got)
	}
}

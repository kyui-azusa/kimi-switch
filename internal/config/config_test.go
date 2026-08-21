package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadBackwardCompatibleWithoutQuotaRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{
  "listen_addr": "127.0.0.1:15721",
  "upstream_base_url": "https://api.kimi.com/coding/v1",
  "local_api_key": "local-kimi-switch",
  "active_key": "work",
  "rotation": {"enabled": false, "mode": "request", "max_attempts": 2, "cooldown_seconds": 600, "retry_rate_limited": true},
  "keys": [{"name": "work", "enabled": true, "api_key_env": "KIMI_KEY", "created_at": 1}]
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Keys) != 1 || cfg.Keys[0].QuotaRefresh != nil {
		t.Fatalf("unexpected config: %+v", cfg.Keys)
	}
}

func TestSaveAndLoadQuotaRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	anchor := time.Date(2026, 8, 25, 10, 0, 0, 0, time.FixedZone("UTC+8", 8*3600))
	recordedAt := time.Date(2026, 8, 21, 9, 30, 0, 0, time.FixedZone("UTC+8", 8*3600))
	cfg := Default()
	cfg.ActiveKey = "work"
	cfg.Keys = []KeyConfig{{
		Name:      "work",
		Enabled:   true,
		APIKeyEnv: "KIMI_KEY",
		QuotaRefresh: &QuotaRefreshConfig{
			FiveHour: &QuotaRefreshRecord{
				AnchorAt:      &anchor,
				Source:        QuotaRefreshSourceManual,
				RecordedAt:    &recordedAt,
				Authoritative: false,
			},
		},
	}}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	record := loaded.Keys[0].QuotaRefresh.FiveHour
	if record == nil || record.AnchorAt == nil || record.RecordedAt == nil {
		t.Fatalf("quota refresh missing after reload: %+v", loaded.Keys[0].QuotaRefresh)
	}
	if got, want := record.AnchorAt.UTC(), anchor.UTC(); !got.Equal(want) {
		t.Fatalf("anchor = %s, want %s", got, want)
	}
	if got, want := record.RecordedAt.UTC(), recordedAt.UTC(); !got.Equal(want) {
		t.Fatalf("recorded_at = %s, want %s", got, want)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	keys := persisted["keys"].([]any)
	key := keys[0].(map[string]any)
	if _, ok := key["quota_refresh"]; !ok {
		t.Fatalf("quota_refresh missing from persisted JSON: %s", raw)
	}
}

func TestNextRefreshAtStrictlyAfterNow(t *testing.T) {
	anchor := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	now := anchor.Add(10 * time.Hour)
	got := NextRefreshAt(anchor, 5*time.Hour, now)
	want := anchor.Add(15 * time.Hour)
	if !got.Equal(want) {
		t.Fatalf("next = %s, want %s", got, want)
	}
}

func TestNormalizeQuotaRefreshPreservesSourceMetadata(t *testing.T) {
	anchor := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	value := NormalizeQuotaRefresh(&QuotaRefreshConfig{FiveHour: &QuotaRefreshRecord{
		AnchorAt:      &anchor,
		Source:        "official",
		Authoritative: true,
	}})
	if value.FiveHour.Source != "official" || !value.FiveHour.Authoritative {
		t.Fatalf("source metadata changed during normalization: %+v", value.FiveHour)
	}
}

func TestSaveAndLoadPreservesOfficialQuotaRefreshMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	anchor := time.Date(2026, 8, 25, 2, 0, 0, 0, time.UTC)
	recordedAt := time.Date(2026, 8, 21, 1, 30, 0, 0, time.UTC)
	cfg := Default()
	cfg.ActiveKey = "work"
	cfg.Keys = []KeyConfig{{
		Name:    "work",
		Enabled: true,
		QuotaRefresh: &QuotaRefreshConfig{Weekly: &QuotaRefreshRecord{
			AnchorAt:      &anchor,
			Source:        "official",
			RecordedAt:    &recordedAt,
			Authoritative: true,
		}},
	}}

	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	record := loaded.Keys[0].QuotaRefresh.Weekly
	if record.Source != "official" || !record.Authoritative {
		t.Fatalf("official metadata did not survive save/load: %+v", record)
	}
}

func TestMergeImportedKeyKeepsManualFieldsAndQuotaRefresh(t *testing.T) {
	anchor := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	recordedAt := anchor.Add(30 * time.Minute)
	existing := KeyConfig{
		Name:      "work",
		Account:   "manual-account",
		Notes:     "keep me",
		Enabled:   false,
		CreatedAt: 42,
		QuotaRefresh: &QuotaRefreshConfig{
			Weekly: &QuotaRefreshRecord{
				AnchorAt:      &anchor,
				Source:        QuotaRefreshSourceManual,
				RecordedAt:    &recordedAt,
				Authoritative: false,
			},
		},
	}
	incoming := KeyConfig{Name: "work", Account: "", Notes: "", Enabled: true, APIKeyEnv: "NEW_ENV"}
	merged := MergeImportedKey(existing, incoming)
	if merged.CreatedAt != 42 || merged.QuotaRefresh == nil || merged.QuotaRefresh.Weekly == nil {
		t.Fatalf("metadata not preserved: %+v", merged)
	}
	if merged.Account != "manual-account" || merged.Notes != "keep me" || merged.Enabled {
		t.Fatalf("manual fields not preserved: %+v", merged)
	}
}

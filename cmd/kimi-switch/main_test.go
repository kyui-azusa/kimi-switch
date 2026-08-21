package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/kyui-azusa/kimi-switch/internal/config"
)

func TestAddKeyPreservesExistingQuotaRefreshMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	anchor := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	recordedAt := anchor.Add(30 * time.Minute)
	cfg := config.Default()
	cfg.ActiveKey = "work"
	cfg.Keys = []config.KeyConfig{{
		Name:      "work",
		Account:   "manual-account",
		Notes:     "keep notes",
		Enabled:   false,
		APIKeyEnv: "OLD_ENV",
		CreatedAt: 99,
		QuotaRefresh: &config.QuotaRefreshConfig{
			Weekly: &config.QuotaRefreshRecord{
				AnchorAt:      &anchor,
				Source:        config.QuotaRefreshSourceManual,
				RecordedAt:    &recordedAt,
				Authoritative: false,
			},
		},
	}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	if err := addKey(path, []string{"work", "--env", "NEW_ENV"}); err != nil {
		t.Fatal(err)
	}
	updated, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	key := updated.Keys[0]
	if key.APIKeyEnv != "NEW_ENV" || key.CreatedAt != 99 {
		t.Fatalf("unexpected key after add override: %+v", key)
	}
	if key.QuotaRefresh == nil || key.QuotaRefresh.Weekly == nil {
		t.Fatalf("quota refresh metadata was lost: %+v", key.QuotaRefresh)
	}
	if key.Account != "manual-account" || key.Notes != "keep notes" || key.Enabled {
		t.Fatalf("manual fields should be preserved on add override: %+v", key)
	}
}

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kyui-azusa/kimi-switch/internal/config"
)

func TestBackfillLocalAPIKeysPersistsLegacySecretRefEntries(t *testing.T) {
	originalRead := secretReadForBackfill
	originalSave := configSaveForBackfill
	defer func() {
		secretReadForBackfill = originalRead
		configSaveForBackfill = originalSave
	}()

	secretReadForBackfill = func(service string) (string, error) {
		if service != "kimi-switch/work" {
			t.Fatalf("unexpected service %q", service)
		}
		return "sk-work", nil
	}
	configSaveForBackfill = config.Save

	path := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Default()
	cfg.Keys = []config.KeyConfig{{Name: "work", SecretRef: "kimi-switch/work", Enabled: true}}
	cfg.ActiveKey = "work"

	var warnings []string
	updated := backfillLocalAPIKeys(path, cfg, func(message string) { warnings = append(warnings, message) })
	if updated.Keys[0].APIKey != "sk-work" {
		t.Fatalf("api key = %q, want sk-work", updated.Keys[0].APIKey)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
	persisted, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Keys[0].APIKey != "sk-work" {
		t.Fatalf("persisted api key = %q, want sk-work", persisted.Keys[0].APIKey)
	}
}

func TestBackfillLocalAPIKeysSkipsFailedReadAndPersistsOthers(t *testing.T) {
	originalRead := secretReadForBackfill
	originalSave := configSaveForBackfill
	defer func() {
		secretReadForBackfill = originalRead
		configSaveForBackfill = originalSave
	}()

	secretReadForBackfill = func(service string) (string, error) {
		if service == "kimi-switch/bad" {
			return "", errors.New("broken pipe")
		}
		return "sk-good", nil
	}
	configSaveForBackfill = config.Save

	path := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Default()
	cfg.Keys = []config.KeyConfig{
		{Name: "bad", SecretRef: "kimi-switch/bad", Enabled: true},
		{Name: "good", SecretRef: "kimi-switch/good", Enabled: true},
	}
	cfg.ActiveKey = "bad"

	var warnings []string
	updated := backfillLocalAPIKeys(path, cfg, func(message string) { warnings = append(warnings, message) })
	if updated.Keys[0].APIKey != "" {
		t.Fatalf("bad key fallback should stay empty, got %q", updated.Keys[0].APIKey)
	}
	if updated.Keys[1].APIKey != "sk-good" {
		t.Fatalf("good key fallback = %q, want sk-good", updated.Keys[1].APIKey)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `"bad"`) {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
	persisted, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Keys[0].APIKey != "" || persisted.Keys[1].APIKey != "sk-good" {
		t.Fatalf("unexpected persisted keys: %+v", persisted.Keys)
	}
}

func TestBackfillLocalAPIKeysDoesNotReadWhenFallbackAlreadyExists(t *testing.T) {
	originalRead := secretReadForBackfill
	originalSave := configSaveForBackfill
	defer func() {
		secretReadForBackfill = originalRead
		configSaveForBackfill = originalSave
	}()

	readCalls := 0
	saveCalls := 0
	secretReadForBackfill = func(string) (string, error) {
		readCalls++
		return "unused", nil
	}
	configSaveForBackfill = func(string, config.Config) error {
		saveCalls++
		return nil
	}

	cfg := config.Default()
	cfg.Keys = []config.KeyConfig{{Name: "work", SecretRef: "kimi-switch/work", APIKey: "sk-work", Enabled: true}}
	cfg.ActiveKey = "work"

	updated := backfillLocalAPIKeys(filepath.Join(t.TempDir(), "config.json"), cfg, nil)
	if updated.Keys[0].APIKey != "sk-work" {
		t.Fatalf("api key = %q, want sk-work", updated.Keys[0].APIKey)
	}
	if readCalls != 0 || saveCalls != 0 {
		t.Fatalf("unexpected read/save calls: reads=%d saves=%d", readCalls, saveCalls)
	}
}

func TestBackfillLocalAPIKeysWarnsOnSaveFailureButKeepsUpdatedConfig(t *testing.T) {
	originalRead := secretReadForBackfill
	originalSave := configSaveForBackfill
	defer func() {
		secretReadForBackfill = originalRead
		configSaveForBackfill = originalSave
	}()

	secretReadForBackfill = func(string) (string, error) { return "sk-work", nil }
	configSaveForBackfill = func(string, config.Config) error { return errors.New("disk full") }

	cfg := config.Default()
	cfg.Keys = []config.KeyConfig{{Name: "work", SecretRef: "kimi-switch/work", Enabled: true}}
	cfg.ActiveKey = "work"

	var warnings []string
	updated := backfillLocalAPIKeys(filepath.Join(t.TempDir(), "config.json"), cfg, func(message string) { warnings = append(warnings, message) })
	if updated.Keys[0].APIKey != "sk-work" {
		t.Fatalf("api key = %q, want sk-work", updated.Keys[0].APIKey)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "could not persist local API-key fallbacks") {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
}

func TestServeBackfillDoesNotBlockStartupPathSetup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Default()
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

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

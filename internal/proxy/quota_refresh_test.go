package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kyui-azusa/kimi-switch/internal/config"
	"github.com/kyui-azusa/kimi-switch/internal/rotation"
)

func TestStatusIncludesQuotaRefreshNextRefreshAt(t *testing.T) {
	anchor := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	c := config.Default()
	c.LocalAPIKey = "local"
	c.ActiveKey = "work"
	c.Keys = []config.KeyConfig{{
		Name:      "work",
		Enabled:   true,
		APIKeyEnv: "KIMI_KEY",
		QuotaRefresh: &config.QuotaRefreshConfig{
			FiveHour: &config.QuotaRefreshRecord{
				AnchorAt:      &anchor,
				Source:        "official",
				RecordedAt:    &anchor,
				Authoritative: true,
			},
		},
	}}
	temp := t.TempDir()
	manager, err := rotation.NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	request.Header.Set("Authorization", "Bearer local")
	response := httptest.NewRecorder()
	NewServer(manager).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	configPayload := payload["config"].(map[string]any)
	keys := configPayload["keys"].([]any)
	key := keys[0].(map[string]any)
	quotaRefresh := key["quota_refresh"].(map[string]any)
	fiveHour := quotaRefresh["five_hour"].(map[string]any)
	if fiveHour["source"] != "official" || fiveHour["authoritative"] != true {
		t.Fatalf("unexpected quota refresh payload: %+v", fiveHour)
	}
	if got := fiveHour["next_refresh_at"].(string); got != anchor.Format(time.RFC3339) {
		t.Fatalf("next_refresh_at = %q, want %q", got, anchor.Format(time.RFC3339))
	}
}

func TestKeyUpsertQuotaRefreshManualReferencePreserveAndClear(t *testing.T) {
	weeklyAnchor := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	weeklyRecordedAt := weeklyAnchor.Add(15 * time.Minute)
	c := config.Default()
	c.LocalAPIKey = "local"
	c.ActiveKey = "work"
	c.Keys = []config.KeyConfig{{
		Name:      "work",
		Enabled:   true,
		APIKeyEnv: "KIMI_KEY",
		QuotaRefresh: &config.QuotaRefreshConfig{
			Weekly: &config.QuotaRefreshRecord{
				AnchorAt:      &weeklyAnchor,
				Source:        config.QuotaRefreshSourceManual,
				RecordedAt:    &weeklyRecordedAt,
				Authoritative: false,
			},
		},
	}}
	temp := t.TempDir()
	configPath := filepath.Join(temp, "config.json")
	manager, err := rotation.NewManager(c, configPath, filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(manager)

	firstBody := `{"name":"work","account":"acct","enabled":true,"api_key_env":"KIMI_KEY","quota_refresh":{"five_hour":{"anchor_at":"2026-08-21T05:00:00Z"}}}`
	request := httptest.NewRequest(http.MethodPut, "/api/keys/work", bytes.NewBufferString(firstBody))
	request.Header.Set("Authorization", "Bearer local")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("first update status = %d body=%s", response.Code, response.Body.String())
	}

	updated := manager.Config().Keys[0]
	if updated.QuotaRefresh == nil || updated.QuotaRefresh.FiveHour == nil || updated.QuotaRefresh.Weekly == nil {
		t.Fatalf("quota refresh missing after first update: %+v", updated.QuotaRefresh)
	}
	if updated.Account != "acct" {
		t.Fatalf("explicit edit should update account: %+v", updated)
	}
	fiveHourRecordedAt := updated.QuotaRefresh.FiveHour.RecordedAt
	if updated.QuotaRefresh.FiveHour.Source != config.QuotaRefreshSourceManual || updated.QuotaRefresh.FiveHour.Authoritative {
		t.Fatalf("unexpected five hour record: %+v", updated.QuotaRefresh.FiveHour)
	}
	if !updated.QuotaRefresh.Weekly.RecordedAt.Equal(weeklyRecordedAt) {
		t.Fatalf("weekly record should be preserved: %+v", updated.QuotaRefresh.Weekly)
	}

	secondBody := `{"name":"work","account":"acct2","enabled":true,"api_key_env":"KIMI_KEY","quota_refresh":{"five_hour":{"anchor_at":"2026-08-21T05:00:00Z"},"weekly":{"anchor_at":""}}}`
	request = httptest.NewRequest(http.MethodPut, "/api/keys/work", bytes.NewBufferString(secondBody))
	request.Header.Set("Authorization", "Bearer local")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("second update status = %d body=%s", response.Code, response.Body.String())
	}

	updated = manager.Config().Keys[0]
	if updated.QuotaRefresh.Weekly != nil {
		t.Fatalf("weekly record should be cleared: %+v", updated.QuotaRefresh.Weekly)
	}
	if updated.Account != "acct2" {
		t.Fatalf("explicit edit should keep overriding account: %+v", updated)
	}
	if updated.QuotaRefresh.FiveHour.RecordedAt == nil || !updated.QuotaRefresh.FiveHour.RecordedAt.Equal(*fiveHourRecordedAt) {
		t.Fatalf("five hour recorded_at should be preserved for same anchor: %+v", updated.QuotaRefresh.FiveHour)
	}

	thirdBody := `{"name":"work","account":"acct3","enabled":true,"api_key_env":"KIMI_KEY"}`
	request = httptest.NewRequest(http.MethodPut, "/api/keys/work", bytes.NewBufferString(thirdBody))
	request.Header.Set("Authorization", "Bearer local")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("third update status = %d body=%s", response.Code, response.Body.String())
	}

	persisted, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Keys[0].QuotaRefresh == nil || persisted.Keys[0].QuotaRefresh.FiveHour == nil {
		t.Fatalf("quota refresh should persist when field omitted: %+v", persisted.Keys[0].QuotaRefresh)
	}
	if persisted.Keys[0].Account != "acct3" {
		t.Fatalf("explicit edit semantics should remain overwrite-by-request: %+v", persisted.Keys[0])
	}
}

func TestKeyUpsertAcceptsLegacyFlatQuotaRefreshInput(t *testing.T) {
	c := config.Default()
	c.LocalAPIKey = "local"
	c.ActiveKey = "work"
	c.Keys = []config.KeyConfig{{Name: "work", Enabled: true, APIKeyEnv: "KIMI_KEY"}}
	temp := t.TempDir()
	manager, err := rotation.NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	body := `{"name":"work","enabled":true,"api_key_env":"KIMI_KEY","quota_refresh":{"five_hour_anchor_at":"2026-08-21T05:00:00Z","weekly_anchor_at":"2026-08-28T00:00:00Z"}}`
	request := httptest.NewRequest(http.MethodPut, "/api/keys/work", bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer local")
	response := httptest.NewRecorder()
	NewServer(manager).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	key := manager.Config().Keys[0]
	if key.QuotaRefresh == nil || key.QuotaRefresh.FiveHour == nil || key.QuotaRefresh.Weekly == nil {
		t.Fatalf("legacy flat quota refresh input was not accepted: %+v", key.QuotaRefresh)
	}
}

func TestKeyUpsertOmittedQuotaRefreshPreservesOfficialRecordExactly(t *testing.T) {
	anchor := time.Date(2026, 8, 22, 5, 0, 37, 0, time.UTC)
	recordedAt := time.Date(2026, 8, 21, 12, 30, 45, 0, time.UTC)
	c := config.Default()
	c.LocalAPIKey = "local"
	c.ActiveKey = "work"
	c.Keys = []config.KeyConfig{{
		Name:      "work",
		Account:   "before",
		Enabled:   true,
		APIKeyEnv: "KIMI_KEY",
		QuotaRefresh: &config.QuotaRefreshConfig{
			FiveHour: &config.QuotaRefreshRecord{
				AnchorAt:      &anchor,
				Source:        "official",
				RecordedAt:    &recordedAt,
				Authoritative: true,
			},
		},
	}}
	temp := t.TempDir()
	manager, err := rotation.NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	body := `{"name":"work","account":"after","notes":"edited","enabled":true,"api_key_env":"NEW_ENV"}`
	request := httptest.NewRequest(http.MethodPut, "/api/keys/work", bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer local")
	response := httptest.NewRecorder()
	NewServer(manager).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	key := manager.Config().Keys[0]
	if key.Account != "after" || key.Notes != "edited" || key.APIKeyEnv != "NEW_ENV" {
		t.Fatalf("non-quota fields were not updated: %+v", key)
	}
	record := key.QuotaRefresh.FiveHour
	if record == nil || record.Source != "official" || !record.Authoritative {
		t.Fatalf("official metadata should be preserved: %+v", record)
	}
	if record.AnchorAt == nil || !record.AnchorAt.Equal(anchor) {
		t.Fatalf("anchor should keep second precision: got=%v want=%v", record.AnchorAt, anchor)
	}
	if record.RecordedAt == nil || !record.RecordedAt.Equal(recordedAt) {
		t.Fatalf("recorded_at should be preserved: got=%v want=%v", record.RecordedAt, recordedAt)
	}
}

func TestKeyUpsertExplicitQuotaRefreshDowngradesOfficialRecord(t *testing.T) {
	anchor := time.Date(2026, 8, 22, 5, 0, 37, 0, time.UTC)
	recordedAt := time.Date(2026, 8, 21, 12, 30, 45, 0, time.UTC)
	weeklyAnchor := time.Date(2026, 8, 28, 0, 0, 29, 0, time.UTC)
	weeklyRecordedAt := time.Date(2026, 8, 21, 12, 31, 46, 0, time.UTC)
	c := config.Default()
	c.LocalAPIKey = "local"
	c.ActiveKey = "work"
	c.Keys = []config.KeyConfig{{
		Name:      "work",
		Enabled:   true,
		APIKeyEnv: "KIMI_KEY",
		QuotaRefresh: &config.QuotaRefreshConfig{
			FiveHour: &config.QuotaRefreshRecord{
				AnchorAt:      &anchor,
				Source:        "official",
				RecordedAt:    &recordedAt,
				Authoritative: true,
			},
			Weekly: &config.QuotaRefreshRecord{
				AnchorAt:      &weeklyAnchor,
				Source:        "official",
				RecordedAt:    &weeklyRecordedAt,
				Authoritative: true,
			},
		},
	}}
	temp := t.TempDir()
	manager, err := rotation.NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	body := `{"name":"work","enabled":true,"api_key_env":"KIMI_KEY","quota_refresh":{"five_hour":{"anchor_at":"2026-08-22T05:00:00Z"}}}`
	request := httptest.NewRequest(http.MethodPut, "/api/keys/work", bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer local")
	response := httptest.NewRecorder()
	NewServer(manager).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	record := manager.Config().Keys[0].QuotaRefresh.FiveHour
	if record == nil {
		t.Fatal("five_hour quota refresh missing")
	}
	if record.Source != config.QuotaRefreshSourceManual || record.Authoritative {
		t.Fatalf("explicit manual edit should downgrade official metadata: %+v", record)
	}
	if record.AnchorAt == nil || record.AnchorAt.Format(time.RFC3339) != "2026-08-22T05:00:00Z" {
		t.Fatalf("anchor was not updated to submitted manual value: %+v", record.AnchorAt)
	}
	weekly := manager.Config().Keys[0].QuotaRefresh.Weekly
	if weekly == nil || weekly.Source != "official" || !weekly.Authoritative {
		t.Fatalf("unchanged weekly official metadata should be preserved: %+v", weekly)
	}
	if weekly.AnchorAt == nil || !weekly.AnchorAt.Equal(weeklyAnchor) || weekly.RecordedAt == nil || !weekly.RecordedAt.Equal(weeklyRecordedAt) {
		t.Fatalf("unchanged weekly timestamps should be preserved exactly: %+v", weekly)
	}
}

func TestKimiImportPreservesManualFieldsForExistingKey(t *testing.T) {
	c := config.Default()
	c.LocalAPIKey = "local"
	c.ActiveKey = "work"
	anchor := time.Date(2026, 8, 21, 5, 0, 0, 0, time.UTC)
	recordedAt := anchor.Add(10 * time.Minute)
	c.Keys = []config.KeyConfig{{
		Name:      "work",
		Account:   "manual-account",
		Notes:     "keep notes",
		Enabled:   false,
		SecretRef: "kimi-switch/work-old",
		QuotaRefresh: &config.QuotaRefreshConfig{
			FiveHour: &config.QuotaRefreshRecord{
				AnchorAt:      &anchor,
				Source:        config.QuotaRefreshSourceManual,
				RecordedAt:    &recordedAt,
				Authoritative: false,
			},
		},
	}}
	temp := t.TempDir()
	manager, err := rotation.NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(manager)
	importPath := filepath.Join(temp, "config.toml")
	if err := os.WriteFile(importPath, []byte("[providers.work]\napi_key = \"sk-work\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/import/kimi", bytes.NewBufferString(`{"path":`+`"`+importPath+`"`+`}`))
	request.Header.Set("Authorization", "Bearer local")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	updated := manager.Config().Keys[0]
	if updated.Account != "manual-account" || updated.Notes != "keep notes" || updated.Enabled {
		t.Fatalf("manual fields should be preserved on import: %+v", updated)
	}
	if updated.QuotaRefresh == nil || updated.QuotaRefresh.FiveHour == nil {
		t.Fatalf("quota refresh should be preserved on import: %+v", updated.QuotaRefresh)
	}
}

func TestReorderKeysAPIPreservesQuotaRefresh(t *testing.T) {
	anchor := time.Date(2026, 8, 21, 5, 0, 0, 0, time.UTC)
	c := config.Default()
	c.LocalAPIKey = "local"
	c.ActiveKey = "a"
	c.Keys = []config.KeyConfig{
		{
			Name:    "a",
			Enabled: true,
			APIKey:  "a-key",
			QuotaRefresh: &config.QuotaRefreshConfig{FiveHour: &config.QuotaRefreshRecord{
				AnchorAt:      &anchor,
				Source:        config.QuotaRefreshSourceManual,
				RecordedAt:    &anchor,
				Authoritative: false,
			}},
		},
		{Name: "b", Enabled: true, APIKey: "b-key"},
	}
	temp := t.TempDir()
	configPath := filepath.Join(temp, "config.json")
	manager, err := rotation.NewManager(c, configPath, filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/keys/reorder", bytes.NewBufferString(`{"names":["b","a"]}`))
	request.Header.Set("Authorization", "Bearer local")
	response := httptest.NewRecorder()
	NewServer(manager).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	persisted, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Keys[1].Name != "a" || persisted.Keys[1].QuotaRefresh == nil || persisted.Keys[1].QuotaRefresh.FiveHour == nil {
		t.Fatalf("quota refresh did not follow reordered account: %+v", persisted.Keys)
	}
	if got := persisted.Keys[1].QuotaRefresh.FiveHour.AnchorAt; got == nil || !got.Equal(anchor) {
		t.Fatalf("anchor changed after reorder: %+v", got)
	}
}

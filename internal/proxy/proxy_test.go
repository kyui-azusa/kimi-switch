package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kyui-azusa/kimi-switch/internal/config"
	"github.com/kyui-azusa/kimi-switch/internal/rotation"
)

func TestBuildUpstreamURLAvoidsDuplicateV1(t *testing.T) {
	got, err := buildUpstreamURL("https://api.kimi.com/coding/v1", "/v1/chat/completions", "x=1")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://api.kimi.com/coding/v1/chat/completions?x=1"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestProxyFailsOverAfterRateLimit(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		if call == 1 {
			if got := r.Header.Get("Authorization"); got != "Bearer first-key" {
				t.Errorf("first upstream authorization = %q", got)
			}
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"quota"}`))
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer second-key" {
			t.Errorf("second upstream authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":4,"completion_tokens":6,"total_tokens":10},"choices":[]}`))
	}))
	defer upstream.Close()

	c := config.Default()
	c.UpstreamBaseURL = upstream.URL + "/v1"
	c.LocalAPIKey = "local"
	c.ActiveKey = "first"
	c.Rotation.Enabled = true
	c.Rotation.Mode = "token"
	c.Rotation.MaxAttempts = 2
	c.Keys = []config.KeyConfig{
		{Name: "first", Enabled: true, APIKey: "first-key"},
		{Name: "second", Enabled: true, APIKey: "second-key"},
	}
	temp := t.TempDir()
	manager, err := rotation.NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(NewServer(manager))
	defer local.Close()

	requestBody := `{"model":"kimi-for-coding","messages":[{"role":"user","content":"hi"}]}`
	req, err := http.NewRequest(http.MethodPost, local.URL+"/v1/chat/completions", strings.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer local")
	req.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls.Load())
	}
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["usage"] == nil {
		t.Fatal("response lost usage object")
	}
}

func TestProxyFailsOverAfterBillingCycleQuotaError(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"type":"provider.auth_error","message":"You've reached your usage limit for this billing cycle. Your quota will be refreshed in the next cycle."}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
	}))
	defer upstream.Close()
	c := config.Default()
	c.UpstreamBaseURL = upstream.URL + "/v1"
	c.LocalAPIKey = "local"
	c.Rotation.Enabled = true
	c.Rotation.MaxAttempts = 2
	c.Keys = []config.KeyConfig{{Name: "a-empty", Enabled: true, APIKey: "a"}, {Name: "b-ready", Enabled: true, APIKey: "b"}}
	c.ActiveKey = "a-empty"
	temp := t.TempDir()
	manager, err := rotation.NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(NewServer(manager))
	defer local.Close()
	req, _ := http.NewRequest(http.MethodPost, local.URL+"/v1/chat/completions", strings.NewReader(`{"model":"kimi-for-coding","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer local")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("status=%d calls=%d", response.StatusCode, calls.Load())
	}
	_, state := manager.Snapshot()
	if state.Keys["a-empty"].LastCheckStatus != "quota_exhausted" {
		t.Fatalf("quota account status = %+v", state.Keys["a-empty"])
	}
}

func TestDesktopCORS(t *testing.T) {
	c := config.Default()
	temp := t.TempDir()
	manager, err := rotation.NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(manager)

	request := httptest.NewRequest(http.MethodOptions, "/api/status", nil)
	request.Header.Set("Origin", "tauri://localhost")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "tauri://localhost" {
		t.Fatalf("allow origin = %q", got)
	}

	request = httptest.NewRequest(http.MethodOptions, "/api/status", nil)
	request.Header.Set("Origin", "http://tauri.localhost")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("Windows Tauri status = %d", response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "http://tauri.localhost" {
		t.Fatalf("Windows Tauri allow origin = %q", got)
	}

	request = httptest.NewRequest(http.MethodOptions, "/api/status", nil)
	request.Header.Set("Origin", "null")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "null" {
		t.Fatalf("allow null origin = %q", got)
	}

	request = httptest.NewRequest(http.MethodOptions, "/api/status", nil)
	request.Header.Set("Origin", "https://example.com")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("unexpected CORS permission for an external origin")
	}
}

func TestParseUsageIncludesCacheAndReasoningBreakdown(t *testing.T) {
	usage := parseUsage([]byte(`{"usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150,"prompt_tokens_details":{"cached_tokens":30},"completion_tokens_details":{"reasoning_tokens":20}}}`))
	if !usage.Known || usage.InputTokens != 100 || usage.OutputTokens != 50 || usage.TotalTokens != 150 {
		t.Fatalf("unexpected base usage: %+v", usage)
	}
	if usage.CacheReadTokens != 30 || usage.ReasoningTokens != 20 {
		t.Fatalf("unexpected token breakdown: %+v", usage)
	}
}

func TestConnectivityUsesMinimalCompletionAndRecordsHealth(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("method/path = %s %q", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer account-key" {
			t.Fatalf("authorization = %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "kimi-for-coding" || payload["max_tokens"] != float64(1) {
			t.Fatalf("unexpected probe payload: %+v", payload)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`))
	}))
	defer upstream.Close()

	c := config.Default()
	c.UpstreamBaseURL = upstream.URL + "/v1"
	c.LocalAPIKey = "local"
	c.Keys = []config.KeyConfig{{Name: "work", Enabled: true, APIKey: "account-key"}}
	c.ActiveKey = "work"
	temp := t.TempDir()
	manager, err := rotation.NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(NewServer(manager))
	defer local.Close()
	req, _ := http.NewRequest(http.MethodPost, local.URL+"/api/keys/work/test", nil)
	req.Header.Set("Authorization", "Bearer local")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result ConnectivityResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.StatusCode != http.StatusOK {
		t.Fatalf("unexpected result: %+v", result)
	}
	_, state := manager.Snapshot()
	if state.Keys["work"].LastCheckStatus != "ok" || state.Keys["work"].LastCheckAt.IsZero() {
		t.Fatalf("health was not recorded: %+v", state.Keys["work"])
	}
}

func TestConnectivityClassifiesBillingCycleLimit(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"type":"provider.auth_error","message":"You've reached your usage limit for this billing cycle. Your quota will be refreshed in the next cycle."}}`))
	}))
	defer upstream.Close()
	c := config.Default()
	c.UpstreamBaseURL = upstream.URL + "/v1"
	c.LocalAPIKey = "local"
	c.Keys = []config.KeyConfig{{Name: "empty", Enabled: true, APIKey: "account-key"}}
	c.ActiveKey = "empty"
	temp := t.TempDir()
	manager, err := rotation.NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(NewServer(manager))
	defer local.Close()
	req, _ := http.NewRequest(http.MethodPost, local.URL+"/api/keys/empty/test", nil)
	req.Header.Set("Authorization", "Bearer local")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result ConnectivityResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Status != "quota_exhausted" || result.StatusCode != http.StatusForbidden {
		t.Fatalf("unexpected result: %+v", result)
	}
	_, state := manager.Snapshot()
	stats := state.Keys["empty"]
	if stats.LastCheckStatus != "quota_exhausted" || !stats.CooldownUntil.After(time.Now().UTC()) {
		t.Fatalf("quota state was not recorded: %+v", stats)
	}
}

func TestClassifyProviderFailureDistinguishesQuotaAndOverload(t *testing.T) {
	status, _ := classifyProviderFailure(429, []byte(`{"error":{"type":"exceeded_current_quota_error","message":"You've reached kimi monthly usage limit"}}`))
	if status != "quota_exhausted" {
		t.Fatalf("quota status = %q", status)
	}
	status, _ = classifyProviderFailure(429, []byte(`{"error":{"type":"engine_overloaded_error","message":"The engine is currently overloaded"}}`))
	if status != "rate_limited" {
		t.Fatalf("overload status = %q", status)
	}
}

func TestDeleteActiveKeyPersistsToIsolatedConfigAndSelectsNext(t *testing.T) {
	temp := t.TempDir()
	configPath := filepath.Join(temp, "config.json")
	c := config.Default()
	c.LocalAPIKey = "local"
	c.ActiveKey = "a"
	c.Keys = []config.KeyConfig{
		{Name: "a", Enabled: true, APIKey: "a-key"},
		{Name: "b", Enabled: true, APIKey: "b-key"},
	}
	manager, err := rotation.NewManager(c, configPath, filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodDelete, "/api/keys/a", nil)
	request.Header.Set("Authorization", "Bearer local")
	response := httptest.NewRecorder()
	NewServer(manager).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	persisted, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Keys) != 1 || persisted.Keys[0].Name != "b" || persisted.ActiveKey != "b" {
		t.Fatalf("unexpected persisted config: %+v", persisted)
	}
}

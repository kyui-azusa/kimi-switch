package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kyui-azusa/kimi-switch/internal/config"
	"github.com/kyui-azusa/kimi-switch/internal/kimitakeover"
	"github.com/kyui-azusa/kimi-switch/internal/rotation"
)

const directTakeoverFixture = `default_model = "kimi-code/k3-256k"

[providers."managed:kimi-code"]
type = "kimi"
api_key = "secret"
base_url = "https://api.kimi.com/coding/v1"

[models."kimi-code/k3-256k"]
provider = "managed:kimi-code"
model = "k3-256k"
max_context_size = 262144
`

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
	c.Rotation.Mode = "sticky"
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

func TestStatusAutoRepairsDuplicateTakeoverProvider(t *testing.T) {
	temp := t.TempDir()
	kimiHome := filepath.Join(temp, "kimi-code")
	if err := os.MkdirAll(kimiHome, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIMI_CODE_HOME", kimiHome)
	broken := `default_model = "kimi-code/k3-256k"

[providers."managed:kimi-code"]
type = "kimi"
api_key = "secret"
base_url = "https://api.kimi.com/coding/v1"

[providers.kimi-switch-local]
type = "kimi"
api_key = "local"
base_url = "http://127.0.0.1:15722/v1"

[models."kimi-code/k3-256k"]
provider = "kimi-switch-local"
model = "k3-256k"
max_context_size = 262144

# >>> KimiSwitch managed provider
# original_provider = "managed:kimi-code"
[providers."kimi-switch-local"]
type = "kimi"
api_key = "local"
base_url = "http://127.0.0.1:15722/v1"
# <<< KimiSwitch managed provider
`
	configPath := filepath.Join(kimiHome, "config.toml")
	if err := os.WriteFile(configPath, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}

	c := config.Default()
	manager, err := rotation.NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(manager)
	request := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	request.Header.Set("Authorization", "Bearer "+c.LocalAPIKey)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	status, err := kimitakeover.Inspect(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if status.ConfigError != "" || !status.Enabled {
		t.Fatalf("takeover was not repaired: %+v", status)
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(content), "[providers."); got != 2 {
		t.Fatalf("provider table count = %d, config:\n%s", got, content)
	}
}

func TestStatusRestoresDesiredTakeoverAfterExternalConfigRewrite(t *testing.T) {
	temp := t.TempDir()
	kimiHome := filepath.Join(temp, "kimi-code")
	if err := os.MkdirAll(kimiHome, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIMI_CODE_HOME", kimiHome)
	kimiConfigPath := filepath.Join(kimiHome, "config.toml")
	if err := os.WriteFile(kimiConfigPath, []byte(directTakeoverFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := kimitakeover.Set(kimiConfigPath, "127.0.0.1:15722", "local", true); err != nil {
		t.Fatal(err)
	}

	c := config.Default()
	desired := true
	c.TakeoverEnabled = &desired
	manager, err := rotation.NewManager(c, filepath.Join(temp, "config.json"), filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kimiConfigPath, []byte(directTakeoverFixture), 0o600); err != nil {
		t.Fatal(err)
	}

	server := NewServer(manager)
	request := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	request.Header.Set("Authorization", "Bearer "+c.LocalAPIKey)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	status, err := kimitakeover.Inspect(kimiConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || status.AffectedModels != 1 || status.ConfigError != "" {
		t.Fatalf("takeover was not restored after external rewrite: %+v", status)
	}
}

func TestStatusMigratesLegacyTakeoverPreference(t *testing.T) {
	temp := t.TempDir()
	kimiHome := filepath.Join(temp, "kimi-code")
	if err := os.MkdirAll(kimiHome, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIMI_CODE_HOME", kimiHome)
	kimiConfigPath := filepath.Join(kimiHome, "config.toml")
	if err := os.WriteFile(kimiConfigPath, []byte(directTakeoverFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := kimitakeover.Set(kimiConfigPath, "127.0.0.1:15722", "local", true); err != nil {
		t.Fatal(err)
	}

	c := config.Default()
	appConfigPath := filepath.Join(temp, "config.json")
	manager, err := rotation.NewManager(c, appConfigPath, filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(manager)
	request := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	request.Header.Set("Authorization", "Bearer "+c.LocalAPIKey)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	loaded, err := config.Load(appConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TakeoverEnabled == nil || !*loaded.TakeoverEnabled {
		t.Fatalf("migrated takeover preference = %v", loaded.TakeoverEnabled)
	}
}

func TestExplicitDisableRemainsAuthoritative(t *testing.T) {
	temp := t.TempDir()
	kimiHome := filepath.Join(temp, "kimi-code")
	if err := os.MkdirAll(kimiHome, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIMI_CODE_HOME", kimiHome)
	kimiConfigPath := filepath.Join(kimiHome, "config.toml")
	if err := os.WriteFile(kimiConfigPath, []byte(directTakeoverFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	managed, err := kimitakeover.Set(kimiConfigPath, "127.0.0.1:15722", "local", true)
	if err != nil || !managed.Enabled {
		t.Fatalf("initial takeover failed: status=%+v err=%v", managed, err)
	}
	managedContent, err := os.ReadFile(kimiConfigPath)
	if err != nil {
		t.Fatal(err)
	}

	c := config.Default()
	desired := false
	c.TakeoverEnabled = &desired
	appConfigPath := filepath.Join(temp, "config.json")
	manager, err := rotation.NewManager(c, appConfigPath, filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetTakeoverPreference(false); err != nil {
		t.Fatal(err)
	}
	server := NewServer(manager)
	request := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	request.Header.Set("Authorization", "Bearer "+c.LocalAPIKey)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	status, err := kimitakeover.Inspect(kimiConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if status.Enabled || status.AffectedModels != 0 {
		t.Fatalf("explicit disable was not applied: %+v", status)
	}

	if err := os.WriteFile(kimiConfigPath, managedContent, 0o600); err != nil {
		t.Fatal(err)
	}
	persisted, err := config.Load(appConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := rotation.NewManager(persisted, appConfigPath, filepath.Join(temp, "state-restarted.json"))
	if err != nil {
		t.Fatal(err)
	}
	server = NewServer(restarted)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	status, err = kimitakeover.Inspect(kimiConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if status.Enabled || status.AffectedModels != 0 {
		t.Fatalf("explicit disable was lost after restart: %+v", status)
	}
}

func TestTakeoverEndpointPersistsUserPreference(t *testing.T) {
	temp := t.TempDir()
	kimiHome := filepath.Join(temp, "kimi-code")
	if err := os.MkdirAll(kimiHome, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIMI_CODE_HOME", kimiHome)
	kimiConfigPath := filepath.Join(kimiHome, "config.toml")
	if err := os.WriteFile(kimiConfigPath, []byte(directTakeoverFixture), 0o600); err != nil {
		t.Fatal(err)
	}

	c := config.Default()
	appConfigPath := filepath.Join(temp, "config.json")
	manager, err := rotation.NewManager(c, appConfigPath, filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(manager)

	setTakeover := func(enabled bool) {
		t.Helper()
		body := strings.NewReader(fmt.Sprintf(`{"enabled":%t}`, enabled))
		request := httptest.NewRequest(http.MethodPut, "/api/kimi/takeover", body)
		request.Header.Set("Authorization", "Bearer "+c.LocalAPIKey)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("set takeover %t: status = %d, body = %s", enabled, response.Code, response.Body.String())
		}
		loaded, err := config.Load(appConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.TakeoverEnabled == nil || *loaded.TakeoverEnabled != enabled {
			t.Fatalf("persisted takeover preference = %v, want %t", loaded.TakeoverEnabled, enabled)
		}
	}

	setTakeover(true)
	setTakeover(false)
	status, err := kimitakeover.Inspect(kimiConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if status.Enabled || status.AffectedModels != 0 {
		t.Fatalf("explicit endpoint disable did not restore direct mode: %+v", status)
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

func TestResolveAPIKeyPrefersKeychainWhenAvailable(t *testing.T) {
	originalRead := secretRead
	defer func() { secretRead = originalRead }()
	secretRead = func(string) (string, error) { return "from-keychain", nil }

	got, err := resolveAPIKey(config.KeyConfig{Name: "work", SecretRef: "kimi-switch/work", APIKey: "from-local"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-keychain" {
		t.Fatalf("api key = %q, want from-keychain", got)
	}
}

func TestResolveAPIKeyFallsBackToLocalCopyWhenKeychainFails(t *testing.T) {
	originalRead := secretRead
	defer func() { secretRead = originalRead }()
	secretRead = func(string) (string, error) { return "", errors.New("broken pipe") }

	got, err := resolveAPIKey(config.KeyConfig{Name: "work", SecretRef: "kimi-switch/work", APIKey: "from-local"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-local" {
		t.Fatalf("api key = %q, want from-local", got)
	}
}

func TestResolveAPIKeySupportsLocalOnlyConfig(t *testing.T) {
	got, err := resolveAPIKey(config.KeyConfig{Name: "work", APIKey: "from-local"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-local" {
		t.Fatalf("api key = %q, want from-local", got)
	}
}

func TestPublicConfigMarksKeychainLocalFallbackWithoutLeakingAPIKey(t *testing.T) {
	public := publicConfig(config.Config{
		Keys: []config.KeyConfig{
			{Name: "work", SecretRef: "kimi-switch/work", APIKey: "secret-value", Enabled: true},
			{Name: "env", APIKeyEnv: "KIMI_ENV", Enabled: true},
			{Name: "local", APIKey: "local-only", Enabled: true},
		},
	})
	keysAny, ok := public["keys"].([]map[string]any)
	if !ok || len(keysAny) != 3 {
		t.Fatalf("unexpected public keys payload: %#v", public["keys"])
	}
	if keysAny[0]["source"] != "keychain+local" || keysAny[1]["source"] != "environment" || keysAny[2]["source"] != "inline" {
		t.Fatalf("unexpected sources: %#v", keysAny)
	}
	if _, ok := keysAny[0]["api_key"]; ok {
		t.Fatal("public config leaked api_key")
	}
}

func TestHandleKeyUpsertStoresKeychainAndLocalFallback(t *testing.T) {
	originalWrite := secretWrite
	defer func() { secretWrite = originalWrite }()
	writes := map[string]string{}
	secretWrite = func(service, value string) error {
		writes[service] = value
		return nil
	}

	temp := t.TempDir()
	configPath := filepath.Join(temp, "config.json")
	c := config.Default()
	c.LocalAPIKey = "local"
	manager, err := rotation.NewManager(c, configPath, filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(manager)

	body := `{"name":"tyx","account":"tyx","api_key":"sk-123","enabled":true}`
	request := httptest.NewRequest(http.MethodPost, "/api/keys", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer local")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if writes["kimi-switch/tyx"] != "sk-123" {
		t.Fatalf("keychain writes = %#v", writes)
	}
	persisted, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Keys) != 1 || persisted.Keys[0].SecretRef != "kimi-switch/tyx" || persisted.Keys[0].APIKey != "sk-123" {
		t.Fatalf("unexpected persisted key: %+v", persisted.Keys)
	}
}

func TestHandleKimiImportStoresKeychainAndLocalFallback(t *testing.T) {
	originalWrite := secretWrite
	defer func() { secretWrite = originalWrite }()
	secretWrite = func(string, string) error { return nil }

	temp := t.TempDir()
	configPath := filepath.Join(temp, "config.json")
	c := config.Default()
	c.LocalAPIKey = "local"
	manager, err := rotation.NewManager(c, configPath, filepath.Join(temp, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(manager)

	kimiConfig := filepath.Join(temp, "kimi.toml")
	if err := os.WriteFile(kimiConfig, []byte("[providers.work]\napi_key = \"sk-imported\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body := `{"path":"` + kimiConfig + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/import/kimi", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer local")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	persisted, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Keys) != 1 || persisted.Keys[0].SecretRef != "kimi-switch/work" || persisted.Keys[0].APIKey != "sk-imported" {
		t.Fatalf("unexpected persisted key: %+v", persisted.Keys)
	}
}

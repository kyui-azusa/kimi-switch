package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kyui-azusa/kimi-switch/internal/config"
	"github.com/kyui-azusa/kimi-switch/internal/kimiimport"
	"github.com/kyui-azusa/kimi-switch/internal/kimitakeover"
	"github.com/kyui-azusa/kimi-switch/internal/rotation"
	"github.com/kyui-azusa/kimi-switch/internal/secret"
)

const maxResponseBytes = 64 << 20

var (
	secretRead  = secret.Read
	secretWrite = secret.Write
)

type Server struct {
	manager    *rotation.Manager
	client     *http.Client
	takeoverMu sync.Mutex
}

func NewServer(manager *rotation.Manager) *Server {
	return &Server{
		manager: manager,
		client:  &http.Client{Timeout: 0}, // streaming requests must not have a fixed timeout
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.handleDesktopCORS(w, r) {
		return
	}
	if r.URL.Path == "/" || r.URL.Path == "/index.html" {
		s.serveDashboard(w)
		return
	}
	if r.URL.Path == "/healthz" {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":               true,
			"available_keys":   s.manager.AvailableKeyCount(),
			"rotation_enabled": s.manager.Config().Rotation.Enabled,
		})
		return
	}
	if !s.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid local proxy key"})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.handleAdmin(w, r)
		return
	}
	s.handleUpstream(w, r)
}

// handleDesktopCORS only admits the origins used by KimiSwitch's local Vite
// preview and Tauri WebView. Every admin request still needs the local key.
func (s *Server) handleDesktopCORS(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	switch origin {
	case "http://localhost:1420", "http://127.0.0.1:1420", "tauri://localhost", "http://tauri.localhost", "https://tauri.localhost", "null":
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	}
	if r.Method == http.MethodOptions && w.Header().Get("Access-Control-Allow-Origin") != "" {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}

func (s *Server) serveDashboard(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, dashboardHTML(s.manager.Config().LocalAPIKey))
}

func (s *Server) authorized(r *http.Request) bool {
	key := s.manager.Config().LocalAPIKey
	if key == "" {
		return true
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		auth = strings.TrimSpace(auth[len("bearer "):])
	}
	return auth == key
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/status":
		c, state := s.manager.Snapshot()
		takeover := s.takeoverStatus()
		writeJSON(w, http.StatusOK, map[string]any{"config": publicConfig(c), "keys": state.Keys, "kimi_takeover": takeover})
	case r.Method == http.MethodGet && r.URL.Path == "/api/usage":
		_, state := s.manager.Snapshot()
		writeJSON(w, http.StatusOK, map[string]any{"daily": state.Daily})
	case r.Method == http.MethodPost && r.URL.Path == "/api/rotation":
		var input struct {
			Enabled *bool  `json:"enabled"`
			Mode    string `json:"mode"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&input); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if input.Enabled == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "enabled is required"})
			return
		}
		if err := s.manager.SetRotation(*input.Enabled, input.Mode); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "rotation": s.manager.Config().Rotation})
	case r.Method == http.MethodPut && r.URL.Path == "/api/settings":
		var input struct {
			UpstreamBaseURL string                `json:"upstream_base_url"`
			Rotation        config.RotationConfig `json:"rotation"`
		}
		if err := decodeJSON(r, &input); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.manager.UpdateSettings(input.UpstreamBaseURL, input.Rotation); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case r.Method == http.MethodPut && r.URL.Path == "/api/kimi/takeover":
		var input struct {
			Enabled bool `json:"enabled"`
		}
		if err := decodeJSON(r, &input); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		c := s.manager.Config()
		s.takeoverMu.Lock()
		status, err := kimitakeover.Set(kimitakeover.DefaultPath(), c.ListenAddr, c.LocalAPIKey, input.Enabled)
		if err == nil {
			if persistErr := s.manager.SetTakeoverPreference(input.Enabled); persistErr != nil {
				err = fmt.Errorf("persist takeover preference: %w", persistErr)
			}
		}
		s.takeoverMu.Unlock()
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, status)
	case r.Method == http.MethodPost && r.URL.Path == "/api/import/kimi":
		s.handleKimiImport(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/keys":
		s.handleKeyUpsert(w, r, "")
	case r.Method == http.MethodPost && r.URL.Path == "/api/keys/reorder":
		var input struct {
			Names []string `json:"names"`
		}
		if err := decodeJSON(r, &input); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.manager.ReorderKeys(input.Names); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/keys/") && strings.HasSuffix(r.URL.Path, "/test"):
		name, action, err := parseKeyPath(r.URL.Path)
		if err != nil || action != "test" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid key path"})
			return
		}
		result := s.testConnectivity(r.Context(), name)
		writeJSON(w, http.StatusOK, result)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/keys/"):
		name, action, err := parseKeyPath(r.URL.Path)
		if err != nil || action != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid key path"})
			return
		}
		s.handleKeyUpsert(w, r, name)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/keys/") && strings.HasSuffix(r.URL.Path, "/use"):
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/keys/"), "/use")
		name, err := url.PathUnescape(name)
		if err != nil || name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid key name"})
			return
		}
		if err := s.manager.SetActive(name); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "active_key": name})
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/keys/") && strings.HasSuffix(r.URL.Path, "/duplicate"):
		name, _, err := parseKeyPath(r.URL.Path)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		created, err := s.manager.DuplicateKey(name)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": created})
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/keys/") && strings.HasSuffix(r.URL.Path, "/toggle"):
		name, _, err := parseKeyPath(r.URL.Path)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		var input struct {
			Enabled bool `json:"enabled"`
		}
		if err := decodeJSON(r, &input); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.manager.SetKeyEnabled(name, input.Enabled); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/keys/"):
		name, action, err := parseKeyPath(r.URL.Path)
		if err != nil || action != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid key path"})
			return
		}
		if err := s.manager.DeleteKey(name); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown admin endpoint"})
	}
}

func (s *Server) takeoverStatus() kimitakeover.Status {
	s.takeoverMu.Lock()
	defer s.takeoverMu.Unlock()

	c := s.manager.Config()
	path := kimitakeover.DefaultPath()
	status, err := kimitakeover.Inspect(path)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	desired := c.TakeoverEnabled
	if desired == nil {
		migrated := status.Enabled || status.AffectedModels > 0
		if err := s.manager.SetTakeoverPreference(migrated); err != nil {
			status.Error = "could not migrate takeover preference: " + err.Error()
			return status
		}
		desired = &migrated
	}

	healthy := status.ConfigError == "" && status.AffectedModels > 0
	if *desired && status.Enabled && healthy {
		return status
	}
	if !*desired && !status.Enabled && status.AffectedModels == 0 {
		return status
	}

	repaired, err := kimitakeover.Set(path, c.ListenAddr, c.LocalAPIKey, *desired)
	if err != nil {
		status.Error = "automatic takeover reconciliation failed: " + err.Error()
		return status
	}
	if repaired.ConfigError != "" {
		repaired.Error = "automatic config repair did not produce a valid config"
	}
	return repaired
}

type ConnectivityResult struct {
	Name       string    `json:"name"`
	OK         bool      `json:"ok"`
	Status     string    `json:"status"`
	StatusCode int       `json:"status_code,omitempty"`
	LatencyMS  int64     `json:"latency_ms"`
	Message    string    `json:"message"`
	TestedAt   time.Time `json:"tested_at"`
}

func (s *Server) testConnectivity(parent context.Context, name string) ConnectivityResult {
	result := ConnectivityResult{Name: name, Status: "failed", TestedAt: time.Now().UTC()}
	var key config.KeyConfig
	for _, candidate := range s.manager.Config().Keys {
		if candidate.Name == name {
			key = candidate
			break
		}
	}
	if key.Name == "" {
		result.Message = "账号不存在"
		return result
	}

	started := time.Now()
	apiKey, err := resolveAPIKey(key)
	if err != nil {
		result.LatencyMS = time.Since(started).Milliseconds()
		result.Status = "invalid_key"
		result.Message = err.Error()
		_ = s.manager.RecordConnectivity(name, result.Status, time.Since(started), result.Message)
		return result
	}
	endpoint, err := buildUpstreamURL(s.manager.Config().UpstreamBaseURL, "/v1/chat/completions", "")
	if err != nil {
		result.LatencyMS = time.Since(started).Milliseconds()
		result.Message = err.Error()
		_ = s.manager.RecordConnectivity(name, result.Status, time.Since(started), result.Message)
		return result
	}
	payload, _ := json.Marshal(map[string]any{
		"model":      "kimi-for-coding",
		"messages":   []map[string]string{{"role": "user", "content": "Reply OK"}},
		"max_tokens": 1,
		"stream":     false,
	})
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err == nil {
		request.Header.Set("Authorization", "Bearer "+apiKey)
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Content-Type", "application/json")
	}
	if err != nil {
		result.LatencyMS = time.Since(started).Milliseconds()
		result.Message = err.Error()
		_ = s.manager.RecordConnectivity(name, result.Status, time.Since(started), result.Message)
		return result
	}
	response, err := s.client.Do(request)
	latency := time.Since(started)
	result.LatencyMS = latency.Milliseconds()
	if err != nil {
		result.Status = "network_error"
		result.Message = err.Error()
		_ = s.manager.RecordConnectivity(name, result.Status, latency, result.Message)
		return result
	}
	defer response.Body.Close()
	result.StatusCode = response.StatusCode
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	result.OK = response.StatusCode >= 200 && response.StatusCode < 300
	if result.OK {
		result.Status = "ok"
		result.Message = "连接正常，账号可生成内容"
	} else {
		result.Status, result.Message = classifyProviderFailure(response.StatusCode, body)
	}
	_ = s.manager.RecordConnectivity(name, result.Status, latency, result.Message)
	return result
}

func classifyProviderFailure(httpStatus int, body []byte) (string, string) {
	errorType, message := providerErrorDetails(body)
	lower := strings.ToLower(errorType + " " + message)
	quota := containsAny(lower,
		"usage limit", "billing cycle", "monthly usage limit", "usage quota",
		"exceeded_current_quota", "insufficient quota", "insufficient balance",
		"quota exhausted", "额度用尽", "额度不足", "余额不足", "达到上限",
	)
	status := "failed"
	switch {
	case quota && (httpStatus == 402 || httpStatus == 403 || httpStatus == 429):
		status = "quota_exhausted"
	case httpStatus == 401:
		status = "invalid_key"
	case httpStatus == 403 && containsAny(lower, "access terminated", "disabled", "suspended", "禁用", "封禁"):
		status = "account_disabled"
	case httpStatus == 403:
		status = "forbidden"
	case httpStatus == 429:
		status = "rate_limited"
	case httpStatus >= 500:
		status = "upstream_error"
	}
	if message == "" {
		message = fmt.Sprintf("HTTP %d", httpStatus)
	} else {
		message = fmt.Sprintf("HTTP %d: %s", httpStatus, message)
	}
	return status, message
}

func providerErrorDetails(body []byte) (string, string) {
	errorType, message := "", ""
	var payload map[string]any
	if json.Unmarshal(body, &payload) == nil {
		if value, ok := payload["error"].(string); ok {
			message = value
		}
		if value, ok := payload["error"].(map[string]any); ok {
			errorType, _ = value["type"].(string)
			message, _ = value["message"].(string)
		}
		if message == "" {
			message, _ = payload["message"].(string)
		}
	}
	if message == "" {
		message = string(body)
	}
	message = strings.Join(strings.Fields(strings.TrimSpace(message)), " ")
	if len(message) > 240 {
		message = message[:240]
	}
	return strings.TrimSpace(errorType), message
}

func containsAny(value string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(value, term) {
			return true
		}
	}
	return false
}

type keyInput struct {
	Name      string `json:"name"`
	Account   string `json:"account"`
	Notes     string `json:"notes"`
	APIKey    string `json:"api_key"`
	APIKeyEnv string `json:"api_key_env"`
	Enabled   bool   `json:"enabled"`
}

func (s *Server) handleKeyUpsert(w http.ResponseWriter, r *http.Request, originalName string) {
	var input keyInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	var existing config.KeyConfig
	if originalName != "" {
		for _, item := range s.manager.Config().Keys {
			if item.Name == originalName {
				existing = item
				break
			}
		}
	}
	key := config.KeyConfig{
		Name:      input.Name,
		Account:   strings.TrimSpace(input.Account),
		Notes:     strings.TrimSpace(input.Notes),
		APIKeyEnv: strings.TrimSpace(input.APIKeyEnv),
		Enabled:   input.Enabled,
		CreatedAt: existing.CreatedAt,
		SecretRef: existing.SecretRef,
		APIKey:    existing.APIKey,
	}
	if input.APIKey != "" {
		service := "kimi-switch/" + input.Name
		if err := secretWrite(service, input.APIKey); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		key.SecretRef = service
		key.APIKey = strings.TrimSpace(input.APIKey)
		key.APIKeyEnv = ""
	} else if key.APIKeyEnv != "" {
		key.SecretRef = ""
		key.APIKey = ""
	}
	if key.SecretRef == "" && key.APIKeyEnv == "" && key.APIKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "API key or environment variable is required"})
		return
	}
	if err := s.manager.UpsertKey(originalName, key); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": key.Name})
}

func (s *Server) handleKimiImport(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path string `json:"path"`
	}
	if r.Body != http.NoBody {
		_ = decodeJSON(r, &input)
	}
	if strings.TrimSpace(input.Path) == "" {
		input.Path = kimiimport.DefaultPath()
	}
	providers, err := kimiimport.Parse(input.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	imported := []string{}
	for name, apiKey := range providers {
		service := "kimi-switch/" + name
		if err := secretWrite(service, apiKey); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		key := config.KeyConfig{Name: name, Account: name, SecretRef: service, APIKey: apiKey, Enabled: true}
		original := ""
		for _, existing := range s.manager.Config().Keys {
			if existing.Name == name {
				original = name
				key.CreatedAt = existing.CreatedAt
				break
			}
		}
		if err := s.manager.UpsertKey(original, key); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		imported = append(imported, name)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": input.Path, "imported": imported})
}

func parseKeyPath(path string) (string, string, error) {
	rest := strings.TrimPrefix(path, "/api/keys/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", "", errors.New("missing key name")
	}
	name, err := url.PathUnescape(parts[0])
	if err != nil {
		return "", "", err
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	return name, action, nil
}

func decodeJSON(r *http.Request, value any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(value)
}

func (s *Server) handleUpstream(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxResponseBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read request body: " + err.Error()})
		return
	}
	stream := requestIsStream(body)
	estimate := estimateTokens(body)
	maxAttempts := 1
	if s.manager.Config().Rotation.Enabled {
		maxAttempts = s.manager.Config().Rotation.MaxAttempts
	}
	var lastStatus int
	var lastHeaders http.Header
	var lastBody []byte
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		selected, selectErr := s.manager.Select(estimate, attempt)
		if selectErr != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": selectErr.Error()})
			return
		}
		apiKey, keyErr := resolveAPIKey(selected.Key)
		if keyErr != nil {
			s.manager.Finish(selected, 0, rotation.Usage{}, keyErr)
			s.manager.SetAccountStatus(selected.Key.Name, "invalid_key", keyErr.Error())
			lastErr = keyErr
			continue
		}
		upstream, urlErr := buildUpstreamURL(s.manager.Config().UpstreamBaseURL, r.URL.Path, r.URL.RawQuery)
		if urlErr != nil {
			s.manager.Finish(selected, 0, rotation.Usage{}, urlErr)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": urlErr.Error()})
			return
		}
		request, reqErr := http.NewRequestWithContext(r.Context(), r.Method, upstream, bytes.NewReader(body))
		if reqErr != nil {
			s.manager.Finish(selected, 0, rotation.Usage{}, reqErr)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": reqErr.Error()})
			return
		}
		copyRequestHeaders(request.Header, r.Header, apiKey)
		response, doErr := s.client.Do(request)
		if doErr != nil {
			s.manager.Finish(selected, 0, rotation.Usage{}, doErr)
			lastErr = doErr
			continue
		}

		lastStatus = response.StatusCode
		lastHeaders = response.Header.Clone()
		if response.StatusCode >= 400 && s.manager.CanRetry(response.StatusCode) && attempt < maxAttempts {
			lastBody, _ = io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
			response.Body.Close()
			usage := parseUsage(lastBody)
			s.manager.Finish(selected, response.StatusCode, usage, nil)
			accountStatus, accountMessage := classifyProviderFailure(response.StatusCode, lastBody)
			s.manager.SetAccountStatus(selected.Key.Name, accountStatus, accountMessage)
			continue
		}

		if stream && response.StatusCode < 400 {
			s.streamResponse(w, response, selected)
			return
		}
		lastBody, err = io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
		response.Body.Close()
		usage := parseUsage(lastBody)
		s.manager.Finish(selected, response.StatusCode, usage, nil)
		if response.StatusCode >= 400 {
			accountStatus, accountMessage := classifyProviderFailure(response.StatusCode, lastBody)
			s.manager.SetAccountStatus(selected.Key.Name, accountStatus, accountMessage)
		}
		writeHeaders(w, lastHeaders)
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(lastBody)
		return
	}

	if lastStatus != 0 {
		writeHeaders(w, lastHeaders)
		w.WriteHeader(lastStatus)
		_, _ = w.Write(lastBody)
		return
	}
	if lastErr != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": lastErr.Error()})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": "all upstream attempts failed"})
}

func (s *Server) streamResponse(w http.ResponseWriter, response *http.Response, selected rotation.Selected) {
	defer response.Body.Close()
	writeHeaders(w, response.Header)
	w.WriteHeader(response.StatusCode)
	flusher, _ := w.(http.Flusher)
	reader := bufio.NewReaderSize(response.Body, 64<<10)
	usage := rotation.Usage{}
	var streamErr error
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			if parsed := parseUsage([]byte(line)); parsed.Known {
				usage = parsed
			}
			if _, writeErr := io.WriteString(w, line); writeErr != nil {
				streamErr = writeErr
				break
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				streamErr = err
			}
			break
		}
	}
	s.manager.Finish(selected, response.StatusCode, usage, streamErr)
}

func resolveAPIKey(key config.KeyConfig) (string, error) {
	if key.SecretRef != "" {
		value, err := secretRead(key.SecretRef)
		if err == nil {
			return value, nil
		}
		if key.APIKey != "" {
			return key.APIKey, nil
		}
		return "", err
	}
	if key.APIKey != "" {
		return key.APIKey, nil
	}
	if key.APIKeyEnv != "" {
		if value := os.Getenv(key.APIKeyEnv); value != "" {
			return value, nil
		}
		return "", fmt.Errorf("environment variable %s is empty", key.APIKeyEnv)
	}
	return "", fmt.Errorf("key %q has no api_key, api_key_env, or secret_ref", key.Name)
}

func buildUpstreamURL(base, requestPath, rawQuery string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid upstream_base_url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", errors.New("upstream_base_url must include scheme and host")
	}
	basePath := strings.TrimRight(u.Path, "/")
	path := requestPath
	if strings.HasSuffix(basePath, "/v1") && (path == "/v1" || strings.HasPrefix(path, "/v1/")) {
		path = strings.TrimPrefix(path, "/v1")
	}
	u.Path = basePath + "/" + strings.TrimLeft(path, "/")
	u.RawQuery = rawQuery
	return u.String(), nil
}

func copyRequestHeaders(dst, src http.Header, apiKey string) {
	for key, values := range src {
		if strings.EqualFold(key, "Authorization") || strings.EqualFold(key, "Host") || strings.EqualFold(key, "Content-Length") {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
	dst.Set("Authorization", "Bearer "+apiKey)
}

func writeHeaders(w http.ResponseWriter, headers http.Header) {
	for key, values := range headers {
		if strings.EqualFold(key, "Content-Length") || strings.EqualFold(key, "Connection") {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
}

func requestIsStream(body []byte) bool {
	var value struct {
		Stream bool `json:"stream"`
	}
	return json.Unmarshal(body, &value) == nil && value.Stream
}

func estimateTokens(body []byte) int64 {
	if len(body) == 0 {
		return 0
	}
	// This is only a reservation for balancing before the response arrives.
	// Actual statistics use the provider's usage fields whenever available.
	return int64((len(body) + 3) / 4)
}

func parseUsage(body []byte) rotation.Usage {
	var value any
	if json.Unmarshal(body, &value) != nil {
		line := strings.TrimSpace(string(body))
		if strings.HasPrefix(line, "data:") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if line == "[DONE]" {
				return rotation.Usage{}
			}
			if json.Unmarshal([]byte(line), &value) != nil {
				return rotation.Usage{}
			}
		} else {
			return rotation.Usage{}
		}
	}
	return findUsage(value)
}

func findUsage(value any) rotation.Usage {
	switch current := value.(type) {
	case map[string]any:
		if raw, ok := current["usage"].(map[string]any); ok {
			input := firstNumber(raw, "prompt_tokens", "input_tokens")
			output := firstNumber(raw, "completion_tokens", "output_tokens")
			total := firstNumber(raw, "total_tokens")
			cacheRead := firstNumber(raw, "cached_input_tokens", "cache_read_input_tokens")
			cacheRead += nestedNumber(raw, "prompt_tokens_details", "cached_tokens")
			cacheRead += nestedNumber(raw, "input_tokens_details", "cached_tokens")
			reasoning := firstNumber(raw, "reasoning_tokens", "reasoning_output_tokens")
			reasoning += nestedNumber(raw, "completion_tokens_details", "reasoning_tokens")
			reasoning += nestedNumber(raw, "output_tokens_details", "reasoning_tokens")
			if input > 0 || output > 0 || total > 0 || cacheRead > 0 || reasoning > 0 {
				return rotation.Usage{
					InputTokens: input, OutputTokens: output, TotalTokens: total,
					CacheReadTokens: cacheRead, ReasoningTokens: reasoning, Known: true,
				}
			}
		}
		for _, child := range current {
			if usage := findUsage(child); usage.Known {
				return usage
			}
		}
	case []any:
		for _, child := range current {
			if usage := findUsage(child); usage.Known {
				return usage
			}
		}
	}
	return rotation.Usage{}
}

func nestedNumber(value map[string]any, objectName, fieldName string) int64 {
	object, ok := value[objectName].(map[string]any)
	if !ok {
		return 0
	}
	return firstNumber(object, fieldName)
}

func firstNumber(value map[string]any, names ...string) int64 {
	for _, name := range names {
		if raw, ok := value[name]; ok {
			switch number := raw.(type) {
			case float64:
				return int64(number)
			case json.Number:
				if value, err := number.Int64(); err == nil {
					return value
				}
			case int64:
				return number
			}
		}
	}
	return 0
}

func publicConfig(c config.Config) map[string]any {
	keys := make([]map[string]any, 0, len(c.Keys))
	for _, key := range c.Keys {
		source := "inline"
		if key.SecretRef != "" && key.APIKey != "" {
			source = "keychain+local"
		} else if key.SecretRef != "" {
			source = "keychain"
		} else if key.APIKeyEnv != "" {
			source = "environment"
		}
		keys = append(keys, map[string]any{
			"name":        key.Name,
			"account":     key.Account,
			"notes":       key.Notes,
			"enabled":     key.Enabled,
			"created_at":  key.CreatedAt,
			"source":      source,
			"api_key_env": key.APIKeyEnv,
			"api_key_set": key.APIKey != "" || key.APIKeyEnv != "" || key.SecretRef != "",
		})
	}
	return map[string]any{
		"listen_addr":       c.ListenAddr,
		"upstream_base_url": c.UpstreamBaseURL,
		"active_key":        c.ActiveKey,
		"rotation":          c.Rotation,
		"keys":              keys,
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

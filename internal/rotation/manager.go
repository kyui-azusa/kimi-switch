package rotation

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kyui-azusa/kimi-switch/internal/config"
)

type Stats struct {
	Requests        int64     `json:"requests"`
	Successes       int64     `json:"successes"`
	Failures        int64     `json:"failures"`
	InputTokens     int64     `json:"input_tokens"`
	OutputTokens    int64     `json:"output_tokens"`
	TotalTokens     int64     `json:"total_tokens"`
	CacheReadTokens int64     `json:"cache_read_tokens"`
	ReasoningTokens int64     `json:"reasoning_tokens"`
	EstimatedTokens int64     `json:"estimated_tokens"`
	ReservedTokens  int64     `json:"reserved_tokens"`
	InFlight        int64     `json:"in_flight"`
	CooldownUntil   time.Time `json:"cooldown_until,omitempty"`
	LastUsedAt      time.Time `json:"last_used_at,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
	LastCheckAt     time.Time `json:"last_check_at,omitempty"`
	LastCheckStatus string    `json:"last_check_status,omitempty"`
	LastCheckMS     int64     `json:"last_check_ms,omitempty"`
}

type State struct {
	Keys  map[string]Stats                 `json:"keys"`
	Daily map[string]map[string]DailyUsage `json:"daily,omitempty"`
}

type DailyUsage struct {
	Requests        int64 `json:"requests"`
	Successes       int64 `json:"successes"`
	Failures        int64 `json:"failures"`
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	TotalTokens     int64 `json:"total_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
	EstimatedTokens int64 `json:"estimated_tokens"`
}

type Selected struct {
	Key      config.KeyConfig
	Estimate int64
	Attempt  int
}

type Usage struct {
	InputTokens     int64
	OutputTokens    int64
	TotalTokens     int64
	CacheReadTokens int64
	ReasoningTokens int64
	Known           bool
}

type Manager struct {
	mu         sync.Mutex
	cfg        config.Config
	state      State
	configPath string
	statePath  string
}

func NewManager(c config.Config, configPath, statePath string) (*Manager, error) {
	if err := c.Normalize(); err != nil {
		return nil, err
	}
	s := State{Keys: map[string]Stats{}, Daily: map[string]map[string]DailyUsage{}}
	if b, err := os.ReadFile(statePath); err == nil {
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, fmt.Errorf("parse %s: %w", statePath, err)
		}
		if s.Keys == nil {
			s.Keys = map[string]Stats{}
		}
		if s.Daily == nil {
			s.Daily = map[string]map[string]DailyUsage{}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, k := range c.Keys {
		if _, ok := s.Keys[k.Name]; !ok {
			s.Keys[k.Name] = Stats{}
		}
	}
	if strings.TrimSpace(configPath) == "" {
		return nil, errors.New("config path is required")
	}
	return &Manager{cfg: c, state: s, configPath: configPath, statePath: statePath}, nil
}

func (m *Manager) Config() config.Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg
}

func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneState(m.state)
}

func (m *Manager) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.persistLocked()
}

func (m *Manager) SetActive(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.keyIndexLocked(name)
	if index < 0 {
		return fmt.Errorf("unknown key %q", name)
	}
	originalCfg := m.cfg
	originalState := cloneState(m.state)
	m.cfg.Keys[index].Enabled = true
	// Selecting an account is a configuration choice, not a connectivity
	// test. Keep the last observed health/cooldown so the UI does not report
	// an account as healthy merely because it was selected.
	m.cfg.ActiveKey = name
	if err := m.persistLocked(); err != nil {
		m.cfg = originalCfg
		m.state = originalState
		return err
	}
	return nil
}

func (m *Manager) SetRotation(enabled bool, mode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mode != "" {
		normalized, err := config.NormalizeRotationMode(mode)
		if err != nil {
			return err
		}
		mode = normalized
	}
	m.cfg.Rotation.Enabled = enabled
	if mode != "" {
		m.cfg.Rotation.Mode = mode
	}
	return m.persistLocked()
}

func (m *Manager) SetTakeoverPreference(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := m.cfg.TakeoverEnabled
	value := enabled
	m.cfg.TakeoverEnabled = &value
	if err := config.Save(m.configPath, m.cfg); err != nil {
		m.cfg.TakeoverEnabled = previous
		return err
	}
	return nil
}

func (m *Manager) UpsertKey(originalName string, key config.KeyConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key.Name = strings.TrimSpace(key.Name)
	if key.Name == "" {
		return errors.New("key name cannot be empty")
	}
	if key.CreatedAt == 0 {
		key.CreatedAt = time.Now().UnixMilli()
	}
	for _, existing := range m.cfg.Keys {
		if existing.Name == key.Name && existing.Name != originalName {
			return fmt.Errorf("key %q already exists", key.Name)
		}
	}
	index := -1
	for i := range m.cfg.Keys {
		if m.cfg.Keys[i].Name == originalName || (originalName == "" && m.cfg.Keys[i].Name == key.Name) {
			index = i
			break
		}
	}
	if index >= 0 {
		oldName := m.cfg.Keys[index].Name
		m.cfg.Keys[index] = key
		if oldName != key.Name {
			stats := m.state.Keys[oldName]
			delete(m.state.Keys, oldName)
			m.state.Keys[key.Name] = stats
			for date, entries := range m.state.Daily {
				if daily, ok := entries[oldName]; ok {
					delete(entries, oldName)
					entries[key.Name] = daily
					m.state.Daily[date] = entries
				}
			}
			if m.cfg.ActiveKey == oldName {
				m.cfg.ActiveKey = key.Name
			}
		}
	} else {
		m.cfg.Keys = append(m.cfg.Keys, key)
		m.state.Keys[key.Name] = Stats{}
		if m.cfg.ActiveKey == "" {
			m.cfg.ActiveKey = key.Name
		}
	}
	return m.persistLocked()
}

func (m *Manager) DeleteKey(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := -1
	for i := range m.cfg.Keys {
		if m.cfg.Keys[i].Name == name {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("unknown key %q", name)
	}
	m.cfg.Keys = append(m.cfg.Keys[:index], m.cfg.Keys[index+1:]...)
	delete(m.state.Keys, name)
	for date, entries := range m.state.Daily {
		delete(entries, name)
		if len(entries) == 0 {
			delete(m.state.Daily, date)
		}
	}
	if m.cfg.ActiveKey == name {
		m.cfg.ActiveKey = ""
		if len(m.cfg.Keys) > 0 {
			m.cfg.ActiveKey = m.cfg.Keys[0].Name
		}
	}
	return m.persistLocked()
}

func (m *Manager) SetKeyEnabled(name string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.cfg.Keys {
		if m.cfg.Keys[i].Name == name {
			originalCfg := m.cfg
			originalState := cloneState(m.state)
			m.cfg.Keys[i].Enabled = enabled
			if err := m.persistLocked(); err != nil {
				m.cfg = originalCfg
				m.state = originalState
				return err
			}
			return nil
		}
	}
	return fmt.Errorf("unknown key %q", name)
}

func (m *Manager) ReorderKeys(names []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(names) != len(m.cfg.Keys) {
		return fmt.Errorf("reorder requires exactly %d names", len(m.cfg.Keys))
	}

	byName := make(map[string]config.KeyConfig, len(m.cfg.Keys))
	for _, key := range m.cfg.Keys {
		byName[key.Name] = key
	}

	seen := make(map[string]bool, len(names))
	reordered := make([]config.KeyConfig, 0, len(names))
	for _, rawName := range names {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return errors.New("reorder names cannot be empty")
		}
		if seen[name] {
			return fmt.Errorf("duplicate key %q in reorder", name)
		}
		key, ok := byName[name]
		if !ok {
			return fmt.Errorf("unknown key %q", name)
		}
		seen[name] = true
		reordered = append(reordered, key)
	}

	for _, key := range m.cfg.Keys {
		if !seen[key.Name] {
			return fmt.Errorf("missing key %q in reorder", key.Name)
		}
	}

	m.cfg.Keys = reordered
	return m.persistLocked()
}

func (m *Manager) DuplicateKey(name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var source *config.KeyConfig
	for i := range m.cfg.Keys {
		if m.cfg.Keys[i].Name == name {
			copy := m.cfg.Keys[i]
			source = &copy
			break
		}
	}
	if source == nil {
		return "", fmt.Errorf("unknown key %q", name)
	}
	base := name + "-copy"
	newName := base
	for suffix := 2; m.hasKeyLocked(newName); suffix++ {
		newName = fmt.Sprintf("%s-%d", base, suffix)
	}
	source.Name = newName
	source.Account = strings.TrimSpace(source.Account + " copy")
	source.CreatedAt = time.Now().UnixMilli()
	m.cfg.Keys = append(m.cfg.Keys, *source)
	m.state.Keys[newName] = Stats{}
	return newName, m.persistLocked()
}

func (m *Manager) UpdateSettings(upstream string, rotationConfig config.RotationConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.TrimSpace(upstream) != "" {
		m.cfg.UpstreamBaseURL = strings.TrimSpace(upstream)
	}
	normalized, err := config.NormalizeRotationMode(rotationConfig.Mode)
	if err != nil {
		return err
	}
	rotationConfig.Mode = normalized
	if rotationConfig.MaxAttempts < 1 {
		return errors.New("max_attempts must be at least 1")
	}
	if rotationConfig.CooldownSeconds < config.DefaultFailureCooldownSeconds {
		return fmt.Errorf("cooldown_seconds must be at least %d", config.DefaultFailureCooldownSeconds)
	}
	m.cfg.Rotation = rotationConfig
	return m.persistLocked()
}

func (m *Manager) Select(estimate int64, attempt int) (Selected, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if estimate < 0 {
		estimate = 0
	}
	name := ""
	if m.cfg.Rotation.Enabled {
		var err error
		name, err = m.selectStickyLocked(false)
		if err != nil {
			return Selected{}, err
		}
	} else if key, ok := m.keyLocked(m.cfg.ActiveKey); ok && key.Enabled && !m.cooldownLocked(key.Name) {
		name = key.Name
	} else {
		name = m.selectFirstHealthyLocked()
	}
	// A cooled-down account is only retried when every enabled account is
	// unavailable. This keeps traffic moving without permanently deadlocking.
	if name == "" {
		if m.cfg.Rotation.Enabled {
			var err error
			name, err = m.selectStickyLocked(true)
			if err != nil {
				return Selected{}, err
			}
		} else {
			name = m.selectFirstEnabledLocked()
		}
	}
	if name == "" {
		return Selected{}, errors.New("no active Kimi key configured")
	}
	key, ok := m.keyLocked(name)
	if !ok {
		return Selected{}, fmt.Errorf("configured key %q does not exist", name)
	}
	stats := m.state.Keys[name]
	stats.InFlight++
	stats.ReservedTokens += estimate
	stats.LastUsedAt = time.Now().UTC()
	m.state.Keys[name] = stats
	_ = m.persistStateLocked()
	return Selected{Key: key, Estimate: estimate, Attempt: attempt}, nil
}

func (m *Manager) Finish(selected Selected, status int, usage Usage, requestError error) {
	m.finish(selected, status, usage, requestError, true, "")
}

// FinishTransient records a failed upstream attempt without making the
// account unavailable. It is used for provider-wide transient conditions,
// such as an overloaded engine, that do not indicate an unhealthy account.
func (m *Manager) FinishTransient(selected Selected, status int, usage Usage, statusName, reason string) {
	if prefix := fmt.Sprintf("HTTP %d: ", status); strings.HasPrefix(reason, prefix) {
		reason = strings.TrimPrefix(reason, prefix)
	}
	m.finish(selected, status, usage, errors.New(reason), false, statusName)
}

func (m *Manager) finish(selected Selected, status int, usage Usage, requestError error, cooldown bool, statusName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stats := m.state.Keys[selected.Key.Name]
	date := time.Now().Local().Format("2006-01-02")
	if m.state.Daily == nil {
		m.state.Daily = map[string]map[string]DailyUsage{}
	}
	if m.state.Daily[date] == nil {
		m.state.Daily[date] = map[string]DailyUsage{}
	}
	daily := m.state.Daily[date][selected.Key.Name]
	releaseSelection(&stats, selected.Estimate)
	stats.Requests++
	daily.Requests++
	if requestError != nil || status >= 400 {
		stats.Failures++
		daily.Failures++
		stats.LastError = formatError(status, requestError)
		stats.LastCheckAt = time.Now().UTC()
		if statusName == "" {
			statusName = basicFailureStatus(status, requestError)
		}
		stats.LastCheckStatus = statusName
		if cooldown {
			stats.CooldownUntil = time.Now().UTC().Add(time.Duration(m.cfg.Rotation.CooldownSeconds) * time.Second)
		} else {
			stats.CooldownUntil = time.Time{}
		}
	} else {
		stats.Successes++
		daily.Successes++
		stats.LastError = ""
		stats.LastCheckAt = time.Now().UTC()
		stats.LastCheckStatus = "ok"
		stats.CooldownUntil = time.Time{}
	}
	if usage.Known {
		stats.InputTokens += usage.InputTokens
		stats.OutputTokens += usage.OutputTokens
		stats.CacheReadTokens += usage.CacheReadTokens
		stats.ReasoningTokens += usage.ReasoningTokens
		daily.InputTokens += usage.InputTokens
		daily.OutputTokens += usage.OutputTokens
		daily.CacheReadTokens += usage.CacheReadTokens
		daily.ReasoningTokens += usage.ReasoningTokens
		if usage.TotalTokens > 0 {
			stats.TotalTokens += usage.TotalTokens
			daily.TotalTokens += usage.TotalTokens
		} else {
			stats.TotalTokens += usage.InputTokens + usage.OutputTokens
			daily.TotalTokens += usage.InputTokens + usage.OutputTokens
		}
	} else if requestError == nil && status < 400 {
		stats.EstimatedTokens += selected.Estimate
		daily.EstimatedTokens += selected.Estimate
	}
	m.state.Keys[selected.Key.Name] = stats
	m.state.Daily[date][selected.Key.Name] = daily
	_ = m.persistStateLocked()
}

// Cancel releases request reservations without treating a caller-initiated
// interruption as evidence that the account or upstream failed.
func (m *Manager) Cancel(selected Selected) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stats := m.state.Keys[selected.Key.Name]
	releaseSelection(&stats, selected.Estimate)
	m.state.Keys[selected.Key.Name] = stats
	_ = m.persistStateLocked()
}

func releaseSelection(stats *Stats, estimate int64) {
	if stats.InFlight > 0 {
		stats.InFlight--
	}
	if stats.ReservedTokens >= estimate {
		stats.ReservedTokens -= estimate
	} else {
		stats.ReservedTokens = 0
	}
}

func (m *Manager) MarkCooldown(name string, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stats := m.state.Keys[name]
	stats.CooldownUntil = time.Now().UTC().Add(time.Duration(m.cfg.Rotation.CooldownSeconds) * time.Second)
	stats.LastError = reason
	m.state.Keys[name] = stats
	_ = m.persistStateLocked()
}

func (m *Manager) RecordConnectivity(name, status string, latency time.Duration, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.hasKeyLocked(name) {
		return fmt.Errorf("unknown key %q", name)
	}
	stats := m.state.Keys[name]
	stats.LastCheckAt = time.Now().UTC()
	stats.LastCheckMS = latency.Milliseconds()
	if stats.LastCheckMS < 0 {
		stats.LastCheckMS = 0
	}
	if status == "ok" {
		stats.LastCheckStatus = "ok"
		stats.LastError = ""
		stats.CooldownUntil = time.Time{}
	} else if status == "upstream_overloaded" {
		// Provider-wide overload is transient and must not cool down an account.
		stats.LastCheckStatus = status
		stats.LastError = strings.TrimSpace(reason)
		stats.CooldownUntil = time.Time{}
	} else {
		stats.LastCheckStatus = status
		stats.LastError = strings.TrimSpace(reason)
		stats.CooldownUntil = time.Now().UTC().Add(time.Duration(m.cfg.Rotation.CooldownSeconds) * time.Second)
	}
	m.state.Keys[name] = stats
	return m.persistStateLocked()
}

func (m *Manager) SetAccountStatus(name, status, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.hasKeyLocked(name) {
		return
	}
	stats := m.state.Keys[name]
	stats.LastCheckAt = time.Now().UTC()
	stats.LastCheckStatus = status
	stats.LastError = strings.TrimSpace(reason)
	if status == "ok" {
		stats.CooldownUntil = time.Time{}
	} else {
		stats.CooldownUntil = time.Now().UTC().Add(time.Duration(m.cfg.Rotation.CooldownSeconds) * time.Second)
	}
	m.state.Keys[name] = stats
	_ = m.persistStateLocked()
}

func (m *Manager) CanRetry(status int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if status == 429 {
		return m.cfg.Rotation.RetryRateLimited
	}
	return status == 401 || status == 402 || status == 403 || status >= 500
}

func (m *Manager) AvailableKeyCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, k := range m.cfg.Keys {
		if k.Enabled && !m.cooldownLocked(k.Name) {
			n++
		}
	}
	return n
}

func (m *Manager) Snapshot() (config.Config, State) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg, cloneState(m.state)
}

func (m *Manager) selectStickyLocked(allowCooldown bool) (string, error) {
	if key, ok := m.keyLocked(m.cfg.ActiveKey); ok && key.Enabled && (allowCooldown || !m.cooldownLocked(key.Name)) {
		return key.Name, nil
	}
	for _, key := range m.cfg.Keys {
		if !key.Enabled || (!allowCooldown && m.cooldownLocked(key.Name)) {
			continue
		}
		if key.Name != m.cfg.ActiveKey {
			originalCfg := m.cfg
			originalState := cloneState(m.state)
			m.preferKeyLocked(key.Name)
			if err := config.Save(m.configPath, m.cfg); err != nil {
				m.cfg = originalCfg
				m.state = originalState
				return "", err
			}
		}
		return key.Name, nil
	}
	return "", nil
}

func (m *Manager) selectFirstHealthyLocked() string {
	for _, key := range m.cfg.Keys {
		if key.Enabled && !m.cooldownLocked(key.Name) {
			return key.Name
		}
	}
	return ""
}

func (m *Manager) selectFirstEnabledLocked() string {
	for _, key := range m.cfg.Keys {
		if key.Enabled {
			return key.Name
		}
	}
	return ""
}

func (m *Manager) preferKeyLocked(name string) {
	m.cfg.ActiveKey = name
	stats := m.state.Keys[name]
	stats.CooldownUntil = time.Time{}
	stats.LastError = ""
	stats.LastCheckStatus = ""
	m.state.Keys[name] = stats
}

func (m *Manager) cooldownLocked(name string) bool {
	until := m.state.Keys[name].CooldownUntil
	return !until.IsZero() && time.Now().UTC().Before(until)
}

func (m *Manager) hasKeyLocked(name string) bool {
	_, ok := m.keyLocked(name)
	return ok
}

func (m *Manager) keyLocked(name string) (config.KeyConfig, bool) {
	index := m.keyIndexLocked(name)
	if index >= 0 {
		return m.cfg.Keys[index], true
	}
	return config.KeyConfig{}, false
}

func (m *Manager) keyIndexLocked(name string) int {
	for _, k := range m.cfg.Keys {
		if k.Name == name {
			break
		}
	}
	for i := range m.cfg.Keys {
		if m.cfg.Keys[i].Name == name {
			return i
		}
	}
	return -1
}

func (m *Manager) persistLocked() error {
	if err := config.Save(m.configPath, m.cfg); err != nil {
		return err
	}
	return m.persistStateLocked()
}

func (m *Manager) persistStateLocked() error {
	if err := os.MkdirAll(filepath.Dir(m.statePath), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(m.statePath), ".kimi-switch-state-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, m.statePath)
}

func cloneState(s State) State {
	copy := State{Keys: map[string]Stats{}, Daily: map[string]map[string]DailyUsage{}}
	for k, v := range s.Keys {
		copy.Keys[k] = v
	}
	for date, entries := range s.Daily {
		copy.Daily[date] = map[string]DailyUsage{}
		for key, value := range entries {
			copy.Daily[date][key] = value
		}
	}
	return copy
}

func formatError(status int, err error) string {
	parts := []string{}
	if status > 0 {
		parts = append(parts, fmt.Sprintf("HTTP %d", status))
	}
	if err != nil {
		parts = append(parts, err.Error())
	}
	return strings.Join(parts, ": ")
}

func basicFailureStatus(status int, err error) string {
	if err != nil {
		return "network_error"
	}
	switch {
	case status == 401:
		return "invalid_key"
	case status == 403:
		return "forbidden"
	case status == 429:
		return "rate_limited"
	case status >= 500:
		return "upstream_error"
	default:
		return "failed"
	}
}

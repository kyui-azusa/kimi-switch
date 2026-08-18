package rotation

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	RouteStatus     string    `json:"route_status"`
	NextProbeAt     time.Time `json:"next_probe_at,omitempty"`
	LastProbeAt     time.Time `json:"last_probe_at,omitempty"`
	LastProbeStatus string    `json:"last_probe_status,omitempty"`
	LastRecoveredAt time.Time `json:"last_recovered_at,omitempty"`
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
	mu            sync.Mutex
	cfg           config.Config
	state         State
	configPath    string
	statePath     string
	probeRecovery map[string]bool
}

const (
	RouteHealthy     = "healthy"
	RouteQuarantined = "quarantined"
	RouteProbing     = "probing"
)

var ErrProbeInProgress = errors.New("account recovery probe already in progress")

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
	stateChanged := false
	for _, k := range c.Keys {
		if _, ok := s.Keys[k.Name]; !ok {
			s.Keys[k.Name] = Stats{RouteStatus: RouteHealthy}
			stateChanged = true
			continue
		}
		stats := s.Keys[k.Name]
		if migrateStats(&stats, c.Rotation.CooldownSeconds) {
			stateChanged = true
		}
		s.Keys[k.Name] = stats
	}
	if strings.TrimSpace(configPath) == "" {
		return nil, errors.New("config path is required")
	}
	m := &Manager{cfg: c, state: s, configPath: configPath, statePath: statePath, probeRecovery: map[string]bool{}}
	if stateChanged {
		if err := m.persistStateLocked(); err != nil {
			return nil, err
		}
	}
	return m, nil
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
	if !m.hasKeyLocked(name) {
		return fmt.Errorf("unknown key %q", name)
	}
	m.cfg.ActiveKey = name
	return m.persistLocked()
}

func (m *Manager) SetRotation(enabled bool, mode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mode != "" && mode != "token" && mode != "request" {
		return fmt.Errorf("rotation mode must be token or request")
	}
	m.cfg.Rotation.Enabled = enabled
	if mode != "" {
		m.cfg.Rotation.Mode = mode
	}
	return m.persistLocked()
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
		m.state.Keys[key.Name] = Stats{RouteStatus: RouteHealthy}
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
			m.cfg.Keys[i].Enabled = enabled
			return m.persistLocked()
		}
	}
	return fmt.Errorf("unknown key %q", name)
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
	m.state.Keys[newName] = Stats{RouteStatus: RouteHealthy}
	return newName, m.persistLocked()
}

func (m *Manager) UpdateSettings(upstream string, rotationConfig config.RotationConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.TrimSpace(upstream) != "" {
		m.cfg.UpstreamBaseURL = strings.TrimSpace(upstream)
	}
	if rotationConfig.Mode != "token" && rotationConfig.Mode != "request" {
		return errors.New("rotation mode must be token or request")
	}
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
		name = m.selectBalancedLocked()
	} else if key, ok := m.keyLocked(m.cfg.ActiveKey); ok && key.Enabled && m.healthyLocked(key.Name) {
		name = key.Name
	} else {
		name = m.selectFirstHealthyLocked()
	}
	if name == "" {
		return Selected{}, errors.New("no healthy Kimi key available")
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
	if stats.InFlight > 0 {
		stats.InFlight--
	}
	if stats.ReservedTokens >= selected.Estimate {
		stats.ReservedTokens -= selected.Estimate
	} else {
		stats.ReservedTokens = 0
	}
	stats.Requests++
	daily.Requests++
	if requestError != nil || status >= 400 {
		stats.Failures++
		daily.Failures++
		stats.LastError = formatError(status, requestError)
		stats.LastCheckAt = time.Now().UTC()
		stats.LastCheckStatus = basicFailureStatus(status, requestError)
		m.quarantineLocked(&stats, false)
	} else {
		stats.Successes++
		daily.Successes++
		stats.LastError = ""
		stats.LastCheckAt = time.Now().UTC()
		stats.LastCheckStatus = "ok"
		stats.RouteStatus = RouteHealthy
		stats.CooldownUntil = time.Time{}
		stats.NextProbeAt = time.Time{}
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

func (m *Manager) MarkCooldown(name string, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stats := m.state.Keys[name]
	stats.LastError = reason
	m.quarantineLocked(&stats, false)
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
	wasRecovery := m.probeRecovery[name]
	delete(m.probeRecovery, name)
	stats.LastCheckAt = time.Now().UTC()
	stats.LastProbeAt = stats.LastCheckAt
	stats.LastProbeStatus = status
	stats.LastCheckMS = latency.Milliseconds()
	if stats.LastCheckMS < 0 {
		stats.LastCheckMS = 0
	}
	if status == "ok" {
		stats.LastCheckStatus = "ok"
		stats.LastError = ""
		stats.CooldownUntil = time.Time{}
		stats.NextProbeAt = time.Time{}
		stats.RouteStatus = RouteHealthy
		if wasRecovery {
			stats.LastRecoveredAt = stats.LastCheckAt
		}
	} else {
		stats.LastCheckStatus = status
		stats.LastError = strings.TrimSpace(reason)
		m.quarantineLocked(&stats, true)
	}
	m.state.Keys[name] = stats
	return m.persistStateLocked()
}

// BeginProbe atomically reserves an account for a connectivity probe. A
// recovery probe must be due; manual tests may probe any configured account.
func (m *Manager) BeginProbe(name string, recoveryOnly bool) (config.KeyConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key, ok := m.keyLocked(name)
	if !ok {
		return config.KeyConfig{}, fmt.Errorf("unknown key %q", name)
	}
	stats := m.state.Keys[name]
	if stats.RouteStatus == "" {
		migrateStats(&stats, m.cfg.Rotation.CooldownSeconds)
	}
	if stats.RouteStatus == RouteProbing {
		return config.KeyConfig{}, ErrProbeInProgress
	}
	if recoveryOnly && (stats.RouteStatus != RouteQuarantined || !probeDue(stats)) {
		return config.KeyConfig{}, errors.New("account recovery probe is not due")
	}
	wasQuarantined := stats.RouteStatus == RouteQuarantined
	previous := stats
	stats.RouteStatus = RouteProbing
	m.probeRecovery[name] = recoveryOnly || wasQuarantined
	m.state.Keys[name] = stats
	if err := m.persistStateLocked(); err != nil {
		m.state.Keys[name] = previous
		delete(m.probeRecovery, name)
		return config.KeyConfig{}, err
	}
	return key, nil
}

// BeginDueProbe reserves the next due quarantined account, if one exists.
func (m *Manager) BeginDueProbe() (config.KeyConfig, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := append([]config.KeyConfig(nil), m.cfg.Keys...)
	sort.SliceStable(keys, func(i, j int) bool { return keys[i].Name < keys[j].Name })
	for _, key := range keys {
		if !key.Enabled {
			continue
		}
		stats := m.state.Keys[key.Name]
		if stats.RouteStatus != RouteQuarantined || !probeDue(stats) {
			continue
		}
		previous := stats
		stats.RouteStatus = RouteProbing
		m.probeRecovery[key.Name] = true
		m.state.Keys[key.Name] = stats
		if err := m.persistStateLocked(); err != nil {
			m.state.Keys[key.Name] = previous
			delete(m.probeRecovery, key.Name)
			return config.KeyConfig{}, false
		}
		return key, true
	}
	return config.KeyConfig{}, false
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
		stats.NextProbeAt = time.Time{}
		stats.RouteStatus = RouteHealthy
	} else {
		m.quarantineLocked(&stats, false)
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
		if k.Enabled && m.healthyLocked(k.Name) {
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

func (m *Manager) selectBalancedLocked() string {
	best := ""
	bestScore := int64(0)
	keys := append([]config.KeyConfig(nil), m.cfg.Keys...)
	sort.SliceStable(keys, func(i, j int) bool { return keys[i].Name < keys[j].Name })
	for _, key := range keys {
		if !key.Enabled || !m.healthyLocked(key.Name) {
			continue
		}
		stats := m.state.Keys[key.Name]
		score := stats.InputTokens + stats.OutputTokens + stats.EstimatedTokens + stats.ReservedTokens
		if m.cfg.Rotation.Mode == "request" {
			score = stats.Requests + stats.InFlight
		}
		if best == "" || score < bestScore {
			best = key.Name
			bestScore = score
		}
	}
	return best
}

func (m *Manager) selectFirstHealthyLocked() string {
	for _, key := range m.cfg.Keys {
		if key.Enabled && m.healthyLocked(key.Name) {
			return key.Name
		}
	}
	return ""
}

func (m *Manager) healthyLocked(name string) bool {
	return m.state.Keys[name].RouteStatus == RouteHealthy
}

func (m *Manager) quarantineLocked(stats *Stats, fromProbe bool) {
	now := time.Now().UTC()
	delay := time.Duration(m.cfg.Rotation.CooldownSeconds) * time.Second
	stats.RouteStatus = RouteQuarantined
	stats.NextProbeAt = now.Add(delay)
	stats.CooldownUntil = stats.NextProbeAt
	if !fromProbe {
		stats.LastProbeAt = time.Time{}
		stats.LastProbeStatus = ""
	}
}

func probeDue(stats Stats) bool {
	return !stats.NextProbeAt.IsZero() && !time.Now().UTC().Before(stats.NextProbeAt)
}

func migrateStats(stats *Stats, cooldownSeconds int) bool {
	now := time.Now().UTC()
	changed := false
	if stats.RouteStatus == RouteProbing {
		stats.RouteStatus = RouteQuarantined
		changed = true
	}
	if stats.RouteStatus == RouteHealthy {
		if !stats.NextProbeAt.IsZero() || !stats.CooldownUntil.IsZero() {
			stats.NextProbeAt = time.Time{}
			stats.CooldownUntil = time.Time{}
			changed = true
		}
		return changed
	}
	if stats.RouteStatus == RouteQuarantined {
		if stats.NextProbeAt.IsZero() {
			stats.NextProbeAt = stats.CooldownUntil
			if stats.NextProbeAt.IsZero() {
				stats.NextProbeAt = now.Add(time.Duration(cooldownSeconds) * time.Second)
			}
			changed = true
		}
		if stats.CooldownUntil != stats.NextProbeAt {
			stats.CooldownUntil = stats.NextProbeAt
			changed = true
		}
		return changed
	}
	failed := stats.LastError != "" || (stats.LastCheckStatus != "" && stats.LastCheckStatus != "ok") || !stats.CooldownUntil.IsZero()
	if !failed {
		stats.RouteStatus = RouteHealthy
		return true
	}
	stats.RouteStatus = RouteQuarantined
	stats.NextProbeAt = stats.CooldownUntil
	if stats.NextProbeAt.IsZero() {
		stats.NextProbeAt = now.Add(time.Duration(cooldownSeconds) * time.Second)
	}
	stats.CooldownUntil = stats.NextProbeAt
	return true
}

func (m *Manager) hasKeyLocked(name string) bool {
	_, ok := m.keyLocked(name)
	return ok
}

func (m *Manager) keyLocked(name string) (config.KeyConfig, bool) {
	for _, k := range m.cfg.Keys {
		if k.Name == name {
			return k, true
		}
	}
	return config.KeyConfig{}, false
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

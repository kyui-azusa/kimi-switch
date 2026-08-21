package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DefaultListenAddr             = "127.0.0.1:15721"
	DefaultUpstreamBase           = "https://api.kimi.com/coding/v1"
	DefaultLocalAPIKey            = "local-kimi-switch"
	DefaultFailureCooldownSeconds = 600
	QuotaRefreshSourceManual      = "manual_reference"
)

// Config is intentionally separate from Kimi Code's config.toml. KimiSwitch
// owns routing state here; the optional takeover feature only edits marked
// provider routing in Kimi's config and can restore the original mapping.
type Config struct {
	ListenAddr      string         `json:"listen_addr"`
	UpstreamBaseURL string         `json:"upstream_base_url"`
	LocalAPIKey     string         `json:"local_api_key"`
	ActiveKey       string         `json:"active_key"`
	Rotation        RotationConfig `json:"rotation"`
	Keys            []KeyConfig    `json:"keys"`
	StatePath       string         `json:"state_path,omitempty"`
}

type RotationConfig struct {
	Enabled          bool   `json:"enabled"`
	Mode             string `json:"mode"` // token or request
	MaxAttempts      int    `json:"max_attempts"`
	CooldownSeconds  int    `json:"cooldown_seconds"`
	RetryRateLimited bool   `json:"retry_rate_limited"`
}

type KeyConfig struct {
	Name         string              `json:"name"`
	Account      string              `json:"account,omitempty"`
	Notes        string              `json:"notes,omitempty"`
	APIKey       string              `json:"api_key,omitempty"`
	APIKeyEnv    string              `json:"api_key_env,omitempty"`
	SecretRef    string              `json:"secret_ref,omitempty"`
	Enabled      bool                `json:"enabled"`
	CreatedAt    int64               `json:"created_at,omitempty"`
	QuotaRefresh *QuotaRefreshConfig `json:"quota_refresh,omitempty"`
}

type QuotaRefreshConfig struct {
	FiveHour *QuotaRefreshRecord `json:"five_hour,omitempty"`
	Weekly   *QuotaRefreshRecord `json:"weekly,omitempty"`
}

type QuotaRefreshRecord struct {
	AnchorAt      *time.Time `json:"anchor_at,omitempty"`
	Source        string     `json:"source,omitempty"`
	RecordedAt    *time.Time `json:"recorded_at,omitempty"`
	Authoritative bool       `json:"authoritative"`
}

func Default() Config {
	return Config{
		ListenAddr:      DefaultListenAddr,
		UpstreamBaseURL: DefaultUpstreamBase,
		LocalAPIKey:     DefaultLocalAPIKey,
		Rotation: RotationConfig{
			Enabled:          false,
			Mode:             "request",
			MaxAttempts:      2,
			CooldownSeconds:  DefaultFailureCooldownSeconds,
			RetryRateLimited: true,
		},
		Keys: []KeyConfig{},
	}
}

func (c *Config) Normalize() error {
	if strings.TrimSpace(c.ListenAddr) == "" {
		c.ListenAddr = DefaultListenAddr
	}
	if strings.TrimSpace(c.UpstreamBaseURL) == "" {
		c.UpstreamBaseURL = DefaultUpstreamBase
	}
	if c.Rotation.Mode == "" {
		c.Rotation.Mode = "request"
	}
	if c.Rotation.Mode != "token" && c.Rotation.Mode != "request" {
		return fmt.Errorf("rotation.mode must be token or request, got %q", c.Rotation.Mode)
	}
	if c.Rotation.MaxAttempts < 1 {
		c.Rotation.MaxAttempts = 2
	}
	if c.Rotation.CooldownSeconds < DefaultFailureCooldownSeconds {
		c.Rotation.CooldownSeconds = DefaultFailureCooldownSeconds
	}
	if c.LocalAPIKey == "" {
		c.LocalAPIKey = DefaultLocalAPIKey
	}
	seen := map[string]bool{}
	for i := range c.Keys {
		c.Keys[i].Name = strings.TrimSpace(c.Keys[i].Name)
		if c.Keys[i].Name == "" {
			return errors.New("key name cannot be empty")
		}
		if seen[c.Keys[i].Name] {
			return fmt.Errorf("duplicate key name %q", c.Keys[i].Name)
		}
		seen[c.Keys[i].Name] = true
		if c.Keys[i].CreatedAt == 0 {
			c.Keys[i].CreatedAt = time.Now().UnixMilli()
		}
		c.Keys[i].QuotaRefresh = NormalizeQuotaRefresh(c.Keys[i].QuotaRefresh)
	}
	if c.ActiveKey != "" && !seen[c.ActiveKey] {
		return fmt.Errorf("active_key %q does not exist", c.ActiveKey)
	}
	if c.ActiveKey == "" && len(c.Keys) > 0 {
		c.ActiveKey = c.Keys[0].Name
	}
	return nil
}

func CloneQuotaRefresh(value *QuotaRefreshConfig) *QuotaRefreshConfig {
	if value == nil {
		return nil
	}
	clone := &QuotaRefreshConfig{
		FiveHour: cloneQuotaRefreshRecord(value.FiveHour),
		Weekly:   cloneQuotaRefreshRecord(value.Weekly),
	}
	if clone.FiveHour == nil && clone.Weekly == nil {
		return nil
	}
	return clone
}

func NormalizeQuotaRefresh(value *QuotaRefreshConfig) *QuotaRefreshConfig {
	clone := CloneQuotaRefresh(value)
	if clone == nil {
		return nil
	}
	clone.FiveHour = normalizeQuotaRefreshRecord(clone.FiveHour)
	clone.Weekly = normalizeQuotaRefreshRecord(clone.Weekly)
	if clone.FiveHour == nil && clone.Weekly == nil {
		return nil
	}
	return clone
}

func MergeImportedKey(existing KeyConfig, incoming KeyConfig) KeyConfig {
	if incoming.CreatedAt == 0 {
		incoming.CreatedAt = existing.CreatedAt
	}
	if incoming.QuotaRefresh == nil {
		incoming.QuotaRefresh = CloneQuotaRefresh(existing.QuotaRefresh)
	}
	incoming.Account = existing.Account
	incoming.Notes = existing.Notes
	if !existing.Enabled {
		incoming.Enabled = false
	}
	return incoming
}

func NextRefreshAt(anchor time.Time, period time.Duration, now time.Time) time.Time {
	if period <= 0 {
		return time.Time{}
	}
	anchor = anchor.UTC()
	now = now.UTC()
	if now.Before(anchor) {
		return anchor
	}
	elapsed := now.Sub(anchor)
	cycles := elapsed/period + 1
	return anchor.Add(cycles * period)
}

func cloneQuotaRefreshRecord(value *QuotaRefreshRecord) *QuotaRefreshRecord {
	if value == nil {
		return nil
	}
	clone := &QuotaRefreshRecord{
		Source:        value.Source,
		Authoritative: value.Authoritative,
	}
	if value.AnchorAt != nil {
		anchor := value.AnchorAt.UTC()
		clone.AnchorAt = &anchor
	}
	if value.RecordedAt != nil {
		recorded := value.RecordedAt.UTC()
		clone.RecordedAt = &recorded
	}
	return clone
}

func normalizeQuotaRefreshRecord(value *QuotaRefreshRecord) *QuotaRefreshRecord {
	if value == nil || value.AnchorAt == nil || value.AnchorAt.IsZero() {
		return nil
	}
	clone := cloneQuotaRefreshRecord(value)
	if clone.RecordedAt == nil || clone.RecordedAt.IsZero() {
		now := time.Now().UTC()
		clone.RecordedAt = &now
	}
	return clone
}

func Path() string {
	if explicit := os.Getenv("KIMI_SWITCH_CONFIG"); explicit != "" {
		return explicit
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join(".", ".kimi-switch", "config.json")
	}
	return filepath.Join(base, "kimi-switch", "config.json")
}

func StatePath(c Config) string {
	if c.StatePath != "" {
		return c.StatePath
	}
	return filepath.Join(filepath.Dir(Path()), "state.json")
}

func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := c.Normalize(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func Save(path string, c Config) error {
	if err := c.Normalize(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".kimi-switch-config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
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
	return os.Rename(tmpName, path)
}

func Ensure(path string) (Config, error) {
	if _, err := os.Stat(path); err == nil {
		return Load(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, err
	}
	c := Default()
	if err := Save(path, c); err != nil {
		return Config{}, err
	}
	return c, nil
}

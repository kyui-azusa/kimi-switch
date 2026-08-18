package kimitakeover

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	providerName = "kimi-switch-local"
	markerBegin  = "# >>> KimiSwitch managed provider"
	markerEnd    = "# <<< KimiSwitch managed provider"
)

var (
	defaultModelPattern = regexp.MustCompile(`(?m)^\s*default_model\s*=\s*"([^"]+)"\s*$`)
	sectionPattern      = regexp.MustCompile(`^\s*\[([^]]+)]\s*$`)
	providerPattern     = regexp.MustCompile(`^(\s*provider\s*=\s*)"([^"]+)"(\s*(?:#.*)?)$`)
	originalPattern     = regexp.MustCompile(`^# original_provider = "([^"]+)"$`)
)

type Status struct {
	Enabled          bool   `json:"enabled"`
	ConfigPath       string `json:"config_path"`
	OriginalProvider string `json:"original_provider,omitempty"`
	AffectedModels   int    `json:"affected_models"`
	ReloadRequired   bool   `json:"reload_required"`
	ConfigError      string `json:"config_error,omitempty"`
	Error            string `json:"error,omitempty"`
}

func DefaultPath() string {
	if home := strings.TrimSpace(os.Getenv("KIMI_CODE_HOME")); home != "" {
		return filepath.Join(home, "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".kimi-code", "config.toml")
	}
	return filepath.Join(home, ".kimi-code", "config.toml")
}

func Inspect(path string) (Status, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Status{ConfigPath: path}, err
	}
	text := string(content)
	original := markerOriginalProvider(text)
	enabled := strings.Contains(text, markerBegin) && strings.Contains(text, markerEnd) && original != ""
	status := Status{
		Enabled:          enabled,
		ConfigPath:       path,
		OriginalProvider: original,
		AffectedModels:   countModelProvider(text, providerName),
		ReloadRequired:   false,
	}
	if err := validate(text); err != nil {
		status.ConfigError = err.Error()
	}
	return status, nil
}

func Set(path, listenAddr, localAPIKey string, enabled bool) (Status, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Status{ConfigPath: path}, err
	}
	text := string(content)
	current, err := Inspect(path)
	if err != nil {
		return current, err
	}
	if current.Enabled == enabled && !needsRepair(text, enabled) {
		return current, nil
	}

	// A pristine copy of the config predates any KimiSwitch edit, so it still knows
	// which upstream provider the models belong to even when the live file no longer does.
	fallback := backupProvider(path)

	var updated string
	if enabled {
		updated, err = enable(text, listenAddr, localAPIKey, fallback)
	} else {
		updated, err = disable(text, fallback)
	}
	if err != nil {
		return current, err
	}
	if err := validate(updated); err != nil {
		return current, fmt.Errorf("refusing to write an invalid Kimi config: %w", err)
	}
	if err := ensureBackup(path, content); err != nil {
		return current, err
	}
	if err := atomicWrite(path, []byte(updated)); err != nil {
		return current, err
	}
	result, err := Inspect(path)
	result.ReloadRequired = true
	return result, err
}

// enable rebuilds the managed block from scratch, so it also repairs a config that a
// previous run left half-written.
func enable(text, listenAddr, localAPIKey, fallbackOriginal string) (string, error) {
	match := defaultModelPattern.FindStringSubmatch(text)
	if len(match) != 2 {
		return "", errors.New("Kimi config has no default_model")
	}

	original := markerOriginalProvider(text)
	if strings.Contains(text, markerBegin) || strings.Contains(text, markerEnd) {
		stripped, err := removeManagedBlock(text)
		if err != nil {
			return "", err
		}
		text = stripped
	}
	// Any provider table outside the managed block would collide with the one we append
	// and make the whole file unparseable.
	text = stripStrayProvider(text)

	if original == "" {
		original = providerForModel(text, match[1])
	}
	if original == "" || original == providerName {
		original = fallbackOriginal
	}
	if original == "" || original == providerName {
		return "", fmt.Errorf("cannot find the upstream provider for default model %q", match[1])
	}

	rewritten, _ := rewriteModelProvider(text, original, providerName)
	if countModelProvider(rewritten, providerName) == 0 {
		return "", fmt.Errorf("no model uses provider %q", original)
	}
	baseURL := "http://" + strings.TrimSpace(listenAddr) + "/v1"
	block := strings.Join([]string{
		markerBegin,
		"# original_provider = " + strconv.Quote(original),
		"[providers." + strconv.Quote(providerName) + "]",
		`type = "kimi"`,
		"api_key = " + strconv.Quote(localAPIKey),
		"base_url = " + strconv.Quote(baseURL),
		markerEnd,
	}, "\n")
	return strings.TrimRight(rewritten, "\n") + "\n\n" + block + "\n", nil
}

func disable(text, fallbackOriginal string) (string, error) {
	original := markerOriginalProvider(text)
	if original == "" {
		original = fallbackOriginal
	}
	if original == "" {
		return "", errors.New("KimiSwitch managed block does not contain original provider")
	}
	withoutBlock := text
	if strings.Contains(text, markerBegin) || strings.Contains(text, markerEnd) {
		stripped, err := removeManagedBlock(text)
		if err != nil {
			return "", err
		}
		withoutBlock = stripped
	}
	withoutBlock = stripStrayProvider(withoutBlock)

	rewritten, affected := rewriteModelProvider(withoutBlock, providerName, original)
	if affected == 0 && !strings.Contains(text, markerBegin) {
		return "", errors.New("no Kimi model references the managed KimiSwitch provider")
	}
	return strings.TrimRight(rewritten, "\n") + "\n", nil
}

// needsRepair reports whether the config is dirty for the requested mode even though
// Inspect already agrees on the enabled flag.
func needsRepair(text string, enabled bool) bool {
	if hasStrayProvider(text) {
		return true
	}
	return !enabled && countModelProvider(text, providerName) > 0
}

func providerForModel(text, model string) string {
	section := ""
	for _, line := range strings.Split(text, "\n") {
		if match := sectionPattern.FindStringSubmatch(line); len(match) == 2 {
			section = match[1]
			continue
		}
		if modelName(section) != model {
			continue
		}
		if match := providerPattern.FindStringSubmatch(line); len(match) >= 3 {
			return match[2]
		}
	}
	return ""
}

func rewriteModelProvider(text, from, to string) (string, int) {
	section := ""
	affected := 0
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		if match := sectionPattern.FindStringSubmatch(line); len(match) == 2 {
			section = match[1]
			continue
		}
		if modelName(section) == "" {
			continue
		}
		match := providerPattern.FindStringSubmatch(line)
		if len(match) >= 4 && match[2] == from {
			lines[index] = match[1] + strconv.Quote(to) + match[3]
			affected++
		}
	}
	return strings.Join(lines, "\n"), affected
}

func countModelProvider(text, provider string) int {
	_, count := rewriteModelProvider(text, provider, provider)
	return count
}

// keyPath splits a TOML table header into its unquoted segments.
func keyPath(raw string) []string {
	var parts []string
	var current strings.Builder
	quoted := false
	escaped := false
	for _, r := range raw {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case quoted && r == '\\':
			escaped = true
		case r == '"':
			quoted = !quoted
		case r == '.' && !quoted:
			parts = append(parts, strings.TrimSpace(current.String()))
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	return append(parts, strings.TrimSpace(current.String()))
}

// canonicalSection normalises a header so that [providers.x] and [providers."x"] compare equal.
func canonicalSection(raw string) string {
	return strings.Join(keyPath(raw), "\x00")
}

func sectionChild(section, parent string) string {
	parts := keyPath(section)
	if len(parts) != 2 || parts[0] != parent {
		return ""
	}
	return parts[1]
}

func modelName(section string) string {
	return sectionChild(section, "models")
}

// strayProviderLines returns the line indexes of every managed-provider table that sits
// outside the managed block.
func strayProviderLines(lines []string) []int {
	var stray []int
	inBlock := false
	for index := 0; index < len(lines); index++ {
		switch strings.TrimSpace(lines[index]) {
		case markerBegin:
			inBlock = true
			continue
		case markerEnd:
			inBlock = false
			continue
		}
		match := sectionPattern.FindStringSubmatch(lines[index])
		if len(match) != 2 || inBlock {
			continue
		}
		if sectionChild(match[1], "providers") == providerName {
			stray = append(stray, index)
		}
	}
	return stray
}

func hasStrayProvider(text string) bool {
	return len(strayProviderLines(strings.Split(text, "\n"))) > 0
}

func stripStrayProvider(text string) string {
	lines := strings.Split(text, "\n")
	stray := strayProviderLines(lines)
	if len(stray) == 0 {
		return text
	}
	drop := make(map[int]bool, len(stray))
	for _, start := range stray {
		for index := start; index < len(lines); index++ {
			trimmed := strings.TrimSpace(lines[index])
			if index > start && (sectionPattern.MatchString(lines[index]) || trimmed == markerBegin) {
				break
			}
			drop[index] = true
		}
	}
	kept := make([]string, 0, len(lines))
	for index, line := range lines {
		if drop[index] {
			continue
		}
		if strings.TrimSpace(line) == "" && len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// validate catches the config shapes that make Kimi Code reject every model: a table
// declared twice, or models pointing at a provider table that is not there.
func validate(text string) error {
	seen := map[string]string{}
	referenced := false
	declared := false
	section := ""
	for _, line := range strings.Split(text, "\n") {
		if match := sectionPattern.FindStringSubmatch(line); len(match) == 2 {
			section = match[1]
			key := canonicalSection(section)
			if previous, ok := seen[key]; ok {
				return fmt.Errorf("table [%s] is declared twice (also as [%s])", section, previous)
			}
			seen[key] = section
			if sectionChild(section, "providers") == providerName {
				declared = true
			}
			continue
		}
		if modelName(section) == "" {
			continue
		}
		if match := providerPattern.FindStringSubmatch(line); len(match) >= 3 && match[2] == providerName {
			referenced = true
		}
	}
	if referenced && !declared {
		return fmt.Errorf("models reference provider %q but no [providers.%s] table exists", providerName, providerName)
	}
	return nil
}

func markerOriginalProvider(text string) string {
	inBlock := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == markerBegin {
			inBlock = true
			continue
		}
		if trimmed == markerEnd {
			return ""
		}
		if inBlock {
			if match := originalPattern.FindStringSubmatch(trimmed); len(match) == 2 {
				return match[1]
			}
		}
	}
	return ""
}

func removeManagedBlock(text string) (string, error) {
	start := strings.Index(text, markerBegin)
	end := strings.Index(text, markerEnd)
	if start < 0 || end < start {
		return "", errors.New("KimiSwitch managed block is incomplete")
	}
	end += len(markerEnd)
	for end < len(text) && (text[end] == '\r' || text[end] == '\n') {
		end++
	}
	return strings.TrimRight(text[:start], "\n") + "\n" + text[end:], nil
}

func backupPath(path string) string {
	return path + ".kimiswitch.bak"
}

// backupProvider recovers the upstream provider from the pristine backup taken before
// the first takeover.
func backupProvider(path string) string {
	content, err := os.ReadFile(backupPath(path))
	if err != nil {
		return ""
	}
	text := string(content)
	match := defaultModelPattern.FindStringSubmatch(text)
	if len(match) != 2 {
		return ""
	}
	provider := providerForModel(text, match[1])
	if provider == providerName {
		return ""
	}
	return provider
}

func ensureBackup(path string, content []byte) error {
	backup := backupPath(path)
	if _, err := os.Stat(backup); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return atomicWrite(backup, content)
}

func atomicWrite(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".kimiswitch-*.tmp")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

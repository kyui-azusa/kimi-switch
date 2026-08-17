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
	return Status{
		Enabled:          enabled,
		ConfigPath:       path,
		OriginalProvider: original,
		AffectedModels:   countModelProvider(text, providerName),
		ReloadRequired:   false,
	}, nil
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
	if current.Enabled == enabled {
		return current, nil
	}

	var updated string
	if enabled {
		updated, err = enable(text, listenAddr, localAPIKey)
	} else {
		updated, err = disable(text)
	}
	if err != nil {
		return current, err
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

func enable(text, listenAddr, localAPIKey string) (string, error) {
	if strings.Contains(text, markerBegin) || strings.Contains(text, markerEnd) {
		return "", errors.New("incomplete KimiSwitch managed block in Kimi config")
	}
	match := defaultModelPattern.FindStringSubmatch(text)
	if len(match) != 2 {
		return "", errors.New("Kimi config has no default_model")
	}
	originalProvider := providerForModel(text, match[1])
	if originalProvider == "" {
		return "", fmt.Errorf("cannot find provider for default model %q", match[1])
	}
	if originalProvider == providerName {
		return "", errors.New("Kimi config already references the KimiSwitch provider without a managed marker")
	}

	rewritten, affected := rewriteModelProvider(text, originalProvider, providerName)
	if affected == 0 {
		return "", fmt.Errorf("no model uses provider %q", originalProvider)
	}
	baseURL := "http://" + strings.TrimSpace(listenAddr) + "/v1"
	block := strings.Join([]string{
		markerBegin,
		"# original_provider = " + strconv.Quote(originalProvider),
		"[providers." + strconv.Quote(providerName) + "]",
		`type = "kimi"`,
		"api_key = " + strconv.Quote(localAPIKey),
		"base_url = " + strconv.Quote(baseURL),
		markerEnd,
	}, "\n")
	return strings.TrimRight(rewritten, "\n") + "\n\n" + block + "\n", nil
}

func disable(text string) (string, error) {
	original := markerOriginalProvider(text)
	if original == "" {
		return "", errors.New("KimiSwitch managed block does not contain original provider")
	}
	withoutBlock, err := removeManagedBlock(text)
	if err != nil {
		return "", err
	}
	rewritten, affected := rewriteModelProvider(withoutBlock, providerName, original)
	if affected == 0 {
		return "", errors.New("no Kimi model references the managed KimiSwitch provider")
	}
	return strings.TrimRight(rewritten, "\n") + "\n", nil
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

func modelName(section string) string {
	if !strings.HasPrefix(section, "models.") {
		return ""
	}
	raw := strings.TrimPrefix(section, "models.")
	if unquoted, err := strconv.Unquote(raw); err == nil {
		return unquoted
	}
	return raw
}

func markerOriginalProvider(text string) string {
	inBlock := false
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == markerBegin {
			inBlock = true
			continue
		}
		if strings.TrimSpace(line) == markerEnd {
			return ""
		}
		if inBlock {
			if match := originalPattern.FindStringSubmatch(strings.TrimSpace(line)); len(match) == 2 {
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

func ensureBackup(path string, content []byte) error {
	backup := path + ".kimiswitch.bak"
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

package kimiimport

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("~", ".kimi-code", "config.toml")
	}
	return filepath.Join(home, ".kimi-code", "config.toml")
}

// Parse extracts only API-key providers. OAuth state stays owned by Kimi Code.
func Parse(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Kimi config %s: %w", path, err)
	}
	providers := map[string]string{}
	sectionProvider := ""
	sectionEnv := false
	for _, rawLine := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(stripComment(rawLine))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section := strings.TrimSpace(line[1 : len(line)-1])
			sectionProvider, sectionEnv = providerSection(section)
			continue
		}
		if sectionProvider == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		if (sectionEnv && name != "KIMI_API_KEY") || (!sectionEnv && name != "api_key") {
			continue
		}
		value, err := parseString(strings.TrimSpace(parts[1]))
		if err != nil {
			return nil, fmt.Errorf("parse provider %q: %w", sectionProvider, err)
		}
		if value != "" {
			providers[sectionProvider] = value
		}
	}
	return providers, nil
}

func providerSection(section string) (string, bool) {
	const prefix = "providers."
	if !strings.HasPrefix(section, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(section, prefix)
	name, suffix, err := splitProviderName(rest)
	if err != nil || name == "" {
		return "", false
	}
	return name, suffix == ".env"
}

func splitProviderName(value string) (string, string, error) {
	if strings.HasPrefix(value, "\"") {
		escaped := false
		for i := 1; i < len(value); i++ {
			switch {
			case value[i] == '\\' && !escaped:
				escaped = true
			case value[i] == '"' && !escaped:
				name, err := strconv.Unquote(value[:i+1])
				return name, value[i+1:], err
			default:
				escaped = false
			}
		}
		return "", "", errors.New("unterminated quoted provider name")
	}
	if strings.HasPrefix(value, "'") {
		if end := strings.Index(value[1:], "'"); end >= 0 {
			end++
			return value[1:end], value[end+1:], nil
		}
		return "", "", errors.New("unterminated literal provider name")
	}
	parts := strings.SplitN(value, ".", 2)
	suffix := ""
	if len(parts) == 2 {
		suffix = "." + parts[1]
	}
	return parts[0], suffix, nil
}

func parseString(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "\"") {
		end := strings.LastIndex(value, "\"")
		if end == 0 {
			return "", errors.New("unterminated quoted value")
		}
		return strconv.Unquote(value[:end+1])
	}
	if strings.HasPrefix(value, "'") {
		end := strings.LastIndex(value, "'")
		if end == 0 {
			return "", errors.New("unterminated literal value")
		}
		return value[1:end], nil
	}
	return strings.TrimSpace(strings.SplitN(value, "#", 2)[0]), nil
}

func stripComment(line string) string {
	quoted := false
	escaped := false
	for i, char := range line {
		switch {
		case char == '\\' && quoted && !escaped:
			escaped = true
		case char == '"' && !escaped:
			quoted = !quoted
		case char == '#' && !quoted:
			return line[:i]
		default:
			escaped = false
		}
	}
	return line
}

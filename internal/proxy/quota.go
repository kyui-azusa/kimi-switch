package proxy

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kyui-azusa/kimi-switch/internal/rotation"
)

// quotaObservation extracts only explicit provider reset metadata. Retry-After
// is retained as an observation and becomes an anchor only when a trusted cycle
// is already known for the account.
func quotaObservation(headers http.Header, body []byte, observedAt time.Time, quotaResponse bool) rotation.QuotaObservation {
	observation := rotation.QuotaObservation{ObservedAt: observedAt.UTC(), Source: rotation.QuotaSourceObserved, Confidence: "medium"}
	var cycle int64
	for _, name := range []string{"X-Quota-Cycle-Seconds", "X-RateLimit-Cycle-Seconds", "Quota-Cycle-Seconds"} {
		if value := parsePositiveInt(headers.Get(name)); value > 0 {
			cycle = value
			break
		}
	}
	for _, name := range []string{"X-Quota-Reset", "X-Quota-Reset-At", "X-RateLimit-Reset", "RateLimit-Reset"} {
		if value := parseResetTime(headers.Get(name), observedAt); !value.IsZero() {
			observation.AnchorAt = value
			observation.Source = rotation.QuotaSourceProvider
			observation.Confidence = "high"
			break
		}
	}
	if body != nil {
		bodyAnchor, bodyCycle := parseQuotaBody(body, observedAt)
		if !bodyAnchor.IsZero() {
			observation.AnchorAt = bodyAnchor
			observation.Source = rotation.QuotaSourceProvider
			observation.Confidence = "high"
		}
		if bodyCycle > 0 {
			cycle = bodyCycle
		}
	}
	if cycle == 0 && quotaResponse {
		if retry := parseRetryAfter(headers.Get("Retry-After"), observedAt); !retry.IsZero() {
			observation.AnchorAt = retry
			observation.Source = rotation.QuotaSourceObserved
			observation.Confidence = "medium"
		}
	}
	observation.CycleSeconds = cycle
	return observation
}

func parseQuotaBody(body []byte, observedAt time.Time) (time.Time, int64) {
	var value any
	if json.Unmarshal(body, &value) != nil {
		return time.Time{}, 0
	}
	return findQuotaMetadata(value, observedAt)
}

func findQuotaMetadata(value any, observedAt time.Time) (time.Time, int64) {
	switch current := value.(type) {
	case map[string]any:
		var anchor time.Time
		var cycle int64
		for key, raw := range current {
			switch strings.ToLower(key) {
			case "quota_cycle_anchor_at", "quota_reset_at", "quota_reset_time", "reset_at", "next_refresh_at":
				if parsed := parseAnyResetTime(raw, observedAt); !parsed.IsZero() {
					anchor = parsed
				}
			case "quota_cycle_seconds", "cycle_seconds", "quota_period_seconds":
				cycle = parseAnyInt(raw)
			}
		}
		for _, child := range current {
			childAnchor, childCycle := findQuotaMetadata(child, observedAt)
			if anchor.IsZero() {
				anchor = childAnchor
			}
			if cycle == 0 {
				cycle = childCycle
			}
		}
		return anchor, cycle
	case []any:
		for _, child := range current {
			if anchor, cycle := findQuotaMetadata(child, observedAt); !anchor.IsZero() || cycle > 0 {
				return anchor, cycle
			}
		}
	}
	return time.Time{}, 0
}

func parseResetTime(value string, observedAt time.Time) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC()
	}
	if number, err := strconv.ParseInt(value, 10, 64); err == nil {
		if number > 1_000_000_000_000 {
			return time.UnixMilli(number).UTC()
		}
		if number > 1_000_000_000 {
			return time.Unix(number, 0).UTC()
		}
		if number > 0 {
			return observedAt.UTC().Add(time.Duration(number) * time.Second)
		}
	}
	return time.Time{}
}

func parseRetryAfter(value string, observedAt time.Time) time.Time {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 {
		return observedAt.UTC().Add(time.Duration(seconds) * time.Second)
	}
	if parsed, err := http.ParseTime(value); err == nil {
		return parsed.UTC()
	}
	return time.Time{}
}

func parseAnyResetTime(value any, observedAt time.Time) time.Time {
	switch raw := value.(type) {
	case string:
		return parseResetTime(raw, observedAt)
	case float64:
		return parseResetTime(strconv.FormatInt(int64(raw), 10), observedAt)
	case json.Number:
		return parseResetTime(string(raw), observedAt)
	default:
		return time.Time{}
	}
}

func parseAnyInt(value any) int64 {
	switch raw := value.(type) {
	case float64:
		return int64(raw)
	case json.Number:
		value, _ := raw.Int64()
		return value
	case string:
		return parsePositiveInt(raw)
	default:
		return 0
	}
}

func parsePositiveInt(value string) int64 {
	number, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || number <= 0 {
		return 0
	}
	return number
}

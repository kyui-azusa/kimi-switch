package proxy

import (
	"strings"
	"testing"
)

func TestDashboardManualQuotaRefreshFieldsPresent(t *testing.T) {
	html := dashboardHTML("local")

	for _, needle := range []string{
		`id="fiveHourAnchorAt"`,
		`id="weeklyAnchorAt"`,
		`function toLocalDateTimeInput(iso)`,
		`function toIsoOrEmpty(localValue)`,
		`function buildQuotaRefreshPayload(original,currentForm)`,
		`if(next.anchor_at===next.initial)continue`,
		`$('fiveHourAnchorAt').dataset.initial=fiveHourAnchorAt`,
		`$('weeklyAnchorAt').dataset.initial=weeklyAnchorAt`,
		`quota_refresh:buildQuotaRefreshPayload(original,{five_hour:{anchor_at:$('fiveHourAnchorAt').value,initial:$('fiveHourAnchorAt').dataset.initial||''},weekly:{anchor_at:$('weeklyAnchorAt').value,initial:$('weeklyAnchorAt').dataset.initial||''}})`,
		`const fiveHourAnchorAt=toLocalDateTimeInput(`,
		`const weeklyAnchorAt=toLocalDateTimeInput(`,
		`$('fiveHourAnchorAt').value=fiveHourAnchorAt`,
		`$('weeklyAnchorAt').value=weeklyAnchorAt`,
	} {
		if !strings.Contains(html, needle) {
			t.Fatalf("dashboard HTML missing %q", needle)
		}
	}
}

package cli

import (
	"net/http"
	"strings"
	"testing"
)

// ── app and account ─────────────────────────────────────────────────────

func TestAppViewPrintsTheApplicationAndItsCost(t *testing.T) {
	h := newHarness(t).loggedIn()
	h.api.on("GET", "/v1/app", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Aether-Billing", "credits")
		w.Header().Set("X-Aether-Balance", "4200000")
		answer(w, sampleApp)
	})
	r := h.run("", "app", "view").wantCode(t, ExitOK)
	for _, want := range []string{"Reports", "$4.20", "credits — the application's balance pays"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, r.stdout)
		}
	}
	if !strings.Contains(r.stderr, "balance $4.2") {
		t.Errorf("stderr = %q", r.stderr)
	}

	r = h.run("", "app", "view", "-o", "json", "-q").wantCode(t, ExitOK)
	if decodeBody(t, []byte(r.stdout))["name"] != "Reports" || r.stderr != "" {
		t.Errorf("json = %q, stderr = %q", r.stdout, r.stderr)
	}
}

func TestAppUsageSendsTheWindow(t *testing.T) {
	h := newHarness(t).loggedIn()
	h.api.ok("GET", "/v1/usage", map[string]any{
		"from": "2026-09-18", "to": "2026-09-25", "calls": 3,
		"credits": map[string]any{"credits": 1500, "display": "$0.0015"},
		"ops":     []any{map[string]any{"op": "search:query", "calls": 3, "units": 3, "credits": map[string]any{"credits": 1500, "display": "$0.0015"}}},
	})
	r := h.run("", "app", "usage", "--days", "7", "--by-day").wantCode(t, ExitOK)
	if q := h.api.last().Query; q != "by_day=true&days=7" {
		t.Errorf("query = %q", q)
	}
	if !strings.Contains(r.stdout, "search:query") {
		t.Errorf("stdout = %q", r.stdout)
	}
	h.run("", "app", "usage", "--days", "400").wantCode(t, ExitUsage)
}

func TestNotLoggedIn(t *testing.T) {
	h := newHarness(t)
	r := h.run("", "app", "view").wantCode(t, ExitAuth)
	if !strings.Contains(r.stderr, "celadon auth login") {
		t.Errorf("stderr = %q", r.stderr)
	}
}

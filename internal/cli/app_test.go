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
		"days": []any{
			map[string]any{"day": "2026-09-25", "op": "search:query", "calls": 2, "units": 2, "credits": map[string]any{"credits": 1000, "display": "$0.001"}},
			map[string]any{"day": "2026-09-24", "op": "search:query", "calls": 1, "units": 1, "credits": map[string]any{"credits": 500, "display": "$0.0005"}},
		},
	})
	r := h.run("", "app", "usage", "--days", "7", "--by-day").wantCode(t, ExitOK)
	if q := h.api.last().Query; q != "by_day=true&days=7" {
		t.Errorf("query = %q", q)
	}
	if !strings.Contains(r.stdout, "search:query") || !strings.Contains(r.stdout, "OPERATION") {
		t.Errorf("stdout = %q", r.stdout)
	}
	for _, day := range []string{"2026-09-25  search:query  2", "2026-09-24  search:query  1"} {
		if !strings.Contains(r.stdout, day) {
			t.Errorf("stdout lacks the row %q:\n%s", day, r.stdout)
		}
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

func TestAppPricesListsEveryRateCeiling(t *testing.T) {
	h := newHarness(t).loggedIn()
	h.api.ok("GET", "/v1/prices", map[string]any{
		"prices": []any{
			map[string]any{"op": "search:query", "unit": "query", "price": map[string]any{"credits": 500, "display": "$0.0005"}, "charging": "always"},
			map[string]any{"op": "vault:entry", "unit": "entry", "price": map[string]any{"credits": 10000, "display": "$0.01"}, "charging": "overflow", "limit": "vault:entries"},
		},
		"credits_per_cent":  10000,
		"rates":             map[string]any{"credits": map[string]any{"per_second": 2.5, "burst": 5}},
		"account_rate":      map[string]any{"per_second": 10, "burst": 20},
		"quota_ttl_seconds": 60,
	})
	r := h.run("", "app", "prices").wantCode(t, ExitOK)
	for _, want := range []string{"every call", "past the plan", "credits 2.5/s (burst 5)", "10/s (burst 20), every application of an account", "every 1m"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, r.stdout)
		}
	}
	if strings.Contains(r.stdout, "Per deployment") {
		t.Errorf("a ceiling that is off was listed:\n%s", r.stdout)
	}
}

func TestAccountViewSaysWhenItWasMeasuredAndThatThePlanDoesNotLapse(t *testing.T) {
	h := newHarness(t).loggedIn()
	h.api.ok("GET", "/v1/account", map[string]any{
		"account_id": 42, "plan": map[string]any{"tier": "max", "name": "Max", "permissions": []string{"search", "vault"}},
		"measured_at": 1790000000,
		"quotas":      []any{map[string]any{"key": "search:daily", "unlimited": true, "used": 12, "limit": nil, "remaining": nil}},
	})
	r := h.run("", "account", "view").wantCode(t, ExitOK)
	for _, want := range []string{"does not lapse", "Measured", "(at most every 10 seconds)", "12 used · unlimited"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, r.stdout)
		}
	}
	h.api.ok("GET", "/v1/app", sampleApp)
	r = h.run("", "app", "view").wantCode(t, ExitOK)
	if !strings.Contains(r.stdout, "Last used:") || !strings.Contains(r.stdout, "never") {
		t.Errorf("a key never used reads:\n%s", r.stdout)
	}
}

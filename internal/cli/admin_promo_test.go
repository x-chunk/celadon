package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

var sampleCampaign = map[string]any{
	"id": 7, "kind": "event", "name": "Summer week", "state": "scheduled", "active": true,
	"starts_at": 1790812800, "ends_at": 1791417600,
	"benefits": map[string]any{"discounts": []any{map[string]any{"tier": "pro", "percent": 25}}},
}

var sampleCode = map[string]any{
	"id": 3, "code": "SUMMER25", "name": "Summer sale", "active": true, "max_redemptions": 100, "redemptions": 4,
	"duration": 2592000, "benefits": map[string]any{"discounts": []any{map[string]any{"tier": "", "percent": 25}}},
}

func TestAdminCampaigns(t *testing.T) {
	h := newHarness(t).adminLoggedIn()
	h.api.adminOK("GET", "/admin/promo/campaigns", []any{sampleCampaign})
	h.api.adminOK("GET", "/admin/promo/campaigns/7", sampleCampaign)
	h.api.adminOK("POST", "/admin/promo/campaigns", sampleCampaign)
	h.api.adminOK("PATCH", "/admin/promo/campaigns/7", sampleCampaign)
	h.api.adminOK("DELETE", "/admin/promo/campaigns/7", map[string]any{})

	r := h.run("", "admin", "campaigns", "list", "--kind", "event", "-a", "--limit", "10").wantCode(t, ExitOK)
	if q := h.api.last().Query; q != "inactive=true&kind=event&limit=10" {
		t.Errorf("query = %q", q)
	}
	if !strings.Contains(r.stdout, "Summer week") || !strings.Contains(r.stdout, "−25% pro") {
		t.Errorf("list = %q", r.stdout)
	}
	h.run("", "admin", "campaigns", "list", "--kind", "sale").wantCode(t, ExitUsage)

	r = h.run("", "admin", "campaigns", "view", "7").wantCode(t, ExitOK)
	if !strings.Contains(r.stdout, "Announced") || !strings.Contains(r.stdout, "not yet") {
		t.Errorf("view = %q", r.stdout)
	}

	h.run("", "admin", "campaigns", "create", "--kind", "event", "--name", "Summer week",
		"--starts", "2026-10-01T00:00:00Z", "--for", "7d",
		"--discount", "pro:25", "--bonus", "50:20", "--grant", "free:search:daily=200",
		"--announcement", "Tell a friend").wantCode(t, ExitOK)
	body := decodeBody(t, h.api.last().Body)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if body["kind"] != "event" || body["starts_at"] != float64(start.Unix()) ||
		body["ends_at"] != float64(start.Add(7*24*time.Hour).Unix()) || body["announcement"] != "Tell a friend" {
		t.Errorf("create body = %v", body)
	}
	if _, has := body["active"]; has {
		t.Error("create sent active without --inactive")
	}
	b := body["benefits"].(map[string]any)
	if len(b["discounts"].([]any)) != 1 || b["top_up_bonuses"].([]any)[0].(map[string]any)["amount_cents"] != float64(5000) ||
		b["limits"].([]any)[0].(map[string]any)["limit"] != "search:daily" {
		t.Errorf("benefits = %v", b)
	}

	calls := h.api.calls()
	h.run("", "admin", "campaigns", "create", "--name", "x").wantCode(t, ExitUsage)
	h.run("", "admin", "campaigns", "create", "--name", "x", "--discount", "pro").wantCode(t, ExitUsage)
	h.run("", "admin", "campaigns", "create", "--name", "x", "--discount", "pro:5", "--ends", "+1d", "--for", "2d").wantCode(t, ExitUsage)
	h.run("", "admin", "campaigns", "create", "--name", "x", "--discount", "pro:5", "--starts", "2026-10-02", "--ends", "2026-10-01").wantCode(t, ExitUsage)
	r = h.run("", "admin", "campaigns", "create", "--name", "Quiet", "--benefits", "discount all:10", "--dry-run").wantCode(t, ExitOK)
	if h.api.calls() != calls {
		t.Error("a refused or dry-run create reached the API")
	}
	if dry := decodeBody(t, []byte(r.stdout)); dry["kind"] != "offer" || dry["name"] != "Quiet" {
		t.Errorf("dry run = %q", r.stdout)
	}

	h.run("", "admin", "campaigns", "edit", "7", "--ends", "never", "--description", "").wantCode(t, ExitOK)
	if body := decodeBody(t, h.api.last().Body); len(body) != 2 || body["ends_at"] != float64(0) || body["description"] != "" {
		t.Errorf("edit body = %v", body)
	}
	h.run("", "admin", "campaigns", "edit", "7").wantCode(t, ExitUsage)

	h.run("", "admin", "campaigns", "stop", "7").wantCode(t, ExitOK)
	if body := decodeBody(t, h.api.last().Body); len(body) != 1 || body["active"] != false {
		t.Errorf("stop body = %v", body)
	}
	h.run("", "admin", "campaigns", "delete", "7").wantCode(t, ExitUsage)
	h.run("", "admin", "campaigns", "rm", "7", "-y").wantCode(t, ExitOK)
	if h.api.last().Method != "DELETE" {
		t.Error("delete did not delete")
	}
}

func TestAdminCampaignRefusalIsExplained(t *testing.T) {
	h := newHarness(t).adminLoggedIn()
	h.api.on("POST", "/admin/promo/campaigns", adminRefuse(http.StatusBadRequest, "a discount is between 1 and 90 percent"))
	r := h.run("", "admin", "campaigns", "create", "--name", "x", "--discount", "pro:95").wantCode(t, ExitError)
	if !strings.Contains(r.stderr, "between 1 and 90 percent") || !strings.Contains(r.stderr, "admin reference") {
		t.Errorf("stderr = %q", r.stderr)
	}
	if h.api.calls() != 1 {
		t.Errorf("a refused write was sent %d times", h.api.calls())
	}
}

func TestAdminCodes(t *testing.T) {
	h := newHarness(t).adminLoggedIn()
	h.api.adminOK("GET", "/admin/promo/codes", []any{sampleCode})
	h.api.adminOK("GET", "/admin/promo/codes/SUMMER25", sampleCode)
	h.api.adminOK("POST", "/admin/promo/codes", sampleCode)
	h.api.adminOK("PATCH", "/admin/promo/codes/SUMMER25", sampleCode)
	h.api.adminOK("DELETE", "/admin/promo/codes/SUMMER25", map[string]any{})
	h.api.adminOK("GET", "/admin/promo/codes/SUMMER25/redemptions", []any{
		map[string]any{"id": 1, "code": "SUMMER25", "account_id": 42, "expires_at": 1, "created_at": "2026-09-01T00:00:00Z"},
	})

	r := h.run("", "admin", "codes", "list").wantCode(t, ExitOK)
	if !strings.Contains(r.stdout, "SUMMER25") || !strings.Contains(r.stdout, "4 / 100") || !strings.Contains(r.stdout, "30d") {
		t.Errorf("list = %q", r.stdout)
	}

	r = h.run("", "admin", "codes", "create", "SUMMER25", "--name", "Summer sale", "--max", "100", "--lasts", "30d", "--discount", "all:25").wantCode(t, ExitOK)
	body := decodeBody(t, h.api.last().Body)
	if body["code"] != "SUMMER25" || body["max_redemptions"] != float64(100) || body["duration"] != float64(2592000) {
		t.Errorf("create body = %v", body)
	}
	if strings.TrimSpace(r.stdout) != "SUMMER25" {
		t.Errorf("create printed %q, want the code alone", r.stdout)
	}
	h.run("", "admin", "codes", "create", "--name", "Drawn", "--grant", "all:search:daily=unlimited").wantCode(t, ExitOK)
	if body := decodeBody(t, h.api.last().Body); body["code"] != nil {
		t.Errorf("a drawn code sent one: %v", body)
	}

	h.run("", "admin", "codes", "edit", "SUMMER25", "--max", "0", "--rename", "AUTUMN25").wantCode(t, ExitOK)
	if body := decodeBody(t, h.api.last().Body); body["max_redemptions"] != float64(0) || body["code"] != "AUTUMN25" || len(body) != 2 {
		t.Errorf("edit body = %v", body)
	}
	h.run("", "admin", "codes", "disable", "SUMMER25").wantCode(t, ExitOK)
	if body := decodeBody(t, h.api.last().Body); body["active"] != false {
		t.Errorf("disable body = %v", body)
	}

	r = h.run("", "admin", "codes", "redemptions", "SUMMER25").wantCode(t, ExitOK)
	if !strings.Contains(r.stdout, "42") || !strings.Contains(r.stdout, "(expired)") {
		t.Errorf("redemptions = %q", r.stdout)
	}
	h.run("", "admin", "codes", "delete", "SUMMER25", "--yes").wantCode(t, ExitOK)
}

func TestAdminCodeConflict(t *testing.T) {
	h := newHarness(t).adminLoggedIn()
	h.api.on("POST", "/admin/promo/codes", adminRefuse(http.StatusConflict, "code already exists"))
	r := h.run("", "admin", "codes", "create", "SUMMER25", "--name", "x", "--discount", "all:5").wantCode(t, ExitError)
	if !strings.Contains(r.stderr, "already exists") || !strings.Contains(r.stderr, "drawn") {
		t.Errorf("stderr = %q", r.stderr)
	}
}

func TestAdminBenefitsFile(t *testing.T) {
	h := newHarness(t).adminLoggedIn()
	h.api.adminOK("POST", "/admin/promo/codes", sampleCode)
	stdin := `{"discounts":[{"tier":"pro","percent":10}],"limits":[{"tier":"","limit":"search:daily","value":-1}]}`
	h.run(stdin, "admin", "codes", "create", "--name", "x", "--benefits-file", "-").wantCode(t, ExitOK)
	b := decodeBody(t, h.api.last().Body)["benefits"].(map[string]any)
	if len(b["discounts"].([]any)) != 1 || b["limits"].([]any)[0].(map[string]any)["value"] != float64(-1) {
		t.Errorf("benefits = %v", b)
	}
	h.run(`{"benifits":[]}`, "admin", "codes", "create", "--name", "x", "--benefits-file", "-").wantCode(t, ExitUsage)
	h.run(stdin, "admin", "codes", "create", "--name", "x", "--benefits-file", "-", "--discount", "pro:5").wantCode(t, ExitUsage)
}

package tui

import (
	"net/http"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

var adminCode = map[string]any{
	"id": 3, "code": "SUMMER25", "name": "Summer sale", "active": true,
	"max_redemptions": 100, "redemptions": 1, "duration": 2592000,
	"benefits": map[string]any{"discounts": []any{map[string]any{"tier": "", "percent": 25}}},
}

// newAdminDriver is the admin interface over the fake API, with the lists
// it opens on already answered.
func TestAdminCodes(t *testing.T) {
	d := newAdminDriver(t)
	d.api.ok("GET", "/admin/promo/codes", []any{adminCode})
	d.api.ok("GET", "/admin/promo/codes/SUMMER25/redemptions", []any{
		map[string]any{"id": 1, "code": "SUMMER25", "account_id": 4242, "expires_at": 1, "created_at": "2026-09-01T00:00:00Z"},
	})
	d.api.ok("PATCH", "/admin/promo/codes/SUMMER25", adminCode)
	d.api.ok("POST", "/admin/promo/codes", adminCode)

	d.open("Codes")
	d.wantView("SUMMER25", "1/100", "30d once redeemed", "Press enter to list")

	d.key(tea.KeyEnter)
	d.wantView("account 4242", "expired")

	d.keys("e")
	d.wantView("Edit SUMMER25")
	for range kfMax {
		d.key(tea.KeyTab)
	}
	d.send(tea.KeyMsg{Type: tea.KeyCtrlU})
	d.keys("0")
	d.key(tea.KeyCtrlS)
	if body := d.body("PATCH", "/admin/promo/codes/SUMMER25"); len(body) != 1 || body["max_redemptions"] != float64(0) {
		t.Errorf("edit = %v, want only max_redemptions 0", body)
	}

	d.keys("n")
	d.key(tea.KeyTab)
	d.keys("Goodwill")
	for range kfBenefits - kfName {
		d.key(tea.KeyTab)
	}
	d.keys("grant all:search:daily=unlimited")
	d.key(tea.KeyEnter)
	d.keys("plan pro:30, balance all:2.50")
	d.key(tea.KeyEnter)
	body := d.body("POST", "/admin/promo/codes")
	for _, absent := range []string{"code", "max_redemptions", "duration", "starts_at", "ends_at"} {
		if _, has := body[absent]; has {
			t.Errorf("a new code sent %q, left at its default: %v", absent, body)
		}
	}
	grants, _ := body["grants"].(map[string]any)
	plan, _ := grants["subscription"].(map[string]any)
	if plan["tier"] != "pro" || plan["days"] != float64(30) ||
		grants["balance_cents"].(map[string]any)[""] != float64(250) {
		t.Errorf("grants = %v", body["grants"])
	}
}

// A code may give only gifts, and its card and redemptions say what it gave
// and what an erasure took back.
func TestAdminCodeWithGifts(t *testing.T) {
	d := newAdminDriver(t)
	gift := map[string]any{
		"id": 4, "code": "MONTHOFPRO", "name": "A month of Pro", "active": true, "redemptions": 2,
		"benefits": map[string]any{},
		"grants":   map[string]any{"subscription": map[string]any{"tier": "pro", "days": 30}},
	}
	d.api.ok("GET", "/admin/promo/codes", []any{gift})
	d.api.ok("GET", "/admin/promo/codes/MONTHOFPRO/redemptions", []any{
		map[string]any{"id": 1, "code": "MONTHOFPRO", "account_id": 4242, "benefits_revoked_at": 1788000000, "created_at": "2026-09-01T00:00:00Z"},
		map[string]any{"id": 2, "code": "MONTHOFPRO", "account_id": 4343, "created_at": "2026-09-02T00:00:00Z"},
	})
	d.api.ok("PATCH", "/admin/promo/codes/MONTHOFPRO", gift)

	d.open("Codes")
	d.wantView("MONTHOFPRO", "grants nothing", "Given once", "30 days of pro")
	d.key(tea.KeyEnter)
	d.wantView("account 4242", "revoked", "account 4343", "with the code")

	d.keys("e")
	d.wantView("plan pro:30")
	for range kfGifts {
		d.key(tea.KeyTab)
	}
	d.send(tea.KeyMsg{Type: tea.KeyCtrlU})
	d.keys("plan pro:90")
	d.key(tea.KeyCtrlS)
	body := d.body("PATCH", "/admin/promo/codes/MONTHOFPRO")
	plan, _ := body["grants"].(map[string]any)["subscription"].(map[string]any)
	if len(body) != 1 || plan["days"] != float64(90) {
		t.Errorf("patch = %v, want only the grants", body)
	}
}

func TestAdminUnauthorizedReachesTheStatusLine(t *testing.T) {
	d := newAdminDriver(t)
	d.api.on("GET", "/admin/promo/codes", adminRefuse(http.StatusUnauthorized, "unauthorized"))
	d.open("Codes")
	d.wantView("admin token was not accepted", "celadon admin login")
}

func TestSpanText(t *testing.T) {
	for secs, want := range map[int64]string{0: "0", 86400: "1d", 93600: "26h", 90: "90s", 120: "2m"} {
		if got := spanText(secs); got != want {
			t.Errorf("spanText(%d) = %q, want %q", secs, got, want)
		}
	}
}

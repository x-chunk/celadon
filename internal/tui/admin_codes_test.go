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
	d.api.ok("GET", "/api/admin/promo/codes", []any{adminCode})
	d.api.ok("GET", "/api/admin/promo/codes/SUMMER25/redemptions", []any{
		map[string]any{"id": 1, "code": "SUMMER25", "account_id": 4242, "expires_at": 1, "created_at": "2026-09-01T00:00:00Z"},
	})
	d.api.ok("PATCH", "/api/admin/promo/codes/SUMMER25", adminCode)
	d.api.ok("POST", "/api/admin/promo/codes", adminCode)

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
	if body := d.body("PATCH", "/api/admin/promo/codes/SUMMER25"); len(body) != 1 || body["max_redemptions"] != float64(0) {
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
	body := d.body("POST", "/api/admin/promo/codes")
	for _, absent := range []string{"code", "max_redemptions", "duration", "starts_at", "ends_at"} {
		if _, has := body[absent]; has {
			t.Errorf("a new code sent %q, left at its default: %v", absent, body)
		}
	}
}

func TestAdminUnauthorizedReachesTheStatusLine(t *testing.T) {
	d := newAdminDriver(t)
	d.api.on("GET", "/api/admin/promo/codes", adminRefuse(http.StatusUnauthorized, "unauthorized"))
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

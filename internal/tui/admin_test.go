package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/x-chunk/celadon/internal/admin"
)

const testAdminToken = "0123456789abcdef0123456789abcdef"

var adminCampaign = map[string]any{
	"id": 7, "kind": "event", "name": "Summer week", "state": "running", "active": true,
	"description": "Seven days of lower prices.",
	"starts_at":   1788000000, "ends_at": 0,
	"benefits": map[string]any{
		"discounts":      []any{map[string]any{"tier": "pro", "percent": 25}},
		"top_up_bonuses": []any{map[string]any{"amount_cents": 5000, "percent": 20}},
	},
}

func newAdminDriver(t *testing.T) *driver {
	t.Helper()
	api := newFakeAPI(t)
	api.ok("GET", "/admin/promo/campaigns", []any{adminCampaign})
	c, err := admin.New(testAdminToken, admin.WithBaseURL(api.srv.URL), admin.WithRetry(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	d := &driver{t: t, api: api, m: NewAdmin(context.Background(), c, Options{Profile: "ops", BaseURL: api.srv.URL})}
	d.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	d.exec(d.m.Init())
	return d
}

func adminRefuse(status int, message string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "message": message})
	}
}

func TestAdminOpensOnCampaigns(t *testing.T) {
	d := newAdminDriver(t)
	d.wantView("celadon admin", "ops", "1 Campaigns", "2 Codes", "3 Reference",
		"Summer week", "−25% pro", "+20% on a $50.00 top-up", "Seven days of lower prices.")
	if q := d.api.called("GET", "/admin/promo/campaigns"); len(q) != 1 {
		t.Fatalf("campaigns were loaded %d times", len(q))
	}
	if strings.Contains(d.view(), "balance") {
		t.Error("the admin header shows a balance")
	}
}

func TestAdminCampaignFiltersReload(t *testing.T) {
	d := newAdminDriver(t)
	d.keys("f")
	d.keys("a")
	calls := d.api.called("GET", "/admin/promo/campaigns")
	if len(calls) != 3 {
		t.Fatalf("filters loaded %d times", len(calls))
	}
	d.wantView("offers")
}

func TestAdminCreateCampaign(t *testing.T) {
	d := newAdminDriver(t)
	d.api.ok("POST", "/admin/promo/campaigns", adminCampaign)

	d.keys("n")
	d.wantView("New campaign", "offer runs quietly")
	d.send(tea.KeyMsg{Type: tea.KeyCtrlU}) // clear "offer"
	d.keys("event")
	d.key(tea.KeyTab)
	d.keys("Autumn q")
	if d.quit {
		t.Fatal("q in a form quit the interface")
	}
	d.key(tea.KeyTab)
	d.key(tea.KeyTab)
	d.key(tea.KeyTab) // starts: now
	d.key(tea.KeyTab) // ends
	d.send(tea.KeyMsg{Type: tea.KeyCtrlU})
	d.keys("+7d")
	d.key(tea.KeyTab)
	d.keys("gift pro:25")
	d.key(tea.KeyCtrlS)
	if len(d.api.called("POST", "/admin/promo/campaigns")) != 0 {
		t.Fatal("unreadable benefits were sent")
	}
	d.wantView("start it with discount, bonus or grant")

	d.send(tea.KeyMsg{Type: tea.KeyCtrlU})
	d.keys("discount pro:25, grant free:search:daily=unlimited")
	before := time.Now()
	d.key(tea.KeyEnter)
	body := d.body("POST", "/admin/promo/campaigns")
	if body["kind"] != "event" || body["name"] != "Autumn q" {
		t.Errorf("create body = %v", body)
	}
	if _, has := body["starts_at"]; has {
		t.Error("a start of now was sent instead of left to the server")
	}
	ends := int64(body["ends_at"].(float64))
	if want := before.Add(7 * 24 * time.Hour).Unix(); ends < want-5 || ends > want+5 {
		t.Errorf("ends_at = %d, want about %d", ends, want)
	}
	limits := body["benefits"].(map[string]any)["limits"].([]any)
	if limits[0].(map[string]any)["value"] != float64(-1) {
		t.Errorf("limits = %v", limits)
	}
	if n := len(d.api.called("GET", "/admin/promo/campaigns")); n != 2 {
		t.Errorf("the list was loaded %d times, want a reload after saving", n)
	}
	d.wantView("Saved")
}

func TestAdminEditCampaignSendsOnlyWhatChanged(t *testing.T) {
	d := newAdminDriver(t)
	d.api.ok("PATCH", "/admin/promo/campaigns/7", adminCampaign)

	d.keys("e")
	d.wantView("Edit campaign #7", "discount pro:25, bonus 50:20")
	d.key(tea.KeyCtrlS)
	if len(d.api.called("PATCH", "/admin/promo/campaigns/7")) != 0 {
		t.Fatal("an unchanged form was sent")
	}
	d.wantView("Nothing changed.")

	d.keys("e")
	d.key(tea.KeyTab)
	d.keys(" 2")
	d.key(tea.KeyCtrlS)
	body := d.body("PATCH", "/admin/promo/campaigns/7")
	if len(body) != 1 || body["name"] != "Summer week 2" {
		t.Errorf("patch = %v, want only the name", body)
	}
}

func TestAdminCampaignRefusalStaysOnTheForm(t *testing.T) {
	d := newAdminDriver(t)
	d.api.on("POST", "/admin/promo/campaigns", adminRefuse(http.StatusBadRequest, "a discount is between 1 and 90 percent"))
	d.keys("n")
	d.key(tea.KeyTab)
	d.keys("Deep")
	for range 5 {
		d.key(tea.KeyTab)
	}
	d.keys("discount pro:95")
	d.key(tea.KeyCtrlS)
	d.wantView("New campaign", "between 1 and 90 percent")
	d.key(tea.KeyEsc)
	d.wantView("Summer week")
}

func TestAdminStopAndDeleteCampaign(t *testing.T) {
	d := newAdminDriver(t)
	stopped := map[string]any{"id": 7, "name": "Summer week", "state": "stopped", "active": false}
	d.api.ok("PATCH", "/admin/promo/campaigns/7", stopped)
	d.api.ok("DELETE", "/admin/promo/campaigns/7", map[string]any{})

	d.keys("s")
	if body := d.body("PATCH", "/admin/promo/campaigns/7"); len(body) != 1 || body["active"] != false {
		t.Errorf("stop = %v", body)
	}
	d.wantView(`"Summer week" is stopped.`)

	d.keys("d")
	d.wantView("for good? y/n")
	d.keys("q") // not quit: the question has the keyboard
	if d.quit {
		t.Fatal("q quit during a confirmation")
	}
	d.keys("n")
	if len(d.api.called("DELETE", "/admin/promo/campaigns/7")) != 0 {
		t.Fatal("deleted without a yes")
	}
	d.keys("d")
	d.keys("y")
	if len(d.api.called("DELETE", "/admin/promo/campaigns/7")) != 1 {
		t.Fatal("y did not delete")
	}
}

func TestAdminReference(t *testing.T) {
	d := newAdminDriver(t)
	d.api.ok("GET", "/admin/promo/reference", map[string]any{
		"tiers":                []any{map[string]any{"tier": "pro", "name": "Pro", "price_cents": 899, "paid": true}},
		"limits":               []any{map[string]any{"limit": "search:daily", "label": "Searches a day", "unit": "searches"}},
		"top_up_amounts_cents": []int64{5000},
		"max_discount_percent": 90,
	})
	d.open("Reference")
	d.wantView("discountable", "search:daily", "$50.00", "90%")
}

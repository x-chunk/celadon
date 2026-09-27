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
	d.key(tea.KeyEnter)
	d.send(tea.KeyMsg{Type: tea.KeyCtrlU}) // clear "off"
	d.keys("30")
	d.key(tea.KeyEnter)
	d.keys("balance all:1, balance pro:2")
	before := time.Now()
	d.key(tea.KeyEnter) // the last field submits
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
	benefits := body["benefits"].(map[string]any)
	limits := benefits["limits"].([]any)
	if limits[0].(map[string]any)["value"] != float64(-1) {
		t.Errorf("limits = %v", limits)
	}
	if benefits["trial_days"] != float64(30) {
		t.Errorf("trial_days = %v", benefits["trial_days"])
	}
	balances := body["grants"].(map[string]any)["balance_cents"].(map[string]any)
	if len(balances) != 2 || balances[""] != float64(100) || balances["pro"] != float64(200) {
		t.Errorf("grants = %v", body["grants"])
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
	d.wantView("can be discounted or given", "search:daily", "$50.00", "90%")
}

func TestAdminReferenceNamesTheGiftSets(t *testing.T) {
	d := newAdminDriver(t)
	d.api.ok("GET", "/admin/promo/reference", map[string]any{
		"tiers":             []any{map[string]any{"tier": "pro", "name": "Pro", "price_cents": 899, "paid": true}},
		"max_gift_cents":    10000000,
		"subscription_days": []int{1, 3, 7, 14, 30, 90, 365},
		"trial_days":        []int{14, 30, 90},
		"any_tier":          "",
	})
	d.open("Reference")
	d.wantView("$100000.00 per account", "1, 3, 7, 14, 30, 90, 365 days", "14, 30, 90 days", "balance all:5")
}

// An announced event's gifts and trial are frozen. The card says so, the
// form says so, and an edit that leaves them alone sends neither — the API
// refuses any change to them, even one that spells the same thing.
func TestAdminFrozenEvent(t *testing.T) {
	d := newAdminDriver(t)
	frozen := map[string]any{
		"id": 9, "kind": "event", "name": "Gift week", "state": "running", "active": true,
		"starts_at": 1788000000, "announced_at": 1788000000, "first_announced_at": 1787000000, "generation": 2,
		"benefits": map[string]any{"trial_days": 30},
		"grants":   map[string]any{"balance_cents": map[string]any{"": 100, "pro": 200, "ultra": 0}},
	}
	d.api.ok("GET", "/admin/promo/campaigns", []any{frozen})
	d.api.ok("PATCH", "/admin/promo/campaigns/9", frozen)
	d.keys("r")
	d.wantView("Gift week", "(announcement 2)", "frozen since the first announcement", "a 30-day trial",
		"Given once", "+$1.00 balance (every other plan)", "+$2.00 balance (pro)", "no balance (ultra)")

	d.keys("e")
	d.wantView("Edit campaign #9")
	for range cfTrial {
		d.key(tea.KeyTab)
	}
	d.wantView("Frozen: the event was announced")
	d.key(tea.KeyTab)
	d.wantView("balance all:1, balance pro:2, balance ultra:0")

	d.key(tea.KeyTab) // wraps nowhere: the last field keeps the keyboard
	for range cfGifts - cfName {
		d.send(tea.KeyMsg{Type: tea.KeyShiftTab})
	}
	d.keys(" 2")
	d.key(tea.KeyCtrlS)
	body := d.body("PATCH", "/admin/promo/campaigns/9")
	if len(body) != 1 || body["name"] != "Gift week 2" {
		t.Errorf("patch = %v, want only the name", body)
	}
}

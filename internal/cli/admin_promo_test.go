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

var giftEvent = map[string]any{
	"id": 9, "kind": "event", "name": "Gift week", "state": "running", "active": true,
	"announced_at": 1788000000, "first_announced_at": 1787000000, "generation": 2,
	"benefits": map[string]any{
		"discounts":  []any{map[string]any{"tier": "pro", "percent": 25}},
		"trial_days": 30,
	},
	"grants": map[string]any{"balance_cents": map[string]any{"": 100, "pro": 200}},
}

func TestAdminCampaignGifts(t *testing.T) {
	h := newHarness(t).adminLoggedIn()
	h.api.adminOK("POST", "/admin/promo/campaigns", giftEvent)
	h.api.adminOK("GET", "/admin/promo/campaigns/9", giftEvent)
	h.api.adminOK("GET", "/admin/promo/campaigns", []any{giftEvent})

	// An event that only lengthens the trial and gives a balance grants
	// something.
	h.run("", "admin", "campaigns", "create", "--kind", "event", "--name", "Gift week",
		"--trial", "30", "--gift-balance", "all:1", "--gift-balance", "pro:2").wantCode(t, ExitOK)
	body := decodeBody(t, h.api.last().Body)
	if b := body["benefits"].(map[string]any); b["trial_days"] != float64(30) || len(b) != 1 {
		t.Errorf("benefits = %v", body["benefits"])
	}
	balances := body["grants"].(map[string]any)["balance_cents"].(map[string]any)
	if balances[""] != float64(100) || balances["pro"] != float64(200) {
		t.Errorf("grants = %v", body["grants"])
	}

	calls := h.api.calls()
	h.run("", "admin", "campaigns", "create", "--name", "x", "--trial", "a month").wantCode(t, ExitUsage)
	h.run("", "admin", "campaigns", "create", "--name", "x", "--gift-balance", "pro:1", "--gift-balance", "Pro:2").wantCode(t, ExitUsage)
	h.run("", "admin", "campaigns", "create", "--name", "x", "--gifts", "none").wantCode(t, ExitUsage)
	h.run("", "admin", "campaigns", "create", "--name", "x", "--gift-plan", "pro:30").wantCode(t, ExitUsage) // codes only
	h.run("{}", "admin", "campaigns", "create", "--name", "x", "--benefits-file", "-", "--trial", "30").wantCode(t, ExitUsage)
	if h.api.calls() != calls {
		t.Error("a refused create reached the API")
	}

	r := h.run("", "admin", "campaigns", "view", "9").wantCode(t, ExitOK)
	for _, want := range []string{"(announcement 2)", "frozen", "While in force", "a 30-day trial",
		"Given once", "+$1.00 balance (every other plan)", "+$2.00 balance (pro)"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("view lacks %q:\n%s", want, r.stdout)
		}
	}
	r = h.run("", "admin", "campaigns", "list").wantCode(t, ExitOK)
	if !strings.Contains(r.stdout, "a 30-day trial") || !strings.Contains(r.stdout, "+$2.00 balance (pro)") {
		t.Errorf("list = %q", r.stdout)
	}
	r = h.run("", "admin", "campaigns", "view", "9", "-o", "json").wantCode(t, ExitOK)
	if out := decodeBody(t, []byte(r.stdout)); out["first_announced_at"] != float64(1787000000) || out["grants"] == nil {
		t.Errorf("json = %s", r.stdout)
	}
}

// The API replaces the benefits as a whole, the trial with them. An edit that
// names only the lists keeps the campaign's trial, and one that names only
// the trial keeps its lists — which is what lets a frozen event's discount be
// changed at all.
func TestAdminCampaignEditKeepsWhatWasNotGiven(t *testing.T) {
	h := newHarness(t).adminLoggedIn()
	h.api.adminOK("GET", "/admin/promo/campaigns/9", giftEvent)
	h.api.adminOK("PATCH", "/admin/promo/campaigns/9", giftEvent)

	h.run("", "admin", "campaigns", "edit", "9", "--discount", "pro:30").wantCode(t, ExitOK)
	body := decodeBody(t, h.api.last().Body)
	b := body["benefits"].(map[string]any)
	if len(body) != 1 || b["trial_days"] != float64(30) ||
		b["discounts"].([]any)[0].(map[string]any)["percent"] != float64(30) {
		t.Errorf("edit of the lists = %v", body)
	}

	h.run("", "admin", "campaigns", "edit", "9", "--trial", "off").wantCode(t, ExitOK)
	body = decodeBody(t, h.api.last().Body)
	b = body["benefits"].(map[string]any)
	if _, has := b["trial_days"]; has || b["discounts"].([]any)[0].(map[string]any)["percent"] != float64(25) {
		t.Errorf("edit of the trial = %v", body)
	}

	// Both parts given, or a whole file: nothing to keep, nothing read.
	calls := h.api.calls()
	h.run("", "admin", "campaigns", "edit", "9", "--benefits", "none", "--trial", "90").wantCode(t, ExitOK)
	if body := decodeBody(t, h.api.last().Body); body["benefits"].(map[string]any)["trial_days"] != float64(90) {
		t.Errorf("edit of both = %v", body)
	}
	h.run(`{"trial_days":14}`, "admin", "campaigns", "edit", "9", "--benefits-file", "-").wantCode(t, ExitOK)
	if h.api.calls() != calls+2 {
		t.Errorf("an edit giving the whole benefits read the campaign first (%d calls)", h.api.calls()-calls)
	}

	// Gifts are replaced whole, and none empties them.
	h.run("", "admin", "campaigns", "edit", "9", "--gifts", "none").wantCode(t, ExitOK)
	if body := decodeBody(t, h.api.last().Body); len(body) != 1 || len(body["grants"].(map[string]any)) != 0 {
		t.Errorf("emptying the gifts = %v", body)
	}

	// A dry run that has to keep something reads, and writes nothing.
	calls = h.api.calls()
	r := h.run("", "admin", "campaigns", "edit", "9", "--bonus", "50:10", "--dry-run").wantCode(t, ExitOK)
	if h.api.calls() != calls+1 || h.api.last().Method != "GET" {
		t.Error("the dry run did not read the campaign, or wrote it")
	}
	if dry := decodeBody(t, []byte(r.stdout)); dry["benefits"].(map[string]any)["trial_days"] != float64(30) {
		t.Errorf("dry run = %s", r.stdout)
	}
}

func TestAdminCodeGifts(t *testing.T) {
	h := newHarness(t).adminLoggedIn()
	gift := map[string]any{
		"id": 4, "code": "MONTHOFPRO", "name": "A month of Pro", "active": true,
		"benefits": map[string]any{},
		"grants": map[string]any{
			"subscription":  map[string]any{"tier": "pro", "days": 30},
			"balance_cents": map[string]any{"": 500},
		},
	}
	h.api.adminOK("POST", "/admin/promo/codes", gift)
	h.api.adminOK("GET", "/admin/promo/codes/MONTHOFPRO", gift)
	h.api.adminOK("PATCH", "/admin/promo/codes/MONTHOFPRO", gift)
	h.api.adminOK("GET", "/admin/promo/codes/MONTHOFPRO/redemptions", []any{
		map[string]any{"id": 1, "code": "MONTHOFPRO", "account_id": 42, "benefits_revoked_at": 1788000000, "created_at": "2026-09-01T00:00:00Z"},
	})

	// A code may give only gifts.
	h.run("", "admin", "codes", "create", "MONTHOFPRO", "--name", "A month of Pro",
		"--gift-plan", "pro:30", "--gift-balance", "all:5").wantCode(t, ExitOK)
	body := decodeBody(t, h.api.last().Body)
	grants := body["grants"].(map[string]any)
	if grants["subscription"].(map[string]any)["days"] != float64(30) || grants["balance_cents"].(map[string]any)[""] != float64(500) {
		t.Errorf("create body = %v", body)
	}

	stdin := `{"subscription":{"tier":"go","days":7}}`
	h.run(stdin, "admin", "codes", "edit", "MONTHOFPRO", "--gifts-file", "-").wantCode(t, ExitOK)
	if body := decodeBody(t, h.api.last().Body); body["grants"].(map[string]any)["subscription"].(map[string]any)["tier"] != "go" || len(body) != 1 {
		t.Errorf("edit body = %v", body)
	}

	calls := h.api.calls()
	h.run(`{"balance":{"":1}}`, "admin", "codes", "edit", "MONTHOFPRO", "--gifts-file", "-").wantCode(t, ExitUsage)
	h.run(stdin, "admin", "codes", "edit", "MONTHOFPRO", "--gifts-file", "-", "--gift-plan", "pro:30").wantCode(t, ExitUsage)
	h.run(stdin, "admin", "codes", "edit", "MONTHOFPRO", "--gifts-file", "-", "--benefits-file", "-").wantCode(t, ExitUsage)
	h.run("", "admin", "codes", "edit", "MONTHOFPRO", "--trial", "30").wantCode(t, ExitUsage) // events only
	h.run("", "admin", "codes", "create", "--name", "x", "--gift-plan", "pro:30", "--gift-plan", "go:7").wantCode(t, ExitUsage)
	if h.api.calls() != calls {
		t.Error("a refused edit reached the API")
	}

	r := h.run("", "admin", "codes", "view", "MONTHOFPRO").wantCode(t, ExitOK)
	if !strings.Contains(r.stdout, "30 days of pro") || !strings.Contains(r.stdout, "+$5.00 balance (every plan)") {
		t.Errorf("view = %q", r.stdout)
	}
	r = h.run("", "admin", "codes", "redemptions", "MONTHOFPRO").wantCode(t, ExitOK)
	rows := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if len(rows) != 2 || !strings.Contains(rows[0], "REVOKED") || strings.Count(rows[1], "2026-") != 2 {
		t.Errorf("redemptions = %q, want the revocation's time", r.stdout)
	}
}

func TestAdminReferenceNamesTheGiftSets(t *testing.T) {
	h := newHarness(t).adminLoggedIn()
	h.api.adminOK("GET", "/admin/promo/reference", map[string]any{
		"tiers":             []any{map[string]any{"tier": "free", "name": "Free", "price_cents": 0, "paid": false}},
		"max_gift_cents":    10000000,
		"subscription_days": []int{1, 3, 7, 14, 30, 90, 365},
		"trial_days":        []int{14, 30, 90},
		"any_tier":          "",
	})
	r := h.run("", "admin", "reference").wantCode(t, ExitOK)
	for _, want := range []string{"cannot be discounted or given", "$100000.00 per account",
		"1, 3, 7, 14, 30, 90, 365 days", "14, 30, 90 days", `"" (written as all)`} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("reference lacks %q:\n%s", want, r.stdout)
		}
	}
}

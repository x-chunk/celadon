package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testToken = "0123456789abcdef0123456789abcdef"

// server answers every request with h, after checking the token.
func server(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"ok":false,"message":"unauthorized"}`)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func client(t *testing.T, url string, opts ...Option) *Client {
	t.Helper()
	c, err := New(testToken, append([]Option{WithBaseURL(url), WithRetry(2, time.Millisecond)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ok(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "message": "success", "data": data})
}

func refuse(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"ok": false, "message": message, "data": nil})
}

func TestNewRefusesAShortToken(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Error("an empty token was accepted")
	}
	if _, err := New("short"); err == nil || !strings.Contains(err.Error(), "24") {
		t.Errorf("a short token = %v", err)
	}
	if _, err := New(testToken, WithBaseURL("nohost")); err == nil {
		t.Error("a base url without a host was accepted")
	}
}

func TestReference(t *testing.T) {
	srv := server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/admin/promo/reference" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		// The response example is the one the server's own type renders.
		io.WriteString(w, `{"ok":true,"message":"success","data":{
			"tiers":[{"tier":"pro","name":"Pro","price_cents":899,"paid":true}],
			"limits":[{"limit":"search:daily","label":"Searches a day","unit":"searches"}],
			"top_up_amounts_cents":[500,5000],
			"campaign_kinds":["offer","event"],
			"max_discount_percent":90,"max_bonus_percent":500,"unlimited":-1,
			"max_gift_cents":10000000,"subscription_days":[1,3,7,14,30,90,365],
			"trial_days":[14,30,90],"any_tier":""}}`)
	})
	ref, meta, err := client(t, srv.URL).Promo.Reference(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ref.Tiers[0].Tier != "pro" || ref.Limits[0].Key != "search:daily" || ref.TopUpAmountsCents[1] != 5000 ||
		ref.MaxDiscountPercent != 90 || ref.Unlimited != Unlimited {
		t.Errorf("reference = %+v", ref)
	}
	if ref.MaxGiftCents != 10_000_000 || len(ref.SubscriptionDays) != 7 || ref.SubscriptionDays[6] != 365 ||
		len(ref.TrialDays) != 3 || ref.TrialDays[0] != 14 || ref.AnyTier != AnyTier {
		t.Errorf("the gift sets = %+v", ref)
	}
	if meta.StatusCode != 200 || meta.Message != "success" {
		t.Errorf("meta = %+v", meta)
	}
}

func TestCampaignCalls(t *testing.T) {
	var got []string
	var bodies []map[string]any
	srv := server(t, func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		if r.Body != nil {
			var m map[string]any
			if json.NewDecoder(r.Body).Decode(&m) == nil {
				bodies = append(bodies, m)
			}
		}
		switch r.Method {
		case http.MethodGet:
			if strings.HasSuffix(r.URL.Path, "/campaigns") {
				ok(w, []any{map[string]any{"id": 7, "kind": "event", "name": "Summer", "state": "running",
					"starts_at": 1757116800, "created_at": "2026-09-01T00:00:00Z"}})
				return
			}
			ok(w, map[string]any{"id": 7, "kind": "event", "name": "Summer"})
		case http.MethodDelete:
			ok(w, map[string]any{})
		default:
			ok(w, map[string]any{"id": 7, "kind": "event", "name": "Summer", "active": false})
		}
	})
	c := client(t, srv.URL)
	ctx := context.Background()

	list, _, err := c.Promo.Campaigns(ctx, &CampaignListRequest{Kind: KindEvent, Inactive: true, Limit: 10, Offset: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].StartsAt.At().Year() != 2025 || list[0].CreatedAt.IsZero() {
		t.Errorf("list = %+v", list)
	}
	if _, _, err := c.Promo.Campaign(ctx, 7); err != nil {
		t.Fatal(err)
	}
	_, _, err = c.Promo.CreateCampaign(ctx, CampaignCreateRequest{
		Kind: KindEvent, Name: "Summer",
		Benefits: Benefits{Discounts: []Discount{{Tier: "pro", Percent: 25}}},
		EndsAt:   Ptr(Unix(1757721600)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Promo.UpdateCampaign(ctx, 7, CampaignUpdateRequest{Active: Ptr(false)}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Promo.DeleteCampaign(ctx, 7); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"GET /admin/promo/campaigns?inactive=true&kind=event&limit=10&offset=20",
		"GET /admin/promo/campaigns/7?",
		"POST /admin/promo/campaigns?",
		"PATCH /admin/promo/campaigns/7?",
		"DELETE /admin/promo/campaigns/7?",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	create := bodies[0]
	if create["kind"] != "event" || create["ends_at"] != float64(1757721600) {
		t.Errorf("create body = %v", create)
	}
	for _, absent := range []string{"starts_at", "active", "description", "announcement", "grants"} {
		if _, has := create[absent]; has {
			t.Errorf("create body carries %q, which was left out", absent)
		}
	}
	if patch := bodies[1]; len(patch) != 1 || patch["active"] != false {
		t.Errorf("patch body = %v, want only active=false", patch)
	}
}

func TestZeroIsSentWhenItIsSet(t *testing.T) {
	var body map[string]any
	srv := server(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		ok(w, map[string]any{})
	})
	_, _, err := client(t, srv.URL).Promo.UpdateCode(context.Background(), "X", CodeUpdateRequest{
		EndsAt: Ptr(Unix(0)), MaxRedemptions: Ptr(int64(0)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if body["ends_at"] != float64(0) || body["max_redemptions"] != float64(0) || len(body) != 2 {
		t.Errorf("body = %v: a zero that was set must be sent", body)
	}
}

// The gifts travel in the shape the API reads and answers them in: the
// balance keyed by plan with "" for every plan not named, zero kept where it
// leaves a plan out, and an emptied set sent as an empty object.
func TestGiftsTravel(t *testing.T) {
	var body map[string]any
	srv := server(t, func(w http.ResponseWriter, r *http.Request) {
		body = nil // Decode merges into a map it is given
		json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, `{"ok":true,"message":"success","data":{
			"id":9,"kind":"event","name":"Gift week","active":true,"state":"running",
			"benefits":{"trial_days":30},
			"grants":{"balance_cents":{"":100,"pro":200,"ultra":0}},
			"announced_at":0,"first_announced_at":1787000000,"generation":1}}`)
	})
	c := client(t, srv.URL)
	ctx := context.Background()

	camp, _, err := c.Promo.CreateCampaign(ctx, CampaignCreateRequest{
		Kind: KindEvent, Name: "Gift week",
		Benefits: Benefits{TrialDays: 30},
		Grants:   &Grants{BalanceCents: map[string]int64{AnyTier: 100, "pro": 200, "ultra": 0}},
	})
	if err != nil {
		t.Fatal(err)
	}
	balances, _ := body["grants"].(map[string]any)["balance_cents"].(map[string]any)
	if len(balances) != 3 || balances[""] != float64(100) || balances["ultra"] != float64(0) {
		t.Errorf("create body = %v", body)
	}
	if body["benefits"].(map[string]any)["trial_days"] != float64(30) {
		t.Errorf("the trial was not sent: %v", body)
	}
	if !camp.Frozen() || camp.Generation != 1 || camp.Benefits.TrialDays != 30 ||
		camp.Grants.BalanceCents[AnyTier] != 100 || camp.Grants.Empty() {
		t.Errorf("campaign = %+v", camp)
	}

	if _, _, err := c.Promo.UpdateCampaign(ctx, 9, CampaignUpdateRequest{Grants: &Grants{}}); err != nil {
		t.Fatal(err)
	}
	if grants, has := body["grants"].(map[string]any); !has || len(grants) != 0 || len(body) != 1 {
		t.Errorf("emptying the gifts sent %v, want grants:{} alone", body)
	}

	if _, _, err := c.Promo.UpdateCode(ctx, "X", CodeUpdateRequest{
		Grants: &Grants{Subscription: &SubscriptionGrant{Tier: "pro", Days: 30}},
	}); err != nil {
		t.Fatal(err)
	}
	plan, _ := body["grants"].(map[string]any)["subscription"].(map[string]any)
	if plan["tier"] != "pro" || plan["days"] != float64(30) {
		t.Errorf("code body = %v", body)
	}
}

func TestGrantsEmpty(t *testing.T) {
	for _, g := range []Grants{{}, {BalanceCents: map[string]int64{}}, {BalanceCents: map[string]int64{"pro": 0}}} {
		if !g.Empty() {
			t.Errorf("%+v is not empty", g)
		}
	}
	for _, g := range []Grants{{BalanceCents: map[string]int64{"": 1}}, {Subscription: &SubscriptionGrant{Tier: "go", Days: 1}}} {
		if g.Empty() {
			t.Errorf("%+v is empty", g)
		}
	}
	if !(Benefits{}).Empty() || (Benefits{TrialDays: 14}).Empty() {
		t.Error("a trial is not counted as a benefit")
	}
}

func TestRedemptionLive(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for _, c := range []struct {
		r    Redemption
		live bool
	}{
		{Redemption{}, true},
		{Redemption{ExpiresAt: UnixOf(now.Add(time.Hour))}, true},
		{Redemption{ExpiresAt: UnixOf(now.Add(-time.Hour))}, false},
		{Redemption{RevokedAt: UnixOf(now.Add(-time.Hour))}, false},
	} {
		if got := c.r.Live(now); got != c.live {
			t.Errorf("%+v.Live = %v", c.r, got)
		}
	}
	var r Redemption
	if err := json.Unmarshal([]byte(`{"account_id":1,"benefits_revoked_at":1788000000}`), &r); err != nil || r.RevokedAt != 1788000000 {
		t.Errorf("benefits_revoked_at = %+v, %v", r, err)
	}
}

func TestCodeCallsEscapeTheCode(t *testing.T) {
	var paths []string
	srv := server(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		if strings.HasSuffix(r.URL.Path, "/redemptions") {
			ok(w, []any{map[string]any{"id": 1, "code": "SUMMER25", "account_id": 42, "expires_at": 1757721600}})
			return
		}
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/codes") {
			ok(w, []any{})
			return
		}
		ok(w, map[string]any{"code": "SUMMER25", "max_redemptions": 100})
	})
	c := client(t, srv.URL)
	ctx := context.Background()

	if _, _, err := c.Promo.Codes(ctx, &CodeListRequest{Inactive: true}); err != nil {
		t.Fatal(err)
	}
	code, _, err := c.Promo.CreateCode(ctx, CodeCreateRequest{Name: "Summer", Benefits: Benefits{TopUps: []TopUpBonus{{AmountCents: 5000, Percent: 20}}}})
	if err != nil || code.MaxRedemptions != 100 {
		t.Fatalf("create = %+v, %v", code, err)
	}
	if _, _, err := c.Promo.Code(ctx, "a/b c"); err != nil {
		t.Fatal(err)
	}
	reds, _, err := c.Promo.Redemptions(ctx, "SUMMER25", &RedemptionListRequest{Limit: 5})
	if err != nil || len(reds) != 1 || reds[0].AccountID != 42 {
		t.Fatalf("redemptions = %+v, %v", reds, err)
	}
	if _, err := c.Promo.DeleteCode(ctx, "SUMMER25"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"GET /admin/promo/codes?inactive=true",
		"POST /admin/promo/codes?",
		"GET /admin/promo/codes/a%2Fb%20c?",
		"GET /admin/promo/codes/SUMMER25/redemptions?limit=5",
		"DELETE /admin/promo/codes/SUMMER25?",
	}
	if strings.Join(paths, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests:\n%s\nwant:\n%s", strings.Join(paths, "\n"), strings.Join(want, "\n"))
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		code    string
		message string
	}{
		{"refused", func(w http.ResponseWriter, _ *http.Request) {
			refuse(w, 400, "a discount is between 1 and 90 percent")
		}, CodeBadRequest, "between 1 and 90"},
		{"missing", func(w http.ResponseWriter, _ *http.Request) { refuse(w, 404, "campaign not found") }, CodeNotFound, "campaign not found"},
		{"duplicate", func(w http.ResponseWriter, _ *http.Request) { refuse(w, 409, "code already exists") }, CodeConflict, "already exists"},
		{"not served", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "404 page not found", http.StatusNotFound)
		}, CodeNotServed, "ADMIN_TOKEN"},
		{"ok false with 200", func(w http.ResponseWriter, _ *http.Request) {
			io.WriteString(w, `{"ok":false,"message":"odd"}`)
		}, CodeInternal, "odd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := server(t, tc.handler)
			_, meta, err := client(t, srv.URL).Promo.Campaign(context.Background(), 1)
			e, isErr := AsError(err)
			if !isErr || e.Code != tc.code || !strings.Contains(e.Message, tc.message) {
				t.Fatalf("err = %#v", err)
			}
			if !IsCode(err, tc.code) || meta == nil {
				t.Errorf("IsCode = false or meta = nil")
			}
		})
	}

	t.Run("wrong token", func(t *testing.T) {
		srv := server(t, nil)
		c, err := New(strings.Repeat("x", 30), WithBaseURL(srv.URL))
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = c.Promo.Reference(context.Background())
		if !IsCode(err, CodeUnauthorized) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestRetries(t *testing.T) {
	var calls atomic.Int32
	srv := server(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			refuse(w, 500, "internal")
			return
		}
		ok(w, []any{})
	})
	c := client(t, srv.URL)

	if _, _, err := c.Promo.Codes(context.Background(), nil); err != nil {
		t.Fatalf("a read was not retried through: %v", err)
	}
	if n := calls.Load(); n != 3 {
		t.Errorf("a read was sent %d times, want 3", n)
	}

	calls.Store(0)
	_, _, err := c.Promo.CreateCode(context.Background(), CodeCreateRequest{Name: "x"})
	if !IsCode(err, CodeInternal) {
		t.Fatalf("err = %v", err)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("a write was sent %d times after a server failure, want 1", n)
	}

	calls.Store(0)
	srv2 := server(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		refuse(w, 400, "no")
	})
	if _, _, err := client(t, srv2.URL).Promo.Codes(context.Background(), nil); !IsCode(err, CodeBadRequest) {
		t.Fatalf("err = %v", err)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("a refusal was retried: %d calls", n)
	}
}

func TestTransportFailure(t *testing.T) {
	c := client(t, "http://127.0.0.1:1", WithRetry(0, 0))
	_, meta, err := c.Promo.Reference(context.Background())
	if err == nil || meta != nil {
		t.Errorf("err = %v, meta = %v", err, meta)
	}
	var e *Error
	if errors.As(err, &e) {
		t.Error("a transport failure became an API refusal")
	}
}

func TestUnix(t *testing.T) {
	if !Unix(0).At().IsZero() || UnixOf(time.Time{}) != 0 {
		t.Error("zero is not the zero time")
	}
	at := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	if UnixOf(at).At() != at {
		t.Error("Unix does not round-trip")
	}
}

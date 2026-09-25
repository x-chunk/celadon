package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/x-chunk/celadon/internal/config"
)

const testAdminToken = "0123456789abcdef0123456789abcdef"

// adminLoggedIn points the environment at the fake API with an admin token.
func (h *harness) adminLoggedIn() *harness {
	h.t.Setenv(config.EnvAdminToken, testAdminToken)
	h.t.Setenv(config.EnvBaseURL, h.api.srv.URL)
	return h
}

// adminOK answers in the admin API's envelope.
func (f *fakeAPI) adminOK(method, path string, data any) {
	f.on(method, path, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testAdminToken {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"ok":false,"message":"unauthorized"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "message": "success", "data": data})
	})
}

func adminRefuse(status int, message string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "message": message})
	}
}

var sampleReference = map[string]any{
	"tiers":                []any{map[string]any{"tier": "free", "name": "Free"}, map[string]any{"tier": "pro", "name": "Pro", "price_cents": 899, "paid": true}},
	"limits":               []any{map[string]any{"limit": "search:daily", "label": "Searches a day", "unit": "searches"}},
	"top_up_amounts_cents": []int64{500, 5000},
	"campaign_kinds":       []string{"offer", "event"},
	"max_discount_percent": 90, "max_bonus_percent": 500, "unlimited": -1,
}

func TestAdminLoginStoresAVerifiedToken(t *testing.T) {
	h := newHarness(t)
	h.api.adminOK("GET", "/admin/promo/reference", sampleReference)

	r := h.run(testAdminToken+"\n", "admin", "login", "--with-token", "--base-url", h.api.srv.URL, "-p", "ops").wantCode(t, ExitOK)
	if !strings.Contains(r.stderr, "accepts the token") || strings.Contains(r.stdout+r.stderr, testAdminToken) {
		t.Errorf("stderr = %q", r.stderr)
	}
	store, _ := config.Open(h.dir)
	tok, err := store.LoadAdminToken("ops")
	if err != nil || tok != testAdminToken {
		t.Fatalf("stored token = %q, %v", tok, err)
	}
	if store.HasKey("ops") {
		t.Error("an admin login stored an application key")
	}
	cfg, _ := store.Load()
	if cfg.Profiles["ops"].BaseURL != h.api.srv.URL || cfg.DefaultProfile != "ops" {
		t.Errorf("config = %+v", cfg)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(store.AdminTokenPath("ops"))
		if info.Mode().Perm() != 0o600 {
			t.Errorf("token mode = %04o", info.Mode().Perm())
		}
	}

	// The admin-only profile answers the admin commands with no key at all.
	r = h.run("", "admin", "reference").wantCode(t, ExitOK)
	if !strings.Contains(r.stdout, "search:daily") || !strings.Contains(r.stdout, "$50.00") {
		t.Errorf("reference = %q", r.stdout)
	}

	r = h.run("", "admin", "status", "-o", "json").wantCode(t, ExitOK)
	var st adminStatus
	if err := json.Unmarshal([]byte(r.stdout), &st); err != nil || st.State != stateValid || st.TokenFrom != config.SourceFile {
		t.Errorf("status = %q (%v)", r.stdout, err)
	}

	h.run("", "admin", "logout").wantCode(t, ExitUsage)
	h.run("", "admin", "logout", "--yes").wantCode(t, ExitOK)
	if store.HasAdminToken("ops") {
		t.Error("logout left the token")
	}
	h.run("", "admin", "status").wantCode(t, ExitAuth)
}

func TestAdminLoginRefusesARejectedOrShortToken(t *testing.T) {
	h := newHarness(t)
	h.api.adminOK("GET", "/admin/promo/reference", sampleReference)
	r := h.run(strings.Repeat("x", 30)+"\n", "admin", "login", "--with-token", "--base-url", h.api.srv.URL).wantCode(t, ExitAuth)
	if !strings.Contains(r.stderr, "admin token was not accepted") {
		t.Errorf("stderr = %q", r.stderr)
	}
	h.run("short\n", "admin", "login", "--with-token", "--base-url", h.api.srv.URL).wantCode(t, ExitUsage)
	store, _ := config.Open(h.dir)
	if store.HasAdminToken(config.DefaultProfile) {
		t.Error("a refused token was stored")
	}
}

func TestAdminWithoutAToken(t *testing.T) {
	h := newHarness(t)
	r := h.run("", "admin", "reference").wantCode(t, ExitAuth)
	if !strings.Contains(r.stderr, "celadon admin login") {
		t.Errorf("stderr = %q", r.stderr)
	}
}

func TestAdminNotServed(t *testing.T) {
	h := newHarness(t).adminLoggedIn()
	// What Go's router answers for a route nobody registered.
	h.api.on("GET", "/admin/promo/reference", http.NotFound)
	r := h.run("", "admin", "reference").wantCode(t, ExitError)
	if !strings.Contains(r.stderr, "ADMIN_TOKEN") {
		t.Errorf("stderr = %q", r.stderr)
	}
}

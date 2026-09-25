package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/x-chunk/celadon/internal/config"
	"github.com/x-chunk/celadon/internal/iostreams"
)

// ── auth ────────────────────────────────────────────────────────────────

func TestAuthLoginStoresAVerifiedKey(t *testing.T) {
	h := newHarness(t)
	h.api.ok("GET", "/v1/app", sampleApp)

	r := h.run(testKey+"\n", "auth", "login", "--with-token", "--base-url", h.api.srv.URL, "--profile", "work").wantCode(t, ExitOK)
	if !strings.Contains(r.stderr, `application "Reports" (#7)`) {
		t.Errorf("stderr = %q", r.stderr)
	}
	if got := h.api.last().Header.Get("Authorization"); got != "Bearer "+testKey {
		t.Errorf("Authorization = %q", got)
	}

	store, _ := config.Open(h.dir)
	key, err := store.LoadKey("work")
	if err != nil || key != testKey {
		t.Fatalf("stored key = %q, %v", key, err)
	}
	cfg, _ := store.Load()
	if cfg.DefaultProfile != "work" || cfg.Profiles["work"].AppName != "Reports" || cfg.Profiles["work"].BaseURL != h.api.srv.URL {
		t.Errorf("config = %+v", cfg)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(store.KeyPath("work"))
		if info.Mode().Perm() != 0o600 {
			t.Errorf("key mode = %04o", info.Mode().Perm())
		}
	}
	if strings.Contains(r.stdout+r.stderr, testKey) {
		t.Error("the key was printed")
	}
}

func TestAuthLoginRefusesARejectedKey(t *testing.T) {
	h := newHarness(t)
	h.api.on("GET", "/v1/app", func(w http.ResponseWriter, _ *http.Request) {
		refuse(w, http.StatusUnauthorized, "unauthorized", "invalid api key")
	})
	r := h.run(testKey+"\n", "auth", "login", "--with-token", "--base-url", h.api.srv.URL).wantCode(t, ExitAuth)
	if !strings.Contains(r.stderr, "invalid api key") || !strings.Contains(r.stderr, "hint:") {
		t.Errorf("stderr = %q", r.stderr)
	}
	store, _ := config.Open(h.dir)
	if store.HasKey(config.DefaultProfile) {
		t.Error("a rejected key was stored")
	}
}

func TestAuthLoginRefusesSomethingThatIsNotAKey(t *testing.T) {
	h := newHarness(t)
	h.run("sk_live_nope\n", "auth", "login", "--with-token", "--base-url", h.api.srv.URL).wantCode(t, ExitUsage)
	if h.api.calls() != 0 {
		t.Error("a malformed key was sent to the API")
	}
}

func TestAuthLoginWithoutATerminalNeedsWithToken(t *testing.T) {
	h := newHarness(t)
	r := h.run("", "auth", "login", "--base-url", h.api.srv.URL).wantCode(t, ExitUsage)
	if !strings.Contains(r.stderr, "--with-token") {
		t.Errorf("stderr = %q", r.stderr)
	}
}

func TestAuthLoginPromptsOnATerminal(t *testing.T) {
	h := newHarness(t)
	h.api.ok("GET", "/v1/app", sampleApp)
	r := h.runWith(func(s *iostreams.Streams) {
		s.InTTY, s.ErrTTY = true, true
		s.SetSecretReader(func() (string, error) { return testKey, nil })
	}, "", "auth", "login", "--base-url", h.api.srv.URL).wantCode(t, ExitOK)
	if !strings.Contains(r.stderr, "Key: ") {
		t.Errorf("no prompt: %q", r.stderr)
	}
}

func TestAuthStatusLogoutAndSwitch(t *testing.T) {
	h := newHarness(t)
	h.api.ok("GET", "/v1/app", sampleApp)
	h.run(testKey+"\n", "auth", "login", "--with-token", "--base-url", h.api.srv.URL, "-p", "one").wantCode(t, ExitOK)
	h.run(testKey+"\n", "auth", "login", "--with-token", "--base-url", h.api.srv.URL, "-p", "two").wantCode(t, ExitOK)

	r := h.run("", "auth", "status", "-o", "json").wantCode(t, ExitOK)
	var rows []profileStatus
	if err := json.Unmarshal([]byte(r.stdout), &rows); err != nil {
		t.Fatalf("status json %q: %v", r.stdout, err)
	}
	if len(rows) != 2 || !rows[0].Active || !rows[0].Default || rows[0].State != stateValid || rows[1].Active {
		t.Errorf("status = %+v", rows)
	}
	if strings.Contains(r.stdout, testKey) {
		t.Error("status printed the whole key")
	}

	h.run("", "auth", "switch", "two").wantCode(t, ExitOK)
	h.run("", "auth", "switch", "three").wantCode(t, ExitError)
	r = h.run("", "auth", "token").wantCode(t, ExitOK)
	if strings.TrimSpace(r.stdout) != testKey {
		t.Errorf("token = %q", r.stdout)
	}

	h.run("", "auth", "logout").wantCode(t, ExitUsage)
	h.run("", "auth", "logout", "--yes").wantCode(t, ExitOK)
	store, _ := config.Open(h.dir)
	cfg, _ := store.Load()
	if _, ok := cfg.Profiles["two"]; ok || store.HasKey("two") || cfg.DefaultProfile != "one" {
		t.Errorf("after logout: %+v", cfg)
	}
}

func TestAuthStatusFailsWhenTheActiveKeyIsRejected(t *testing.T) {
	h := newHarness(t)
	h.api.ok("GET", "/v1/app", sampleApp)
	h.run(testKey+"\n", "auth", "login", "--with-token", "--base-url", h.api.srv.URL).wantCode(t, ExitOK)
	h.api.on("GET", "/v1/app", func(w http.ResponseWriter, _ *http.Request) {
		refuse(w, http.StatusUnauthorized, "unauthorized", "invalid api key")
	})
	r := h.run("", "auth", "status").wantCode(t, ExitAuth)
	if !strings.Contains(r.stdout, "rejected") {
		t.Errorf("stdout = %q", r.stdout)
	}
	h.run("", "auth", "status", "--offline").wantCode(t, ExitOK)
}

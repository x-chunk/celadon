package cli

import (
	"strings"
	"testing"
)

// ── insights and settings ───────────────────────────────────────────────

func TestSettingsReadWithoutFlagsAndWriteWithThem(t *testing.T) {
	h := newHarness(t).loggedIn()
	retention := map[string]any{"mode": "rotate", "ttl_seconds": 604800, "can_ttl": true, "archive": map[string]any{"limit": 1000, "used": 10, "unit": "messages"}}
	h.api.ok("GET", "/v1/settings/retention", retention)
	h.api.ok("PATCH", "/v1/settings/retention", retention)
	h.api.ok("PATCH", "/v1/settings/language", map[string]any{"effective": "en"})
	h.api.ok("PATCH", "/v1/settings/vault", map[string]any{"reveal_ttl_seconds": 300})

	r := h.run("", "settings", "retention").wantCode(t, ExitOK)
	if h.api.last().Method != "GET" || !strings.Contains(r.stdout, "7d") {
		t.Errorf("%s, stdout = %q", h.api.last().Method, r.stdout)
	}

	h.run("", "settings", "retention", "--mode", "rotate", "--ttl", "7d", "--in-chat=false").wantCode(t, ExitOK)
	if b := decodeBody(t, h.api.last().Body); b["mode"] != "rotate" || b["ttl_seconds"] != float64(604800) || b["in_chat"] != false {
		t.Errorf("retention body = %v", b)
	}
	h.run("", "settings", "retention", "--ttl", "off").wantCode(t, ExitOK)
	if b := decodeBody(t, h.api.last().Body); b["ttl_seconds"] != float64(0) || len(b) != 1 {
		t.Errorf("ttl off body = %v", b)
	}
	h.run("", "settings", "retention", "--mode", "shred").wantCode(t, ExitUsage)

	h.run("", "settings", "language", "--auto").wantCode(t, ExitOK)
	if b := decodeBody(t, h.api.last().Body); b["language"] != "" {
		t.Errorf("language body = %v", b)
	}
	h.run("", "settings", "vault", "--reveal-ttl", "5m").wantCode(t, ExitOK)
	if b := decodeBody(t, h.api.last().Body); b["reveal_ttl_seconds"] != float64(300) {
		t.Errorf("vault body = %v", b)
	}
}

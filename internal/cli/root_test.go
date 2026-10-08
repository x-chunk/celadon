package cli

import (
	"net/http"
	"strings"
	"testing"
)

func TestUsageErrors(t *testing.T) {
	h := newHarness(t).loggedIn()
	h.run("", "--output", "yaml", "app", "view").wantCode(t, ExitUsage)
	h.run("", "no-such-command").wantCode(t, ExitUsage)
	h.run("", "message", "view").wantCode(t, ExitUsage)
	h.run("", "settings", "language", "--set", "en", "--auto").wantCode(t, ExitUsage)
	h.run("", "search", "--nope").wantCode(t, ExitUsage)
	r := h.run("", "version", "-o", "json").wantCode(t, ExitOK)
	if decodeBody(t, []byte(r.stdout))["version"] == "" {
		t.Errorf("version = %q", r.stdout)
	}
}

func TestRootWithoutATerminalPrintsHelp(t *testing.T) {
	h := newHarness(t)
	r := h.run("").wantCode(t, ExitOK)
	if !strings.Contains(r.stdout, "Usage:") {
		t.Errorf("stdout = %q", r.stdout)
	}
	h.run("", "tui").wantCode(t, ExitError)
}

func TestARefusalIsExplainedAndSanitized(t *testing.T) {
	h := newHarness(t).loggedIn()
	h.api.on("GET", "/v1/messages/1/versions", func(w http.ResponseWriter, _ *http.Request) {
		refuseWith(w, http.StatusForbidden, map[string]any{
			"code": "forbidden", "message": "the plan does not open the edit history",
			"permission": "messages:versions", "required_plan": "max\x1b]0;pwned\x07",
		})
	})
	r := h.run("", "message", "versions", "1").wantCode(t, ExitError)
	if !strings.Contains(r.stderr, "does not open messages:versions; max") {
		t.Errorf("stderr = %q", r.stderr)
	}
	if strings.Contains(r.stderr, "\x1b") {
		t.Error("an escape sequence from the API reached the terminal")
	}
}

func TestAKeyThatMayOnlyReadIsToldWhereToOpenTheScope(t *testing.T) {
	h := newHarness(t).loggedIn()
	h.api.on("PATCH", "/v1/settings/language", func(w http.ResponseWriter, _ *http.Request) {
		refuseWith(w, http.StatusForbidden, map[string]any{
			"code": "scope_required", "scope": "settings:write", "message": "this key may not change settings",
		})
	})
	r := h.run("", "settings", "language", "--auto").wantCode(t, ExitError)
	if !strings.Contains(r.stderr, "(settings:write) to this key") || !strings.Contains(r.stderr, "screen in the bot") {
		t.Errorf("stderr = %q", r.stderr)
	}
}

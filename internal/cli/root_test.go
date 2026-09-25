package cli

import (
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

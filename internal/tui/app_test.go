package tui

import (
	"net/http"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"go.xchunk.org/teal"
)

func TestOverviewLoadsOnStart(t *testing.T) {
	d := newDriver(t)
	d.wantView("Reports", "balance $4.20", "search:daily", "35 / 50", "Ultra", "1 Overview", "work", "does not lapse", "never")
}

func TestQuit(t *testing.T) {
	d := newDriver(t)
	d.keys("q")
	if !d.quit {
		t.Error("q did not quit")
	}
}

func TestHelpAndASmallTerminal(t *testing.T) {
	d := newDriver(t)
	d.keys("?")
	d.wantView("Everywhere", "1 Overview")
	d.keys("x")
	d.wantView("Application")

	d.send(tea.WindowSizeMsg{Width: 40, Height: 10})
	d.wantView("at least 60×16")
}

func TestOverviewDrawsTheSpendingOfEachDay(t *testing.T) {
	d := newDriver(t)
	credits := func(n int) map[string]any { return map[string]any{"credits": n} }
	d.api.ok("GET", "/v1/usage", map[string]any{
		"from": "2026-09-20", "to": "2026-09-25", "calls": 3, "credits": credits(3000),
		"ops": []any{map[string]any{"op": "search:query", "calls": 3, "credits": credits(3000)}},
		"days": []any{
			map[string]any{"day": "2026-09-25", "op": "search:query", "calls": 2, "credits": credits(2000)},
			map[string]any{"day": "2026-09-21", "op": "search:query", "calls": 1, "credits": credits(1000)},
		},
	})
	d.keys("r")
	if q := d.api.called("GET", "/v1/usage"); len(q) == 0 || !strings.Contains(q[len(q)-1].query, "by_day=true") {
		t.Fatalf("usage was asked for without its days: %v", q)
	}
	d.wantView("per day")
}

func TestDailySpend(t *testing.T) {
	u := teal.Usage{From: "2026-09-20", To: "2026-09-23", Days: []teal.UsageOp{
		{Day: "2026-09-23", Op: "search:query", Credits: teal.Money{Credits: 500}},
		{Day: "2026-09-23", Op: "messages:read", Credits: teal.Money{Credits: 250}},
		{Day: "2026-09-21", Op: "search:query", Credits: teal.Money{Credits: 100}},
		{Day: "2026-08-01", Op: "search:query", Credits: teal.Money{Credits: 999}},
	}}
	got := dailySpend(u)
	want := []float64{0, 100, 0, 750}
	if len(got) != len(want) {
		t.Fatalf("dailySpend = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("dailySpend = %v, want %v", got, want)
			break
		}
	}
	if dailySpend(teal.Usage{From: "2026-09-25", To: "2026-09-20"}) != nil {
		t.Error("a window that ends before it starts was drawn")
	}
}

func TestTheInterfaceSanitizesWhatTheAPISends(t *testing.T) {
	d := newDriver(t)
	const evil = "\x1b]0;pwned\x07\x1b[2J"
	clean := func(where string) {
		t.Helper()
		if v := d.view(); strings.ContainsAny(v, "\x1b\x07") {
			t.Errorf("%s: a control from the API reached the screen:\n%q", where, v)
		}
	}
	money := func(display string) map[string]any { return map[string]any{"credits": 1, "display": display} }
	d.api.ok("GET", "/v1/app", map[string]any{"id": 7, "name": "Reports", "billing": "credits" + evil, "balance": money("$1" + evil)})
	d.api.ok("GET", "/v1/account", map[string]any{
		"plan":   map[string]any{"tier": "max", "name": "Max"},
		"quotas": []any{map[string]any{"key": "search:daily" + evil, "limit": 50, "used": 35, "window": "day" + evil}},
	})
	d.api.ok("GET", "/v1/usage", map[string]any{
		"from": "2026-08-26" + evil, "to": "2026-09-25", "calls": 3, "credits": money("$x" + evil),
		"ops": []any{map[string]any{"op": "search:query" + evil, "calls": 3, "credits": money("$y" + evil)}},
	})
	d.keys("r")
	d.wantView("search:daily", "search:query")
	clean("overview")

	d.api.on("GET", "/v1/actions", func(w http.ResponseWriter, _ *http.Request) {
		refuse(w, http.StatusInternalServerError, "internal", "boom"+evil)
	})
	d.open("Actions")
	d.wantView("boom")
	clean("status line")

	d.api.ok("GET", "/v1/actions", map[string]any{"prefix": "." + evil, "actions": []any{map[string]any{"id": 1, "name": "hi", "body": "Hi"}}, "quota": map[string]any{"limit": 25}})
	d.api.ok("GET", "/v1/actions/placeholders", []any{map[string]any{"token": "[[X]]" + evil, "level": "basic" + evil, "required_plan": "max" + evil}})
	d.keys("r")
	clean("actions")
	d.keys("p")
	d.wantView("[[X]]", "needs max")
	clean("placeholders")
}

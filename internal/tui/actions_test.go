package tui

import (
	"net/http"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTabsLoadLazilyAndOnce(t *testing.T) {
	d := newDriver(t)
	d.api.ok("GET", "/v1/actions", map[string]any{"prefix": ".", "actions": []any{}, "quota": map[string]any{"limit": 25}})
	if n := len(d.api.called("GET", "/v1/actions")); n != 0 {
		t.Fatalf("a tab nobody opened loaded: %d calls", n)
	}
	d.open("Actions")
	d.open("Overview")
	d.open("Actions")
	if n := len(d.api.called("GET", "/v1/actions")); n != 1 {
		t.Errorf("the actions tab loaded %d times", n)
	}
	d.wantView("No shortcuts yet")
	d.key(tea.KeyTab)
	if d.activeTitle() == "Actions" {
		t.Error("tab did not move to the next tab")
	}
}

func TestActionsCreateEditDelete(t *testing.T) {
	d := newDriver(t)
	action := map[string]any{"id": 12, "name": "hi", "body": "Hi [[YOU_FIRST]]!", "uses": 3}
	d.api.ok("GET", "/v1/actions", map[string]any{"prefix": ".", "actions": []any{action}, "quota": map[string]any{"limit": 25, "used": 1}})
	d.api.ok("POST", "/v1/actions", action)
	d.api.ok("PATCH", "/v1/actions/12", action)
	d.api.ok("DELETE", "/v1/actions/12", map[string]any{})

	d.open("Actions")
	d.wantView(".hi", "Hi [[YOU_FIRST]]!", "1 / 25")

	d.keys("n")
	d.keys("bye")
	d.key(tea.KeyTab)
	d.keys("See you, [[YOU_FIRST]]")
	d.key(tea.KeyCtrlS)
	if b := d.body("POST", "/v1/actions"); b["name"] != "bye" || b["body"] != "See you, [[YOU_FIRST]]" {
		t.Errorf("create body = %v", b)
	}
	if n := len(d.api.called("GET", "/v1/actions")); n != 2 {
		t.Errorf("the list was loaded %d times, want a reload after saving", n)
	}

	d.keys("e")
	d.key(tea.KeyTab)
	d.keys(" Bye.")
	d.key(tea.KeyCtrlS)
	if b := d.body("PATCH", "/v1/actions/12"); b["name"] != nil || b["body"] != "Hi [[YOU_FIRST]]! Bye." {
		t.Errorf("edit body = %v", b)
	}

	d.keys("d")
	d.wantView(`Delete "hi"?`)
	d.keys("y")
	if len(d.api.called("DELETE", "/v1/actions/12")) != 1 {
		t.Error("y did not delete")
	}
}

func TestErrorsReachTheStatusLine(t *testing.T) {
	d := newDriver(t)
	d.api.on("GET", "/v1/actions", func(w http.ResponseWriter, _ *http.Request) {
		refuse(w, http.StatusUnauthorized, "unauthorized", "invalid api key")
	})
	d.open("Actions")
	d.wantView("invalid api key", "celadon auth login")
}

func TestASuccessClearsTheLastFailure(t *testing.T) {
	d := newDriver(t)
	d.api.on("GET", "/v1/actions", func(w http.ResponseWriter, _ *http.Request) {
		refuse(w, http.StatusInternalServerError, "internal", "boom")
	})
	d.open("Actions")
	d.wantView("boom")
	d.open("Overview")
	d.keys("r")
	if strings.Contains(d.view(), "boom") {
		t.Error("a failure stayed in the status line after a call went through")
	}
}

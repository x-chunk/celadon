package tui

import (
	"net/http"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestArchiveChatSearchAndPortraitJump(t *testing.T) {
	d := newDriver(t)
	d.api.ok("GET", "/v1/chats", map[string]any{"chats": []any{map[string]any{"id": -100, "title": "Ann", "messages": 12}}, "total": 1})
	d.api.ok("POST", "/v1/messages/search", map[string]any{"messages": []any{}, "total": 0, "pages": 0})
	d.api.ok("GET", "/v1/portraits/-100", map[string]any{"chat_id": -100, "subject": map[string]any{"title": "Ann"}, "archetype": map[string]any{"label": "The Essayist", "fit": 0.8}})
	d.api.ok("GET", "/v1/insights", map[string]any{"insights": []string{"You have 12 messages."}})

	d.open("Archive")
	d.key(tea.KeyEnter)
	if body := d.body("POST", "/v1/messages/search"); body["chat"] != float64(-100) {
		t.Errorf("chat search body = %v", body)
	}
	d.wantView("No messages match", "in Ann")

	d.keys("h")
	d.keys("p")
	if d.activeTitle() != "Insights" {
		t.Fatalf("p opened %s", d.activeTitle())
	}
	d.wantView("The Essayist (fit 80%)", "You have 12 messages.")
}

func TestPortraitRetriesWhileItIsBuilt(t *testing.T) {
	d := newDriver(t)
	d.api.ok("GET", "/v1/insights", map[string]any{"insights": []string{}})
	calls := 0
	d.api.on("GET", "/v1/portraits/5", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "1")
			refuse(w, http.StatusNotFound, "not_found", "being built")
			return
		}
		answer(w, map[string]any{"chat_id": 5, "summary": "Short and fast.", "archetype": map[string]any{"label": "The Sprinter", "fit": 0.5}})
	})
	d.open("Insights")
	d.keys("/")
	d.keys("5")
	d.key(tea.KeyEnter)
	if calls != 2 {
		t.Errorf("the portrait was asked for %d times", calls)
	}
	d.wantView("The Sprinter", "Short and fast.")
}

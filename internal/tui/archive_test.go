package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestArchiveSearchAndMessageCard(t *testing.T) {
	d := newDriver(t)
	d.api.ok("GET", "/v1/chats", map[string]any{"chats": []any{map[string]any{"id": -100, "title": "Ann Weber", "messages": 12}}, "total": 1})
	d.api.ok("POST", "/v1/messages/search", map[string]any{
		"messages": []any{map[string]any{"id": 90210, "chat": -100, "text": "the invoice \x1b]0;pwned\x07is attached", "sender_username": "ann", "created_at": "2026-09-01T09:14:02Z", "versions": 2}},
		"total":    1, "pages": 1,
	})
	d.api.ok("GET", "/v1/messages/90210", map[string]any{"id": 90210, "chat": -100, "text": "the invoice is attached", "sender_username": "ann", "versions": 2})
	d.api.ok("GET", "/v1/messages/90210/versions", map[string]any{"versions": []any{map[string]any{"text": "the invoice is attached", "at": 1788000000, "current": true}, map[string]any{"text": "the invoce", "at": 1787000000}}})

	d.open("Archive")
	d.wantView("Ann Weber")

	d.keys("/")
	d.keys("invoice suser=ann q")
	if d.quit || d.activeTitle() != "Archive" {
		t.Fatal("keys typed into the search box acted as shortcuts")
	}
	d.key(tea.KeyEnter)
	body := d.body("POST", "/v1/messages/search")
	conds := body["conditions"].([]any)
	if len(conds) != 2 || conds[0].(map[string]any)["value"] != "invoice q" || conds[1].(map[string]any)["field"] != "suser" {
		t.Errorf("conditions = %v", conds)
	}
	d.wantView("1 match", "@ann")
	if strings.Contains(d.view(), "\x1b]0;") {
		t.Error("an escape sequence from the archive reached the screen")
	}

	d.key(tea.KeyEnter)
	d.wantView("Message #90210", "press v")
	d.keys("v")
	d.wantView("Edit history", "the invoce")
	d.key(tea.KeyEsc)
	d.wantView("Results")
}

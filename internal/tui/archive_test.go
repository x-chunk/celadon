package tui

import (
	"net/http"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestArchiveSearchAndMessageCard(t *testing.T) {
	d := newDriver(t)
	d.api.ok("GET", "/v1/chats", map[string]any{"chats": []any{map[string]any{"id": -100, "title": "Ann Weber", "messages": 12}}, "total": 1})
	d.api.ok("POST", "/v1/messages/search", map[string]any{
		"messages": []any{map[string]any{"id": 90210, "chat": -100, "text": "the invoice \x1b]0;pwned\x07is attached", "sender_username": "ann", "created_at": "2026-09-01T09:14:02Z", "versions": 1, "reply_to_id": 90200}},
		"total":    1, "pages": 1,
	})
	// A read by id need not count the edits; the search's count stands.
	d.api.ok("GET", "/v1/messages/90210", map[string]any{"id": 90210, "chat": -100, "text": "the invoice is attached", "sender_username": "ann", "first_name": "Ann", "reply_to_id": 90200})
	d.api.ok("GET", "/v1/messages/90200", map[string]any{"id": 90200, "chat": -100, "text": "did you send the invoice?", "sender_username": "me"})
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
	d.wantView("Message #90210", "Edited once", "press v", "-100 · Ann", "Answers", "#90200")
	d.keys("v")
	d.wantView("Edit history", "the invoce")
	d.keys("r")
	d.wantView("Message #90200", "did you send the invoice?")
	d.key(tea.KeyEsc)
	d.wantView("Results")
}

func TestArchivePagesAreTurnedForFree(t *testing.T) {
	d := newDriver(t)
	d.api.ok("GET", "/v1/chats", map[string]any{"chats": []any{}, "total": 0})
	page := func(n int) map[string]any {
		return map[string]any{
			"messages": []any{map[string]any{"id": 100 + n, "text": "match on page " + string(rune('1'+n)), "sender_username": "ann"}},
			"total":    3, "page": n, "pages": 3, "query_id": "q1",
		}
	}
	d.api.ok("POST", "/v1/messages/search", page(0))
	d.api.ok("POST", "/v1/messages/search/page", page(1))

	d.open("Archive")
	d.keys("/")
	d.keys("invoice")
	d.key(tea.KeyEnter)
	d.wantView("page 1/3", "match on page 1")

	d.keys("]")
	if b := d.body("POST", "/v1/messages/search/page"); b["query_id"] != "q1" || b["page"] != float64(1) {
		t.Errorf("page body = %v", b)
	}
	if n := len(d.api.called("POST", "/v1/messages/search")); n != 1 {
		t.Errorf("turning a page ran the search again: %d searches", n)
	}
	d.wantView("page 2/3", "match on page 2")

	// A query that has expired is run again, at the page asked for.
	d.api.on("POST", "/v1/messages/search/page", func(w http.ResponseWriter, _ *http.Request) {
		refuse(w, http.StatusNotFound, "not_found", "no such query")
	})
	d.api.ok("POST", "/v1/messages/search", page(2))
	d.keys("]")
	if b := d.body("POST", "/v1/messages/search"); b["page"] != float64(2) {
		t.Errorf("search body = %v", b)
	}
	d.wantView("page 3/3", "match on page 3")
}

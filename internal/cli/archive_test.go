package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ── archive ─────────────────────────────────────────────────────────────

var sampleSearch = map[string]any{
	"messages": []any{map[string]any{
		"id": 90210, "message_id": 4471, "chat": -1001234567890, "sender": 1256738876,
		"text": "the invoice \x1b[31mis attached", "sender_username": "ann",
		"created_at": "2026-09-01T09:14:02Z", "versions": 2,
	}},
	"total": 37, "page": 1, "pages": 4, "per_page": 10,
}

func TestSearchBuildsTheQueryInOrder(t *testing.T) {
	h := newHarness(t).loggedIn()
	h.api.ok("POST", "/v1/messages/search", sampleSearch)

	r := h.run("", "search", "the", "invoice", "--where", "suser=ann", "--or", "sender=1256738876", "--chat", "-1001234567890", "--page", "2").wantCode(t, ExitOK)

	body := decodeBody(t, h.api.last().Body)
	if body["chat"] != float64(-1001234567890) || body["page"] != float64(1) {
		t.Errorf("body = %v", body)
	}
	conds := body["conditions"].([]any)
	want := []map[string]any{
		{"field": "text", "mode": "ct", "value": "the invoice"},
		{"field": "suser", "mode": "eq", "value": "ann", "conn": "and"},
		{"field": "sender", "mode": "eq", "value": "1256738876", "conn": "or"},
	}
	if len(conds) != len(want) {
		t.Fatalf("conditions = %v", conds)
	}
	for i, w := range want {
		got := conds[i].(map[string]any)
		for k, v := range w {
			if got[k] != v {
				t.Errorf("condition %d: %s = %v, want %v", i, k, got[k], v)
			}
		}
		if _, has := got["conn"]; i == 0 && has {
			t.Error("the first condition carries a conn")
		}
	}

	if !strings.Contains(r.stdout, "90210") || !strings.Contains(r.stdout, "@ann") || !strings.Contains(r.stdout, "(edited ×1)") {
		t.Errorf("stdout = %q", r.stdout)
	}
	if strings.Contains(r.stdout, "\x1b") {
		t.Error("an escape sequence from the archive reached the terminal")
	}
	if !strings.Contains(r.stderr, "page 2 of 4 · 37 matches") {
		t.Errorf("stderr = %q", r.stderr)
	}
}

func TestSearchRejectsABadFilterBeforeCallingTheAPI(t *testing.T) {
	h := newHarness(t).loggedIn()
	for _, args := range [][]string{
		{"search", "--where", "nothing"},
		{"search", "--where", "=x"},
		{"search", "--where", "Bad Field=x"},
		{"search", "--page", "0"},
		{"search", "--chat", "abc"},
	} {
		h.run("", args...).wantCode(t, ExitUsage)
	}
	if h.api.calls() != 0 {
		t.Error("an invalid query reached the API")
	}
}

func TestSearchExplainsASpentQuota(t *testing.T) {
	h := newHarness(t).loggedIn()
	h.api.on("POST", "/v1/messages/search", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": map[string]any{
			"code": "quota_exhausted", "message": "daily searches used up",
			"limit": "search:daily", "used": 50, "limit_value": 50,
			"reset_at": time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC).Unix(),
		}})
	})
	r := h.run("", "search", "x").wantCode(t, ExitError)
	if !strings.Contains(r.stderr, "quota search:daily is used up (50 of 50)") || !strings.Contains(r.stderr, "it resets") {
		t.Errorf("stderr = %q", r.stderr)
	}
	if h.api.calls() != 1 {
		t.Errorf("a spent quota was retried: %d calls", h.api.calls())
	}
}

func TestCount(t *testing.T) {
	h := newHarness(t).loggedIn()
	h.api.ok("POST", "/v1/messages/count", map[string]any{"total": 412})
	r := h.run("", "count", "--where", "media=photo").wantCode(t, ExitOK)
	if strings.TrimSpace(r.stdout) != "412" {
		t.Errorf("stdout = %q", r.stdout)
	}
}

func TestExportWritesAWholeDocument(t *testing.T) {
	h := newHarness(t).loggedIn()
	doc := `{"account_id":1,"total":2,"messages":[{"id":1},{"id":2}]}`
	h.api.on("POST", "/v1/messages/export", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Aether-Export-Total", "2")
		io.WriteString(w, doc)
	})
	file := filepath.Join(t.TempDir(), "out.json")
	r := h.run("", "export", "--chat", "5", "-f", file).wantCode(t, ExitOK)
	got, err := os.ReadFile(file)
	if err != nil || string(got) != doc {
		t.Fatalf("file = %q, %v", got, err)
	}
	if !strings.Contains(r.stderr, "Exported 2 messages") {
		t.Errorf("stderr = %q", r.stderr)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(file)
		if info.Mode().Perm() != 0o600 {
			t.Errorf("export mode = %04o", info.Mode().Perm())
		}
	}

	h.run("", "export", "-f", file).wantCode(t, ExitUsage)
	h.run("", "export", "-f", file, "--force").wantCode(t, ExitOK)

	r = h.run("", "export").wantCode(t, ExitOK)
	if r.stdout != doc {
		t.Errorf("stdout export = %q", r.stdout)
	}
}

func TestExportSaysWhenTheStreamWasCut(t *testing.T) {
	h := newHarness(t).loggedIn()
	h.api.on("POST", "/v1/messages/export", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"total":2,"messages":[{"id":1},`)
	})
	file := filepath.Join(t.TempDir(), "out.json")
	r := h.run("", "export", "-f", file).wantCode(t, ExitError)
	if !strings.Contains(r.stderr, "50 MB cap") {
		t.Errorf("stderr = %q", r.stderr)
	}
	if _, err := os.Stat(file); err != nil {
		t.Error("the part that did arrive was thrown away")
	}
}

func TestChatsAndMessages(t *testing.T) {
	h := newHarness(t).loggedIn()
	h.api.ok("GET", "/v1/chats", map[string]any{"chats": []any{map[string]any{"id": -100, "title": "Ann", "messages": 12}}, "total": 1, "page": 1})
	h.api.ok("GET", "/v1/messages/90210", map[string]any{"id": 90210, "chat": -100, "text": "line one\nline two", "sender_username": "ann"})
	h.api.ok("GET", "/v1/messages/90210/versions", map[string]any{"versions": []any{
		map[string]any{"text": "now", "at": 1788000000, "current": true},
		map[string]any{"text": "before", "at": 1787000000},
	}})

	r := h.run("", "chats", "--page", "2").wantCode(t, ExitOK)
	if h.api.last().Query != "page=1" || !strings.Contains(r.stdout, "Ann") {
		t.Errorf("query = %q, stdout = %q", h.api.last().Query, r.stdout)
	}
	r = h.run("", "message", "view", "90210").wantCode(t, ExitOK)
	if !strings.Contains(r.stdout, "line one\nline two") {
		t.Errorf("stdout = %q", r.stdout)
	}
	r = h.run("", "msg", "versions", "90210").wantCode(t, ExitOK)
	if !strings.Contains(r.stdout, "(current)") || !strings.Contains(r.stdout, "before") {
		t.Errorf("stdout = %q", r.stdout)
	}
	h.run("", "message", "view", "abc").wantCode(t, ExitUsage)
}

func TestPortraitWaitsForAPortraitUnderConstruction(t *testing.T) {
	h := newHarness(t).loggedIn()
	calls := 0
	h.api.on("GET", "/v1/portraits/-100", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "1")
			refuse(w, http.StatusNotFound, "not_found", "portrait is being built")
			return
		}
		answer(w, map[string]any{"chat_id": -100, "summary": "Writes long.", "archetype": map[string]any{"label": "The Essayist", "fit": 0.8}})
	})
	r := h.run("", "portrait", "--", "-100").wantCode(t, ExitError)
	if !strings.Contains(r.stderr, "--wait") {
		t.Errorf("stderr = %q", r.stderr)
	}
	calls = 0
	r = h.run("", "portrait", "--wait", "--", "-100").wantCode(t, ExitOK)
	if !strings.Contains(r.stdout, "The Essayist (fit 80%)") || calls != 2 {
		t.Errorf("stdout = %q after %d calls", r.stdout, calls)
	}
}

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
		"created_at": "2026-09-01T09:14:02Z", "versions": 1, "reply_to_id": 90200,
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

	if !strings.Contains(r.stdout, "90210") || !strings.Contains(r.stdout, "@ann") || !strings.Contains(r.stdout, "↩ the invoice") || !strings.Contains(r.stdout, "(edited ×1)") {
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

func TestExportKeepsNothingOfADocumentCutShort(t *testing.T) {
	h := newHarness(t).loggedIn()
	dir := t.TempDir()
	file := filepath.Join(dir, "out.json")
	nothingKept := func() {
		t.Helper()
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Errorf("left behind: %v, %v", entries, err)
		}
	}

	// The API breaks the connection of an export it fails part way through.
	h.api.on("POST", "/v1/messages/export", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		io.WriteString(w, `{"total":2,"messages":[{"id":1},`)
	})
	r := h.run("", "export", "-f", file).wantCode(t, ExitError)
	if !strings.Contains(r.stderr, "cut off") || !strings.Contains(r.stderr, "refunded") {
		t.Errorf("stderr = %q", r.stderr)
	}
	nothingKept()

	// A stream that ends cleanly before its document does is no more use.
	h.api.on("POST", "/v1/messages/export", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"total":2,"messages":[{"id":1},`)
	})
	r = h.run("", "export", "-f", file).wantCode(t, ExitError)
	if !strings.Contains(r.stderr, "before its document did") {
		t.Errorf("stderr = %q", r.stderr)
	}
	nothingKept()

	// One that would pass the cap is refused before a byte is sent.
	h.api.on("POST", "/v1/messages/export", func(w http.ResponseWriter, _ *http.Request) {
		refuse(w, http.StatusRequestEntityTooLarge, "too_large", "the export would pass 50 MB")
	})
	r = h.run("", "export", "-f", file).wantCode(t, ExitError)
	if !strings.Contains(r.stderr, "capped at 50 MB") || !strings.Contains(r.stderr, "nothing was sent or charged") {
		t.Errorf("stderr = %q", r.stderr)
	}
	nothingKept()
}

func TestChatsAndMessages(t *testing.T) {
	h := newHarness(t).loggedIn()
	h.api.ok("GET", "/v1/chats", map[string]any{"chats": []any{map[string]any{"id": -100, "title": "Ann", "messages": 12}}, "total": 1, "page": 1})
	h.api.ok("GET", "/v1/messages/90210", map[string]any{
		"id": 90210, "chat": -100, "text": "line one\nline two", "sender_username": "ann", "sender_name": "Ann Weber",
		"first_name": "Ann", "last_name": "Weber", "username": "ann", "reply_to_message_id": 4470,
		"deleted": true, "deleted_at": 1788000000,
	})
	h.api.ok("GET", "/v1/messages/90210/versions", map[string]any{"versions": []any{
		map[string]any{"text": "now", "at": 1788000000, "current": true},
		map[string]any{"text": "before", "at": 1787000000},
	}})

	r := h.run("", "chats", "--page", "2").wantCode(t, ExitOK)
	if h.api.last().Query != "page=1" || !strings.Contains(r.stdout, "Ann") {
		t.Errorf("query = %q, stdout = %q", h.api.last().Query, r.stdout)
	}
	r = h.run("", "message", "view", "90210").wantCode(t, ExitOK)
	for _, want := range []string{"line one\nline two", "-100 · Ann Weber (@ann)", "Ann Weber (@ann)", "Telegram message 4470, not in the archive", "deleted in Telegram 2026-"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, r.stdout)
		}
	}
	r = h.run("", "msg", "versions", "90210").wantCode(t, ExitOK)
	if !strings.Contains(r.stdout, "(current)") || !strings.Contains(r.stdout, "before") {
		t.Errorf("stdout = %q", r.stdout)
	}
	h.run("", "message", "view", "abc").wantCode(t, ExitUsage)
}

func TestPortraitWaitsForAModelStillInTraining(t *testing.T) {
	h := newHarness(t).loggedIn()
	calls := 0
	h.api.on("GET", "/v1/portraits/-100", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "1")
			refuseWith(w, http.StatusServiceUnavailable, map[string]any{
				"code": "unavailable", "reason": "model_training", "message": "the model is being fitted",
			})
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

func TestPortraitDoesNotWaitForAChatTooShort(t *testing.T) {
	h := newHarness(t).loggedIn()
	h.api.on("GET", "/v1/portraits/5", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "1")
		refuseWith(w, http.StatusNotFound, map[string]any{
			"code": "not_found", "reason": "not_enough_history", "message": "not enough history",
		})
	})
	r := h.run("", "portrait", "--wait", "5").wantCode(t, ExitError)
	if !strings.Contains(r.stderr, "too short to portray") || h.api.calls() != 1 {
		t.Errorf("stderr = %q after %d calls", r.stderr, h.api.calls())
	}
}

func TestSearchTurnsPagesForFreeByTheQueryID(t *testing.T) {
	h := newHarness(t).loggedIn()
	first := map[string]any{"messages": sampleSearch["messages"], "total": 37, "page": 0, "pages": 4, "per_page": 10, "query_id": "q_7f3a9c"}
	h.api.ok("POST", "/v1/messages/search", first)
	h.api.ok("POST", "/v1/messages/search/page", map[string]any{"messages": sampleSearch["messages"], "total": 37, "page": 2, "pages": 4, "query_id": "q_7f3a9c"})

	r := h.run("", "search", "invoice").wantCode(t, ExitOK)
	if !strings.Contains(r.stderr, "celadon search --query-id q_7f3a9c --page 2") {
		t.Errorf("stderr = %q", r.stderr)
	}

	r = h.run("", "search", "--query-id", "q_7f3a9c", "--page", "3").wantCode(t, ExitOK)
	last := h.api.last()
	if last.Path != "/v1/messages/search/page" {
		t.Fatalf("the page was turned at %s", last.Path)
	}
	if b := decodeBody(t, last.Body); b["query_id"] != "q_7f3a9c" || b["page"] != float64(2) || len(b) != 2 {
		t.Errorf("page body = %v", b)
	}
	if !strings.Contains(r.stderr, "page 3 of 4") || !strings.Contains(r.stdout, "90210") {
		t.Errorf("stdout = %q, stderr = %q", r.stdout, r.stderr)
	}

	calls := h.api.calls()
	h.run("", "search", "invoice", "--query-id", "q_7f3a9c").wantCode(t, ExitUsage)
	h.run("", "search", "--chat", "5", "--query-id", "q_7f3a9c").wantCode(t, ExitUsage)
	if h.api.calls() != calls {
		t.Error("a query id given with a query reached the API")
	}

	h.api.on("POST", "/v1/messages/search/page", func(w http.ResponseWriter, _ *http.Request) {
		refuse(w, http.StatusNotFound, "not_found", "no such query")
	})
	r = h.run("", "search", "--query-id", "q_7f3a9c", "--page", "2").wantCode(t, ExitError)
	if !strings.Contains(r.stderr, "run the search again") {
		t.Errorf("stderr = %q", r.stderr)
	}
}

var samplePortrait = map[string]any{
	"chat_id": -100, "summary": "Writes long, late and about work.",
	"subject":   map[string]any{"title": "Ann Weber", "username": "ann", "messages": 1200},
	"archetype": map[string]any{"label": "The Essayist", "fit": 0.8, "description": "Long messages, carefully built.", "terms": []string{"however", "therefore"}},
	"traits":    []any{map[string]any{"name": "Sentence length", "level": "high", "z": 1.7, "note": "twice the usual"}},
	"activity":  map[string]any{"messages": 640, "words": 52000, "unique_terms": 6100, "first": "2025-01-02T10:00:00Z", "last": "2026-09-20T23:10:00Z"},
	"similar":   []any{map[string]any{"title": "Bob \x1b[31mStone", "score": 0.73}},
	"signals":   []any{map[string]any{"kind": "style_shift", "key": "style.shift", "text": "Their style shifted in August.", "severity": "warning"}},
}

func TestPortraitShowsWhoItSoundsLikeAndWhatIsWorthKnowing(t *testing.T) {
	h := newHarness(t).loggedIn()
	h.api.ok("GET", "/v1/portraits/-100", samplePortrait)
	r := h.run("", "portrait", "--", "-100").wantCode(t, ExitOK)
	for _, want := range []string{
		"Long messages, carefully built.", "however, therefore", "640 messages, 52000 words, 6100 distinct, 2025-01-",
		"twice the usual", "SOUNDS LIKE", "Bob �[31mStone", "0.73", "Their style shifted in August. (warning)",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, r.stdout)
		}
	}
	if strings.Contains(r.stdout, "\x1b") {
		t.Error("an escape sequence from a chat title reached the terminal")
	}
}

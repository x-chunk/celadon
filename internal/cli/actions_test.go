package cli

import (
	"strings"
	"testing"
)

// ── actions ─────────────────────────────────────────────────────────────

func TestActions(t *testing.T) {
	h := newHarness(t).loggedIn()
	action := map[string]any{"id": 12, "name": "hi", "body": "Hi [[YOU_FIRST]]!", "uses": 3}
	h.api.ok("GET", "/v1/actions", map[string]any{
		"prefix": ".", "actions": []any{action},
		"quota": map[string]any{"key": "actions:saved", "limit": 25, "used": 1},
	})
	h.api.ok("POST", "/v1/actions", action)
	h.api.ok("PATCH", "/v1/actions/12", action)
	h.api.ok("DELETE", "/v1/actions/12", map[string]any{})
	h.api.ok("GET", "/v1/actions/placeholders", []any{
		map[string]any{"token": "[[YOU_FIRST]]", "level": "basic", "allowed": true, "label": "their first name"},
		map[string]any{"token": "[[STARS]]", "level": "exclusive", "allowed": false, "required_plan": "ultra"},
	})

	r := h.run("", "actions", "list").wantCode(t, ExitOK)
	if !strings.Contains(r.stdout, ".hi") || !strings.Contains(r.stderr, "1 / 25 shortcuts") {
		t.Errorf("list stdout = %q, stderr = %q", r.stdout, r.stderr)
	}

	h.run("", "actions", "create", "hi", "--body", "Hi [[YOU_FIRST]]!").wantCode(t, ExitOK)
	if b := decodeBody(t, h.api.last().Body); b["name"] != "hi" || b["body"] != "Hi [[YOU_FIRST]]!" {
		t.Errorf("create body = %v", b)
	}
	h.run("Body from\nstdin\n", "actions", "create", "x", "-F", "-").wantCode(t, ExitOK)
	if b := decodeBody(t, h.api.last().Body); b["body"] != "Body from\nstdin" {
		t.Errorf("create from stdin = %v", b)
	}
	h.run("", "actions", "create", "x").wantCode(t, ExitUsage)
	h.run("", "actions", "create", "x", "--body", "a", "--body-file", "f").wantCode(t, ExitUsage)

	h.run("", "actions", "edit", "12", "--name", "hello").wantCode(t, ExitOK)
	if b := decodeBody(t, h.api.last().Body); b["name"] != "hello" || b["body"] != nil {
		t.Errorf("edit body = %v", b)
	}
	h.run("", "actions", "edit", "12").wantCode(t, ExitUsage)

	h.run("", "actions", "rm", "12").wantCode(t, ExitUsage)
	h.run("", "actions", "rm", "12", "--yes").wantCode(t, ExitOK)

	r = h.run("", "actions", "placeholders").wantCode(t, ExitOK)
	if !strings.Contains(r.stdout, "needs ultra") {
		t.Errorf("placeholders = %q", r.stdout)
	}
}

func TestActionsArgumentsAttachmentsAndTheBodyLimit(t *testing.T) {
	h := newHarness(t).loggedIn()
	action := map[string]any{
		"id": 12, "name": "order", "body": "Order [[ARG1]] for [[ARG2]]", "min_args": 2, "args_required": true,
		"media_type": "photo", "file_id": "AgACAgIAAxk",
	}
	h.api.ok("GET", "/v1/actions/12", action)
	h.api.ok("PATCH", "/v1/actions/12", action)
	h.api.ok("GET", "/v1/actions", map[string]any{"prefix": ".", "actions": []any{action}, "quota": map[string]any{"limit": 25, "used": 1}})

	r := h.run("", "actions", "view", "12").wantCode(t, ExitOK)
	for _, want := range []string{"2 words; a call short of them does not fire", "photo · file AgACAgIAAxk", "27 of 900"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("view lacks %q:\n%s", want, r.stdout)
		}
	}
	r = h.run("", "actions", "list").wantCode(t, ExitOK)
	if !strings.Contains(r.stdout, "2, required") || !strings.Contains(r.stdout, "[photo] Order") {
		t.Errorf("list = %q", r.stdout)
	}

	h.run("", "actions", "edit", "12", "--args-required").wantCode(t, ExitOK)
	if b := decodeBody(t, h.api.last().Body); b["args_required"] != true || len(b) != 1 {
		t.Errorf("switch on body = %v", b)
	}
	h.run("", "actions", "edit", "12", "--args-required=false").wantCode(t, ExitOK)
	if b := decodeBody(t, h.api.last().Body); b["args_required"] != false || len(b) != 1 {
		t.Errorf("switch off body = %v", b)
	}

	calls := h.api.calls()
	r = h.run("", "actions", "create", "long", "--body", strings.Repeat("🙂", 450)+"!").wantCode(t, ExitUsage)
	if !strings.Contains(r.stderr, "901 characters long") {
		t.Errorf("stderr = %q", r.stderr)
	}
	h.run("", "actions", "edit", "12", "--body", "nul\x00").wantCode(t, ExitUsage)
	if h.api.calls() != calls {
		t.Error("a body the API refuses was sent")
	}
}

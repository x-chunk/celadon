package tui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/x-chunk/teal"
)

const testKey = "aek_AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_abcd"

// fakeAPI answers from a table and remembers what it was asked.
type fakeAPI struct {
	srv    *httptest.Server
	mu     sync.Mutex
	routes map[string]http.HandlerFunc
	seen   []seen
}

type seen struct {
	method, path string
	body         []byte
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{routes: map[string]http.HandlerFunc{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.seen = append(f.seen, seen{r.Method, r.URL.Path, body})
		h, ok := f.routes[r.Method+" "+r.URL.Path]
		f.mu.Unlock()
		if !ok {
			refuse(w, http.StatusNotFound, "not_found", "no route")
			return
		}
		h(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) ok(method, path string, data any) {
	f.on(method, path, func(w http.ResponseWriter, _ *http.Request) { answer(w, data) })
}

func (f *fakeAPI) on(method, path string, h http.HandlerFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[method+" "+path] = h
}

// called returns the requests made to method and path, in order.
func (f *fakeAPI) called(method, path string) []seen {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []seen
	for _, s := range f.seen {
		if s.method == method && s.path == path {
			out = append(out, s)
		}
	}
	return out
}

func answer(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": data})
}

func refuse(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": map[string]any{"code": code, "message": message}})
}

// driver plays the Bubble Tea runtime: it feeds messages to the model and
// runs the commands that come back, synchronously, until there are none.
type driver struct {
	t    *testing.T
	api  *fakeAPI
	m    *Model
	quit bool
}

func newDriver(t *testing.T) *driver {
	t.Helper()
	api := newFakeAPI(t)
	api.ok("GET", "/v1/app", map[string]any{
		"id": 7, "name": "Reports", "billing": "credits", "key_prefix": "aek_AbCdEfGh",
		"balance": map[string]any{"credits": 4200000, "display": "$4.20"},
	})
	api.ok("GET", "/v1/account", map[string]any{
		"account_id": 42, "plan": map[string]any{"tier": "ultra", "name": "Ultra"},
		"quotas": []any{map[string]any{"key": "search:daily", "limit": 50, "used": 35, "window": "day"}},
	})
	api.ok("GET", "/v1/usage", map[string]any{"from": "2026-08-26", "to": "2026-09-25"})
	c, err := teal.New(testKey, teal.WithBaseURL(api.srv.URL), teal.WithRetry(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	d := &driver{t: t, api: api, m: New(context.Background(), c, Options{Profile: "work", BaseURL: api.srv.URL})}
	d.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	d.exec(d.m.Init())
	return d
}

func (d *driver) send(msg tea.Msg) {
	d.t.Helper()
	_, cmd := d.m.Update(msg)
	d.exec(cmd)
}

func (d *driver) exec(cmd tea.Cmd) {
	d.t.Helper()
	if cmd == nil {
		return
	}
	got := make(chan tea.Msg, 1)
	go func() { got <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-got:
	case <-time.After(3 * time.Second):
		return // a timer nobody is waiting for
	}
	switch msg := msg.(type) {
	case nil:
	case tea.QuitMsg:
		d.quit = true
	case tea.BatchMsg:
		for _, c := range msg {
			d.exec(c)
		}
	default:
		d.send(msg)
	}
}

func (d *driver) keys(s string) {
	d.t.Helper()
	d.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
}

func (d *driver) key(k tea.KeyType) {
	d.t.Helper()
	d.send(tea.KeyMsg{Type: k})
}

func (d *driver) view() string { return d.m.View() }

// open switches to a tab the way a person does, by its number.
func (d *driver) open(title string) {
	d.t.Helper()
	i := d.m.find(title)
	if i < 0 {
		d.t.Fatalf("there is no %s tab", title)
	}
	d.keys(string(rune('1' + i)))
}

func (d *driver) activeTitle() string { return d.m.tabs[d.m.active].title() }

func (d *driver) wantView(substrings ...string) {
	d.t.Helper()
	v := d.view()
	for _, s := range substrings {
		if !strings.Contains(v, s) {
			d.t.Errorf("the screen lacks %q:\n%s", s, v)
		}
	}
}

func (d *driver) body(method, path string) map[string]any {
	d.t.Helper()
	calls := d.api.called(method, path)
	if len(calls) == 0 {
		d.t.Fatalf("%s %s was not called", method, path)
	}
	var m map[string]any
	if err := json.Unmarshal(calls[len(calls)-1].body, &m); err != nil {
		d.t.Fatalf("body of %s %s: %v", method, path, err)
	}
	return m
}

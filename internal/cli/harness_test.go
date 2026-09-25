package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/x-chunk/celadon/internal/config"
	"github.com/x-chunk/celadon/internal/iostreams"
)

const testKey = "aek_AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_abcd"

// fakeAPI is an Aether that answers from a table and remembers what it was
// asked.
type fakeAPI struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	routes map[string]http.HandlerFunc
	seen   []seenRequest
}

type seenRequest struct {
	Method, Path, Query string
	Header              http.Header
	Body                []byte
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{t: t, routes: map[string]http.HandlerFunc{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.seen = append(f.seen, seenRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Header: r.Header.Clone(), Body: body})
		h, ok := f.routes[r.Method+" "+r.URL.Path]
		f.mu.Unlock()
		if !ok {
			refuse(w, http.StatusNotFound, "not_found", "no route "+r.Method+" "+r.URL.Path)
			return
		}
		h(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) on(method, path string, h http.HandlerFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[method+" "+path] = h
}

func (f *fakeAPI) ok(method, path string, data any) {
	f.on(method, path, func(w http.ResponseWriter, _ *http.Request) { answer(w, data) })
}

func (f *fakeAPI) last() seenRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.seen) == 0 {
		f.t.Fatal("the API was not called")
	}
	return f.seen[len(f.seen)-1]
}

func (f *fakeAPI) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.seen)
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

// run is one invocation of the command line.
type run struct {
	code           int
	stdout, stderr string
}

type harness struct {
	t   *testing.T
	api *fakeAPI
	dir string
}

// newHarness isolates a test from the machine: a fresh home directory, and
// none of the environment that would override it.
func newHarness(t *testing.T) *harness {
	t.Helper()
	for _, k := range []string{config.EnvHome, config.EnvProfile, config.EnvAPIKey, config.EnvBaseURL, config.EnvAdminToken, config.EnvAdminBaseURL, config.EnvMetricsToken, config.EnvMetricsURL, "VISUAL", "EDITOR"} {
		t.Setenv(k, "")
	}
	return &harness{t: t, api: newFakeAPI(t), dir: t.TempDir()}
}

// loggedIn points the environment at the fake API with a key, as a script
// in CI would.
func (h *harness) loggedIn() *harness {
	h.t.Setenv(config.EnvAPIKey, testKey)
	h.t.Setenv(config.EnvBaseURL, h.api.srv.URL)
	return h
}

func (h *harness) run(stdin string, args ...string) run {
	return h.runWith(func(*iostreams.Streams) {}, stdin, args...)
}

func (h *harness) runWith(tweak func(*iostreams.Streams), stdin string, args ...string) run {
	h.t.Helper()
	var out, errOut bytes.Buffer
	io := iostreams.Test(strings.NewReader(stdin), &out, &errOut)
	tweak(io)
	env := &Env{
		IO:        io,
		ConfigDir: h.dir,
		Now:       func() time.Time { return time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC) },
	}
	code := Execute(context.Background(), env, append([]string{"--retries", "0"}, args...))
	return run{code: code, stdout: out.String(), stderr: errOut.String()}
}

func (r run) wantCode(t *testing.T, code int) run {
	t.Helper()
	if r.code != code {
		t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", r.code, code, r.stdout, r.stderr)
	}
	return r
}

func decodeBody(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("body %q: %v", raw, err)
	}
	return m
}

var sampleApp = map[string]any{
	"id": 7, "name": "Reports", "billing": "credits", "key_prefix": "aek_AbCdEfGh",
	"balance":    map[string]any{"credits": 4200000, "display": "$4.20"},
	"account_id": 42,
}

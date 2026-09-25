package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/x-chunk/celadon/internal/config"
)

const testMetricsToken = "metrics-secret"

var sampleMetricsInfo = map[string]any{
	"started": "2026-09-25T10:00:00Z", "now": "2026-09-25T12:00:00Z",
	"interval_ms": 1000, "live_window_ms": 900000, "persisted": true,
	"layout": map[string]any{
		"tiles": []any{
			map[string]any{"title": "CPU", "metric": "process_cpu_percent", "unit": "percent"},
			map[string]any{"title": "Queries/s", "metric": "db_queries_total", "unit": "per_second"},
		},
	},
	"catalog": []any{
		map[string]any{"key": "process_cpu_percent", "name": "process_cpu_percent", "kind": "gauge", "unit": "percent", "help": "CPU of this process"},
		map[string]any{"key": `db_queries_total{op="select"}`, "name": "db_queries_total", "kind": "counter", "unit": "per_second", "labels": map[string]any{"op": "select"}},
		map[string]any{"key": `db_queries_total{op="insert"}`, "name": "db_queries_total", "kind": "counter", "unit": "per_second", "labels": map[string]any{"op": "insert"}},
		map[string]any{"key": "go_goroutines", "name": "go_goroutines", "kind": "gauge", "unit": "count"},
	},
}

var sampleSnapshot = map[string]any{
	"at": "2026-09-25T12:00:00Z",
	"values": map[string]any{
		"process_cpu_percent": 12.5, `db_queries_total{op="select"}`: 30, `db_queries_total{op="insert"}`: 8.5, "go_goroutines": 1204,
	},
}

// metricsOn points the environment at the fake API as a metrics listener,
// guarded by the token when guard is set.
func (h *harness) metricsOn(guard bool) *harness {
	h.t.Setenv(config.EnvMetricsURL, h.api.srv.URL)
	if guard {
		h.t.Setenv(config.EnvMetricsToken, testMetricsToken)
	}
	json200 := func(path string, v any) {
		h.api.on("GET", path, func(w http.ResponseWriter, r *http.Request) {
			if guard && r.Header.Get("Authorization") != "Bearer "+testMetricsToken {
				w.WriteHeader(http.StatusUnauthorized)
				io.WriteString(w, `{"ok":false,"message":"unauthorized"}`)
				return
			}
			json.NewEncoder(w).Encode(v)
		})
	}
	h.api.on("GET", "/healthz", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, `{"ok":true}`) })
	h.api.on("GET", "/readyz", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"ready":true,"database":"ok","nats":{"bot":"ok","vault":"ok"}}`)
	})
	json200("/api/meta", sampleMetricsInfo)
	json200("/api/live", sampleSnapshot)
	return h
}

func TestMetricsHealth(t *testing.T) {
	h := newHarness(t).metricsOn(true)
	r := h.run("", "metrics", "health").wantCode(t, ExitOK)
	for _, want := range []string{"✓ up", "✓ ready", "NATS bot", "NATS vault"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, r.stdout)
		}
	}
	// Health needs no token: the request went out without one.
	for _, s := range h.api.seen {
		if s.Path == "/healthz" && s.Header.Get("Authorization") != "Bearer "+testMetricsToken {
			t.Errorf("healthz was sent %q", s.Header.Get("Authorization"))
		}
	}

	h.api.on("GET", "/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, `{"ready":false,"database":"unreachable: dial tcp: connection refused"}`)
	})
	r = h.run("", "metrics", "health", "-o", "json").wantCode(t, ExitError)
	var rep healthReport
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil || rep.Ready || !rep.Live || !strings.HasPrefix(rep.Database, "unreachable") {
		t.Errorf("not ready = %q (%v)", r.stdout, err)
	}
}

func TestMetricsUnreachableSaysTunnel(t *testing.T) {
	h := newHarness(t)
	t.Setenv(config.EnvMetricsURL, "http://127.0.0.1:1")
	r := h.run("", "metrics", "health").wantCode(t, ExitError)
	if !strings.Contains(r.stderr, "could not reach the metrics listener at http://127.0.0.1:1") || !strings.Contains(r.stderr, "ssh -N -L 9090") {
		t.Errorf("stderr = %q", r.stderr)
	}
}

func TestMetricsLoginStatusLogout(t *testing.T) {
	h := newHarness(t)
	h.metricsOn(true)
	t.Setenv(config.EnvMetricsURL, "")
	t.Setenv(config.EnvMetricsToken, "")

	r := h.run(testMetricsToken+"\n", "metrics", "login", "--with-token", "--metrics-url", h.api.srv.URL, "-p", "ops").wantCode(t, ExitOK)
	if !strings.Contains(r.stderr, "4 series, up 2h") {
		t.Errorf("stderr = %q", r.stderr)
	}
	store, _ := config.Open(h.dir)
	if tok, err := store.LoadMetricsToken("ops"); err != nil || tok != testMetricsToken {
		t.Fatalf("token = %q, %v", tok, err)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(store.MetricsTokenPath("ops"))
		if info.Mode().Perm() != 0o600 {
			t.Errorf("mode = %04o", info.Mode().Perm())
		}
	}
	cfg, _ := store.Load()
	if cfg.Profiles["ops"].MetricsURL != h.api.srv.URL {
		t.Errorf("profile = %+v", cfg.Profiles["ops"])
	}

	r = h.run("", "metrics", "status", "-o", "json").wantCode(t, ExitOK)
	var st metricsStatus
	if err := json.Unmarshal([]byte(r.stdout), &st); err != nil || st.Access != "open" || st.Series != 4 || !*st.Ready || st.TokenSource != config.SourceFile {
		t.Errorf("status = %q (%v)", r.stdout, err)
	}

	h.run("", "metrics", "logout").wantCode(t, ExitOK)
	r = h.run("", "metrics", "status").wantCode(t, ExitAuth)
	if !strings.Contains(r.stdout, "refused") {
		t.Errorf("status without a token = %q", r.stdout)
	}

	// A wrong token is refused before anything is stored.
	h.run("wrong\n", "metrics", "login", "--with-token").wantCode(t, ExitAuth)
	if store.HasMetricsToken("ops") {
		t.Error("a refused token was stored")
	}
}

func TestMetricsLoginWithoutAToken(t *testing.T) {
	h := newHarness(t).metricsOn(false)
	t.Setenv(config.EnvMetricsURL, "")
	h.run("", "metrics", "login", "--no-token", "--metrics-url", h.api.srv.URL).wantCode(t, ExitOK)
	h.run("", "metrics", "get", "go_goroutines").wantCode(t, ExitOK)
	if a := h.api.last().Header.Get("Authorization"); a != "" {
		t.Errorf("a token was sent: %q", a)
	}
}

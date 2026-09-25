package tui

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/x-chunk/celadon/internal/metrics"
)

var metricsInfo = map[string]any{
	"started": "2026-09-24T10:00:00Z", "now": "2026-09-25T12:00:00Z",
	"interval_ms": 1000, "live_window_ms": 900000, "recent_window_ms": 21600000,
	"minute_retention_ms": 172800000, "hour_retention_ms": 7776000000, "persisted": true,
	"layout": map[string]any{
		"tiles": []any{
			map[string]any{"title": "CPU", "metric": "process_cpu_percent", "unit": "percent"},
			map[string]any{"title": "Requests/s", "metric": "http_requests_total", "unit": "per_second"},
		},
		"sections": []any{
			map[string]any{"title": "Host", "help": "The machine.", "panels": []any{
				map[string]any{"title": "CPU chart", "unit": "percent", "kind": "area", "max": 100,
					"refs": []any{map[string]any{"metric": "process_cpu_percent", "label": "process"}}},
				map[string]any{"title": "Payments", "unit": "per_second", "kind": "line",
					"refs": []any{map[string]any{"metric": "payments_total", "label": "paid"}}},
			}},
			map[string]any{"title": "Traffic", "panels": []any{
				map[string]any{"title": "HTTP", "unit": "per_second", "kind": "stack", "wide": true,
					"refs": []any{map[string]any{"metric": "http_requests_total", "label": "requests"}}},
			}},
		},
	},
	"catalog": []any{
		map[string]any{"key": "process_cpu_percent", "name": "process_cpu_percent", "kind": "gauge", "unit": "percent", "help": "CPU of the process"},
		map[string]any{"key": `http_requests_total{route="app"}`, "name": "http_requests_total", "kind": "counter", "unit": "per_second", "labels": map[string]any{"route": "app"}},
		map[string]any{"key": `http_requests_total{route="search"}`, "name": "http_requests_total", "kind": "counter", "unit": "per_second", "labels": map[string]any{"route": "search"}},
		map[string]any{"key": "go_goroutines", "name": "go_goroutines", "kind": "gauge", "unit": "count", "help": "Goroutines alive"},
	},
}

// metricsDriver is the metrics dashboard over the fake API standing in for
// the listener, with the ticker off: a test sends tickMsg itself.
func newMetricsDriver(t *testing.T) (*driver, *atomic.Int32) {
	t.Helper()
	api := newFakeAPI(t)
	var queries atomic.Int32
	jsonRoute := func(path string, v any) {
		api.on("GET", path, func(w http.ResponseWriter, _ *http.Request) { json.NewEncoder(w).Encode(v) })
	}
	jsonRoute("/api/meta", metricsInfo)
	jsonRoute("/api/live", map[string]any{"at": "2026-09-25T12:00:00Z", "values": map[string]any{
		"process_cpu_percent": 93.5, `http_requests_total{route="app"}`: 3, `http_requests_total{route="search"}`: 1.5, "go_goroutines": 42,
	}})
	api.on("GET", "/readyz", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"ready":true,"database":"ok","nats":{"bot":"ok"}}`)
	})
	api.on("GET", "/healthz", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, `{"ok":true}`) })
	api.on("GET", "/api/query", func(w http.ResponseWriter, r *http.Request) {
		queries.Add(1)
		var series []any
		for i, k := range r.URL.Query()["keys"] {
			pts := []any{}
			for j := range 30 {
				v := float64(10*(i+1)) + float64(j%5)
				pts = append(pts, []any{1790330400000 + int64(j)*60000, v, v - 1, v + 1, v})
			}
			series = append(series, map[string]any{"meta": map[string]any{"key": k, "name": k, "unit": "percent"}, "points": pts})
		}
		json.NewEncoder(w).Encode(map[string]any{"tier": "minute", "step_ms": 60000,
			"from": "2026-09-25T11:00:00Z", "to": "2026-09-25T12:00:00Z", "series": series})
	})
	c, err := metrics.New(metrics.WithBaseURL(api.srv.URL), metrics.WithRetry(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	d := &driver{t: t, api: api, m: NewMetrics(context.Background(), c, Options{Profile: "prod", BaseURL: api.srv.URL, Refresh: -1})}
	d.send(tea.WindowSizeMsg{Width: 130, Height: 44})
	d.exec(d.m.Init())
	return d, &queries
}

func TestMetricsOverview(t *testing.T) {
	d, _ := newMetricsDriver(t)
	d.wantView("celadon metrics", "● ready", "1 Overview", "Health",
		"CPU", "93.5%", "Requests/s", "4.5/s", "Readiness", "nats bot", "Process", "persisted to Postgres", "1d 2h")
	if len(d.api.called("GET", "/api/query")) != 1 {
		t.Error("the tiles were not seeded from the recent past")
	}
	before := len(d.api.called("GET", "/api/live"))
	d.send(tickMsg{})
	if after := len(d.api.called("GET", "/api/live")); after != before+1 {
		t.Errorf("a tick read live %d times", after-before)
	}
}

func TestMetricsTickOnlyReachesTheOpenTab(t *testing.T) {
	d, _ := newMetricsDriver(t)
	d.open("Health")
	live := len(d.api.called("GET", "/api/live"))
	health := len(d.api.called("GET", "/healthz"))
	d.send(tickMsg{})
	if len(d.api.called("GET", "/api/live")) != live {
		t.Error("the overview refreshed while hidden")
	}
	if len(d.api.called("GET", "/healthz")) != health+1 {
		t.Error("the open tab did not refresh")
	}
}

func TestMetricsHealth(t *testing.T) {
	d, _ := newMetricsDriver(t)
	d.api.on("GET", "/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, `{"ready":false,"database":"unreachable: dial tcp: refused","nats":{"bot":"disconnected"}}`)
	})
	d.open("Health")
	d.wantView("● up", "● not ready", "unreachable: dial tcp", "disconnected", "Measured every", "6h in memory, 2d in all", "90d")
	if !strings.Contains(d.m.header(), "not ready") {
		t.Errorf("header = %q", d.m.header())
	}
}

func TestMetricsUnauthorized(t *testing.T) {
	d, _ := newMetricsDriver(t)
	// The listener guards every API endpoint alike.
	for _, path := range []string{"/api/meta", "/api/live", "/api/query"} {
		d.api.on("GET", path, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"ok":false,"message":"unauthorized"}`)
		})
	}
	d.keys("r")
	d.wantView("METRICS_TOKEN", "celadon metrics login")
}

func TestRenderChart(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	values := make([]float64, 100)
	for i := range values {
		values[i] = math.Sin(float64(i) / 10)
	}
	values[50] = math.NaN()
	spec := chartSpec{
		lines: []plotLine{{label: "a", values: values, color: seriesColor(0)}, {label: "b", values: []float64{1, 2, 3}, color: seriesColor(1)}},
		unit:  metrics.UnitPercent, from: time.Date(2026, 9, 25, 11, 0, 0, 0, time.UTC), to: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC),
	}
	out := renderChart(spec, 60, 10)
	rows := strings.Split(out, "\n")
	if len(rows) != 10 {
		t.Fatalf("%d rows, want 10:\n%s", len(rows), out)
	}
	for i, r := range rows {
		if w := lipgloss.Width(r); w > 60 {
			t.Errorf("row %d is %d wide", i, w)
		}
	}
	if !strings.ContainsAny(out, "⠁⠂⠄⡀⠈⠐⠠⢀") || !strings.Contains(out, "┤") {
		t.Errorf("no plot:\n%s", out)
	}
	if !strings.Contains(out, "-1%") && !strings.Contains(out, "-0.") {
		t.Errorf("a negative series did not take the axis below zero:\n%s", out)
	}
	if renderChart(spec, 10, 10) != "" {
		t.Error("a chart too narrow to draw was drawn")
	}
	empty := renderChart(chartSpec{lines: []plotLine{{label: "x"}}, unit: metrics.UnitCount}, 40, 6)
	if !strings.Contains(empty, "no data in this window") {
		t.Errorf("empty = %q", empty)
	}
	st := stacked([]plotLine{{values: []float64{1, 2}}, {values: []float64{3, math.NaN()}}})
	if st[1].values[0] != 4 || !math.IsNaN(st[1].values[1]) {
		t.Errorf("stacked = %+v", st)
	}
	if l := legend(spec.lines, metrics.UnitPercent, 200); !strings.Contains(l, "b 3%") {
		t.Errorf("legend = %q", l)
	}
	if utf8.RuneCountInString(rangeBar(0)) == 0 || rangeLabel(7*24*time.Hour) != "7d" || rangeLabel(90*time.Minute) != "90m" {
		t.Error("range labels")
	}
}

package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMetricsDashboard(t *testing.T) {
	d, queries := newMetricsDriver(t)
	d.open("Dashboard")
	d.wantView("Host", "Traffic", "The machine.", "CPU chart", "● process", "Payments", "Nothing has happened here yet", "1h", "one per 1m")
	n := queries.Load()
	d.key(tea.KeyRight)
	if queries.Load() != n+1 {
		t.Fatal("changing section did not read it")
	}
	d.wantView("HTTP", "requests app", "requests search")

	d.keys("+")
	d.keys("+")
	if got := lastQuery(d); !strings.Contains(got, "range=1d") {
		t.Errorf("window query = %q", got)
	}
	d.keys("-")
	if got := lastQuery(d); !strings.Contains(got, "range=6h") {
		t.Errorf("window query = %q", got)
	}
	// A six-hour chart is read every fifth tick, not every tick.
	n = queries.Load()
	for range 4 {
		d.send(tickMsg{})
	}
	if queries.Load() != n {
		t.Error("a six-hour chart was read before its fifth tick")
	}
	d.send(tickMsg{})
	if queries.Load() != n+1 {
		t.Error("a six-hour chart was not read on its fifth tick")
	}
}

func lastQuery(d *driver) string {
	d.api.mu.Lock()
	defer d.api.mu.Unlock()
	for i := len(d.api.seen) - 1; i >= 0; i-- {
		if d.api.seen[i].path == "/api/query" {
			return d.api.seen[i].query
		}
	}
	return ""
}

func TestMetricsSeries(t *testing.T) {
	d, _ := newMetricsDriver(t)
	d.open("Series")
	d.wantView("Series · 4 of 4", "go_goroutines", "Goroutines alive", "min", "avg", "max", "last")
	d.keys("/")
	d.keys("http zq")
	if d.quit {
		t.Fatal("typing q in the filter quit")
	}
	d.wantView("Series · 0 of 4")
	for range 3 {
		d.send(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	d.wantView("Series · 2 of 4")
	d.key(tea.KeyEnter)
	if got := lastQuery(d); !strings.Contains(got, "http_requests_total") {
		t.Errorf("the selected series was not read: %q", got)
	}
	d.wantView("counter — drawn as its rate per second")
	d.key(tea.KeyDown)
	if got := lastQuery(d); !strings.Contains(got, "search") {
		t.Errorf("moving did not read the next series: %q", got)
	}
}

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/x-chunk/celadon/internal/iostreams"
	"github.com/x-chunk/celadon/internal/metrics"
)

func TestMetricsOverview(t *testing.T) {
	h := newHarness(t).metricsOn(true)
	h.api.on("GET", "/api/query", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tier":"second","step_ms":1000,"series":[
		  {"meta":{"key":"process_cpu_percent","unit":"percent"},"points":[[1,1,1,1,1],[2,5,5,5,5],[3,9,9,9,9]]},
		  {"meta":{"key":"db_queries_total{op=\"select\"}"},"points":[[1,1,1,1,1],[2,2,2,2,2],[3,3,3,3,3]]},
		  {"meta":{"key":"db_queries_total{op=\"insert\"}"},"points":[[1,1,1,1,1],[2,null,null,null,null],[3,3,3,3,3]]}]}`)
	})
	r := h.run("", "metrics", "overview").wantCode(t, ExitOK)
	for _, want := range []string{"up 2h", "✓ ready", "CPU", "12.5%", "Queries/s", "38.5/s", "▁", "█"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("overview lacks %q:\n%s", want, r.stdout)
		}
	}
	if q := h.api.last().Query; !strings.Contains(q, "range=15m0s") || strings.Count(q, "keys=") != 3 {
		t.Errorf("trend query = %q", q)
	}
	r = h.run("", "metrics", "overview", "-o", "json").wantCode(t, ExitOK)
	var got struct {
		Tiles []tileValue `json:"tiles"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil || *got.Tiles[1].Value != 38.5 {
		t.Errorf("json = %q (%v)", r.stdout, err)
	}
}

func TestMetricsListAndGet(t *testing.T) {
	h := newHarness(t).metricsOn(true)
	r := h.run("", "metrics", "list").wantCode(t, ExitOK)
	for _, want := range []string{`db_queries_total{op="insert"}`, "counter", "/s", "1,204", "CPU of this process", "4 series"} {
		if !strings.Contains(r.stdout+r.stderr, want) {
			t.Errorf("list lacks %q:\n%s", want, r.stdout)
		}
	}
	r = h.run("", "metrics", "list", "db_*", "--kind", "gauge").wantCode(t, ExitOK)
	if !strings.Contains(r.stderr, "No series") {
		t.Errorf("filtered list = %q / %q", r.stdout, r.stderr)
	}

	r = h.run("", "metrics", "get", "db_queries_total").wantCode(t, ExitOK)
	if !strings.Contains(r.stdout, "30/s") || !strings.Contains(r.stdout, "8.5/s") {
		t.Errorf("get by metric = %q", r.stdout)
	}
	r = h.run("", "metrics", "get", "go_goroutines", "--raw").wantCode(t, ExitOK)
	if strings.TrimSpace(r.stdout) != "1204" {
		t.Errorf("raw = %q", r.stdout)
	}
	r = h.run("", "metrics", "get", "*_percent", "-o", "json").wantCode(t, ExitOK)
	var vals map[string]float64
	if err := json.Unmarshal([]byte(r.stdout), &vals); err != nil || vals["process_cpu_percent"] != 12.5 || len(vals) != 1 {
		t.Errorf("json = %q (%v)", r.stdout, err)
	}
	r = h.run("", "metrics", "get", "nope_*").wantCode(t, ExitUsage)
	if !strings.Contains(r.stderr, "no series matches") {
		t.Errorf("stderr = %q", r.stderr)
	}
	h.run("", "metrics", "get").wantCode(t, ExitUsage)
}

func TestMetricsQuery(t *testing.T) {
	h := newHarness(t).metricsOn(true)
	h.api.on("GET", "/api/query", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("range") == "" && r.URL.Query().Get("from") == "" {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"ok":false,"message":"range must be a positive duration"}`)
			return
		}
		fmt.Fprint(w, `{"tier":"minute","step_ms":60000,"from":"2026-09-25T06:00:00Z","to":"2026-09-25T12:00:00Z","series":[
		  {"meta":{"key":"process_cpu_percent","unit":"percent"},"points":[[1790330400000,10,5,20,12],[1790330460000,30,25,40,35]]}]}`)
	})
	r := h.run("", "metrics", "query", "process_cpu_percent", "--range", "6h").wantCode(t, ExitOK)
	if q := h.api.last().Query; q != "keys=process_cpu_percent&range=6h0m0s" {
		t.Errorf("query = %q", q)
	}
	for _, want := range []string{"minute points, one per 1m", "5%", "20%", "40%", "35%"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("summary lacks %q:\n%s", want, r.stdout)
		}
	}
	r = h.run("", "metrics", "query", "process_cpu_percent", "--range", "7d", "--points", "--step", "1h").wantCode(t, ExitOK)
	if q := h.api.last().Query; !strings.Contains(q, "range=7d") || !strings.Contains(q, "step=1h0m0s") {
		t.Errorf("query = %q", q)
	}
	if strings.Count(r.stdout, "%") < 8 {
		t.Errorf("points = %q", r.stdout)
	}
	h.run("", "metrics", "query", "process_cpu_percent", "--from", "2026-09-24", "--to", "2026-09-25").wantCode(t, ExitOK)
	if q := h.api.last().Query; !strings.Contains(q, "from=") || !strings.Contains(q, "to=") || strings.Contains(q, "range") {
		t.Errorf("window query = %q", q)
	}

	calls := h.api.calls()
	h.run("", "metrics", "query", "x", "--range", "soon").wantCode(t, ExitUsage)
	h.run("", "metrics", "query", "x", "--from", "2026-09-25", "--to", "2026-09-24").wantCode(t, ExitUsage)
	h.run("", "metrics", "query", "x", "--range", "1h", "--from", "2026-09-25").wantCode(t, ExitUsage)
	if h.api.calls() != calls {
		t.Error("a malformed query reached the listener")
	}
}

// iostreamsTTY is a terminal that writes into out.
func iostreamsTTY(out io.Writer) *iostreams.Streams {
	s := iostreams.Test(nil, out, io.Discard)
	s.OutTTY = true
	return s
}

type fakeStream struct {
	snaps []metrics.Snapshot
}

func (f *fakeStream) Next() (metrics.Snapshot, error) {
	if len(f.snaps) == 0 {
		return metrics.Snapshot{}, metrics.ErrStreamClosed
	}
	s := f.snaps[0]
	f.snaps = f.snaps[1:]
	return s, nil
}

func TestMetricsWatch(t *testing.T) {
	h := newHarness(t).metricsOn(true)
	h.api.on("GET", "/api/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 1; i <= 3; i++ {
			fmt.Fprintf(w, "data: {\"at\":\"2026-09-25T12:00:0%dZ\",\"values\":{\"go_goroutines\":%d}}\n\n", i, 100*i)
		}
	})
	r := h.run("", "metrics", "watch", "go_goroutines").wantCode(t, ExitError)
	if strings.Count(r.stdout, "go_goroutines=") != 3 || !strings.Contains(r.stdout, "go_goroutines=300") {
		t.Errorf("watch = %q", r.stdout)
	}
	if !strings.Contains(r.stderr, "ended the stream") {
		t.Errorf("stderr = %q", r.stderr)
	}
	r = h.run("", "metrics", "watch", "go_goroutines", "-o", "json").wantCode(t, ExitError)
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if len(lines) != 3 || !strings.Contains(lines[2], `"go_goroutines":300`) {
		t.Errorf("json lines = %q", r.stdout)
	}
}

func TestMetricsProm(t *testing.T) {
	h := newHarness(t).metricsOn(true)
	text := "# TYPE db_queries_total counter\ndb_queries_total{op=\"select\"} 42\n# TYPE go_goroutines gauge\ngo_goroutines 18\n"
	h.api.on("GET", "/metrics", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testMetricsToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		io.WriteString(w, text)
	})
	r := h.run("", "metrics", "prom").wantCode(t, ExitOK)
	if r.stdout != text {
		t.Errorf("raw = %q", r.stdout)
	}
	r = h.run("", "metrics", "prom", "db_*").wantCode(t, ExitOK)
	if !strings.Contains(r.stdout, `db_queries_total{op="select"}`) || !strings.Contains(r.stdout, "counter") || strings.Contains(r.stdout, "go_goroutines") {
		t.Errorf("filtered = %q", r.stdout)
	}
	r = h.run("", "metrics", "prom", "-o", "json").wantCode(t, ExitOK)
	if !strings.Contains(r.stdout, `"type": "gauge"`) {
		t.Errorf("json = %q", r.stdout)
	}
	h.run("", "metrics", "prom", "nope").wantCode(t, ExitUsage)
}

func TestWatchRedrawsInPlaceOnATerminal(t *testing.T) {
	var out strings.Builder
	h := newHarness(t)
	env := &Env{IO: iostreamsTTY(&out), ConfigDir: h.dir}
	info := metrics.Info{}
	rows := []watchRow{{label: "Goroutines", key: "go_goroutines", unit: metrics.UnitCount}}
	src := &fakeStream{snaps: []metrics.Snapshot{
		{Values: map[string]float64{"go_goroutines": 10}},
		{Values: map[string]float64{"go_goroutines": 20}},
	}}
	err := watchLoop(t.Context(), env, info, rows, src)
	if err == nil || !strings.Contains(err.Error(), "ended the stream") {
		t.Fatalf("err = %v", err)
	}
	got := out.String()
	if strings.Count(got, "\x1b[2A") != 1 || !strings.Contains(got, "Goroutines") || !strings.Contains(got, "20") {
		t.Errorf("redraw = %q", got)
	}
}

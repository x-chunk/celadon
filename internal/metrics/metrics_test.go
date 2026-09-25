package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testToken = "metrics-secret"

// listener stands in for Aether's metrics listener: health and readiness
// open, everything else behind the token when one is set.
func listener(t *testing.T, token string, routes map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("%s %s: every endpoint is a GET", r.Method, r.URL.Path)
		}
		open := r.URL.Path == "/healthz" || r.URL.Path == "/readyz"
		if token != "" && !open && r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"ok":false,"message":"unauthorized"}`)
			return
		}
		h, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func jsonAnswer(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}
}

func newClient(t *testing.T, url string, opts ...Option) *Client {
	t.Helper()
	c, err := New(append([]Option{WithBaseURL(url), WithRetry(2, time.Millisecond)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestHealth(t *testing.T) {
	ready := `{"ready":true,"database":"ok","nats":{"bot":"ok"}}`
	var readyStatus atomic.Int32
	readyStatus.Store(200)
	srv := listener(t, testToken, map[string]http.HandlerFunc{
		"/healthz": jsonAnswer(`{"ok":true}`),
		"/readyz": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(int(readyStatus.Load()))
			if readyStatus.Load() == 200 {
				io.WriteString(w, ready)
			} else {
				io.WriteString(w, `{"ready":false,"database":"unreachable: dial tcp","nats":{"bot":"disconnected"}}`)
			}
		},
	})
	// No token: health and readiness are open.
	c := newClient(t, srv.URL)
	h, _, err := c.Health.Live(context.Background())
	if err != nil || !h.OK {
		t.Fatalf("Live = %+v, %v", h, err)
	}
	r, meta, err := c.Health.Ready(context.Background())
	if err != nil || !r.Ready || r.NATS["bot"] != "ok" || meta.StatusCode != 200 {
		t.Fatalf("Ready = %+v, %v", r, err)
	}
	readyStatus.Store(503)
	r, meta, err = c.Health.Ready(context.Background())
	if err != nil {
		t.Fatalf("a not-ready process came back as an error: %v", err)
	}
	if r.Ready || !strings.HasPrefix(r.Database, "unreachable") || meta.StatusCode != 503 {
		t.Errorf("Ready = %+v (%d)", r, meta.StatusCode)
	}
}

const sampleInfo = `{
  "started":"2026-09-25T10:00:00Z","now":"2026-09-25T12:00:00Z",
  "interval_ms":1000,"live_window_ms":900000,"recent_window_ms":21600000,
  "minute_retention_ms":172800000,"hour_retention_ms":7776000000,"persisted":true,
  "layout":{"tiles":[{"title":"CPU","metric":"process_cpu_percent","unit":"percent"}],
    "sections":[{"title":"App host","panels":[{"title":"CPU","unit":"percent","kind":"area","max":100,
      "refs":[{"metric":"process_cpu_percent","label":"process"}]}]}]},
  "catalog":[
    {"key":"process_cpu_percent","name":"process_cpu_percent","kind":"gauge","unit":"percent","help":"CPU"},
    {"key":"db_queries_total{op=\"select\"}","name":"db_queries_total","kind":"counter","unit":"per_second","labels":{"op":"select"}},
    {"key":"db_queries_total{op=\"insert\"}","name":"db_queries_total","kind":"counter","unit":"per_second","labels":{"op":"insert"}}
  ]}`

func TestSeriesInfoAndLive(t *testing.T) {
	srv := listener(t, testToken, map[string]http.HandlerFunc{
		"/api/meta": jsonAnswer(sampleInfo),
		"/api/live": jsonAnswer(`{"at":"2026-09-25T12:00:00Z","values":{"process_cpu_percent":12.5,"db_queries_total{op=\"select\"}":4,"db_queries_total{op=\"insert\"}":1.5}}`),
	})
	c := newClient(t, srv.URL, WithToken(testToken))
	ctx := context.Background()

	info, _, err := c.Series.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Interval() != time.Second || info.LiveWindow() != 15*time.Minute || info.Uptime() != 2*time.Hour || !info.Persisted {
		t.Errorf("info = %+v", info)
	}
	if info.Layout.Tiles[0].Metric != "process_cpu_percent" || info.Layout.Sections[0].Panels[0].Max != 100 {
		t.Errorf("layout = %+v", info.Layout)
	}
	if got := info.SeriesOf("db_queries_total"); len(got) != 2 || got[0].Labels["op"] != "insert" {
		t.Errorf("SeriesOf = %+v", got)
	}

	snap, _, err := c.Series.Live(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := snap.Value("process_cpu_percent"); !ok || v != 12.5 {
		t.Errorf("Value = %v, %v", v, ok)
	}
	if v, ok := snap.Sum(info, "db_queries_total"); !ok || v != 5.5 {
		t.Errorf("Sum = %v, %v", v, ok)
	}
	if _, ok := snap.Sum(info, "nothing"); ok {
		t.Error("a metric with no series summed to something")
	}
}

func TestQuery(t *testing.T) {
	var got []string
	srv := listener(t, "", map[string]http.HandlerFunc{
		"/api/query": func(w http.ResponseWriter, r *http.Request) {
			got = append(got, r.URL.RawQuery)
			io.WriteString(w, `{"tier":"minute","step_ms":60000,"from":"2026-09-25T11:00:00Z","to":"2026-09-25T12:00:00Z",
			  "series":[{"meta":{"key":"process_cpu_percent","name":"process_cpu_percent","kind":"gauge","unit":"percent"},
			    "points":[[1790334000000,10,5,20,12],[1790334060000,null,null,null,null],[1790334120000,30,25,40,35]]}]}`)
		},
	})
	c := newClient(t, srv.URL)
	ctx := context.Background()

	ans, _, err := c.Series.Query(ctx, QueryRequest{Keys: []string{`db_queries_total{op="select",table="a"}`, "process_cpu_percent"}, Range: 7 * 24 * time.Hour, Step: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != `keys=db_queries_total%7Bop%3D%22select%22%2Ctable%3D%22a%22%7D&keys=process_cpu_percent&range=7d&step=30s` {
		t.Errorf("query = %s", got[0])
	}
	s, ok := ans.Find("process_cpu_percent")
	if !ok || ans.Tier != TierMinute || ans.Step() != time.Minute || len(s.Points) != 3 {
		t.Fatalf("answer = %+v", ans)
	}
	if p := s.Points[0]; p.Avg != 10 || p.Min != 5 || p.Max != 20 || p.Last != 12 || p.At.UnixMilli() != 1790334000000 {
		t.Errorf("point = %+v", p)
	}
	if !math.IsNaN(s.Points[1].Avg) {
		t.Error("a null did not become a gap")
	}
	st, ok := s.Stats()
	if !ok || st.Min != 5 || st.Max != 40 || st.Avg != 20 || st.Last != 35 {
		t.Errorf("stats = %+v", st)
	}
	raw, _ := json.Marshal(s.Points[1])
	if string(raw) != "[1790334060000,null,null,null,null]" {
		t.Errorf("a gap marshals as %s", raw)
	}

	from := time.UnixMilli(1790330000000)
	to := time.UnixMilli(1790334000000)
	if _, _, err := c.Series.Query(ctx, QueryRequest{From: from, To: to}); err != nil {
		t.Fatal(err)
	}
	if got[1] != "from=1790330000000&to=1790334000000" {
		t.Errorf("window query = %s", got[1])
	}
}

func TestFormatRange(t *testing.T) {
	for d, want := range map[time.Duration]string{
		15 * time.Minute: "15m0s", 24 * time.Hour: "1d", 30 * 24 * time.Hour: "30d", 36 * time.Hour: "36h0m0s",
	} {
		if got := FormatRange(d); got != want {
			t.Errorf("FormatRange(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestStream(t *testing.T) {
	srv := listener(t, testToken, map[string]http.HandlerFunc{
		"/api/stream": func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Accept") != "text/event-stream" {
				t.Errorf("Accept = %q", r.Header.Get("Accept"))
			}
			w.Header().Set("Content-Type", "text/event-stream")
			for i := 1; i <= 2; i++ {
				fmt.Fprintf(w, "data: {\"at\":\"2026-09-25T12:00:0%dZ\",\"values\":{\"x\":%d}}\n\n", i, i)
				fmt.Fprint(w, ": keep-alive\n\n")
				w.(http.Flusher).Flush()
			}
		},
	})
	c := newClient(t, srv.URL, WithToken(testToken))
	st, _, err := c.Series.Stream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for i := 1; i <= 2; i++ {
		snap, err := st.Next()
		if err != nil {
			t.Fatal(err)
		}
		if snap.Values["x"] != float64(i) {
			t.Errorf("snapshot %d = %+v", i, snap)
		}
	}
	if _, err := st.Next(); !errors.Is(err, ErrStreamClosed) {
		t.Errorf("after the end = %v", err)
	}
}

func TestScrape(t *testing.T) {
	text := `# HELP db_queries_total Queries run, by operation.
# TYPE db_queries_total counter
db_queries_total{op="select",table="a\"b"} 42
db_queries_total{op="insert"} 7 1790334000000
# TYPE go_goroutines gauge
go_goroutines 18
weird_value NaN
`
	srv := listener(t, testToken, map[string]http.HandlerFunc{
		"/metrics": func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, text) },
	})
	exp, _, err := newClient(t, srv.URL, WithToken(testToken)).Prometheus.Scrape(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if exp.Raw != text || len(exp.Samples) != 4 {
		t.Fatalf("samples = %+v", exp.Samples)
	}
	if s := exp.Samples[0]; s.Name != "db_queries_total" || s.Labels["table"] != `a"b` || s.Value != 42 {
		t.Errorf("sample = %+v", s)
	}
	if s := exp.Samples[0]; s.Key() != `db_queries_total{op="select",table="a\"b"}` {
		t.Errorf("Key = %s", s.Key())
	}
	if exp.Samples[1].Value != 7 || exp.Samples[2].Key() != "go_goroutines" || !math.IsNaN(exp.Samples[3].Value) {
		t.Errorf("samples = %+v", exp.Samples)
	}
	if exp.Help["db_queries_total"] != "Queries run, by operation." || exp.Types["go_goroutines"] != "gauge" {
		t.Errorf("help = %v, types = %v", exp.Help, exp.Types)
	}
	if _, err := ParseExposition("broken{a=1} 2"); err == nil {
		t.Error("malformed labels were accepted")
	}
}

func TestErrorsAndRetries(t *testing.T) {
	var calls atomic.Int32
	srv := listener(t, testToken, map[string]http.HandlerFunc{
		"/api/query": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"ok":false,"message":"range must be a positive duration, like 15m, 24h or 7d"}`)
		},
		"/api/live": func(w http.ResponseWriter, _ *http.Request) {
			if calls.Add(1) < 3 {
				w.WriteHeader(http.StatusInternalServerError)
				io.WriteString(w, `{"ok":false,"message":"query failed"}`)
				return
			}
			io.WriteString(w, `{"values":{}}`)
		},
	})
	ctx := context.Background()

	_, _, err := newClient(t, srv.URL).Series.Live(ctx)
	if !IsCode(err, CodeUnauthorized) {
		t.Errorf("no token = %v", err)
	}
	c := newClient(t, srv.URL, WithToken(testToken))
	_, _, err = c.Series.Query(ctx, QueryRequest{})
	if e, ok := AsError(err); !ok || e.Code != CodeBadRequest || !strings.Contains(e.Message, "positive duration") {
		t.Errorf("bad query = %v", err)
	}
	if _, _, err := c.Series.Live(ctx); err != nil || calls.Load() != 3 {
		t.Errorf("a read was not retried through: %v after %d calls", err, calls.Load())
	}
	_, _, err = c.Series.Info(ctx)
	if e, ok := AsError(err); !ok || e.Code != CodeNotFound || !strings.Contains(e.Message, ":9090") {
		t.Errorf("not a listener = %v", err)
	}
	if _, err := New(WithBaseURL("nohost")); err == nil {
		t.Error("a base url without a host was accepted")
	}
}

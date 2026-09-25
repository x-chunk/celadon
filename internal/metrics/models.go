package metrics

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Kind is what a metric measures. A counter is published as a per-second
// rate, a gauge as its value, a histogram as derived series (_avg, _p50,
// _p99).
type Kind string

const (
	KindCounter   Kind = "counter"
	KindGauge     Kind = "gauge"
	KindHistogram Kind = "histogram"
)

// Unit decides how a value is read — never how it is stored.
type Unit string

const (
	UnitNone    Unit = ""
	UnitPercent Unit = "percent"
	UnitBytes   Unit = "bytes"
	UnitSeconds Unit = "seconds"
	UnitCount   Unit = "count"
	UnitPerSec  Unit = "per_second"
)

// Tier is the resolution a range query was answered at.
type Tier string

const (
	TierSecond Tier = "second" // the live ring, in memory
	TierMinute Tier = "minute" // one point a minute
	TierHour   Tier = "hour"   // one point an hour, from Postgres
)

// --- Health -----------------------------------------------------------------

// Health is liveness: the process is up and serving the listener.
type Health struct {
	OK bool `json:"ok"`
}

// Readiness is whether the process can do its work: the database answers
// and every NATS connection is up. A dependency that is not is named with
// the reason.
type Readiness struct {
	Ready    bool              `json:"ready"`
	Database string            `json:"database"`
	NATS     map[string]string `json:"nats,omitempty"`
}

// --- The catalog and the layout ---------------------------------------------

// SeriesInfo is what a reader has to be told about a series before drawing
// it. Key is the series itself — the metric name with its labels,
// metric{a="x"} — and is what every other call names it by.
type SeriesInfo struct {
	Key    string            `json:"key"`
	Name   string            `json:"name"`
	Kind   Kind              `json:"kind"`
	Unit   Unit              `json:"unit"`
	Help   string            `json:"help"`
	Labels map[string]string `json:"labels,omitempty"`
}

// Info describes the listener: how it is configured, what it publishes and
// how its dashboard is laid out.
type Info struct {
	Started           time.Time    `json:"started"`
	Now               time.Time    `json:"now"`
	IntervalMs        int64        `json:"interval_ms"`
	LiveWindowMs      int64        `json:"live_window_ms"`
	RecentWindowMs    int64        `json:"recent_window_ms"`
	MinuteRetentionMs int64        `json:"minute_retention_ms"`
	HourRetentionMs   int64        `json:"hour_retention_ms"`
	Persisted         bool         `json:"persisted"`
	Layout            Layout       `json:"layout"`
	Catalog           []SeriesInfo `json:"catalog"`
}

// Interval is how often everything is measured.
func (i Info) Interval() time.Duration { return ms(i.IntervalMs) }

// LiveWindow is how far back one-second points reach.
func (i Info) LiveWindow() time.Duration { return ms(i.LiveWindowMs) }

// RecentWindow is how far back one-minute points are kept in memory.
func (i Info) RecentWindow() time.Duration { return ms(i.RecentWindowMs) }

// MinuteRetention is how long one-minute points are kept at all.
func (i Info) MinuteRetention() time.Duration { return ms(i.MinuteRetentionMs) }

// HourRetention is how long one-hour points are kept.
func (i Info) HourRetention() time.Duration { return ms(i.HourRetentionMs) }

// Uptime is how long the process has been up, by its own clock.
func (i Info) Uptime() time.Duration {
	if i.Started.IsZero() || i.Now.IsZero() {
		return 0
	}
	return i.Now.Sub(i.Started)
}

// Series finds a series in the catalog.
func (i Info) Series(key string) (SeriesInfo, bool) {
	for _, s := range i.Catalog {
		if s.Key == key {
			return s, true
		}
	}
	return SeriesInfo{}, false
}

// SeriesOf returns every series of a metric: one for a metric without
// labels, one per label combination otherwise, in key order. A histogram's
// derived series are named with their suffix ("db_query_seconds_p99").
func (i Info) SeriesOf(metric string) []SeriesInfo {
	var out []SeriesInfo
	for _, s := range i.Catalog {
		if s.Name == metric {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Key < out[b].Key })
	return out
}

func ms(v int64) time.Duration { return time.Duration(v) * time.Millisecond }

// Layout is the dashboard: the numbers along the top, and the sections of
// charts under them. It is decided by the server, next to the catalog.
type Layout struct {
	Tiles    []Tile    `json:"tiles"`
	Sections []Section `json:"sections"`
}

// Tile is one number worth seeing without scrolling.
type Tile struct {
	Title  string `json:"title"`
	Metric string `json:"metric"`
	Unit   Unit   `json:"unit"`
}

// Section is a group of panels under one heading.
type Section struct {
	Title  string  `json:"title"`
	Help   string  `json:"help,omitempty"`
	Panels []Panel `json:"panels"`
}

// Panel is one chart.
type Panel struct {
	Title string `json:"title"`
	Help  string `json:"help,omitempty"`
	Refs  []Ref  `json:"refs"`
	Unit  Unit   `json:"unit"`
	// Kind is "line", "area" or "stack".
	Kind string `json:"kind"`
	Wide bool   `json:"wide,omitempty"`
	// Max pins the top of the axis; zero leaves it to the data.
	Max float64 `json:"max,omitempty"`
}

// Ref is one line on a chart, named by metric: a metric with labels becomes
// a line per label combination.
type Ref struct {
	Metric string `json:"metric"`
	Label  string `json:"label,omitempty"`
}

// --- Live -------------------------------------------------------------------

// Snapshot is one instant of every series, keyed by series key. A counter's
// value is its rate per second.
type Snapshot struct {
	At     time.Time          `json:"at"`
	Values map[string]float64 `json:"values"`
}

// Value returns a series' value, and whether the snapshot has it.
func (s Snapshot) Value(key string) (float64, bool) {
	v, ok := s.Values[key]
	return v, ok
}

// Sum adds up every series of a metric — the total of a labelled metric.
// It reports false when the snapshot has none of them.
func (s Snapshot) Sum(info Info, metric string) (float64, bool) {
	if v, ok := s.Values[metric]; ok {
		return v, true
	}
	var total float64
	found := false
	for _, series := range info.SeriesOf(metric) {
		if v, ok := s.Values[series.Key]; ok {
			total += v
			found = true
		}
	}
	return total, found
}

// --- Range queries ----------------------------------------------------------

// QueryRequest is one range request. Either Range (a window ending now) or
// From and To (an explicit window) says when; Range wins when both are set.
type QueryRequest struct {
	// Keys are the series to answer for — keys from the catalog, labels and
	// all. Empty asks for every series.
	Keys []string
	// Range is a window ending now: 15m, 24h, 7d.
	Range time.Duration
	From  time.Time
	To    time.Time
	// Step is the width of one point. Zero lets the server choose from the
	// width of the window.
	Step time.Duration
}

func (r QueryRequest) query() url.Values {
	q := url.Values{}
	// One parameter per key, never joined: a key's labels carry commas.
	for _, k := range r.Keys {
		q.Add("keys", k)
	}
	if r.Range > 0 {
		q.Set("range", FormatRange(r.Range))
	} else {
		if !r.From.IsZero() {
			q.Set("from", strconv.FormatInt(r.From.UnixMilli(), 10))
		}
		if !r.To.IsZero() {
			q.Set("to", strconv.FormatInt(r.To.UnixMilli(), 10))
		}
	}
	if r.Step > 0 {
		q.Set("step", r.Step.String())
	}
	return q
}

// FormatRange writes a window the way the listener reads it: whole days as
// "7d", which time.ParseDuration does not read, and anything else as Go
// writes durations.
func FormatRange(d time.Duration) string {
	if d >= 24*time.Hour && d%(24*time.Hour) == 0 {
		return strconv.FormatInt(int64(d/(24*time.Hour)), 10) + "d"
	}
	return d.String()
}

// Answer is a whole range query.
type Answer struct {
	Tier   Tier           `json:"tier"`
	StepMs int64          `json:"step_ms"`
	From   time.Time      `json:"from"`
	To     time.Time      `json:"to"`
	Series []SeriesResult `json:"series"`
}

// Step is the width of one point.
func (a Answer) Step() time.Duration { return ms(a.StepMs) }

// Find returns one series of the answer.
func (a Answer) Find(key string) (SeriesResult, bool) {
	for _, s := range a.Series {
		if s.Info.Key == key {
			return s, true
		}
	}
	return SeriesResult{}, false
}

// SeriesResult is one series' answer.
type SeriesResult struct {
	Info   SeriesInfo `json:"meta"`
	Points []Point    `json:"points"`
}

// Stats sums a series up over its points: the lowest minimum, the highest
// maximum, the mean of the averages and the last value. A series without a
// point reports ok false.
func (s SeriesResult) Stats() (st Stats, ok bool) {
	st = Stats{Min: math.Inf(1), Max: math.Inf(-1)}
	var sum float64
	n := 0
	for _, p := range s.Points {
		if math.IsNaN(p.Avg) {
			continue
		}
		st.Min = math.Min(st.Min, p.Min)
		st.Max = math.Max(st.Max, p.Max)
		sum += p.Avg
		st.Last = p.Last
		n++
	}
	if n == 0 {
		return Stats{}, false
	}
	st.Avg = sum / float64(n)
	return st, true
}

// Stats is a series summed up over a window.
type Stats struct {
	Min, Max, Avg, Last float64
}

// Point is one bucket of one series: its mean, its extremes and the last
// reading in it. A gap in the series is NaN in every field.
type Point struct {
	At   time.Time
	Avg  float64
	Min  float64
	Max  float64
	Last float64
}

// UnmarshalJSON reads the compact form the listener sends a point in —
// [milliseconds, average, minimum, maximum, last], with null for a value
// JSON cannot spell — rather than an object, which would be most of the
// bytes of a large answer.
func (p *Point) UnmarshalJSON(raw []byte) error {
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return fmt.Errorf("metrics: a point is [ms, avg, min, max, last]: %w", err)
	}
	if len(parts) != 5 {
		return fmt.Errorf("metrics: a point has %d fields, want 5", len(parts))
	}
	var millis int64
	if err := json.Unmarshal(parts[0], &millis); err != nil {
		return fmt.Errorf("metrics: a point's time: %w", err)
	}
	p.At = time.UnixMilli(millis).UTC()
	for i, dst := range []*float64{&p.Avg, &p.Min, &p.Max, &p.Last} {
		v := parts[i+1]
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			*dst = math.NaN()
			continue
		}
		if err := json.Unmarshal(v, dst); err != nil {
			return fmt.Errorf("metrics: a point's value: %w", err)
		}
	}
	return nil
}

// MarshalJSON writes a point back in the same compact form.
func (p Point) MarshalJSON() ([]byte, error) {
	num := func(v float64) string {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return "null"
		}
		return strconv.FormatFloat(v, 'g', -1, 64)
	}
	return []byte("[" + strconv.FormatInt(p.At.UnixMilli(), 10) + "," + num(p.Avg) + "," +
		num(p.Min) + "," + num(p.Max) + "," + num(p.Last) + "]"), nil
}

// --- Prometheus -------------------------------------------------------------

// Exposition is the Prometheus text format, as served and as parsed.
type Exposition struct {
	// Raw is the text exactly as the listener sent it.
	Raw string
	// Samples are its sample lines, in order.
	Samples []Sample
	// Help and Types are the # HELP and # TYPE lines, by metric name.
	Help  map[string]string
	Types map[string]string
}

// Sample is one line of the exposition.
type Sample struct {
	Name   string
	Labels map[string]string
	Value  float64
}

// Key is the sample written as the dashboard names series:
// name{a="x",b="y"}, labels in order.
func (s Sample) Key() string {
	if len(s.Labels) == 0 {
		return s.Name
	}
	names := make([]string, 0, len(s.Labels))
	for n := range s.Labels {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, n := range names {
		parts = append(parts, n+"="+strconv.Quote(s.Labels[n]))
	}
	return s.Name + "{" + strings.Join(parts, ",") + "}"
}

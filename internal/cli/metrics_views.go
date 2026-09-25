package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/x-chunk/celadon/internal/config"
	"github.com/x-chunk/celadon/internal/metrics"
	"github.com/x-chunk/celadon/internal/output"
	"github.com/x-chunk/celadon/internal/query"
)

const patternHelp = `A series is named as the catalog names it: the metric, and its labels when it
has any — db_queries_total{op="select"}. A pattern may be a whole key, a metric
name (every series of it), or a glob over either: 'db_*', '*_p99',
'http_requests_total{*'. Quote patterns, or the shell expands them.`

// matchSeries resolves patterns against the catalog, in catalog order and
// without repeats. Every pattern has to match something: a typo is refused
// rather than answered with nothing.
func matchSeries(info metrics.Info, patterns []string) ([]metrics.SeriesInfo, error) {
	catalog := append([]metrics.SeriesInfo(nil), info.Catalog...)
	sort.Slice(catalog, func(a, b int) bool { return catalog[a].Key < catalog[b].Key })
	if len(patterns) == 0 {
		return catalog, nil
	}
	seen := map[string]bool{}
	var out []metrics.SeriesInfo
	for _, pat := range patterns {
		pat = strings.TrimSpace(pat)
		glob := strings.ContainsAny(pat, "*?[")
		if glob {
			if _, err := path.Match(pat, ""); err != nil {
				return nil, usageError(fmt.Errorf("invalid pattern %q: %w", pat, err))
			}
		}
		found := false
		for _, s := range catalog {
			ok := s.Key == pat || s.Name == pat
			if !ok && glob {
				ok, _ = path.Match(pat, s.Key)
				if !ok {
					ok, _ = path.Match(pat, s.Name)
				}
			}
			if ok {
				found = true
				if !seen[s.Key] {
					seen[s.Key] = true
					out = append(out, s)
				}
			}
		}
		if !found {
			return nil, usageError(fmt.Errorf("no series matches %q (`celadon metrics list` shows them; a counter that never moved has none yet)", pat))
		}
	}
	return out, nil
}

// connect builds the client and reads the catalog, which almost every view
// needs to know what it is looking at.
func connect(env *Env, cmd *cobra.Command) (*metrics.Client, config.MetricsResolved, metrics.Info, context.Context, context.CancelFunc, error) {
	c, r, err := env.MetricsClient()
	if err != nil {
		return nil, r, metrics.Info{}, nil, nil, err
	}
	ctx, cancel := env.Context(cmd, false)
	info, _, err := c.Series.Info(ctx)
	if err != nil {
		cancel()
		return nil, r, info, nil, nil, metricsCall(r, err)
	}
	return c, r, info, ctx, cancel, nil
}

// ── overview ───────────────────────────────────────────────────────────────

// tileValue is one tile, read off a snapshot.
type tileValue struct {
	Title  string       `json:"title"`
	Metric string       `json:"metric"`
	Unit   metrics.Unit `json:"unit"`
	Value  *float64     `json:"value"`
	Shown  string       `json:"shown"`
}

func tiles(info metrics.Info, snap metrics.Snapshot) []tileValue {
	out := make([]tileValue, 0, len(info.Layout.Tiles))
	for _, t := range info.Layout.Tiles {
		tv := tileValue{Title: t.Title, Metric: t.Metric, Unit: t.Unit, Shown: output.Dash}
		if v, ok := snap.Sum(info, t.Metric); ok {
			tv.Value = &v
			tv.Shown = output.MetricValue(v, t.Unit)
		}
		out = append(out, tv)
	}
	return out
}

func newMetricsOverviewCmd(env *Env) *cobra.Command {
	var spark time.Duration
	cmd := &cobra.Command{
		Use:     "overview",
		Aliases: []string{"top"},
		Short:   "Show the dashboard's headline numbers, with their recent trend",
		Long: `Show the numbers along the top of the dashboard — CPU, memory, goroutines,
throughput, disk, uptime — with a sparkline of each over --trend, and whether the
process is ready.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, r, info, ctx, cancel, err := connect(env, cmd)
			if err != nil {
				return err
			}
			defer cancel()
			snap, _, err := c.Series.Live(ctx)
			if err != nil {
				return metricsCall(r, err)
			}
			ready, _, rerr := c.Health.Ready(ctx)

			vals := tiles(info, snap)
			trends := map[string][]float64{}
			if spark > 0 && !env.Printer().JSONMode() {
				var keys []string
				for _, t := range info.Layout.Tiles {
					for _, s := range info.SeriesOf(t.Metric) {
						keys = append(keys, s.Key)
					}
				}
				if ans, _, err := c.Series.Query(ctx, metrics.QueryRequest{Keys: keys, Range: spark}); err == nil {
					for _, t := range info.Layout.Tiles {
						trends[t.Metric] = summed(ans, info, t.Metric)
					}
				}
			}

			p := env.Printer()
			payload := map[string]any{"at": snap.At, "tiles": vals, "uptime_seconds": info.Uptime().Seconds()}
			if rerr == nil {
				payload["ready"] = ready
			}
			return p.Result(payload, func() error {
				state := readyWord(ready.Ready)
				if rerr != nil {
					state = "readiness unknown"
				}
				p.Heading(fmt.Sprintf("%s · up %s · %s", r.BaseURL, output.Duration(info.Uptime()), state))
				rows := make([][]string, 0, len(vals))
				for _, v := range vals {
					rows = append(rows, []string{v.Title, v.Shown, output.Sparkline(trends[v.Metric], 30, 0)})
				}
				header := []string{"", "now", "last " + output.Duration(spark)}
				if spark <= 0 {
					header[2] = ""
				}
				p.Table(header, rows)
				return nil
			})
		},
	}
	cmd.Flags().DurationVar(&spark, "trend", 15*time.Minute, "how far back each sparkline reaches (0 for none)")
	return cmd
}

// summed adds a metric's series up point by point, for a tile over a
// labelled metric.
func summed(ans metrics.Answer, info metrics.Info, metric string) []float64 {
	var total []float64
	for _, s := range info.SeriesOf(metric) {
		res, ok := ans.Find(s.Key)
		if !ok {
			continue
		}
		if total == nil {
			total = make([]float64, len(res.Points))
			for i := range total {
				total[i] = math.NaN()
			}
		}
		for i, pt := range res.Points {
			if i >= len(total) || math.IsNaN(pt.Avg) {
				continue
			}
			if math.IsNaN(total[i]) {
				total[i] = 0
			}
			total[i] += pt.Avg
		}
	}
	return total
}

// ── list and get ───────────────────────────────────────────────────────────

func newMetricsListCmd(env *Env) *cobra.Command {
	var kind string
	cmd := &cobra.Command{
		Use:     "list [pattern...]",
		Aliases: []string{"ls", "catalog"},
		Short:   "List the series the process publishes, with their values now",
		Long: "List the catalog: every series, what kind of measurement it is, its unit,\n" +
			"its value now and what it means.\n\n" + patternHelp,
		Example: `  celadon metrics list
  celadon metrics list 'db_*' --kind counter`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if kind != "" && kind != string(metrics.KindCounter) && kind != string(metrics.KindGauge) && kind != string(metrics.KindHistogram) {
				return usageError(fmt.Errorf("invalid --kind %q: use counter, gauge or histogram", kind))
			}
			c, r, info, ctx, cancel, err := connect(env, cmd)
			if err != nil {
				return err
			}
			defer cancel()
			series, err := matchSeries(info, args)
			if err != nil {
				return err
			}
			if kind != "" {
				kept := series[:0]
				for _, s := range series {
					if string(s.Kind) == kind {
						kept = append(kept, s)
					}
				}
				series = kept
			}
			snap, _, err := c.Series.Live(ctx)
			if err != nil {
				return metricsCall(r, err)
			}
			type row struct {
				metrics.SeriesInfo
				Value *float64 `json:"value"`
			}
			out := make([]row, 0, len(series))
			for _, s := range series {
				rw := row{SeriesInfo: s}
				if v, ok := snap.Value(s.Key); ok {
					rw.Value = &v
				}
				out = append(out, rw)
			}
			p := env.Printer()
			return p.Result(out, func() error {
				if len(out) == 0 {
					p.Info("No series.")
					return nil
				}
				rows := make([][]string, 0, len(out))
				for _, s := range out {
					v := output.Dash
					if s.Value != nil {
						v = output.MetricValue(*s.Value, s.Unit)
					}
					rows = append(rows, []string{s.Key, string(s.Kind), unitName(s.Unit), v, fit(env, s.Help, 100)})
				}
				p.Table([]string{"series", "kind", "unit", "now", "measures"}, rows)
				p.Info("%d series", len(out))
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "", "only counters, gauges or histograms")
	_ = cmd.RegisterFlagCompletionFunc("kind", fixedCompletion("counter", "gauge", "histogram"))
	return cmd
}

func unitName(u metrics.Unit) string {
	switch u {
	case metrics.UnitPerSec:
		return "/s"
	case metrics.UnitNone:
		return output.Dash
	}
	return string(u)
}

func newMetricsGetCmd(env *Env) *cobra.Command {
	var raw bool
	cmd := &cobra.Command{
		Use:   "get <pattern>...",
		Short: "Print the value of series now",
		Long:  "Print the value of each matching series now.\n\n" + patternHelp,
		Example: `  celadon metrics get process_cpu_percent
  celadon metrics get 'db_*' --raw
  celadon metrics get go_goroutines -o json`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, r, info, ctx, cancel, err := connect(env, cmd)
			if err != nil {
				return err
			}
			defer cancel()
			series, err := matchSeries(info, args)
			if err != nil {
				return err
			}
			snap, _, err := c.Series.Live(ctx)
			if err != nil {
				return metricsCall(r, err)
			}
			values := map[string]*float64{}
			for _, s := range series {
				if v, ok := snap.Value(s.Key); ok {
					values[s.Key] = &v
				} else {
					values[s.Key] = nil
				}
			}
			p := env.Printer()
			return p.Result(values, func() error {
				if len(series) == 1 && raw {
					if v := values[series[0].Key]; v != nil {
						p.Println(formatRaw(*v))
					}
					return nil
				}
				rows := make([][]string, 0, len(series))
				for _, s := range series {
					v := output.Dash
					if x := values[s.Key]; x != nil {
						v = output.MetricValue(*x, s.Unit)
						if raw {
							v = formatRaw(*x)
						}
					}
					rows = append(rows, []string{s.Key, v})
				}
				p.Table([]string{"series", "now"}, rows)
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&raw, "raw", false, "print plain numbers, without units")
	return cmd
}

func formatRaw(v float64) string { return fmt.Sprintf("%g", v) }

// ── query ──────────────────────────────────────────────────────────────────

func newMetricsQueryCmd(env *Env) *cobra.Command {
	var (
		rangeText, fromText, toText string
		step                        time.Duration
		points                      bool
		width                       int
	)
	cmd := &cobra.Command{
		Use:   "query <pattern>...",
		Short: "Read series over a window of history",
		Long: `Read series over a window: by default each series summed up — lowest,
average, highest and last — with a sparkline of the window; with --points every
point of it.

The window is --range ending now (15m, 6h, 7d) or --from and --to. The listener
answers from the finest resolution that still covers the window — seconds for
the last 15 minutes, minutes for two days, hours beyond — unless --step asks.

` + patternHelp,
		Example: `  celadon metrics query process_cpu_percent --range 1h
  celadon metrics query 'db_query_seconds_p99*' --range 7d
  celadon metrics query go_goroutines --from 2026-09-24 --to 2026-09-25 --step 10m --points`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := metrics.QueryRequest{Step: step}
			now := env.now()
			if fromText != "" || toText != "" {
				if cmd.Flags().Changed("range") {
					return usageError(errors.New("--range and --from/--to cannot be used together"))
				}
				from, err := query.ParseMoment(fromText, now)
				if err != nil || from.IsZero() {
					return usageError(fmt.Errorf("invalid --from %q", fromText))
				}
				to := now
				if toText != "" {
					if to, err = query.ParseMoment(toText, now); err != nil || to.IsZero() {
						return usageError(fmt.Errorf("invalid --to %q", toText))
					}
				}
				if !to.After(from) {
					return usageError(errors.New("--to is not after --from"))
				}
				req.From, req.To = from, to
			} else {
				secs, err := query.Seconds(rangeText)
				if err != nil || secs <= 0 {
					return usageError(fmt.Errorf("invalid --range %q: use 15m, 6h or 7d", rangeText))
				}
				req.Range = time.Duration(secs) * time.Second
			}
			if step < 0 {
				return usageError(errors.New("--step cannot be negative"))
			}

			c, r, info, ctx, cancel, err := connect(env, cmd)
			if err != nil {
				return err
			}
			defer cancel()
			series, err := matchSeries(info, args)
			if err != nil {
				return err
			}
			for _, s := range series {
				req.Keys = append(req.Keys, s.Key)
			}
			ans, _, err := c.Series.Query(ctx, req)
			if err != nil {
				return metricsCall(r, err)
			}

			p := env.Printer()
			return p.Result(ans, func() error {
				p.Heading(fmt.Sprintf("%s → %s · %s points, one per %s",
					output.Time(ans.From), output.Time(ans.To), ans.Tier, output.Duration(ans.Step())))
				if points {
					for i, s := range ans.Series {
						if i > 0 {
							p.Println()
						}
						p.Heading(s.Info.Key)
						rows := make([][]string, 0, len(s.Points))
						for _, pt := range s.Points {
							rows = append(rows, []string{pt.At.Local().Format("2006-01-02 15:04:05"),
								output.MetricValue(pt.Avg, s.Info.Unit), output.MetricValue(pt.Min, s.Info.Unit),
								output.MetricValue(pt.Max, s.Info.Unit), output.MetricValue(pt.Last, s.Info.Unit)})
						}
						p.Table([]string{"at", "avg", "min", "max", "last"}, rows)
					}
					return nil
				}
				w := width
				if w <= 0 {
					w = 40
					if tw := env.IO.Width(); tw > 0 {
						w = min(max(tw-80, 16), 80)
					}
				}
				rows := make([][]string, 0, len(ans.Series))
				for _, s := range ans.Series {
					values := make([]float64, len(s.Points))
					for i, pt := range s.Points {
						values[i] = pt.Avg
					}
					st, ok := s.Stats()
					cells := []string{s.Info.Key, output.Dash, output.Dash, output.Dash, output.Dash}
					if ok {
						u := s.Info.Unit
						cells = []string{s.Info.Key, output.MetricValue(st.Min, u), output.MetricValue(st.Avg, u),
							output.MetricValue(st.Max, u), output.MetricValue(st.Last, u)}
					}
					rows = append(rows, append(cells, output.Sparkline(values, w, 0)))
				}
				p.Table([]string{"series", "min", "avg", "max", "last", "trend"}, rows)
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&rangeText, "range", "r", "1h", "a window ending now: 15m, 6h, 7d")
	cmd.Flags().StringVar(&fromText, "from", "", "the start of an explicit window: 2026-09-24, \"2026-09-24 18:00\", RFC 3339")
	cmd.Flags().StringVar(&toText, "to", "", "the end of an explicit window (default now)")
	cmd.Flags().DurationVar(&step, "step", 0, "the width of one point (default: the listener's choice)")
	cmd.Flags().BoolVar(&points, "points", false, "print every point rather than a summary")
	cmd.Flags().IntVar(&width, "width", 0, "the width of the sparklines, in cells")
	return cmd
}

// ── watch ──────────────────────────────────────────────────────────────────

func newMetricsWatchCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch [pattern...]",
		Short: "Follow series live, one reading per scrape",
		Long: `Follow series over the live stream, one reading per scrape (a second, by
default), until interrupted. Without patterns it follows the dashboard's
headline numbers.

On a terminal the readings are redrawn in place; into a pipe each reading is a
line, and with -o json a JSON object per line.

` + patternHelp,
		Example: `  celadon metrics watch
  celadon metrics watch 'http_*' go_goroutines
  celadon metrics watch process_cpu_percent -o json | jq .values`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, r, err := env.MetricsClient()
			if err != nil {
				return err
			}
			// The catalog within the usual timeout; the stream without one.
			infoCtx, cancel := env.Context(cmd, false)
			info, _, err := c.Series.Info(infoCtx)
			cancel()
			if err != nil {
				return metricsCall(r, err)
			}
			var rows []watchRow
			if len(args) == 0 {
				for _, t := range info.Layout.Tiles {
					rows = append(rows, watchRow{label: t.Title, metric: t.Metric, unit: t.Unit})
				}
			} else {
				series, err := matchSeries(info, args)
				if err != nil {
					return err
				}
				for _, s := range series {
					rows = append(rows, watchRow{label: s.Key, key: s.Key, unit: s.Unit})
				}
			}

			ctx, stop := env.Context(cmd, true)
			defer stop()
			stream, _, err := c.Series.Stream(ctx)
			if err != nil {
				return metricsCall(r, err)
			}
			defer stream.Close()
			return watchLoop(ctx, env, info, rows, stream)
		},
	}
	return cmd
}

type watchRow struct {
	label  string
	key    string // one series
	metric string // or a metric, summed
	unit   metrics.Unit
	trail  []float64
}

// snapshotSource is what watchLoop reads from: the stream, or a test double.
type snapshotSource interface {
	Next() (metrics.Snapshot, error)
}

func watchLoop(ctx context.Context, env *Env, info metrics.Info, rows []watchRow, src snapshotSource) error {
	p := env.Printer()
	redraw := env.IO.OutTTY && !p.JSONMode()
	drawn := 0
	labelW := 0
	for _, rw := range rows {
		labelW = max(labelW, output.Width(rw.label))
	}
	labelW = min(labelW, 48)
	const trailLen = 40
	for {
		snap, err := src.Next()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, metrics.ErrStreamClosed) || errors.Is(err, io.ErrUnexpectedEOF) {
				return errors.New("the listener ended the stream (it restarted?); run the command again")
			}
			return err
		}
		values := make(map[string]*float64, len(rows))
		for i := range rows {
			rw := &rows[i]
			var v float64
			var ok bool
			if rw.key != "" {
				v, ok = snap.Value(rw.key)
			} else {
				v, ok = snap.Sum(info, rw.metric)
			}
			if !ok {
				v = math.NaN()
			} else {
				values[rw.label] = &v
			}
			rw.trail = append(rw.trail, v)
			if len(rw.trail) > trailLen {
				rw.trail = rw.trail[len(rw.trail)-trailLen:]
			}
		}

		switch {
		case p.JSONMode():
			line, _ := json.Marshal(map[string]any{"at": snap.At, "values": values})
			fmt.Fprintln(env.IO.Out, string(line))
		case redraw:
			if drawn > 0 {
				fmt.Fprintf(env.IO.Out, "\x1b[%dA", drawn)
			}
			fmt.Fprintf(env.IO.Out, "\x1b[2K%s\n", output.Sanitize(snap.At.Local().Format("15:04:05"))+"  ctrl+c to stop")
			for _, rw := range rows {
				label := output.Truncate(rw.label, labelW)
				label += strings.Repeat(" ", labelW-output.Width(label))
				v := rw.trail[len(rw.trail)-1]
				fmt.Fprintf(env.IO.Out, "\x1b[2K%s  %12s  %s\n", output.Sanitize(label), output.MetricValue(v, rw.unit), output.Sparkline(rw.trail, trailLen, 0))
			}
			drawn = len(rows) + 1
		default:
			parts := make([]string, 0, len(rows))
			for _, rw := range rows {
				parts = append(parts, rw.label+"="+output.MetricValue(rw.trail[len(rw.trail)-1], rw.unit))
			}
			fmt.Fprintln(env.IO.Out, output.OneLine(snap.At.Local().Format("15:04:05")+" "+strings.Join(parts, " ")))
		}
	}
}

// ── prometheus ─────────────────────────────────────────────────────────────

func newMetricsPromCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "prom [pattern...]",
		Aliases: []string{"prometheus", "scrape"},
		Short:   "Print the Prometheus text exposition",
		Long: `Print the Prometheus text exposition — every metric's current total, the way a
scraper reads it. With patterns, only the matching samples, as a table; with
-o json, the parsed samples. Unlike the rest of this API, counters here are
totals rather than rates.`,
		Example: `  celadon metrics prom > metrics.txt
  celadon metrics prom 'db_*'`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, r, err := env.MetricsClient()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			exp, _, err := c.Prometheus.Scrape(ctx)
			if err != nil {
				return metricsCall(r, err)
			}
			samples := exp.Samples
			if len(args) > 0 {
				var kept []metrics.Sample
				for _, s := range samples {
					for _, pat := range args {
						if ok, _ := path.Match(pat, s.Name); ok || s.Name == pat || s.Key() == pat {
							kept = append(kept, s)
							break
						}
						if ok, _ := path.Match(pat, s.Key()); ok {
							kept = append(kept, s)
							break
						}
					}
				}
				if len(kept) == 0 {
					return usageError(fmt.Errorf("no sample matches %s", strings.Join(args, ", ")))
				}
				samples = kept
			}
			p := env.Printer()
			if p.JSONMode() {
				type js struct {
					Name   string            `json:"name"`
					Labels map[string]string `json:"labels,omitempty"`
					Value  *float64          `json:"value"`
					Type   string            `json:"type,omitempty"`
				}
				out := make([]js, 0, len(samples))
				for _, s := range samples {
					item := js{Name: s.Name, Labels: s.Labels, Type: exp.Types[s.Name]}
					if !math.IsNaN(s.Value) && !math.IsInf(s.Value, 0) {
						v := s.Value
						item.Value = &v
					}
					out = append(out, item)
				}
				return p.JSON(out)
			}
			if len(args) == 0 {
				_, err := io.WriteString(env.IO.Out, exp.Raw)
				return err
			}
			rows := make([][]string, 0, len(samples))
			for _, s := range samples {
				rows = append(rows, []string{s.Key(), exp.Types[baseName(s.Name, exp.Types)], formatRaw(s.Value)})
			}
			p.Table([]string{"sample", "type", "value"}, rows)
			return nil
		},
	}
	return cmd
}

// baseName finds the declared metric a sample belongs to: a histogram's
// samples are named with _bucket, _sum and _count after it.
func baseName(name string, types map[string]string) string {
	if _, ok := types[name]; ok {
		return name
	}
	for _, suffix := range []string{"_bucket", "_sum", "_count"} {
		if b, ok := strings.CutSuffix(name, suffix); ok {
			if _, ok := types[b]; ok {
				return b
			}
		}
	}
	return name
}

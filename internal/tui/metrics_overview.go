package tui

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/x-chunk/celadon/internal/metrics"
	"github.com/x-chunk/celadon/internal/output"
)

// trailLen is how many readings a tile's sparkline keeps.
const trailLen = 150

// metricsOverviewTab is the first screen: the dashboard's headline numbers
// as cards, each with the trend it has been on, beside whether the process
// is ready and how it is configured.
type metricsOverviewTab struct {
	be *backend

	info    *metrics.Info
	ready   *metrics.Readiness
	snap    *metrics.Snapshot
	err     error
	infoSeq int
	liveSeq int
	ticks   int

	// trails are the recent readings of each tile's metric, seeded from the
	// last five minutes and extended by every live reading.
	trails map[string][]float64

	vp      viewport.Model
	content string
}

func newMetricsOverviewTab(be *backend) *metricsOverviewTab {
	return &metricsOverviewTab{be: be, trails: map[string][]float64{}, vp: viewport.New(0, 0)}
}

func (t *metricsOverviewTab) title() string   { return "Overview" }
func (t *metricsOverviewTab) capturing() bool { return false }
func (t *metricsOverviewTab) keys() []keyHelp {
	return []keyHelp{{"r", "refresh"}, {"↑/↓", "scroll"}}
}

func (t *metricsOverviewTab) init() tea.Cmd {
	t.infoSeq++
	return tea.Batch(loadInfo(t.be, "overview-info", t.infoSeq), t.readLive())
}

func (t *metricsOverviewTab) readLive() tea.Cmd {
	t.liveSeq++
	seq := t.liveSeq
	return tea.Batch(
		callMetrics(t.be, "overview-live", seq, func(ctx context.Context, c *metrics.Client) (metrics.Snapshot, *metrics.Meta, error) {
			return c.Series.Live(ctx)
		}),
		callMetrics(t.be, "overview-ready", seq, func(ctx context.Context, c *metrics.Client) (metrics.Readiness, *metrics.Meta, error) {
			return c.Health.Ready(ctx)
		}),
	)
}

// seed fills the trails from the last five minutes, so the cards open on a
// trend rather than on a single dot.
func (t *metricsOverviewTab) seed(info metrics.Info) tea.Cmd {
	var keys []string
	for _, tile := range info.Layout.Tiles {
		for _, s := range info.SeriesOf(tile.Metric) {
			keys = append(keys, s.Key)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	return callMetrics(t.be, "overview-seed", t.infoSeq, func(ctx context.Context, c *metrics.Client) (metrics.Answer, *metrics.Meta, error) {
		return c.Series.Query(ctx, metrics.QueryRequest{Keys: keys, Range: 5 * time.Minute})
	})
}

func (t *metricsOverviewTab) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case done[metrics.Info]:
		if msg.op == "overview-info" && msg.seq == t.infoSeq {
			if msg.err != nil {
				t.err = msg.err
				return nil
			}
			info := msg.val
			t.info = &info
			return t.seed(info)
		}
	case done[metrics.Answer]:
		if msg.op == "overview-seed" && msg.seq == t.infoSeq && msg.err == nil && t.info != nil {
			for _, tile := range t.info.Layout.Tiles {
				if len(t.trails[tile.Metric]) == 0 {
					t.trails[tile.Metric] = sumSeries(msg.val, *t.info, tile.Metric)
				}
			}
		}
	case done[metrics.Snapshot]:
		if msg.op == "overview-live" && msg.seq == t.liveSeq {
			if msg.err != nil {
				t.err = msg.err
				return nil
			}
			t.err = nil
			snap := msg.val
			t.snap = &snap
			if t.info != nil {
				for _, tile := range t.info.Layout.Tiles {
					v, ok := snap.Sum(*t.info, tile.Metric)
					if !ok {
						v = math.NaN()
					}
					trail := append(t.trails[tile.Metric], v)
					if len(trail) > trailLen {
						trail = trail[len(trail)-trailLen:]
					}
					t.trails[tile.Metric] = trail
				}
			}
		}
	case done[metrics.Readiness]:
		if msg.op == "overview-ready" && msg.seq == t.liveSeq && msg.err == nil {
			r := msg.val
			t.ready = &r
		}
	case tickMsg:
		t.ticks++
		// The catalog grows as things first happen; read it again now and
		// then so a new series is not missed.
		if t.ticks%30 == 0 {
			t.infoSeq++
			return tea.Batch(loadInfo(t.be, "overview-info", t.infoSeq), t.readLive())
		}
		return t.readLive()
	case tea.KeyMsg:
		if msg.String() == "r" {
			return t.init()
		}
		var cmd tea.Cmd
		t.vp, cmd = t.vp.Update(msg)
		return cmd
	}
	return nil
}

// sumSeries adds a metric's series up point by point.
func sumSeries(ans metrics.Answer, info metrics.Info, metric string) []float64 {
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
		for i, p := range res.Points {
			if i < len(total) && !math.IsNaN(p.Avg) {
				if math.IsNaN(total[i]) {
					total[i] = 0
				}
				total[i] += p.Avg
			}
		}
	}
	return total
}

func (t *metricsOverviewTab) view(width, height int) string {
	content := t.render(width - 1)
	t.vp.Width, t.vp.Height = width, height
	if content != t.content {
		t.content = content
		t.vp.SetContent(content)
	}
	return t.vp.View()
}

func (t *metricsOverviewTab) render(width int) string {
	if t.info == nil {
		if t.err != nil {
			return errorView(t.err, width)
		}
		return loading("the listener")
	}
	perRow := 4
	switch {
	case width < 64:
		perRow = 1
	case width < 100:
		perRow = 2
	}
	cw := width / perRow
	var cards []string
	for _, tile := range t.info.Layout.Tiles {
		cards = append(cards, t.tileCard(tile, cw))
	}
	var b strings.Builder
	if t.err != nil {
		b.WriteString(errorView(t.err, width) + "\n")
	}
	b.WriteString(grid(cards, perRow) + "\n")

	lower := []string{t.readyCard(width), t.processCard(width)}
	if width >= 90 {
		half := width / 2
		b.WriteString(grid([]string{t.readyCard(half), t.processCard(width - half)}, 2))
	} else {
		b.WriteString(strings.Join(lower, "\n"))
	}
	return b.String()
}

func (t *metricsOverviewTab) tileCard(tile metrics.Tile, width int) string {
	inner := width - 4
	trail := t.trails[tile.Metric]
	value := math.NaN()
	if t.snap != nil {
		if v, ok := t.snap.Sum(*t.info, tile.Metric); ok {
			value = v
		}
	}
	ceiling := 0.0
	if tile.Unit == metrics.UnitPercent {
		ceiling = 100
	}
	shown := valueStyle(value, tile.Unit).Render(output.MetricValue(value, tile.Unit))
	body := styleMuted.Render(output.Truncate(tile.Title, inner)) + "\n" + shown + "\n" +
		styleTitle.Render(output.Sparkline(trail, inner, ceiling))
	return card(body, width, false)
}

func (t *metricsOverviewTab) readyCard(width int) string {
	inner := width - 4
	var b strings.Builder
	switch {
	case t.ready == nil:
		b.WriteString(styleHeading.Render("Readiness") + "\n" + loading("readiness"))
	default:
		head := styleOK.Render("● ready")
		if !t.ready.Ready {
			head = styleError.Render("● not ready")
		}
		b.WriteString(styleHeading.Render("Readiness") + "  " + head + "\n")
		b.WriteString(depLine("database", t.ready.Database, inner) + "\n")
		for _, name := range sortedNames(t.ready.NATS) {
			b.WriteString(depLine("nats "+name, t.ready.NATS[name], inner) + "\n")
		}
	}
	return card(strings.TrimRight(b.String(), "\n"), width, t.ready != nil && !t.ready.Ready)
}

func depLine(name, state string, width int) string {
	mark := styleOK.Render("✓")
	switch state {
	case "ok":
	case "", "not configured":
		mark = styleFaint.Render("·")
	default:
		mark = styleError.Render("✗")
	}
	return output.Truncate(mark+" "+fmt.Sprintf("%-14s", name)+" "+styleMuted.Render(output.OneLine(output.Or(state))), width+20)
}

func sortedNames(m map[string]string) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	for i := range names {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	return names
}

func (t *metricsOverviewTab) processCard(width int) string {
	i := t.info
	history := "live only — nothing survives a restart"
	if i.Persisted {
		history = "persisted to Postgres"
	}
	at := output.Dash
	if t.snap != nil && !t.snap.At.IsZero() {
		at = t.snap.At.Local().Format("15:04:05")
	}
	body := styleHeading.Render("Process") + "\n" + fields([]field{
		{"Up", output.Duration(i.Uptime()) + " (since " + output.Time(i.Started) + ")"},
		{"Series", fmt.Sprintf("%d, measured every %s", len(i.Catalog), output.Duration(i.Interval()))},
		{"History", history},
		{"Last reading", at},
	}, width-4)
	return card(body, width, false)
}

package tui

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/x-chunk/celadon/internal/metrics"
	"github.com/x-chunk/celadon/internal/output"
)

// DefaultRefresh is how often the metrics dashboard refreshes the tab in
// front. The listener measures once a second; two is live enough to watch
// and half the requests.
const DefaultRefresh = 2 * time.Second

// NewMetrics builds the metrics dashboard over a metrics client.
func NewMetrics(ctx context.Context, client *metrics.Client, opts Options) *Model {
	be := &backend{ctx: ctx, metrics: client, timeout: requestTimeout}
	refresh := opts.Refresh
	if refresh == 0 {
		refresh = DefaultRefresh
	}
	m := &Model{
		be:      be,
		opts:    opts,
		now:     time.Now,
		metrics: true,
		refresh: refresh,
		note: "Everything here is read from the metrics listener at " + opts.BaseURL + ", refreshed every " +
			output.Duration(max(refresh, 0)) + " while a tab is open. A counter is drawn as its rate per second.",
	}
	m.tabs = []tab{
		newMetricsOverviewTab(be),
		newMetricsHealthTab(be),
	}
	m.started = make([]bool, len(m.tabs))
	return m
}

// RunMetrics starts the metrics dashboard on the terminal and blocks until
// it is closed.
func RunMetrics(ctx context.Context, client *metrics.Client, opts Options) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	return runProgram(ctx, NewMetrics(ctx, client, opts))
}

// ranges are the windows a chart can show, shortest first.
var ranges = []time.Duration{
	5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour,
}

// rangeLabel is how a window is named on screen: 5m, 1h, 7d.
func rangeLabel(d time.Duration) string {
	switch {
	case d >= 24*time.Hour && d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	case d >= time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	}
	return fmt.Sprintf("%dm", d/time.Minute)
}

// rangeBar draws the choice of windows with the chosen one lit.
func rangeBar(chosen int) string {
	parts := make([]string, len(ranges))
	for i, r := range ranges {
		if i == chosen {
			parts[i] = styleTabActive.Render(rangeLabel(r))
		} else {
			parts[i] = styleFaint.Render(" " + rangeLabel(r) + " ")
		}
	}
	return strings.Join(parts, "")
}

// refreshEvery is how many ticks a chart of a window waits between reads: a
// minute-resolution chart gains nothing from being read every two seconds.
func refreshEvery(window time.Duration) int {
	switch {
	case window <= 15*time.Minute:
		return 1
	case window <= 6*time.Hour:
		return 5
	}
	return 30
}

// panelLines resolves a panel's refs against the catalog and the answer:
// a metric with labels becomes a line per label combination, labelled by
// the ref and the label values. At most max lines are drawn; the count of
// the rest is returned.
func panelLines(p metrics.Panel, info metrics.Info, ans *metrics.Answer, maxLines int) (lines []plotLine, more int) {
	for _, ref := range p.Refs {
		for _, s := range info.SeriesOf(ref.Metric) {
			if len(lines) >= maxLines {
				more++
				continue
			}
			label := ref.Label
			if label == "" {
				label = ref.Metric
			}
			switch {
			case len(s.Labels) > 0:
				label += " " + strings.Join(labelValues(s.Labels), " ")
			case s.Key != s.Name && strings.HasPrefix(s.Key, s.Name):
				// A key that carries labels the catalog did not spell out.
				label += " " + strings.TrimPrefix(s.Key, s.Name)
			}
			var values []float64
			if ans != nil {
				if res, ok := ans.Find(s.Key); ok {
					values = make([]float64, len(res.Points))
					for i, pt := range res.Points {
						values[i] = pt.Avg
					}
				}
			}
			lines = append(lines, plotLine{label: label, values: values, color: seriesColor(len(lines))})
		}
	}
	return lines, more
}

// labelValues lists a series' label values in the order of their names.
func labelValues(labels map[string]string) []string {
	names := make([]string, 0, len(labels))
	for n := range labels {
		names = append(names, n)
	}
	for i := range names {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, labels[n])
	}
	return out
}

// panelKeys lists every series a set of panels draws.
func panelKeys(panels []metrics.Panel, info metrics.Info) []string {
	seen := map[string]bool{}
	var keys []string
	for _, p := range panels {
		for _, ref := range p.Refs {
			for _, s := range info.SeriesOf(ref.Metric) {
				if !seen[s.Key] {
					seen[s.Key] = true
					keys = append(keys, s.Key)
				}
			}
		}
	}
	return keys
}

// valueStyle colors a reading by how close a percentage is to full.
func valueStyle(v float64, unit metrics.Unit) lipgloss.Style {
	if unit != metrics.UnitPercent || math.IsNaN(v) {
		return styleHeading
	}
	switch {
	case v >= 90:
		return styleError.Bold(true)
	case v >= 75:
		return styleWarn.Bold(true)
	}
	return styleOK.Bold(true)
}

// card draws a small bordered box of the given outer width.
func card(body string, width int, accent bool) string {
	st := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorFaint).Padding(0, 1).Width(width - 2)
	if accent {
		st = st.BorderForeground(colorAccent)
	}
	return st.Render(body)
}

// grid lays cards out in rows of perRow.
func grid(cards []string, perRow int) string {
	var rows []string
	for i := 0; i < len(cards); i += perRow {
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, cards[i:min(i+perRow, len(cards))]...))
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// loadInfo reads the catalog and the layout.
func loadInfo(be *backend, op string, seq int) tea.Cmd {
	return callMetrics(be, op, seq, func(ctx context.Context, c *metrics.Client) (metrics.Info, *metrics.Meta, error) {
		return c.Series.Info(ctx)
	})
}

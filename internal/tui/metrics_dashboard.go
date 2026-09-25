package tui

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/x-chunk/celadon/internal/metrics"
	"github.com/x-chunk/celadon/internal/output"
)

// maxPanelLines caps the lines one chart draws; a metric with more label
// combinations than that says how many it left out.
const maxPanelLines = 6

// metricsDashboardTab draws the server's dashboard layout: one section at a
// time, its panels as charts over the chosen window.
type metricsDashboardTab struct {
	be *backend

	info    *metrics.Info
	infoSeq int
	section int
	window  int

	answer  *metrics.Answer
	err     error
	loading bool
	seq     int
	ticks   int

	vp      viewport.Model
	content string
}

func newMetricsDashboardTab(be *backend) *metricsDashboardTab {
	return &metricsDashboardTab{be: be, window: 2, vp: viewport.New(0, 0)}
}

func (t *metricsDashboardTab) title() string   { return "Dashboard" }
func (t *metricsDashboardTab) capturing() bool { return false }
func (t *metricsDashboardTab) keys() []keyHelp {
	return []keyHelp{{"←/→", "section"}, {"-/+", "window"}, {"↑/↓", "scroll"}, {"r", "refresh"}}
}

func (t *metricsDashboardTab) init() tea.Cmd {
	t.infoSeq++
	return loadInfo(t.be, "dashboard-info", t.infoSeq)
}

// load reads the open section over the chosen window.
func (t *metricsDashboardTab) load() tea.Cmd {
	if t.info == nil || len(t.info.Layout.Sections) == 0 {
		return nil
	}
	keys := panelKeys(t.info.Layout.Sections[t.section].Panels, *t.info)
	if len(keys) == 0 {
		t.answer = &metrics.Answer{}
		return nil
	}
	t.seq++
	t.loading = true
	req := metrics.QueryRequest{Keys: keys, Range: ranges[t.window]}
	return callMetrics(t.be, "dashboard", t.seq, func(ctx context.Context, c *metrics.Client) (metrics.Answer, *metrics.Meta, error) {
		return c.Series.Query(ctx, req)
	})
}

func (t *metricsDashboardTab) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case done[metrics.Info]:
		if msg.op == "dashboard-info" && msg.seq == t.infoSeq {
			if msg.err != nil {
				t.err = msg.err
				return nil
			}
			info := msg.val
			t.info = &info
			t.section = min(t.section, max(len(info.Layout.Sections)-1, 0))
			return t.load()
		}
	case done[metrics.Answer]:
		if msg.op == "dashboard" && msg.seq == t.seq {
			t.loading = false
			if msg.err != nil {
				t.err = msg.err
				return nil
			}
			t.err = nil
			ans := msg.val
			t.answer = &ans
		}
	case tickMsg:
		t.ticks++
		if t.ticks%refreshEvery(ranges[t.window]) == 0 && !t.loading {
			return t.load()
		}
	case tea.KeyMsg:
		n := 0
		if t.info != nil {
			n = len(t.info.Layout.Sections)
		}
		switch msg.String() {
		case "right", "l", "]":
			if n > 0 {
				t.section = (t.section + 1) % n
				t.answer = nil
				t.vp.GotoTop()
				return t.load()
			}
		case "left", "h", "[":
			if n > 0 {
				t.section = (t.section + n - 1) % n
				t.answer = nil
				t.vp.GotoTop()
				return t.load()
			}
		case "+", "=":
			if t.window < len(ranges)-1 {
				t.window++
				return t.load()
			}
		case "-", "_":
			if t.window > 0 {
				t.window--
				return t.load()
			}
		case "r":
			return t.init()
		default:
			var cmd tea.Cmd
			t.vp, cmd = t.vp.Update(msg)
			return cmd
		}
	}
	return nil
}

func (t *metricsDashboardTab) view(width, height int) string {
	if t.info == nil {
		if t.err != nil {
			return errorView(t.err, width)
		}
		return loading("the dashboard")
	}
	if len(t.info.Layout.Sections) == 0 {
		return styleMuted.Render("The listener's dashboard has no sections.")
	}
	head := t.header(width)
	bodyH := height - lipgloss.Height(head)
	content := t.render(width - 1)
	t.vp.Width, t.vp.Height = width, max(bodyH, 1)
	if content != t.content {
		t.content = content
		t.vp.SetContent(content)
	}
	return head + "\n" + t.vp.View()
}

// header is the row of sections, the row of windows, and the open section's
// description.
func (t *metricsDashboardTab) header(width int) string {
	var parts []string
	for i, s := range t.info.Layout.Sections {
		if i == t.section {
			parts = append(parts, styleTabActive.Render(s.Title))
		} else {
			parts = append(parts, styleTabInactive.Render(s.Title))
		}
	}
	sections := output.Truncate(strings.Join(parts, ""), width)
	status := ""
	switch {
	case t.loading:
		status = styleFaint.Render("  reading…")
	case t.answer != nil && t.answer.StepMs > 0:
		status = styleFaint.Render("  " + string(t.answer.Tier) + " points, one per " + output.Duration(t.answer.Step()))
	}
	windows := rangeBar(t.window) + status
	sec := t.info.Layout.Sections[t.section]
	out := sections + "\n" + windows
	if sec.Help != "" {
		out += "\n" + styleMuted.Render(output.Truncate(output.OneLine(sec.Help), width))
	}
	return out
}

func (t *metricsDashboardTab) render(width int) string {
	sec := t.info.Layout.Sections[t.section]
	var b strings.Builder
	if t.err != nil {
		b.WriteString(errorView(t.err, width) + "\n")
	}
	twoCols := width >= 110
	half := width / 2
	var row []string
	flush := func() {
		if len(row) > 0 {
			b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, row...) + "\n")
			row = nil
		}
	}
	for _, p := range sec.Panels {
		if !twoCols || p.Wide {
			flush()
			b.WriteString(t.panel(p, width) + "\n")
			continue
		}
		w := half
		if len(row) == 1 {
			w = width - half
		}
		row = append(row, t.panel(p, w))
		if len(row) == 2 {
			flush()
		}
	}
	flush()
	return strings.TrimRight(b.String(), "\n")
}

// panel draws one chart in a box of the given outer width.
func (t *metricsDashboardTab) panel(p metrics.Panel, width int) string {
	inner := width - 4
	lines, more := panelLines(p, *t.info, t.answer, maxPanelLines)
	var b strings.Builder
	b.WriteString(styleHeading.Render(output.Truncate(p.Title, inner)))
	if p.Help != "" {
		b.WriteString("\n" + styleFaint.Render(output.Truncate(output.OneLine(p.Help), inner)))
	}
	b.WriteString("\n")
	switch {
	case len(lines) == 0:
		b.WriteString(styleMuted.Render(wrapText("Nothing has happened here yet — a metric publishes no series until it first moves.", inner)))
	case t.answer == nil:
		b.WriteString(loading("the chart"))
	default:
		spec := chartSpec{lines: lines, unit: p.Unit, ceiling: p.Max, stack: p.Kind == "stack", from: t.answer.From, to: t.answer.To}
		b.WriteString(renderChart(spec, inner, 9) + "\n")
		b.WriteString(legend(lines, p.Unit, inner))
		if more > 0 {
			b.WriteString(styleFaint.Render("   +" + plural(int64(more), "more series", "more series")))
		}
	}
	return card(b.String(), width, false)
}

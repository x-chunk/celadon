package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/x-chunk/celadon/internal/metrics"
	"github.com/x-chunk/celadon/internal/output"
)

// metricsSeriesTab browses the catalog: every series, filtered by what is
// typed, and the one selected drawn over the chosen window.
type metricsSeriesTab struct {
	be *backend

	info    *metrics.Info
	infoSeq int
	err     error

	filter  textinput.Model
	typing  bool
	visible []metrics.SeriesInfo
	cur     selection
	window  int

	answer  *metrics.Answer
	ansKey  string
	ansErr  error
	loading bool
	seq     int
	ticks   int
}

func newMetricsSeriesTab(be *backend) *metricsSeriesTab {
	in := newInput()
	in.Prompt = "/ "
	in.Placeholder = "filter: a name, a label, a word of the help"
	in.CharLimit = 128
	return &metricsSeriesTab{be: be, filter: in, window: 2}
}

func (t *metricsSeriesTab) title() string   { return "Series" }
func (t *metricsSeriesTab) capturing() bool { return t.typing }
func (t *metricsSeriesTab) keys() []keyHelp {
	if t.typing {
		return []keyHelp{{"enter", "keep"}, {"esc", "clear"}}
	}
	return []keyHelp{{"/", "filter"}, {"↑/↓", "choose"}, {"-/+", "window"}, {"r", "refresh"}}
}

func (t *metricsSeriesTab) init() tea.Cmd {
	t.infoSeq++
	return loadInfo(t.be, "series-info", t.infoSeq)
}

// apply narrows the catalog to what the filter says, keeping the selection
// on the same series when it survives.
func (t *metricsSeriesTab) apply() {
	if t.info == nil {
		return
	}
	keep := ""
	if s, ok := t.selected(); ok {
		keep = s.Key
	}
	words := strings.Fields(strings.ToLower(t.filter.Value()))
	t.visible = t.visible[:0]
	for _, s := range t.info.Catalog {
		hay := strings.ToLower(s.Key + " " + s.Help + " " + string(s.Kind) + " " + string(s.Unit))
		ok := true
		for _, w := range words {
			if !strings.Contains(hay, w) {
				ok = false
				break
			}
		}
		if ok {
			t.visible = append(t.visible, s)
		}
	}
	sort.Slice(t.visible, func(a, b int) bool { return t.visible[a].Key < t.visible[b].Key })
	t.cur = selection{}
	for i, s := range t.visible {
		if s.Key == keep {
			t.cur.pos = i
		}
	}
}

func (t *metricsSeriesTab) selected() (metrics.SeriesInfo, bool) {
	if len(t.visible) == 0 || t.cur.pos >= len(t.visible) {
		return metrics.SeriesInfo{}, false
	}
	return t.visible[t.cur.pos], true
}

// load reads the selected series over the chosen window.
func (t *metricsSeriesTab) load() tea.Cmd {
	s, ok := t.selected()
	if !ok {
		return nil
	}
	t.seq++
	t.loading = true
	key := s.Key
	req := metrics.QueryRequest{Keys: []string{key}, Range: ranges[t.window]}
	return callMetrics(t.be, "series:"+key, t.seq, func(ctx context.Context, c *metrics.Client) (metrics.Answer, *metrics.Meta, error) {
		return c.Series.Query(ctx, req)
	})
}

func (t *metricsSeriesTab) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case done[metrics.Info]:
		if msg.op == "series-info" && msg.seq == t.infoSeq {
			if msg.err != nil {
				t.err = msg.err
				return nil
			}
			info := msg.val
			t.info = &info
			t.apply()
			return t.load()
		}
	case done[metrics.Answer]:
		if strings.HasPrefix(msg.op, "series:") && msg.seq == t.seq {
			t.loading = false
			t.ansKey = strings.TrimPrefix(msg.op, "series:")
			t.ansErr = msg.err
			if msg.err == nil {
				ans := msg.val
				t.answer = &ans
			}
		}
	case tickMsg:
		t.ticks++
		if t.ticks%refreshEvery(ranges[t.window]) == 0 && !t.loading && !t.typing {
			return t.load()
		}
	case tea.KeyMsg:
		return t.key(msg)
	}
	return nil
}

func (t *metricsSeriesTab) key(msg tea.KeyMsg) tea.Cmd {
	if t.typing {
		switch msg.String() {
		case "esc":
			t.typing = false
			t.filter.Blur()
			t.filter.Reset()
			t.apply()
			return t.load()
		case "enter":
			t.typing = false
			t.filter.Blur()
			return t.load()
		}
		var cmd tea.Cmd
		t.filter, cmd = t.filter.Update(msg)
		t.apply()
		return cmd
	}
	switch msg.String() {
	case "/":
		t.typing = true
		return t.filter.Focus()
	case "up", "k":
		t.cur.move(-1, len(t.visible))
		return t.load()
	case "down", "j":
		t.cur.move(1, len(t.visible))
		return t.load()
	case "pgup":
		t.cur.move(-10, len(t.visible))
		return t.load()
	case "pgdown":
		t.cur.move(10, len(t.visible))
		return t.load()
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
	}
	return nil
}

func (t *metricsSeriesTab) view(width, height int) string {
	if t.info == nil {
		if t.err != nil {
			return errorView(t.err, width)
		}
		return loading("the catalog")
	}
	top := styleMuted.Render("Press / to filter.")
	if t.typing || t.filter.Value() != "" {
		t.filter.Width = max(width-4, 10)
		top = t.filter.View()
	}
	bodyH := height - 1
	leftW := min(max(width*2/5, 32), 64)
	rightW := width - leftW
	left := pane(fmt.Sprintf("Series · %d of %d", len(t.visible), len(t.info.Catalog)), t.listBody(leftW-4, bodyH-3), leftW, bodyH, !t.typing)
	right := pane("Over the last "+rangeLabel(ranges[t.window]), t.detail(rightW-4, bodyH-3), rightW, bodyH, false)
	return top + "\n" + lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (t *metricsSeriesTab) listBody(width, height int) string {
	if len(t.visible) == 0 {
		return styleMuted.Render("Nothing matches.")
	}
	from, to := t.cur.window(len(t.visible), height)
	var b strings.Builder
	for i := from; i < to; i++ {
		s := t.visible[i]
		kind := string(s.Kind)[:1]
		row := kind + " " + s.Key
		if i == t.cur.pos {
			b.WriteString(line(row, width, true) + "\n")
		} else {
			b.WriteString(styleFaint.Render(kind) + " " + output.Truncate(output.OneLine(s.Key), max(width-2, 1)) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (t *metricsSeriesTab) detail(width, height int) string {
	s, ok := t.selected()
	if !ok {
		return ""
	}
	var b strings.Builder
	b.WriteString(styleTitle.Render(output.Truncate(output.OneLine(s.Key), width)) + "\n")
	b.WriteString(styleMuted.Render(wrapText(output.Or(s.Help), width)) + "\n")
	kind := string(s.Kind)
	if s.Kind == metrics.KindCounter {
		kind += " — drawn as its rate per second"
	}
	b.WriteString(styleFaint.Render(output.Truncate(kind+" · "+output.Or(string(s.Unit)), width)) + "\n")
	b.WriteString(rangeBar(t.window) + "\n\n")

	used := lipgloss.Height(b.String())
	switch {
	case t.ansErr != nil && t.ansKey == s.Key:
		b.WriteString(errorView(t.ansErr, width))
	case t.answer == nil || t.ansKey != s.Key:
		b.WriteString(loading("the series"))
	default:
		res, _ := t.answer.Find(s.Key)
		values := make([]float64, len(res.Points))
		for i, p := range res.Points {
			values[i] = p.Avg
		}
		ceiling := 0.0
		if s.Unit == metrics.UnitPercent {
			ceiling = 100
		}
		chartH := max(min(height-used-3, 16), 5)
		spec := chartSpec{lines: []plotLine{{label: s.Name, values: values, color: seriesColor(0)}},
			unit: s.Unit, ceiling: ceiling, from: t.answer.From, to: t.answer.To}
		b.WriteString(renderChart(spec, width, chartH) + "\n\n")
		if st, ok := res.Stats(); ok {
			u := s.Unit
			stat := func(label string, v float64) string {
				return styleLabel.Render(label) + " " + styleHeading.Render(output.MetricValue(v, u))
			}
			b.WriteString(stat("min", st.Min) + "   " + stat("avg", st.Avg) + "   " + stat("max", st.Max) + "   " + stat("last", st.Last))
			b.WriteString("\n" + styleFaint.Render(fmt.Sprintf("%d %s points, one per %s", len(res.Points), t.answer.Tier, output.Duration(t.answer.Step()))))
		} else {
			b.WriteString(styleMuted.Render("No readings in this window."))
		}
	}
	return clip(b.String(), height)
}

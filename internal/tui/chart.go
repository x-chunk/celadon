package tui

import (
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/x-chunk/celadon/internal/metrics"
	"github.com/x-chunk/celadon/internal/output"
)

// palette colors the lines of a chart, in order. Each is a light/dark pair
// chosen to stay apart from its neighbors on either background.
var palette = []lipgloss.AdaptiveColor{
	{Light: "#1F77B4", Dark: "#6CB6FF"}, // blue
	{Light: "#D1242F", Dark: "#FF8182"}, // red
	{Light: "#1A7F37", Dark: "#7EE787"}, // green
	{Light: "#9A6700", Dark: "#F2CC60"}, // amber
	{Light: "#8250DF", Dark: "#D2A8FF"}, // violet
	{Light: "#0E7C86", Dark: "#76E3EA"}, // teal
}

func seriesColor(i int) lipgloss.AdaptiveColor { return palette[i%len(palette)] }

// plotLine is one series on a chart.
type plotLine struct {
	label  string
	values []float64 // NaN is a gap
	color  lipgloss.AdaptiveColor
}

// chartSpec is everything a chart is drawn from.
type chartSpec struct {
	lines    []plotLine
	unit     metrics.Unit
	ceiling  float64 // pins the top of the axis when > 0
	stack    bool    // draw each line on top of the ones before it
	from, to time.Time
}

// braille dots, by column (0, 1) and row (0 at the top to 3).
var brailleDots = [2][4]rune{
	{0x01, 0x02, 0x04, 0x40},
	{0x08, 0x10, 0x20, 0x80},
}

// renderChart draws lines in width×height cells: an axis of values on the
// left, the plot in braille — two dots across and four down per cell, so a
// line is drawn at four times the resolution a block would give — and the
// window's two ends along the bottom.
func renderChart(spec chartSpec, width, height int) string {
	if width < 20 || height < 3 {
		return ""
	}
	lines := spec.lines
	if spec.stack {
		lines = stacked(lines)
	}

	// The scale: from zero for readings that never go below it, which is
	// almost every one, so a chart does not exaggerate a wobble.
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, l := range lines {
		for _, v := range l.values {
			if !math.IsNaN(v) {
				lo, hi = math.Min(lo, v), math.Max(hi, v)
			}
		}
	}
	empty := math.IsInf(lo, 1)
	if empty {
		lo, hi = 0, 1
	}
	if lo > 0 {
		lo = 0
	}
	if spec.ceiling > 0 {
		hi = math.Max(hi, spec.ceiling)
	} else {
		hi += (hi - lo) * 0.08
	}
	if hi <= lo {
		hi = lo + 1
	}

	axisW := 0
	labels := []string{output.MetricValue(hi, spec.unit), output.MetricValue((hi+lo)/2, spec.unit), output.MetricValue(lo, spec.unit)}
	for _, l := range labels {
		axisW = max(axisW, output.Width(l))
	}
	plotW := width - axisW - 2
	plotH := height - 1
	if plotW < 8 {
		return ""
	}

	dotsW, dotsH := plotW*2, plotH*4
	cells := make([][]rune, plotH)
	owner := make([][]int, plotH)
	for r := range cells {
		cells[r] = make([]rune, plotW)
		owner[r] = make([]int, plotW)
		for c := range owner[r] {
			owner[r][c] = -1
		}
	}
	set := func(x, y, series int) {
		if x < 0 || x >= dotsW || y < 0 || y >= dotsH {
			return
		}
		row, col := y/4, x/2
		cells[row][col] |= brailleDots[x%2][y%4]
		owner[row][col] = series
	}
	toY := func(v float64) int {
		frac := (v - lo) / (hi - lo)
		return dotsH - 1 - int(math.Round(frac*float64(dotsH-1)))
	}

	for si, l := range lines {
		pts := output.Resample(l.values, dotsW)
		// Stretch a short series across the whole width, so a window that
		// has only a few points still reads as a line from end to end.
		scale := 1.0
		if len(pts) > 1 {
			scale = float64(dotsW-1) / float64(len(pts)-1)
		}
		prevX, prevY := -1, 0
		for i, v := range pts {
			if math.IsNaN(v) {
				prevX = -1
				continue
			}
			x, y := int(math.Round(float64(i)*scale)), toY(v)
			if prevX >= 0 {
				// Join the dots: step across, filling the rise or fall.
				steps := max(x-prevX, iabs(y-prevY), 1)
				for s := 1; s <= steps; s++ {
					set(prevX+(x-prevX)*s/steps, prevY+(y-prevY)*s/steps, si)
				}
			} else {
				set(x, y, si)
			}
			prevX, prevY = x, y
		}
	}

	var b strings.Builder
	axisStyle := styleFaint
	for r := 0; r < plotH; r++ {
		label := ""
		switch r {
		case 0:
			label = labels[0]
		case plotH / 2:
			if plotH > 2 {
				label = labels[1]
			}
		case plotH - 1:
			label = labels[2]
		}
		b.WriteString(styleMuted.Render(strings.Repeat(" ", axisW-output.Width(label))+label) + axisStyle.Render(" ┤"))
		for c := 0; c < plotW; c++ {
			if cells[r][c] == 0 {
				if r == plotH-1 {
					b.WriteString(axisStyle.Render("⠤"))
				} else {
					b.WriteByte(' ')
				}
				continue
			}
			b.WriteString(lipgloss.NewStyle().Foreground(lines[owner[r][c]].color).Render(string(0x2800 + cells[r][c])))
		}
		b.WriteByte('\n')
	}
	b.WriteString(strings.Repeat(" ", axisW+2) + timeAxis(spec.from, spec.to, plotW))
	if empty {
		return centerNote(b.String(), "no data in this window")
	}
	return b.String()
}

// stacked turns lines into running totals, each drawn over the ones before.
func stacked(lines []plotLine) []plotLine {
	out := make([]plotLine, len(lines))
	var total []float64
	for i, l := range lines {
		if total == nil {
			total = make([]float64, len(l.values))
		}
		vals := make([]float64, len(l.values))
		for j, v := range l.values {
			if j >= len(total) {
				break
			}
			if math.IsNaN(v) {
				vals[j] = math.NaN()
				continue
			}
			total[j] += v
			vals[j] = total[j]
		}
		out[i] = plotLine{label: l.label, values: vals, color: l.color}
	}
	return out
}

// timeAxis writes the window's start at the left and its end at the right.
func timeAxis(from, to time.Time, width int) string {
	if from.IsZero() || to.IsZero() {
		return ""
	}
	layout := "15:04"
	if to.Sub(from) >= 36*time.Hour {
		layout = "Jan 2"
	} else if to.Sub(from) < 10*time.Minute {
		layout = "15:04:05"
	}
	left, right := from.Local().Format(layout), to.Local().Format(layout)
	gap := width - len(left) - len(right)
	if gap < 1 {
		return styleFaint.Render(right)
	}
	return styleFaint.Render(left + strings.Repeat(" ", gap) + right)
}

// centerNote overlays a note on the middle line of a drawn chart.
func centerNote(chart, note string) string {
	rows := strings.Split(chart, "\n")
	mid := len(rows) / 2
	if mid < len(rows) {
		rows[mid] = rows[mid] + "  " + styleMuted.Render(note)
	}
	return strings.Join(rows, "\n")
}

// legend writes each line's color, label and latest value on as few rows as
// the width allows.
func legend(lines []plotLine, unit metrics.Unit, width int) string {
	var rows []string
	cur := ""
	for _, l := range lines {
		last := math.NaN()
		for i := len(l.values) - 1; i >= 0; i-- {
			if !math.IsNaN(l.values[i]) {
				last = l.values[i]
				break
			}
		}
		item := lipgloss.NewStyle().Foreground(l.color).Render("●") + " " +
			output.Truncate(output.OneLine(l.label), 36) + " " + styleHeading.Render(output.MetricValue(last, unit))
		if cur != "" && lipgloss.Width(cur)+lipgloss.Width(item)+3 > width {
			rows = append(rows, cur)
			cur = ""
		}
		if cur != "" {
			cur += "   "
		}
		cur += item
	}
	if cur != "" {
		rows = append(rows, cur)
	}
	return strings.Join(rows, "\n")
}

func iabs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

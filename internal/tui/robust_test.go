package tui

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/x-chunk/celadon/internal/metrics"
	"github.com/x-chunk/celadon/internal/output"
)

// The API's numbers are drawn as counts of cells; none of them may bring the
// interface down, however far off they are.

func TestZBarTakesAnyZ(t *testing.T) {
	want := output.Width(zBar(0, 17))
	for _, z := range []float64{1e300, -1e300, math.Inf(1), math.Inf(-1), math.NaN(), 2.5, -0.4} {
		if got := output.Width(zBar(z, 17)); got != want {
			t.Errorf("zBar(%g) is %d columns, want %d", z, got, want)
		}
	}
}

func TestBarTakesAnyCount(t *testing.T) {
	for _, used := range []int64{-5, 0, 7, 10, 1 << 60} {
		if got := output.Width(bar(used, 10, 10)); got != 10 {
			t.Errorf("bar(%d, 10) is %d columns", used, got)
		}
	}
}

func TestSeriesWithoutAKindAreListed(t *testing.T) {
	tab := newMetricsSeriesTab(&backend{})
	tab.visible = []metrics.SeriesInfo{{Key: "a"}, {Key: "b", Kind: metrics.KindGauge}}
	tab.cur.pos = 1
	if body := tab.listBody(30, 5); body == "" {
		t.Error("nothing was listed")
	}
}

func TestAChartOfAnOverflowingSumKeepsToItsBox(t *testing.T) {
	drawn := make(chan string, 1)
	go func() {
		spec := chartSpec{stack: true, lines: []plotLine{
			{label: "a", values: []float64{1e308, 1e308, 1, math.NaN()}},
			{label: "b", values: []float64{1e308, 1e308, 2, 3}},
		}}
		drawn <- renderChart(spec, 60, 9)
	}()
	select {
	case chart := <-drawn:
		if lines := strings.Split(chart, "\n"); len(lines) != 9 {
			t.Errorf("the chart is %d lines tall, want 9:\n%s", len(lines), chart)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a sum that overflowed to infinity did not finish drawing")
	}
}

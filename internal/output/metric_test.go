package output

import (
	"math"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/x-chunk/celadon/internal/metrics"
)

func TestMetricValue(t *testing.T) {
	cases := []struct {
		v    float64
		unit metrics.Unit
		want string
	}{
		{12.5, metrics.UnitPercent, "12.5%"},
		{0.25, metrics.UnitPercent, "0.25%"},
		{150, metrics.UnitPercent, "150%"},
		{512, metrics.UnitBytes, "512 B"},
		{1536, metrics.UnitBytes, "1.5 KiB"},
		{3.2 * 1024 * 1024 * 1024, metrics.UnitBytes, "3.2 GiB"},
		{0.0023, metrics.UnitSeconds, "2.3ms"},
		{0.0000042, metrics.UnitSeconds, "4.2µs"},
		{12.34, metrics.UnitSeconds, "12.3s"},
		{93600, metrics.UnitSeconds, "1d 2h"},
		{38.456, metrics.UnitPerSec, "38.5/s"},
		{0, metrics.UnitPerSec, "0/s"},
		{12500, metrics.UnitPerSec, "12.5k/s"},
		{1204, metrics.UnitCount, "1,204"},
		{-1204567, metrics.UnitCount, "-1,204,567"},
		{2.5, metrics.UnitCount, "2.5"},
		{0.333333, metrics.UnitNone, "0.333"},
		{math.NaN(), metrics.UnitPercent, "—"},
	}
	for _, c := range cases {
		if got := MetricValue(c.v, c.unit); got != c.want {
			t.Errorf("MetricValue(%v, %q) = %q, want %q", c.v, c.unit, got, c.want)
		}
	}
}

func TestSparkline(t *testing.T) {
	s := Sparkline([]float64{0, 1, 2, 3, 4, 5, 6, 7}, 8, 0)
	if s != "▁▂▃▄▅▆▇█" {
		t.Errorf("rising = %q", s)
	}
	if s := Sparkline([]float64{1, math.NaN(), 1}, 3, 0); []rune(s)[1] != ' ' {
		t.Errorf("gap = %q", s)
	}
	if s := Sparkline([]float64{50, 50}, 2, 100); s != "▅▅" {
		t.Errorf("against a ceiling = %q", s)
	}
	long := make([]float64, 1000)
	for i := range long {
		long[i] = float64(i)
	}
	s = Sparkline(long, 20, 0)
	if utf8.RuneCountInString(s) != 20 || !strings.HasPrefix(s, "▁") || !strings.HasSuffix(s, "█") {
		t.Errorf("resampled = %q", s)
	}
	if Sparkline(nil, 10, 0) != "" || Sparkline([]float64{math.NaN()}, 5, 0) != " " {
		t.Error("empty input")
	}
}

func TestResample(t *testing.T) {
	got := Resample([]float64{1, 3, 5, 7, math.NaN(), math.NaN()}, 3)
	if got[0] != 2 || got[1] != 6 || !math.IsNaN(got[2]) {
		t.Errorf("Resample = %v", got)
	}
}

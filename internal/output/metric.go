package output

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/x-chunk/celadon/internal/metrics"
)

// MetricValue renders a reading the way its unit reads: 12.5%, 1.3 GiB,
// 2.4ms, 38/s, 1,204. A gap (NaN) is the dash.
func MetricValue(v float64, unit metrics.Unit) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return Dash
	}
	switch unit {
	case metrics.UnitPercent:
		return trimFloat(v, precisionFor(v)) + "%"
	case metrics.UnitBytes:
		return Bytes(v)
	case metrics.UnitSeconds:
		return SecondsValue(v)
	case metrics.UnitPerSec:
		return compact(v) + "/s"
	case metrics.UnitCount:
		if v == math.Trunc(v) && math.Abs(v) < 1e15 {
			return thousands(int64(v))
		}
		return compact(v)
	}
	return compact(v)
}

// Bytes renders a byte count in binary units.
func Bytes(v float64) string {
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return sign + strconv.FormatFloat(v, 'f', 0, 64) + " B"
	}
	return sign + trimFloat(v, precisionFor(v)) + " " + units[i]
}

// SecondsValue renders a span given in seconds, from nanoseconds to days.
func SecondsValue(v float64) string {
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	switch {
	case v == 0:
		return "0s"
	case v < 1e-6:
		return sign + trimFloat(v*1e9, precisionFor(v*1e9)) + "ns"
	case v < 1e-3:
		return sign + trimFloat(v*1e6, precisionFor(v*1e6)) + "µs"
	case v < 1:
		return sign + trimFloat(v*1e3, precisionFor(v*1e3)) + "ms"
	case v < 60:
		return sign + trimFloat(v, precisionFor(v)) + "s"
	}
	return sign + Duration(time.Duration(v*float64(time.Second)))
}

// compact renders a plain number with a metric suffix past a thousand.
func compact(v float64) string {
	a := math.Abs(v)
	switch {
	case a >= 1e9:
		return trimFloat(v/1e9, precisionFor(a/1e9)) + "G"
	case a >= 1e6:
		return trimFloat(v/1e6, precisionFor(a/1e6)) + "M"
	case a >= 1e4:
		return trimFloat(v/1e3, precisionFor(a/1e3)) + "k"
	}
	return trimFloat(v, precisionFor(a))
}

// precisionFor keeps about three significant digits.
func precisionFor(v float64) int {
	v = math.Abs(v)
	switch {
	case v == 0 || v >= 100:
		return 0
	case v >= 10:
		return 1
	case v >= 1:
		return 2
	case v >= 0.01:
		return 3
	}
	return 4
}

func trimFloat(v float64, prec int) string {
	s := strconv.FormatFloat(v, 'f', prec, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		return "0"
	}
	return s
}

func thousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// sparks are the eight heights of a sparkline.
var sparks = []rune("▁▂▃▄▅▆▇█")

// Sparkline draws values in width cells, one bar each, scaled between the
// lowest and highest value (or between 0 and ceiling, when ceiling > 0). A
// run of values longer than width is averaged into buckets; a gap is a blank.
func Sparkline(values []float64, width int, ceiling float64) string {
	if width <= 0 || len(values) == 0 {
		return ""
	}
	buckets := Resample(values, width)
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, v := range buckets {
		if !math.IsNaN(v) {
			lo, hi = math.Min(lo, v), math.Max(hi, v)
		}
	}
	if math.IsInf(lo, 1) {
		return strings.Repeat(" ", len(buckets))
	}
	if ceiling > 0 {
		lo, hi = 0, math.Max(ceiling, hi)
	} else if lo > 0 && hi > 0 && lo/hi > 0.5 {
		// A value that barely moves should not fill the height: anchor the
		// floor lower so a flat line reads as flat.
		lo = 0
	}
	var b strings.Builder
	for _, v := range buckets {
		if math.IsNaN(v) {
			b.WriteRune(' ')
			continue
		}
		idx := 0
		if hi > lo {
			idx = int(math.Round((v - lo) / (hi - lo) * float64(len(sparks)-1)))
		}
		b.WriteRune(sparks[min(max(idx, 0), len(sparks)-1)])
	}
	return b.String()
}

// Resample fits values into n buckets: averaging when there are more values
// than buckets, and leaving them be when there are not. A bucket of nothing
// but gaps is a gap.
func Resample(values []float64, n int) []float64 {
	if len(values) <= n {
		return values
	}
	out := make([]float64, n)
	for i := range n {
		from := i * len(values) / n
		to := max((i+1)*len(values)/n, from+1)
		var sum float64
		count := 0
		for _, v := range values[from:to] {
			if !math.IsNaN(v) {
				sum += v
				count++
			}
		}
		if count == 0 {
			out[i] = math.NaN()
		} else {
			out[i] = sum / float64(count)
		}
	}
	return out
}

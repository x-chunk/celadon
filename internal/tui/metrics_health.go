package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/x-chunk/celadon/internal/metrics"
	"github.com/x-chunk/celadon/internal/output"
)

// metricsHealthTab is liveness, readiness and how the listener keeps its
// history, refreshed while it is open.
type metricsHealthTab struct {
	be *backend

	live    *liveCheck
	ready   *metrics.Readiness
	info    *metrics.Info
	err     error
	seq     int
	checked time.Time
	now     func() time.Time
}

// liveCheck is one liveness answer and how long it took.
type liveCheck struct {
	ok      bool
	latency time.Duration
}

func newMetricsHealthTab(be *backend) *metricsHealthTab {
	return &metricsHealthTab{be: be, now: time.Now}
}

func (t *metricsHealthTab) title() string   { return "Health" }
func (t *metricsHealthTab) capturing() bool { return false }
func (t *metricsHealthTab) keys() []keyHelp { return []keyHelp{{"r", "check now"}} }

func (t *metricsHealthTab) init() tea.Cmd { return t.check(true) }

func (t *metricsHealthTab) check(withInfo bool) tea.Cmd {
	t.seq++
	seq := t.seq
	cmds := []tea.Cmd{
		callMetrics(t.be, "health-live", seq, func(ctx context.Context, c *metrics.Client) (liveCheck, *metrics.Meta, error) {
			h, meta, err := c.Health.Live(ctx)
			if err != nil {
				return liveCheck{}, meta, err
			}
			return liveCheck{ok: h.OK, latency: meta.Elapsed}, meta, nil
		}),
		callMetrics(t.be, "health-ready", seq, func(ctx context.Context, c *metrics.Client) (metrics.Readiness, *metrics.Meta, error) {
			return c.Health.Ready(ctx)
		}),
	}
	if withInfo {
		cmds = append(cmds, loadInfo(t.be, "health-info", seq))
	}
	return tea.Batch(cmds...)
}

func (t *metricsHealthTab) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case done[liveCheck]:
		if msg.op == "health-live" && msg.seq == t.seq {
			t.checked = t.now()
			if msg.err != nil {
				t.err, t.live = msg.err, &liveCheck{}
				return nil
			}
			t.err = nil
			lc := msg.val
			t.live = &lc
		}
	case done[metrics.Readiness]:
		if msg.op == "health-ready" && msg.seq == t.seq && msg.err == nil {
			r := msg.val
			t.ready = &r
		}
	case done[metrics.Info]:
		if msg.op == "health-info" && msg.seq == t.seq && msg.err == nil {
			info := msg.val
			t.info = &info
		}
	case tickMsg:
		return t.check(t.info == nil)
	case tea.KeyMsg:
		if msg.String() == "r" {
			return t.check(true)
		}
	}
	return nil
}

func (t *metricsHealthTab) view(width, height int) string {
	if t.live == nil {
		return loading("the listener's health")
	}
	half := width / 2
	var left, right string

	var b strings.Builder
	b.WriteString(styleHeading.Render("Liveness") + "\n")
	if t.live.ok {
		b.WriteString(styleOK.Render("● up") + styleMuted.Render(fmt.Sprintf("   answered in %s", output.Duration(t.live.latency))) + "\n")
	} else {
		b.WriteString(styleError.Render("● unreachable") + "\n")
		if t.err != nil {
			b.WriteString(errorView(t.err, half-4) + "\n")
		}
	}
	b.WriteString("\n" + styleHeading.Render("Readiness") + "\n")
	switch {
	case t.ready == nil:
		b.WriteString(styleMuted.Render("unknown"))
	default:
		if t.ready.Ready {
			b.WriteString(styleOK.Render("● ready — the process can do its work") + "\n")
		} else {
			b.WriteString(styleError.Render("● not ready — a dependency is down") + "\n")
		}
		b.WriteString(depLine("database", t.ready.Database, half-4) + "\n")
		for _, name := range sortedNames(t.ready.NATS) {
			b.WriteString(depLine("nats "+name, t.ready.NATS[name], half-4) + "\n")
		}
	}
	if !t.checked.IsZero() {
		b.WriteString("\n" + styleFaint.Render("checked at "+t.checked.Local().Format("15:04:05")))
	}
	left = card(strings.TrimRight(b.String(), "\n"), half, t.ready != nil && !t.ready.Ready)

	if t.info != nil {
		i := t.info
		history := "live only — the history does not survive a restart"
		if i.Persisted {
			history = "persisted to Postgres"
		}
		right = card(styleHeading.Render("Listener")+"\n"+fields([]field{
			{"Started", output.Time(i.Started)},
			{"Up", output.Duration(i.Uptime())},
			{"Measured every", output.Duration(i.Interval())},
			{"Seconds kept", output.Duration(i.LiveWindow()) + " in memory"},
			{"Minutes kept", output.Duration(i.RecentWindow()) + " in memory, " + output.Duration(i.MinuteRetention()) + " in all"},
			{"Hours kept", output.Duration(i.HourRetention())},
			{"History", history},
			{"Series", fmt.Sprint(len(i.Catalog))},
			{"Dashboard", fmt.Sprintf("%d tiles, %d sections", len(i.Layout.Tiles), len(i.Layout.Sections))},
		}, width-half-4), width-half, false)
	}
	if width < 90 {
		return clip(left+"\n"+right, height)
	}
	return clip(grid([]string{left, right}, 2), height)
}

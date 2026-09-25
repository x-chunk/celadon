package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/x-chunk/teal"

	"github.com/x-chunk/celadon/internal/output"
)

// portraitRetryCap bounds one wait for a portrait under construction,
// whatever Retry-After says.
const portraitRetryCap = 30 * time.Second

// insightsTab shows At a Glance and, for a chat, its portrait.
type insightsTab struct {
	be *backend

	insights *teal.Insights
	inErr    error
	inSeq    int

	input    textinput.Model
	typing   bool
	chat     int64
	portrait *teal.Portrait
	ptErr    error
	ptSeq    int
	loading  bool
	// retryAt is when a portrait still being built is asked for again.
	retryAt time.Time

	vp      viewport.Model
	content string
}

// portraitRetryMsg asks again for the portrait of the chat in seq.
type portraitRetryMsg struct{ seq int }

func newInsightsTab(be *backend) *insightsTab {
	in := newInput()
	in.Prompt = "chat id: "
	in.Placeholder = "-1001234567890"
	in.CharLimit = 24
	return &insightsTab{be: be, input: in, vp: viewport.New(0, 0)}
}

func (t *insightsTab) title() string   { return insightsTitle }
func (t *insightsTab) capturing() bool { return t.typing }

func (t *insightsTab) keys() []keyHelp {
	if t.typing {
		return []keyHelp{{"enter", "portrait"}, {"esc", "cancel"}}
	}
	return []keyHelp{{"/", "portrait of a chat"}, {"r", "refresh"}, {"↑/↓", "scroll"}}
}

func (t *insightsTab) init() tea.Cmd { return t.loadInsights() }

func (t *insightsTab) loadInsights() tea.Cmd {
	t.inSeq++
	t.inErr = nil
	return call(t.be, "insights", t.inSeq, func(ctx context.Context, c *teal.Client) (teal.Insights, *teal.Meta, error) {
		return c.Insights.Get(ctx)
	})
}

func (t *insightsTab) loadPortrait(chat int64) tea.Cmd {
	t.ptSeq++
	t.chat = chat
	t.loading = true
	t.ptErr = nil
	t.retryAt = time.Time{}
	return call(t.be, "portrait", t.ptSeq, func(ctx context.Context, c *teal.Client) (teal.Portrait, *teal.Meta, error) {
		return c.Insights.Portrait(ctx, chat)
	})
}

func (t *insightsTab) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case openPortraitMsg:
		t.typing = false
		t.input.Blur()
		t.input.SetValue(strconv.FormatInt(msg.chat, 10))
		t.portrait = nil
		return t.loadPortrait(msg.chat)

	case done[teal.Insights]:
		if msg.op == "insights" && msg.seq == t.inSeq {
			if msg.err != nil {
				t.inErr = msg.err
			} else {
				in := msg.val
				t.insights = &in
			}
		}

	case done[teal.Portrait]:
		if msg.op != "portrait" || msg.seq != t.ptSeq {
			return nil
		}
		t.loading = false
		if e, ok := teal.AsError(msg.err); ok && e.Code == teal.CodeNotFound && e.RetryAfter > 0 {
			// Not built yet: nothing was charged, and the API says when
			// to come back.
			wait := min(e.RetryAfter, portraitRetryCap)
			t.retryAt = time.Now().Add(wait)
			seq := t.ptSeq
			return tea.Tick(wait, func(time.Time) tea.Msg { return portraitRetryMsg{seq: seq} })
		}
		if msg.err != nil {
			t.ptErr = msg.err
			return nil
		}
		pt := msg.val
		t.portrait = &pt
		t.vp.GotoTop()

	case portraitRetryMsg:
		if msg.seq == t.ptSeq && !t.retryAt.IsZero() {
			return t.loadPortrait(t.chat)
		}

	case tea.KeyMsg:
		return t.key(msg)
	}
	return nil
}

func (t *insightsTab) key(msg tea.KeyMsg) tea.Cmd {
	if t.typing {
		switch msg.String() {
		case "esc":
			t.typing = false
			t.input.Blur()
			return nil
		case "enter":
			id, err := strconv.ParseInt(strings.TrimSpace(t.input.Value()), 10, 64)
			if err != nil || id == 0 {
				t.ptErr = fmt.Errorf("a chat id is a non-zero number; the Archive tab lists them")
				return nil
			}
			t.typing = false
			t.input.Blur()
			t.portrait = nil
			return t.loadPortrait(id)
		}
		var cmd tea.Cmd
		t.input, cmd = t.input.Update(msg)
		return cmd
	}
	switch msg.String() {
	case "/", "enter":
		t.typing = true
		return t.input.Focus()
	case "r":
		cmds := []tea.Cmd{t.loadInsights()}
		if t.chat != 0 {
			cmds = append(cmds, t.loadPortrait(t.chat))
		}
		return tea.Batch(cmds...)
	}
	var cmd tea.Cmd
	t.vp, cmd = t.vp.Update(msg)
	return cmd
}

func (t *insightsTab) view(width, height int) string {
	t.input.Width = max(width-14, 10)
	content := t.render(width - 2)
	t.vp.Width, t.vp.Height = width, height
	if content != t.content {
		t.content = content
		t.vp.SetContent(content)
	}
	return t.vp.View()
}

func (t *insightsTab) render(width int) string {
	var b strings.Builder
	b.WriteString(styleHeading.Render("At a glance") + "\n")
	switch {
	case t.inErr != nil:
		b.WriteString(errorView(t.inErr, width))
	case t.insights == nil:
		b.WriteString(loading("insights"))
	case len(t.insights.Insights) == 0:
		b.WriteString(styleMuted.Render("Nothing to say yet: the archive is empty."))
	default:
		for _, s := range t.insights.Insights {
			b.WriteString(wrapText("• "+s, width) + "\n")
		}
	}
	b.WriteString("\n\n" + styleHeading.Render("Portrait") + "\n")
	if t.typing || t.input.Value() != "" {
		b.WriteString(t.input.View() + "\n")
	}
	switch {
	case !t.retryAt.IsZero() && t.portrait == nil:
		left := max(time.Until(t.retryAt).Round(time.Second), 0)
		b.WriteString(styleMuted.Render(fmt.Sprintf("The portrait is being built; asking again in about %s. Nothing is charged for the wait.", output.Duration(left))))
	case t.loading:
		b.WriteString(loading("the portrait"))
	case t.ptErr != nil:
		b.WriteString(errorView(t.ptErr, width))
	case t.portrait != nil:
		b.WriteString(portraitView(*t.portrait, width))
	default:
		b.WriteString(styleMuted.Render(wrapText("Press / and give a chat id, or press p on a chat in the Archive tab. A portrait may be billed.", width)))
	}
	return strings.TrimRight(b.String(), "\n")
}

func portraitView(pt teal.Portrait, width int) string {
	subject := output.Or(pt.Subject.Title)
	if pt.Subject.Username != "" {
		subject += " (@" + pt.Subject.Username + ")"
	}
	var b strings.Builder
	b.WriteString(styleTitle.Render(output.Truncate(output.OneLine(subject), width)) + "\n")
	if pt.Summary != "" {
		b.WriteString(wrapText(pt.Summary, width) + "\n\n")
	}
	hours := make([]string, 0, len(pt.Rhythm.PeakHours))
	for _, h := range pt.Rhythm.PeakHours {
		hours = append(hours, fmt.Sprintf("%02d:00", h))
	}
	rhythm := strings.Join(nonEmptyStrings(pt.Rhythm.Chronotype, pt.Rhythm.Tempo, pt.Rhythm.Week), " · ")
	b.WriteString(fields([]field{
		{"Archetype", fmt.Sprintf("%s (fit %.0f%%)", output.Or(pt.Archetype.Label), pt.Archetype.Fit*100)},
		{"Confidence", fmt.Sprintf("%s (%.2f)", output.Or(pt.Confidence.Level), pt.Confidence.Score)},
		{"Messages", fmt.Sprintf("%d, %s → %s", pt.Subject.Messages, output.Time(pt.Subject.First), output.Time(pt.Subject.Last))},
		{"Rhythm", output.Or(rhythm)},
		{"Peak hours", output.Or(strings.Join(hours, ", "))},
		{"Per day", fmt.Sprintf("%.1f messages, %.0f%% media", pt.Activity.MessagesPerDay, pt.Activity.MediaShare*100)},
		{"Topics", output.Or(strings.Join(pt.Topics, ", "))},
		{"Built", fmt.Sprintf("%s · model v%d", output.Time(pt.BuiltAt), pt.Model.Version)},
	}, width))
	if len(pt.Traits) > 0 {
		b.WriteString("\n\n" + styleHeading.Render("Traits") + "\n")
		nameW := 0
		for _, tr := range pt.Traits {
			nameW = max(nameW, output.Width(tr.Name))
		}
		nameW = min(nameW, 28)
		for _, tr := range pt.Traits {
			name := output.Truncate(output.OneLine(tr.Name), nameW)
			name += strings.Repeat(" ", nameW-output.Width(name))
			b.WriteString(fmt.Sprintf("%s  %-6s %s %+.2fσ\n", name, tr.Level, zBar(tr.Z, 17), tr.Z))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// zBar draws how far from the mean a trait sits: a center line with the
// deviation drawn out to one side, three standard deviations to the edge.
func zBar(z float64, width int) string {
	half := width / 2
	n := min(int(abs(z)/3*float64(half)+0.5), half)
	left, right := strings.Repeat(" ", half), strings.Repeat(" ", half)
	if z < 0 {
		left = strings.Repeat(" ", half-n) + strings.Repeat("▒", n)
	} else {
		right = strings.Repeat("▒", n) + strings.Repeat(" ", half-n)
	}
	return styleFaint.Render("[") + styleWarn.Render(left) + styleMuted.Render("|") + styleOK.Render(right) + styleFaint.Render("]")
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func nonEmptyStrings(values ...string) []string {
	var out []string
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

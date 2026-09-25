package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/x-chunk/teal"

	"github.com/x-chunk/celadon/internal/output"
)

// overviewTab is the first screen: the application, the plan behind it, the
// quotas and what has been spent. Everything on it is free to read.
type overviewTab struct {
	be *backend

	app     *teal.Application
	account *teal.Account
	usage   *teal.Usage
	err     error
	seq     int

	vp      viewport.Model
	content string
	now     func() time.Time
}

func newOverviewTab(be *backend) *overviewTab {
	return &overviewTab{be: be, vp: viewport.New(0, 0), now: time.Now}
}

func (t *overviewTab) title() string   { return "Overview" }
func (t *overviewTab) capturing() bool { return false }

func (t *overviewTab) keys() []keyHelp {
	return []keyHelp{{"r", "refresh"}, {"↑/↓", "scroll"}}
}

func (t *overviewTab) init() tea.Cmd { return t.load() }

func (t *overviewTab) load() tea.Cmd {
	t.seq++
	seq := t.seq
	t.err = nil
	return tea.Batch(
		call(t.be, "overview", seq, func(ctx context.Context, c *teal.Client) (teal.Application, *teal.Meta, error) {
			return c.App.Get(ctx)
		}),
		call(t.be, "overview", seq, func(ctx context.Context, c *teal.Client) (teal.Account, *teal.Meta, error) {
			return c.App.Account(ctx)
		}),
		call(t.be, "overview", seq, func(ctx context.Context, c *teal.Client) (teal.Usage, *teal.Meta, error) {
			return c.App.Usage(ctx, &teal.UsageRequest{Days: 30})
		}),
	)
}

func (t *overviewTab) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case done[teal.Application]:
		if msg.op == "overview" && msg.seq == t.seq {
			t.setErr(msg.err)
			if msg.err == nil {
				t.app = &msg.val
			}
		}
	case done[teal.Account]:
		if msg.op == "overview" && msg.seq == t.seq {
			t.setErr(msg.err)
			if msg.err == nil {
				t.account = &msg.val
			}
		}
	case done[teal.Usage]:
		if msg.op == "overview" && msg.seq == t.seq {
			t.setErr(msg.err)
			if msg.err == nil {
				t.usage = &msg.val
			}
		}
	case tea.KeyMsg:
		if msg.String() == "r" {
			return t.load()
		}
		var cmd tea.Cmd
		t.vp, cmd = t.vp.Update(msg)
		return cmd
	}
	return nil
}

func (t *overviewTab) setErr(err error) {
	if err != nil && t.err == nil {
		t.err = err
	}
}

func (t *overviewTab) view(width, height int) string {
	content := t.render(width - 2)
	t.vp.Width, t.vp.Height = width, height
	if content != t.content {
		t.content = content
		t.vp.SetContent(content)
	}
	return t.vp.View()
}

func (t *overviewTab) render(width int) string {
	var sections []string
	if t.err != nil {
		sections = append(sections, errorView(t.err, width))
	}
	if t.app == nil && t.account == nil && t.err == nil {
		return loading("the application")
	}

	colWidth := width
	twoCols := width >= 96
	if twoCols {
		colWidth = (width - 4) / 2
	}
	var cols []string
	if t.app != nil {
		cols = append(cols, t.appSection(colWidth))
	}
	if t.account != nil {
		cols = append(cols, t.planSection(colWidth))
	}
	if twoCols && len(cols) == 2 {
		left := lipgloss.NewStyle().Width(colWidth + 4).Render(cols[0])
		sections = append(sections, lipgloss.JoinHorizontal(lipgloss.Top, left, cols[1]))
	} else {
		sections = append(sections, cols...)
	}
	if t.account != nil && len(t.account.Quotas) > 0 {
		sections = append(sections, t.quotaSection(width))
	}
	if t.usage != nil {
		sections = append(sections, t.usageSection(width))
	}
	return strings.Join(sections, "\n\n")
}

func (t *overviewTab) appSection(width int) string {
	a := t.app
	state := styleOK.Render("active")
	if a.Disabled {
		state = styleError.Render("disabled — the account's plan has lapsed")
	}
	return styleHeading.Render("Application") + "\n" + fields([]field{
		{"Name", a.Name},
		{"ID", fmt.Sprint(a.ID)},
		{"Billing", string(a.Billing)},
		{"Balance", output.Money(a.Balance)},
		{"Funded", output.Money(a.Funded)},
		{"Spent", output.Money(a.Spent)},
		{"Requests", fmt.Sprint(a.Requests)},
		{"Last used", output.Time(a.LastUsedAt)},
		{"Key", a.KeyPrefix + "…"},
	}, width) + "\n" + styleLabel.Render("State") + "      " + state
}

func (t *overviewTab) planSection(width int) string {
	a := t.account
	return styleHeading.Render("Plan") + "\n" + fields([]field{
		{"Plan", fmt.Sprintf("%s (%s)", output.Or(a.Plan.Name), a.Plan.Tier)},
		{"Price", output.Cents(a.Plan.PriceCents)},
		{"Paid until", output.Time(a.Plan.Until)},
		{"Account", fmt.Sprint(a.AccountID)},
		{"Balance", output.Cents(a.BalanceCents) + " (not spendable with a key)"},
		{"Permissions", fmt.Sprint(len(a.Plan.Permissions))},
	}, width)
}

func (t *overviewTab) quotaSection(width int) string {
	now := t.now()
	keyW := 0
	for _, q := range t.account.Quotas {
		keyW = max(keyW, output.Width(q.Key))
	}
	keyW = min(keyW, 28)
	barW := max(min(width-keyW-36, 30), 8)
	var b strings.Builder
	b.WriteString(styleHeading.Render("Quotas") + "\n")
	for _, q := range t.account.Quotas {
		key := output.Truncate(q.Key, keyW)
		key += strings.Repeat(" ", keyW-output.Width(key))
		gauge := bar(q.Used, q.Limit, barW)
		if q.Unlimited {
			gauge = styleFaint.Render(strings.Repeat("·", barW))
		}
		note := ""
		if q.Window != "" {
			note = " per " + q.Window
			if !q.ResetAt.IsZero() {
				note += ", resets " + output.Until(q.ResetAt, now)
			}
			note = styleMuted.Render(note)
		}
		b.WriteString(fmt.Sprintf("%s  %s  %s%s\n", key, gauge, output.Quota(q), note))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (t *overviewTab) usageSection(width int) string {
	u := t.usage
	head := styleHeading.Render("Spending") + styleMuted.Render(fmt.Sprintf("  %s → %s · %s · %s", u.From, u.To, plural(u.Calls, "call", "calls"), output.Money(u.Credits)))
	if len(u.Ops) == 0 {
		return head + "\n" + styleMuted.Render("Nothing was billed in the last 30 days.")
	}
	var b strings.Builder
	b.WriteString(head + "\n")
	opW := 0
	for _, op := range u.Ops {
		opW = max(opW, output.Width(op.Op))
	}
	for _, op := range u.Ops {
		name := op.Op + strings.Repeat(" ", opW-output.Width(op.Op))
		b.WriteString(output.Truncate(fmt.Sprintf("%s  %8d calls  %s", name, op.Calls, output.Money(op.Credits)), width) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

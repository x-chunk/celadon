package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/x-chunk/celadon/internal/admin"
	"github.com/x-chunk/celadon/internal/output"
	"github.com/x-chunk/celadon/internal/query"
)

// NewAdmin builds the admin interface over an admin client: the same frame
// as the main interface, with the deployment in the header and the
// promotions in the tabs.
func NewAdmin(ctx context.Context, client *admin.Client, opts Options) *Model {
	be := &backend{ctx: ctx, admin: client, timeout: requestTimeout}
	m := &Model{
		be:    be,
		opts:  opts,
		now:   time.Now,
		admin: true,
		note: "Every call goes to the admin API at " + opts.BaseURL + " and changes what every user of the deployment " +
			"sees: a campaign applies to everybody while it runs. Nothing here is billed.",
	}
	m.tabs = []tab{
		newCampaignsTab(be),
		newReferenceTab(be),
	}
	m.started = make([]bool, len(m.tabs))
	return m
}

// RunAdmin starts the admin interface on the terminal and blocks until it
// is closed.
func RunAdmin(ctx context.Context, client *admin.Client, opts Options) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	return runProgram(ctx, NewAdmin(ctx, client, opts))
}

// referenceTab shows what a promotion is written against.
type referenceTab struct {
	be      *backend
	ref     *admin.Reference
	err     error
	seq     int
	vp      viewport.Model
	content string
}

func newReferenceTab(be *backend) *referenceTab {
	return &referenceTab{be: be, vp: viewport.New(0, 0)}
}

func (t *referenceTab) title() string   { return "Reference" }
func (t *referenceTab) capturing() bool { return false }
func (t *referenceTab) keys() []keyHelp { return []keyHelp{{"r", "refresh"}, {"↑/↓", "scroll"}} }
func (t *referenceTab) init() tea.Cmd   { return t.load() }

func (t *referenceTab) load() tea.Cmd {
	t.seq++
	t.err = nil
	return callAdmin(t.be, "reference", t.seq, func(ctx context.Context, c *admin.Client) (admin.Reference, *admin.Meta, error) {
		return c.Promo.Reference(ctx)
	})
}

func (t *referenceTab) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case done[admin.Reference]:
		if msg.op == "reference" && msg.seq == t.seq {
			if msg.err != nil {
				t.err = msg.err
				return nil
			}
			ref := msg.val
			t.ref = &ref
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

func (t *referenceTab) view(width, height int) string {
	content := t.render(width - 2)
	t.vp.Width, t.vp.Height = width, height
	if content != t.content {
		t.content = content
		t.vp.SetContent(content)
	}
	return t.vp.View()
}

func (t *referenceTab) render(width int) string {
	switch {
	case t.err != nil:
		return errorView(t.err, width)
	case t.ref == nil:
		return loading("the reference")
	}
	r := t.ref
	var b strings.Builder
	b.WriteString(styleHeading.Render("Plans") + "\n")
	for _, tier := range r.Tiers {
		sold := styleOK.Render("discountable")
		if !tier.Paid {
			sold = styleFaint.Render("not sold")
		}
		b.WriteString(fmt.Sprintf("%-12s %-16s %8s  %s\n", tier.Tier, output.OneLine(tier.Name), output.Cents(tier.PriceCents), sold))
	}
	b.WriteString("\n" + styleHeading.Render("Quotas") + "\n")
	keyW := 0
	for _, l := range r.Limits {
		keyW = max(keyW, output.Width(l.Key))
	}
	for _, l := range r.Limits {
		b.WriteString(output.Truncate(fmt.Sprintf("%-*s  %-10s %s", keyW, l.Key, l.Unit, styleMuted.Render(output.OneLine(l.Label))), width+20) + "\n")
	}
	amounts := make([]string, 0, len(r.TopUpAmountsCents))
	for _, c := range r.TopUpAmountsCents {
		amounts = append(amounts, output.Cents(c))
	}
	b.WriteString("\n" + fields([]field{
		{"Top-up amounts", output.Or(strings.Join(amounts, ", "))},
		{"Campaign kinds", output.Or(strings.Join(r.CampaignKinds, ", "))},
		{"Largest discount", fmt.Sprintf("%d%%", r.MaxDiscountPercent)},
		{"Largest bonus", fmt.Sprintf("%d%%", r.MaxBonusPercent)},
	}, width))
	b.WriteString("\n\n" + styleMuted.Render(wrapText("Benefits are written as: discount pro:25, bonus 50:20, grant free:search:daily=200 — all/any for every plan or amount, unlimited for no ceiling.", width)))
	return b.String()
}

// momentText is a timestamp as a form shows it, and as ParseMoment reads it
// back: local time to the minute, or the word for zero.
func momentText(u admin.Unix, zero string) string {
	if u == 0 {
		return zero
	}
	return u.At().Local().Format("2006-01-02 15:04")
}

// windowText is when a promotion runs, for a list or a card.
func windowText(starts, ends admin.Unix) string {
	return momentText(starts, "now") + " → " + momentText(ends, "never")
}

// benefitLines lists what a promotion grants, one benefit to a line.
func benefitLines(b admin.Benefits, width int) string {
	desc := query.DescribeBenefits(b)
	if desc == "nothing" {
		return styleMuted.Render("grants nothing")
	}
	var out []string
	for _, item := range strings.Split(desc, " · ") {
		out = append(out, output.Truncate("• "+item, width))
	}
	return strings.Join(out, "\n")
}

// parseWindow reads a form's start and end. On creation a start of "now" is
// left for the server's clock; an edit sends a start or an end only when its
// text changed from what the form was filled with.
func parseWindow(startsText, endsText, wasStarts, wasEnds string, creating bool, now time.Time) (starts, ends *admin.Unix, err error) {
	startsText, endsText = strings.TrimSpace(startsText), strings.TrimSpace(endsText)
	base := now
	if creating || startsText != wasStarts {
		t, err := query.ParseMoment(startsText, now)
		if err != nil {
			return nil, nil, err
		}
		if t.IsZero() {
			if !creating {
				return nil, nil, fmt.Errorf("the start cannot be never; stop it instead")
			}
		} else {
			base = t
			if !(creating && strings.EqualFold(startsText, "now")) {
				starts = admin.Ptr(admin.UnixOf(t))
			}
		}
	}
	if creating || endsText != wasEnds {
		t, err := query.ParseMoment(endsText, base)
		if err != nil {
			return nil, nil, err
		}
		if !t.IsZero() && !t.After(base) {
			return nil, nil, fmt.Errorf("the end is not after the start")
		}
		if !(creating && t.IsZero()) {
			ends = admin.Ptr(admin.UnixOf(t))
		}
	}
	return starts, ends, nil
}

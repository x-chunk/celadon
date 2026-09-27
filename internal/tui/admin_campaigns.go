package tui

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/x-chunk/celadon/internal/admin"
	"github.com/x-chunk/celadon/internal/output"
	"github.com/x-chunk/celadon/internal/query"
)

type promoMode int

const (
	promoBrowse promoMode = iota
	promoForm
	promoConfirmDelete
)

// Campaign form fields, in the order they are shown.
const (
	cfKind = iota
	cfName
	cfDescription
	cfAnnouncement
	cfStarts
	cfEnds
	cfBenefits
	cfTrial
	cfGifts
)

// campaignsTab lists campaigns and creates, edits, stops and deletes them.
type campaignsTab struct {
	be *backend

	list    []admin.Campaign
	err     error
	loading bool
	seq     int
	cur     selection

	kind string // "", offer or event
	all  bool   // include stopped campaigns

	mode    promoMode
	form    *form
	editing *admin.Campaign
	busy    bool
	notice  string
	now     func() time.Time
}

func newCampaignsTab(be *backend) *campaignsTab {
	return &campaignsTab{be: be, now: time.Now}
}

func (t *campaignsTab) title() string { return "Campaigns" }

func (t *campaignsTab) capturing() bool { return t.mode != promoBrowse }

func (t *campaignsTab) keys() []keyHelp {
	switch t.mode {
	case promoForm:
		return []keyHelp{{"ctrl+s", "save"}, {"tab", "next field"}, {"esc", "cancel"}}
	case promoConfirmDelete:
		return []keyHelp{{"y", "delete"}, {"n/esc", "keep"}}
	}
	return []keyHelp{{"n", "new"}, {"e", "edit"}, {"s", "stop/start"}, {"d", "delete"}, {"f", "kind"}, {"a", "stopped too"}, {"r", "refresh"}}
}

func (t *campaignsTab) init() tea.Cmd { return t.load() }

func (t *campaignsTab) load() tea.Cmd {
	t.seq++
	t.loading = true
	t.err = nil
	req := &admin.CampaignListRequest{Kind: t.kind, Inactive: t.all, Limit: 500}
	return callAdmin(t.be, "campaigns", t.seq, func(ctx context.Context, c *admin.Client) ([]admin.Campaign, *admin.Meta, error) {
		return c.Promo.Campaigns(ctx, req)
	})
}

func (t *campaignsTab) selected() (admin.Campaign, bool) {
	if len(t.list) == 0 {
		return admin.Campaign{}, false
	}
	return t.list[t.cur.pos], true
}

func (t *campaignsTab) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case done[[]admin.Campaign]:
		if msg.op == "campaigns" && msg.seq == t.seq {
			t.loading = false
			if msg.err != nil {
				t.err = msg.err
				return nil
			}
			t.list = msg.val
			t.cur.clamp(len(t.list))
		}
	case done[admin.Campaign]:
		switch msg.op {
		case "campaign-save":
			if msg.err != nil {
				t.form.fail(msg.err)
				return nil
			}
			t.mode, t.form = promoBrowse, nil
			t.notice = fmt.Sprintf("Saved %q — %s.", msg.val.Name, msg.val.State)
			return t.load()
		case "campaign-toggle":
			t.busy = false
			if msg.err != nil {
				t.err = msg.err
				return nil
			}
			t.notice = fmt.Sprintf("%q is %s.", msg.val.Name, msg.val.State)
			return t.load()
		}
	case done[ack]:
		if msg.op == "campaign-delete" {
			t.busy = false
			t.mode = promoBrowse
			if msg.err != nil {
				t.err = msg.err
				return nil
			}
			t.notice = "Deleted."
			return t.load()
		}
	case tea.KeyMsg:
		return t.key(msg)
	}
	return nil
}

func (t *campaignsTab) key(msg tea.KeyMsg) tea.Cmd {
	switch t.mode {
	case promoForm:
		res, cmd := t.form.update(msg)
		switch res {
		case formCancel:
			t.mode, t.form = promoBrowse, nil
		case formSubmit:
			return t.submit()
		}
		return cmd
	case promoConfirmDelete:
		switch msg.String() {
		case "y", "Y":
			c, ok := t.selected()
			if !ok || t.busy {
				t.mode = promoBrowse
				return nil
			}
			t.busy = true
			return callAdminAck(t.be, "campaign-delete", 0, func(ctx context.Context, cl *admin.Client) (*admin.Meta, error) {
				return cl.Promo.DeleteCampaign(ctx, c.ID)
			})
		case "n", "N", "esc":
			t.mode = promoBrowse
		}
		return nil
	}

	t.notice = ""
	switch msg.String() {
	case "up", "k":
		t.cur.move(-1, len(t.list))
	case "down", "j":
		t.cur.move(1, len(t.list))
	case "r":
		return t.load()
	case "f":
		t.kind = map[string]string{"": admin.KindOffer, admin.KindOffer: admin.KindEvent, admin.KindEvent: ""}[t.kind]
		t.cur = selection{}
		return t.load()
	case "a":
		t.all = !t.all
		t.cur = selection{}
		return t.load()
	case "n":
		return t.openForm(nil)
	case "e", "enter":
		if c, ok := t.selected(); ok {
			return t.openForm(&c)
		}
	case "s":
		c, ok := t.selected()
		if !ok || t.busy {
			return nil
		}
		t.busy = true
		active := !c.Active
		return callAdmin(t.be, "campaign-toggle", 0, func(ctx context.Context, cl *admin.Client) (admin.Campaign, *admin.Meta, error) {
			return cl.Promo.UpdateCampaign(ctx, c.ID, admin.CampaignUpdateRequest{Active: &active})
		})
	case "d", "delete":
		if _, ok := t.selected(); ok {
			t.mode = promoConfirmDelete
		}
	}
	return nil
}

func (t *campaignsTab) openForm(c *admin.Campaign) tea.Cmd {
	t.editing = c
	title := "New campaign"
	var v admin.Campaign
	v.Kind = admin.KindOffer
	if c != nil {
		title = fmt.Sprintf("Edit campaign #%d", c.ID)
		v = *c
	}
	trialHint := "Events only: how long the trial lasts while it runs — 14, 30 or 90 days, or off for the usual length."
	giftsHint := "Events only: a balance given once to every account that existed when it started — balance all:1, balance pro:2; pro:0 leaves Pro out. Empty or none gives nothing."
	if v.Frozen() {
		frozen := "Frozen: the event was announced " + output.Time(v.FirstAnnouncedAt.At()) + ", so this can no longer change. "
		trialHint = frozen + trialHint
		giftsHint = frozen + giftsHint
	}
	t.form = newForm(title,
		formInput("Kind", v.Kind, "offer runs quietly; event announces itself to every account when it starts."),
		formInput("Name", v.Name, "Up to 64 characters, shown to users."),
		formInput("Description", v.Description, "What it is, as users read it. Up to 512 characters; may be empty."),
		formInput("Announcement", v.Announcement, "A line added to an event's announcement; may be empty."),
		formInput("Starts", momentText(v.StartsAt, "now"), "now, 2026-10-01, 2026-10-01 18:00 (local time), or +2h. An event moved into the future announces itself again."),
		formInput("Ends", momentText(v.EndsAt, "never"), "never, a date or a time, or a span from the start such as +7d."),
		formInput("Benefits", query.FormatBenefits(v.Benefits), "discount pro:25, bonus 50:20, grant free:search:daily=200 — all/any for every plan or amount, unlimited for no ceiling. The Reference tab lists the names."),
		formInput("Trial", query.FormatTrial(v.Benefits.TrialDays), trialHint),
		formInput("Gifts", query.FormatGifts(v.Grants), giftsHint),
	)
	t.mode = promoForm
	return t.form.start()
}

// submit turns the form into a create or, for an edit, into a patch of the
// fields that changed.
func (t *campaignsTab) submit() tea.Cmd {
	f := t.form
	kind := strings.ToLower(strings.TrimSpace(f.value(cfKind)))
	if kind != admin.KindOffer && kind != admin.KindEvent {
		f.err = errors.New("the kind is offer or event")
		return f.move(cfKind)
	}
	name := strings.TrimSpace(f.value(cfName))
	if name == "" {
		f.err = errors.New("a campaign needs a name")
		return f.move(cfName)
	}
	benefits, err := query.ParseBenefits(f.value(cfBenefits))
	if err != nil {
		f.err = err
		return f.move(cfBenefits)
	}
	if benefits.TrialDays, err = query.ParseTrial(f.value(cfTrial)); err != nil {
		f.err = err
		return f.move(cfTrial)
	}
	gifts, err := query.ParseGifts(f.value(cfGifts))
	if err != nil {
		f.err = err
		return f.move(cfGifts)
	}
	if benefits.Empty() && gifts.Empty() {
		f.err = errors.New("a campaign has to grant something")
		return f.move(cfBenefits)
	}
	description, announcement := f.value(cfDescription), f.value(cfAnnouncement)

	if t.editing == nil {
		starts, ends, err := parseWindow(f.value(cfStarts), f.value(cfEnds), "", "", true, t.now())
		if err != nil {
			f.err = err
			return nil
		}
		req := admin.CampaignCreateRequest{Kind: kind, Name: name, Benefits: benefits, StartsAt: starts, EndsAt: ends}
		if giftsWritten(gifts) {
			req.Grants = &gifts
		}
		if description != "" {
			req.Description = &description
		}
		if announcement != "" {
			req.Announcement = &announcement
		}
		f.busy, f.err = true, nil
		return callAdmin(t.be, "campaign-save", 0, func(ctx context.Context, c *admin.Client) (admin.Campaign, *admin.Meta, error) {
			return c.Promo.CreateCampaign(ctx, req)
		})
	}

	was := *t.editing
	starts, ends, err := parseWindow(f.value(cfStarts), f.value(cfEnds),
		momentText(was.StartsAt, "now"), momentText(was.EndsAt, "never"), false, t.now())
	if err != nil {
		f.err = err
		return nil
	}
	var req admin.CampaignUpdateRequest
	if kind != was.Kind {
		req.Kind = &kind
	}
	if name != was.Name {
		req.Name = &name
	}
	if description != was.Description {
		req.Description = &description
	}
	if announcement != was.Announcement {
		req.Announcement = &announcement
	}
	if !reflect.DeepEqual(benefits, was.Benefits) {
		req.Benefits = &benefits
	}
	if !sameGifts(gifts, was.Grants) {
		req.Grants = &gifts
	}
	req.StartsAt, req.EndsAt = starts, ends
	if req == (admin.CampaignUpdateRequest{}) {
		t.mode, t.form = promoBrowse, nil
		t.notice = "Nothing changed."
		return nil
	}
	f.busy, f.err = true, nil
	id := was.ID
	return callAdmin(t.be, "campaign-save", 0, func(ctx context.Context, c *admin.Client) (admin.Campaign, *admin.Meta, error) {
		return c.Promo.UpdateCampaign(ctx, id, req)
	})
}

func (t *campaignsTab) view(width, height int) string {
	if t.mode == promoForm {
		return t.form.view(width, height)
	}
	leftW := min(max(width*2/5, 34), 60)
	rightW := width - leftW
	filter := "all kinds"
	if t.kind != "" {
		filter = t.kind + "s"
	}
	if t.all {
		filter += ", stopped too"
	}
	left := pane(fmt.Sprintf("Campaigns · %d · %s", len(t.list), filter), t.listBody(leftW-4, height-3), leftW, height, true)
	right := pane("Campaign", t.cardBody(rightW-4, height-3), rightW, height, false)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

// announcedText says where a campaign's announcement stands. An event moved
// into the future is announced again when it arrives, as a new generation.
func announcedText(c admin.Campaign) string {
	switch {
	case c.AnnouncedAt != 0:
		at := output.Time(c.AnnouncedAt.At())
		if c.Generation > 1 {
			at += fmt.Sprintf(" (announcement %d)", c.Generation)
		}
		return at
	case c.FirstAnnouncedAt != 0:
		return "not yet again — first " + output.Time(c.FirstAnnouncedAt.At())
	case c.Kind == admin.KindEvent:
		return "not yet"
	}
	return output.Dash
}

// stateStyle colors a promotion's state.
func stateStyle(state string) lipgloss.Style {
	switch state {
	case admin.StateRunning:
		return styleOK
	case admin.StateScheduled:
		return styleWarn
	default:
		return styleFaint
	}
}

func (t *campaignsTab) listBody(width, height int) string {
	var foot string
	switch {
	case t.mode == promoConfirmDelete:
		c, _ := t.selected()
		foot = styleWarn.Render(output.Truncate(fmt.Sprintf("Delete %q for good? y/n", c.Name), width))
	case t.busy:
		foot = loading("the change")
	case t.err != nil && len(t.list) > 0:
		foot = errorView(t.err, width)
	case t.notice != "":
		foot = styleOK.Render(output.Truncate(t.notice, width))
	}
	if foot != "" {
		height -= lipgloss.Height(foot)
	}
	var body string
	switch {
	case t.err != nil && len(t.list) == 0:
		body = errorView(t.err, width)
	case t.list == nil && t.loading:
		body = loading("campaigns")
	case len(t.list) == 0:
		body = styleMuted.Render(wrapText("No campaigns here. Press n to create one, or a to see the stopped ones.", width))
	default:
		from, to := t.cur.window(len(t.list), height)
		var b strings.Builder
		for i := from; i < to; i++ {
			c := t.list[i]
			state := fmt.Sprintf("%-9s", c.State)
			row := line(fmt.Sprintf("#%-4d %s %s", c.ID, state, c.Name), width, i == t.cur.pos)
			if i != t.cur.pos {
				row = fmt.Sprintf("#%-4d %s %s", c.ID, stateStyle(c.State).Render(state), output.Truncate(output.OneLine(c.Name), max(width-16, 4)))
			}
			b.WriteString(row + "\n")
		}
		body = strings.TrimRight(b.String(), "\n")
	}
	if foot != "" {
		return clip(body, height) + "\n" + foot
	}
	return body
}

func (t *campaignsTab) cardBody(width, height int) string {
	c, ok := t.selected()
	if !ok {
		return ""
	}
	rows := []field{
		{"ID", strconv.FormatInt(c.ID, 10)},
		{"Kind", c.Kind},
		{"State", c.State},
		{"Runs", windowText(c.StartsAt, c.EndsAt)},
		{"Announced", announcedText(c)},
	}
	if c.Frozen() {
		rows = append(rows, field{"Gifts", "frozen since the first announcement"})
	}
	rows = append(rows, field{"Updated", output.Time(c.UpdatedAt)})
	var b strings.Builder
	b.WriteString(styleTitle.Render(output.Truncate(output.OneLine(c.Name), width)) + "\n")
	b.WriteString(fields(rows, width))
	b.WriteString(givesLines(c.Benefits, c.Grants, width))
	if c.Description != "" {
		b.WriteString("\n\n" + styleHeading.Render("Description") + "\n" + wrapText(c.Description, width))
	}
	if c.Announcement != "" {
		b.WriteString("\n\n" + styleHeading.Render("Announcement") + "\n" + wrapText(c.Announcement, width))
	}
	return clip(b.String(), height)
}

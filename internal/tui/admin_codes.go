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

// Code form fields, in the order they are shown.
const (
	kfCode = iota
	kfName
	kfDescription
	kfStarts
	kfEnds
	kfMax
	kfLasts
	kfBenefits
	kfGifts
)

// codesTab lists promo codes, who redeemed them, and issues, edits,
// disables and deletes them.
type codesTab struct {
	be *backend

	list    []admin.Code
	err     error
	loading bool
	seq     int
	cur     selection
	all     bool

	// The redemptions of the selected code, once asked for.
	redeemedOf  string
	redemptions []admin.Redemption
	redErr      error
	redSeq      int
	redLoading  bool

	mode    promoMode
	form    *form
	editing *admin.Code
	busy    bool
	notice  string
	now     func() time.Time
}

func newCodesTab(be *backend) *codesTab {
	return &codesTab{be: be, now: time.Now}
}

func (t *codesTab) title() string   { return "Codes" }
func (t *codesTab) capturing() bool { return t.mode != promoBrowse }

func (t *codesTab) keys() []keyHelp {
	switch t.mode {
	case promoForm:
		return []keyHelp{{"ctrl+s", "save"}, {"tab", "next field"}, {"esc", "cancel"}}
	case promoConfirmDelete:
		return []keyHelp{{"y", "delete"}, {"n/esc", "keep"}}
	}
	return []keyHelp{{"n", "new"}, {"e", "edit"}, {"enter", "redemptions"}, {"s", "disable/enable"}, {"d", "delete"}, {"a", "disabled too"}, {"r", "refresh"}}
}

func (t *codesTab) init() tea.Cmd { return t.load() }

func (t *codesTab) load() tea.Cmd {
	t.seq++
	t.loading = true
	t.err = nil
	req := &admin.CodeListRequest{Inactive: t.all, Limit: 500}
	return callAdmin(t.be, "codes", t.seq, func(ctx context.Context, c *admin.Client) ([]admin.Code, *admin.Meta, error) {
		return c.Promo.Codes(ctx, req)
	})
}

func (t *codesTab) selected() (admin.Code, bool) {
	if len(t.list) == 0 {
		return admin.Code{}, false
	}
	return t.list[t.cur.pos], true
}

func (t *codesTab) loadRedemptions(code string) tea.Cmd {
	t.redSeq++
	t.redeemedOf, t.redemptions, t.redErr, t.redLoading = code, nil, nil, true
	return callAdmin(t.be, "redemptions", t.redSeq, func(ctx context.Context, c *admin.Client) ([]admin.Redemption, *admin.Meta, error) {
		return c.Promo.Redemptions(ctx, code, &admin.RedemptionListRequest{Limit: 500})
	})
}

func (t *codesTab) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case done[[]admin.Code]:
		if msg.op == "codes" && msg.seq == t.seq {
			t.loading = false
			if msg.err != nil {
				t.err = msg.err
				return nil
			}
			t.list = msg.val
			t.cur.clamp(len(t.list))
		}
	case done[[]admin.Redemption]:
		if msg.op == "redemptions" && msg.seq == t.redSeq {
			t.redLoading = false
			t.redemptions, t.redErr = msg.val, msg.err
		}
	case done[admin.Code]:
		switch msg.op {
		case "code-save":
			if msg.err != nil {
				t.form.fail(msg.err)
				return nil
			}
			t.mode, t.form = promoBrowse, nil
			t.notice = fmt.Sprintf("Saved %s.", msg.val.Code)
			return t.load()
		case "code-toggle":
			t.busy = false
			if msg.err != nil {
				t.err = msg.err
				return nil
			}
			t.notice = fmt.Sprintf("%s is %s.", msg.val.Code, activeText(msg.val.Active))
			return t.load()
		}
	case done[ack]:
		if msg.op == "code-delete" {
			t.busy = false
			t.mode = promoBrowse
			if msg.err != nil {
				t.err = msg.err
				return nil
			}
			t.notice = "Deleted."
			t.redeemedOf = ""
			return t.load()
		}
	case tea.KeyMsg:
		return t.key(msg)
	}
	return nil
}

func activeText(active bool) string {
	if active {
		return "active"
	}
	return "disabled"
}

func (t *codesTab) key(msg tea.KeyMsg) tea.Cmd {
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
			return callAdminAck(t.be, "code-delete", 0, func(ctx context.Context, cl *admin.Client) (*admin.Meta, error) {
				return cl.Promo.DeleteCode(ctx, c.Code)
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
	case "a":
		t.all = !t.all
		t.cur = selection{}
		return t.load()
	case "n":
		return t.openForm(nil)
	case "e":
		if c, ok := t.selected(); ok {
			return t.openForm(&c)
		}
	case "enter", "v":
		if c, ok := t.selected(); ok {
			return t.loadRedemptions(c.Code)
		}
	case "s":
		c, ok := t.selected()
		if !ok || t.busy {
			return nil
		}
		t.busy = true
		active := !c.Active
		return callAdmin(t.be, "code-toggle", 0, func(ctx context.Context, cl *admin.Client) (admin.Code, *admin.Meta, error) {
			return cl.Promo.UpdateCode(ctx, c.Code, admin.CodeUpdateRequest{Active: &active})
		})
	case "d", "delete":
		if _, ok := t.selected(); ok {
			t.mode = promoConfirmDelete
		}
	}
	return nil
}

func (t *codesTab) openForm(c *admin.Code) tea.Cmd {
	t.editing = c
	title := "New promo code"
	var v admin.Code
	if c != nil {
		title = "Edit " + c.Code
		v = *c
	}
	lastsText := spanText(v.Duration)
	codeHint := "Leave it empty to have one drawn — ten characters nobody misreads."
	if c != nil {
		codeHint = "Change it to rename the code. What was already redeemed stays."
	}
	t.form = newForm(title,
		formInput("Code", v.Code, codeHint),
		formInput("Name", v.Name, "Up to 64 characters, shown to users."),
		formInput("Description", v.Description, "What it is, as users read it; may be empty."),
		formInput("Starts", momentText(v.StartsAt, "now"), "When it can first be redeemed: now, 2026-10-01, 2026-10-01 18:00, or +2h."),
		formInput("Ends", momentText(v.EndsAt, "never"), "When it stops being accepted: never, a date or a time, or a span from the start such as +30d."),
		formInput("Max", strconv.FormatInt(v.MaxRedemptions, 10), "How many accounts may redeem it; 0 for no cap."),
		formInput("Lasts", lastsText, "How long the benefits last once redeemed: 30d, 12h, or 0 for as long as the code is valid."),
		formInput("Benefits", query.FormatBenefits(v.Benefits), "discount all:25, bonus any:10, grant pro:exports:weekly=unlimited — the Reference tab lists the names."),
		formInput("Gifts", query.FormatGifts(v.Grants), "Given once, on redemption: balance all:5, balance pro:10 (pro:0 leaves Pro out), plan pro:30 for a term of a paid plan. Empty or none gives nothing."),
	)
	t.mode = promoForm
	return t.form.start()
}

func (t *codesTab) submit() tea.Cmd {
	f := t.form
	code := strings.TrimSpace(f.value(kfCode))
	name := strings.TrimSpace(f.value(kfName))
	if name == "" {
		f.err = errors.New("a code needs a name")
		return f.move(kfName)
	}
	maxRed, err := strconv.ParseInt(strings.TrimSpace(f.value(kfMax)), 10, 64)
	if err != nil || maxRed < 0 {
		f.err = errors.New("max is a number of accounts, 0 for no cap")
		return f.move(kfMax)
	}
	lasts, err := query.Seconds(f.value(kfLasts))
	if err != nil {
		f.err = err
		return f.move(kfLasts)
	}
	benefits, err := query.ParseBenefits(f.value(kfBenefits))
	if err != nil {
		f.err = err
		return f.move(kfBenefits)
	}
	gifts, err := query.ParseGifts(f.value(kfGifts))
	if err != nil {
		f.err = err
		return f.move(kfGifts)
	}
	if benefits.Empty() && gifts.Empty() {
		f.err = errors.New("a code has to grant something")
		return f.move(kfBenefits)
	}
	description := f.value(kfDescription)

	if t.editing == nil {
		starts, ends, err := parseWindow(f.value(kfStarts), f.value(kfEnds), "", "", true, t.now())
		if err != nil {
			f.err = err
			return nil
		}
		req := admin.CodeCreateRequest{Name: name, Benefits: benefits, StartsAt: starts, EndsAt: ends}
		if giftsWritten(gifts) {
			req.Grants = &gifts
		}
		if code != "" {
			req.Code = &code
		}
		if description != "" {
			req.Description = &description
		}
		if maxRed != 0 {
			req.MaxRedemptions = &maxRed
		}
		if lasts != 0 {
			req.Duration = &lasts
		}
		f.busy, f.err = true, nil
		return callAdmin(t.be, "code-save", 0, func(ctx context.Context, c *admin.Client) (admin.Code, *admin.Meta, error) {
			return c.Promo.CreateCode(ctx, req)
		})
	}

	was := *t.editing
	if code == "" {
		f.err = errors.New("an existing code cannot be emptied; delete it instead")
		return f.move(kfCode)
	}
	starts, ends, err := parseWindow(f.value(kfStarts), f.value(kfEnds),
		momentText(was.StartsAt, "now"), momentText(was.EndsAt, "never"), false, t.now())
	if err != nil {
		f.err = err
		return nil
	}
	var req admin.CodeUpdateRequest
	if code != was.Code {
		req.Code = &code
	}
	if name != was.Name {
		req.Name = &name
	}
	if description != was.Description {
		req.Description = &description
	}
	if maxRed != was.MaxRedemptions {
		req.MaxRedemptions = &maxRed
	}
	if lasts != was.Duration {
		req.Duration = &lasts
	}
	if !reflect.DeepEqual(benefits, was.Benefits) {
		req.Benefits = &benefits
	}
	if !sameGifts(gifts, was.Grants) {
		req.Grants = &gifts
	}
	req.StartsAt, req.EndsAt = starts, ends
	if req == (admin.CodeUpdateRequest{}) {
		t.mode, t.form = promoBrowse, nil
		t.notice = "Nothing changed."
		return nil
	}
	f.busy, f.err = true, nil
	target := was.Code
	return callAdmin(t.be, "code-save", 0, func(ctx context.Context, c *admin.Client) (admin.Code, *admin.Meta, error) {
		return c.Promo.UpdateCode(ctx, target, req)
	})
}

func (t *codesTab) view(width, height int) string {
	if t.mode == promoForm {
		return t.form.view(width, height)
	}
	leftW := min(max(width*2/5, 34), 56)
	rightW := width - leftW
	filter := "active"
	if t.all {
		filter = "all"
	}
	left := pane(fmt.Sprintf("Codes · %d %s", len(t.list), filter), t.listBody(leftW-4, height-3), leftW, height, true)
	right := pane("Code", t.cardBody(rightW-4, height-3), rightW, height, false)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func redeemedText(c admin.Code) string {
	if c.MaxRedemptions == 0 {
		return fmt.Sprintf("%d/∞", c.Redemptions)
	}
	return fmt.Sprintf("%d/%d", c.Redemptions, c.MaxRedemptions)
}

func (t *codesTab) listBody(width, height int) string {
	var foot string
	switch {
	case t.mode == promoConfirmDelete:
		c, _ := t.selected()
		foot = styleWarn.Render(output.Truncate(fmt.Sprintf("Delete %s for good? y/n", c.Code), width))
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
		body = loading("codes")
	case len(t.list) == 0:
		body = styleMuted.Render(wrapText("No codes here. Press n to issue one, or a to see the disabled ones.", width))
	default:
		from, to := t.cur.window(len(t.list), height)
		var b strings.Builder
		for i := from; i < to; i++ {
			c := t.list[i]
			used := redeemedText(c)
			name := output.Truncate(output.OneLine(c.Code), max(width-len(used)-1, 4))
			row := name + strings.Repeat(" ", max(width-output.Width(name)-output.Width(used), 1)) + used
			if !c.Active && i != t.cur.pos {
				row = styleFaint.Render(row)
			}
			if i == t.cur.pos {
				row = line(row, width, true)
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

func (t *codesTab) cardBody(width, height int) string {
	c, ok := t.selected()
	if !ok {
		return ""
	}
	lasts := "while the code is valid"
	if c.Duration != 0 {
		lasts = output.Seconds(c.Duration)
	}
	var b strings.Builder
	b.WriteString(styleTitle.Render(output.Truncate(output.OneLine(c.Code+" — "+c.Name), width)) + "\n")
	b.WriteString(fields([]field{
		{"State", activeText(c.Active)},
		{"Valid", windowText(c.StartsAt, c.EndsAt)},
		{"Redeemed", redeemedText(c)},
		{"Lasts", lasts + " once redeemed"},
		{"Updated", output.Time(c.UpdatedAt)},
	}, width))
	b.WriteString(givesLines(c.Benefits, c.Grants, width))
	if c.Description != "" {
		b.WriteString("\n\n" + styleHeading.Render("Description") + "\n" + wrapText(c.Description, width))
	}
	b.WriteString("\n\n" + styleHeading.Render("Redemptions") + "\n")
	switch {
	case t.redeemedOf != c.Code:
		b.WriteString(styleMuted.Render("Press enter to list who redeemed it."))
	case t.redLoading:
		b.WriteString(loading("redemptions"))
	case t.redErr != nil:
		b.WriteString(errorView(t.redErr, width))
	case len(t.redemptions) == 0:
		b.WriteString(styleMuted.Render("Nobody has redeemed it yet."))
	default:
		now := t.now()
		for _, r := range t.redemptions {
			expires := "with the code"
			switch {
			case r.RevokedAt != 0:
				expires = "revoked " + output.Time(r.RevokedAt.At())
			case r.ExpiresAt != 0 && !r.Live(now):
				expires = "expired " + output.Time(r.ExpiresAt.At())
			case r.ExpiresAt != 0:
				expires = "until " + output.Time(r.ExpiresAt.At())
			}
			b.WriteString(output.Truncate(fmt.Sprintf("account %-12d %s · %s", r.AccountID, output.Time(r.CreatedAt), expires), width) + "\n")
		}
	}
	return clip(strings.TrimRight(b.String(), "\n"), height)
}

// spanText writes a count of seconds the way query.Seconds reads it back, in
// the largest unit that divides it exactly.
func spanText(secs int64) string {
	switch {
	case secs == 0:
		return "0"
	case secs%86400 == 0:
		return strconv.FormatInt(secs/86400, 10) + "d"
	case secs%3600 == 0:
		return strconv.FormatInt(secs/3600, 10) + "h"
	case secs%60 == 0:
		return strconv.FormatInt(secs/60, 10) + "m"
	}
	return strconv.FormatInt(secs, 10) + "s"
}

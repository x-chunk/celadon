package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/x-chunk/teal"

	"github.com/x-chunk/celadon/internal/output"
)

type actionsMode int

const (
	actionsBrowse actionsMode = iota
	actionsForm
	actionsConfirmDelete
	actionsPlaceholders
)

// actionsTab lists the account's shortcuts and creates, edits and deletes
// them.
type actionsTab struct {
	be *backend

	list    *teal.ActionList
	err     error
	loading bool
	seq     int
	cur     selection

	mode actionsMode

	// The form, for a new shortcut (editing == 0) or an existing one.
	editing   int64
	name      textinput.Model
	body      textarea.Model
	formFocus int
	formErr   error
	saving    bool
	notice    string

	placeholders []teal.Placeholder
	phErr        error
	phCur        selection
}

func newActionsTab(be *backend) *actionsTab {
	name := newInput()
	name.Prompt = ""
	name.Placeholder = "name, typed behind the prefix"
	name.CharLimit = 64
	body := textarea.New()
	body.Cursor.SetMode(cursor.CursorStatic)
	body.Placeholder = "What the bot writes instead — [[YOU_FIRST]], [[ARG1]], …"
	body.ShowLineNumbers = false
	body.CharLimit = 4096
	return &actionsTab{be: be, name: name, body: body}
}

func (t *actionsTab) title() string { return "Actions" }

func (t *actionsTab) capturing() bool {
	return t.mode == actionsForm || t.mode == actionsConfirmDelete
}

func (t *actionsTab) keys() []keyHelp {
	switch t.mode {
	case actionsForm:
		return []keyHelp{{"ctrl+s", "save"}, {"tab", "next field"}, {"esc", "cancel"}}
	case actionsConfirmDelete:
		return []keyHelp{{"y", "delete"}, {"n/esc", "keep"}}
	case actionsPlaceholders:
		return []keyHelp{{"↑/↓", "move"}, {"esc", "back"}}
	}
	return []keyHelp{{"n", "new"}, {"e", "edit"}, {"d", "delete"}, {"p", "placeholders"}, {"r", "refresh"}}
}

func (t *actionsTab) init() tea.Cmd { return t.load() }

func (t *actionsTab) load() tea.Cmd {
	t.seq++
	t.loading = true
	t.err = nil
	return call(t.be, "actions", t.seq, func(ctx context.Context, c *teal.Client) (teal.ActionList, *teal.Meta, error) {
		return c.Actions.List(ctx)
	})
}

func (t *actionsTab) selected() (teal.Action, bool) {
	if t.list == nil || len(t.list.Actions) == 0 {
		return teal.Action{}, false
	}
	return t.list.Actions[t.cur.pos], true
}

func (t *actionsTab) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case done[teal.ActionList]:
		if msg.op == "actions" && msg.seq == t.seq {
			t.loading = false
			if msg.err != nil {
				t.err = msg.err
				return nil
			}
			l := msg.val
			t.list = &l
			t.cur.clamp(len(l.Actions))
		}
	case done[teal.Action]:
		if msg.op != "action-save" {
			return nil
		}
		t.saving = false
		if msg.err != nil {
			t.formErr = msg.err
			return nil
		}
		t.mode = actionsBrowse
		t.notice = fmt.Sprintf("Saved %q.", msg.val.Name)
		return t.load()
	case done[ack]:
		if msg.op != "action-delete" {
			return nil
		}
		t.mode = actionsBrowse
		if msg.err != nil {
			t.err = msg.err
			return nil
		}
		t.notice = "Deleted."
		return t.load()
	case done[[]teal.Placeholder]:
		if msg.op == "placeholders" {
			t.placeholders, t.phErr = msg.val, msg.err
		}
	case tea.KeyMsg:
		return t.key(msg)
	}
	return nil
}

func (t *actionsTab) key(msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	switch t.mode {
	case actionsForm:
		return t.formKey(msg)
	case actionsConfirmDelete:
		switch k {
		case "y", "Y":
			a, ok := t.selected()
			if !ok {
				t.mode = actionsBrowse
				return nil
			}
			return callAck(t.be, "action-delete", 0, func(ctx context.Context, c *teal.Client) (*teal.Meta, error) {
				return c.Actions.Delete(ctx, a.ID)
			})
		case "n", "N", "esc":
			t.mode = actionsBrowse
		}
		return nil
	case actionsPlaceholders:
		switch k {
		case "esc", "p":
			t.mode = actionsBrowse
		case "up", "k":
			t.phCur.move(-1, len(t.placeholders))
		case "down", "j":
			t.phCur.move(1, len(t.placeholders))
		}
		return nil
	}

	t.notice = ""
	n := 0
	if t.list != nil {
		n = len(t.list.Actions)
	}
	switch k {
	case "up", "k":
		t.cur.move(-1, n)
	case "down", "j":
		t.cur.move(1, n)
	case "r":
		return t.load()
	case "n":
		return t.openForm(nil)
	case "e", "enter":
		if a, ok := t.selected(); ok {
			return t.openForm(&a)
		}
	case "d", "delete":
		if _, ok := t.selected(); ok {
			t.mode = actionsConfirmDelete
		}
	case "p":
		t.mode = actionsPlaceholders
		if t.placeholders == nil {
			return call(t.be, "placeholders", 0, func(ctx context.Context, c *teal.Client) ([]teal.Placeholder, *teal.Meta, error) {
				return c.Actions.Placeholders(ctx)
			})
		}
	}
	return nil
}

func (t *actionsTab) openForm(a *teal.Action) tea.Cmd {
	t.mode = actionsForm
	t.formErr = nil
	t.saving = false
	t.editing = 0
	t.name.Reset()
	t.body.Reset()
	if a != nil {
		t.editing = a.ID
		t.name.SetValue(a.Name)
		t.body.SetValue(a.Body)
	}
	t.formFocus = 0
	t.body.Blur()
	return t.name.Focus()
}

func (t *actionsTab) formKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		t.mode = actionsBrowse
		t.name.Blur()
		t.body.Blur()
		return nil
	case "tab", "shift+tab":
		t.formFocus = 1 - t.formFocus
		if t.formFocus == 0 {
			t.body.Blur()
			return t.name.Focus()
		}
		t.name.Blur()
		return t.body.Focus()
	case "ctrl+s":
		return t.save()
	case "enter":
		if t.formFocus == 0 {
			t.formFocus = 1
			t.name.Blur()
			return t.body.Focus()
		}
	}
	var cmd tea.Cmd
	if t.formFocus == 0 {
		t.name, cmd = t.name.Update(msg)
	} else {
		t.body, cmd = t.body.Update(msg)
	}
	return cmd
}

func (t *actionsTab) save() tea.Cmd {
	if t.saving {
		return nil
	}
	name := strings.TrimSpace(t.name.Value())
	body := strings.TrimRight(t.body.Value(), "\n")
	if name == "" || strings.TrimSpace(body) == "" {
		t.formErr = fmt.Errorf("a shortcut needs a name and a body")
		return nil
	}
	t.saving = true
	t.formErr = nil
	if t.editing == 0 {
		return call(t.be, "action-save", 0, func(ctx context.Context, c *teal.Client) (teal.Action, *teal.Meta, error) {
			return c.Actions.Create(ctx, teal.ActionCreateRequest{Name: name, Body: body})
		})
	}
	id := t.editing
	var req teal.ActionUpdateRequest
	if a, ok := t.find(id); !ok || a.Name != name {
		req.Name = &name
	}
	if a, ok := t.find(id); !ok || a.Body != body {
		req.Body = &body
	}
	if req.Name == nil && req.Body == nil {
		t.saving = false
		t.mode = actionsBrowse
		t.notice = "Nothing changed."
		return nil
	}
	return call(t.be, "action-save", 0, func(ctx context.Context, c *teal.Client) (teal.Action, *teal.Meta, error) {
		return c.Actions.Update(ctx, id, req)
	})
}

func (t *actionsTab) find(id int64) (teal.Action, bool) {
	if t.list == nil {
		return teal.Action{}, false
	}
	for _, a := range t.list.Actions {
		if a.ID == id {
			return a, true
		}
	}
	return teal.Action{}, false
}

func (t *actionsTab) view(width, height int) string {
	switch t.mode {
	case actionsForm:
		return t.formView(width, height)
	case actionsPlaceholders:
		return pane("Placeholders", t.placeholdersBody(width-4, height-3), width, height, true)
	}

	leftW := min(max(width*2/5, 30), 56)
	rightW := width - leftW
	title := "Shortcuts"
	if t.list != nil {
		title += fmt.Sprintf(" · %s · prefix %s", output.Quota(t.list.Quota), t.list.Prefix)
	}
	left := pane(title, t.listBody(leftW-4, height-3), leftW, height, true)
	right := pane("Body", t.previewBody(rightW-4, height-3), rightW, height, false)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (t *actionsTab) listBody(width, height int) string {
	var foot string
	switch {
	case t.mode == actionsConfirmDelete:
		a, _ := t.selected()
		foot = styleWarn.Render(fmt.Sprintf("Delete %q? y/n", a.Name))
	case t.notice != "":
		foot = styleOK.Render(t.notice)
	}
	if foot != "" {
		height--
	}
	var body string
	switch {
	case t.err != nil:
		body = errorView(t.err, width)
	case t.list == nil && t.loading:
		body = loading("shortcuts")
	case t.list == nil || len(t.list.Actions) == 0:
		body = styleMuted.Render(wrapText("No shortcuts yet. Press n to create one.", width))
	default:
		from, to := t.cur.window(len(t.list.Actions), height)
		var b strings.Builder
		for i := from; i < to; i++ {
			a := t.list.Actions[i]
			uses := strconv.FormatInt(a.Uses, 10) + "×"
			name := output.Truncate(t.list.Prefix+output.OneLine(a.Name), max(width-len(uses)-1, 1))
			row := name + strings.Repeat(" ", max(width-output.Width(name)-len(uses), 1)) + uses
			b.WriteString(line(row, width, i == t.cur.pos) + "\n")
		}
		body = strings.TrimRight(b.String(), "\n")
	}
	if foot != "" {
		return clip(body, height) + "\n" + foot
	}
	return body
}

func (t *actionsTab) previewBody(width, height int) string {
	a, ok := t.selected()
	if !ok {
		return ""
	}
	meta := styleMuted.Render(fmt.Sprintf("#%d · used %d times · last %s", a.ID, a.Uses, output.Unix(a.UsedAt)))
	return clip(meta+"\n\n"+wrapText(a.Body, width), height)
}

func (t *actionsTab) formView(width, height int) string {
	title := "New shortcut"
	if t.editing != 0 {
		title = fmt.Sprintf("Edit shortcut #%d", t.editing)
	}
	inner := width - 4
	t.name.Width = max(inner-8, 10)
	t.body.SetWidth(inner)
	t.body.SetHeight(max(height-10, 3))

	prefix := "."
	if t.list != nil {
		prefix = t.list.Prefix
	}
	var b strings.Builder
	b.WriteString(styleLabel.Render("Name  ") + prefix + t.name.View() + "\n\n")
	b.WriteString(styleLabel.Render("Body") + "\n" + t.body.View() + "\n")
	switch {
	case t.saving:
		b.WriteString(styleMuted.Render("Saving…"))
	case t.formErr != nil:
		b.WriteString(errorView(t.formErr, inner))
	default:
		b.WriteString(styleMuted.Render("Placeholders the plan does not open are refused — press esc, then p, to see them."))
	}
	return pane(title, b.String(), width, height, true)
}

func (t *actionsTab) placeholdersBody(width, height int) string {
	switch {
	case t.phErr != nil:
		return errorView(t.phErr, width)
	case t.placeholders == nil:
		return loading("placeholders")
	}
	from, to := t.phCur.window(len(t.placeholders), height)
	var b strings.Builder
	for i := from; i < to; i++ {
		p := t.placeholders[i]
		open := styleOK.Render("open")
		if !p.Allowed {
			open = styleWarn.Render("needs " + output.Or(p.RequiredPlan))
		}
		row := fmt.Sprintf("%-18s %-10s %s  %s", p.Token, p.Level, open, styleMuted.Render(output.OneLine(p.Label)))
		if i == t.phCur.pos {
			row = "› " + row
		} else {
			row = "  " + row
		}
		b.WriteString(output.Truncate(row, width+20) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

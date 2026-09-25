package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/x-chunk/celadon/internal/output"
)

// form is a column of labelled text fields, filled one after another: tab
// and the arrows move between them, enter moves on and submits from the
// last, ctrl+s submits from anywhere, esc cancels.
type form struct {
	title  string
	fields []formField
	focus  int
	err    error
	busy   bool
}

type formField struct {
	label string
	hint  string
	input textinput.Model
}

// formResult is what a key did to a form.
type formResult int

const (
	formEditing formResult = iota
	formSubmit
	formCancel
)

func newForm(title string, fields ...formField) *form {
	return &form{title: title, fields: fields}
}

// formInput is a form field with an initial value and a hint shown under the
// form while it has the keyboard.
func formInput(label, value, hint string) formField {
	in := newInput()
	in.Prompt = ""
	in.CharLimit = 1024
	in.SetValue(value)
	return formField{label: label, hint: hint, input: in}
}

// start gives the keyboard to the first field.
func (f *form) start() tea.Cmd {
	f.focus = 0
	return f.fields[0].input.Focus()
}

func (f *form) value(i int) string { return f.fields[i].input.Value() }

func (f *form) move(to int) tea.Cmd {
	to = min(max(to, 0), len(f.fields)-1)
	f.fields[f.focus].input.Blur()
	f.focus = to
	return f.fields[to].input.Focus()
}

// update handles one key and says whether it submitted or cancelled.
func (f *form) update(msg tea.KeyMsg) (formResult, tea.Cmd) {
	if f.busy {
		return formEditing, nil
	}
	switch msg.String() {
	case "esc":
		return formCancel, nil
	case "ctrl+s":
		return formSubmit, nil
	case "tab", "down":
		return formEditing, f.move(f.focus + 1)
	case "shift+tab", "up":
		return formEditing, f.move(f.focus - 1)
	case "enter":
		if f.focus == len(f.fields)-1 {
			return formSubmit, nil
		}
		return formEditing, f.move(f.focus + 1)
	}
	var cmd tea.Cmd
	f.fields[f.focus].input, cmd = f.fields[f.focus].input.Update(msg)
	return formEditing, cmd
}

// fail shows a refusal and gives the keyboard back.
func (f *form) fail(err error) {
	f.busy = false
	f.err = err
}

func (f *form) view(width, height int) string {
	labelW := 0
	for _, fl := range f.fields {
		labelW = max(labelW, output.Width(fl.label))
	}
	inner := width - 4
	var b strings.Builder
	for i := range f.fields {
		fl := &f.fields[i]
		fl.input.Width = max(inner-labelW-3, 8)
		label := fl.label + strings.Repeat(" ", labelW-output.Width(fl.label))
		if i == f.focus {
			label = styleKey.Render(label)
		} else {
			label = styleLabel.Render(label)
		}
		b.WriteString(label + "  " + fl.input.View() + "\n")
	}
	b.WriteString("\n")
	switch {
	case f.busy:
		b.WriteString(loading("the answer"))
	case f.err != nil:
		b.WriteString(errorView(f.err, inner))
	default:
		b.WriteString(styleMuted.Render(wrapText(f.fields[f.focus].hint, inner)))
	}
	return pane(f.title, b.String(), width, height, true)
}

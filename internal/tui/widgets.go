package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wordwrap"
	"github.com/muesli/reflow/wrap"

	"github.com/x-chunk/celadon/internal/api"
	"github.com/x-chunk/celadon/internal/output"
)

// selection is a position in a list of n items that keeps itself in range
// and scrolls a window of height rows along with it.
type selection struct {
	pos    int
	offset int
}

func (c *selection) move(delta, n int) {
	if n == 0 {
		c.pos, c.offset = 0, 0
		return
	}
	c.pos = min(max(c.pos+delta, 0), n-1)
}

func (c *selection) clamp(n int) { c.move(0, n) }

// window returns the range of items to draw in height rows, scrolled so the
// selection is on screen.
func (c *selection) window(n, height int) (from, to int) {
	if height <= 0 || n == 0 {
		return 0, 0
	}
	if c.pos < c.offset {
		c.offset = c.pos
	}
	if c.pos >= c.offset+height {
		c.offset = c.pos - height + 1
	}
	c.offset = min(c.offset, max(n-height, 0))
	return c.offset, min(c.offset+height, n)
}

// line draws one row of a list: the text cut to width, highlighted when
// selected.
func line(text string, width int, selected bool) string {
	text = output.Truncate(output.OneLine(text), width)
	if selected {
		pad := width - output.Width(text)
		return styleSelected.Render(text + strings.Repeat(" ", max(pad, 0)))
	}
	return text
}

// wrapText wraps sanitized text to width, breaking words only when a single
// word is wider than the line.
func wrapText(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return wrap.String(wordwrap.String(output.Sanitize(s), width), width)
}

// field is one label and value of a detail view.
type field struct{ label, value string }

// fields draws labelled values with the labels aligned.
func fields(pairs []field, width int) string {
	lw := 0
	for _, p := range pairs {
		lw = max(lw, output.Width(p.label))
	}
	var b strings.Builder
	for _, p := range pairs {
		label := styleLabel.Render(p.label + strings.Repeat(" ", lw-output.Width(p.label)))
		b.WriteString(label + "  " + output.Truncate(output.OneLine(p.value), max(width-lw-2, 1)) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// bar draws how much of a quota has been used, colored by how close to the
// ceiling it is.
func bar(used, limit int64, width int) string {
	if width < 3 {
		return ""
	}
	if limit <= 0 {
		return styleFaint.Render(strings.Repeat("·", width))
	}
	ratio := min(float64(used)/float64(limit), 1)
	filled := int(ratio * float64(width))
	style := styleOK
	switch {
	case ratio >= 1:
		style = styleError
	case ratio >= 0.8:
		style = styleWarn
	}
	return style.Render(strings.Repeat("█", filled)) + styleFaint.Render(strings.Repeat("░", width-filled))
}

// errorView explains a failure for a pane.
func errorView(err error, width int) string {
	p := api.Explain(err, time.Now())
	s := styleError.Render(wrapText("✗ "+p.Title, width))
	if p.Hint != "" {
		s += "\n" + styleMuted.Render(wrapText(p.Hint, width))
	}
	return s
}

// loading is what a pane shows while its first answer is on the way.
func loading(what string) string { return styleMuted.Render("Loading " + what + "…") }

// keyHelp is one key and what it does, for the footer and the help screen.
type keyHelp struct{ key, desc string }

func renderKeys(keys []keyHelp, width int) string {
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, styleKey.Render(k.key)+" "+styleMuted.Render(k.desc))
	}
	s := strings.Join(parts, styleFaint.Render(" · "))
	if lipgloss.Width(s) > width {
		// Drop from the end rather than wrap: the footer is one line.
		for len(parts) > 1 && lipgloss.Width(s) > width {
			parts = parts[:len(parts)-1]
			s = strings.Join(parts, styleFaint.Render(" · "))
		}
	}
	return s
}

// pane draws a bordered box of the given outer size.
func pane(title, body string, width, height int, focused bool) string {
	style := stylePane
	if focused {
		style = stylePaneFocused
	}
	innerW, innerH := max(width-4, 1), max(height-2, 1)
	head := styleHeading.Render(output.Truncate(title, innerW))
	if focused {
		head = styleTitle.Render(output.Truncate(title, innerW))
	}
	content := head + "\n" + body
	content = lipgloss.NewStyle().Width(innerW).Height(innerH).MaxHeight(innerH).MaxWidth(innerW).Render(content)
	return style.Width(innerW + 2).Render(content)
}

// clip cuts a block of text to height lines.
func clip(s string, height int) string {
	if height <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

func plural(n int64, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// newInput is a text field with a steady cursor. A blinking one is a timer
// that never stops, redrawing the screen twice a second for nothing.
func newInput() textinput.Model {
	in := textinput.New()
	in.Cursor.SetMode(cursor.CursorStatic)
	return in
}

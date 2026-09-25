// Package output writes what a command has to say, as a table for a person
// or as JSON for a program.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/x-chunk/teal"

	"github.com/x-chunk/celadon/internal/iostreams"
)

// Format is how a command's result is written.
type Format string

const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

// ParseFormat reads the value of --output.
func ParseFormat(s string) (Format, error) {
	switch Format(strings.ToLower(s)) {
	case FormatText, "table", "":
		return FormatText, nil
	case FormatJSON:
		return FormatJSON, nil
	}
	return "", fmt.Errorf("invalid output format %q: use text or json", s)
}

// Printer writes a command's result and its footnotes.
type Printer struct {
	IO     *iostreams.Streams
	Format Format
	// Quiet drops everything written to the error stream but errors.
	Quiet bool

	out, err *lipgloss.Renderer
}

// New returns a printer over the given streams.
func New(io *iostreams.Streams, format Format, quiet bool) *Printer {
	p := &Printer{IO: io, Format: format, Quiet: quiet}
	p.out = lipgloss.NewRenderer(io.Out)
	p.err = lipgloss.NewRenderer(io.Err)
	if !io.Color {
		p.out.SetColorProfile(termenv.Ascii)
	}
	if !io.Color || !io.ErrTTY {
		p.err.SetColorProfile(termenv.Ascii)
	}
	return p
}

// JSONMode reports whether the result is to be written as JSON.
func (p *Printer) JSONMode() bool { return p.Format == FormatJSON }

// JSON writes v as indented JSON. It is the API's payload as teal decoded
// it, so a script sees the API's own field names.
func (p *Printer) JSON(v any) error {
	enc := json.NewEncoder(p.IO.Out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// Result writes v as JSON in JSON mode and calls text otherwise.
func (p *Printer) Result(v any, text func() error) error {
	if p.JSONMode() {
		return p.JSON(v)
	}
	return text()
}

// Table writes rows under a header, aligned into columns. Every cell is
// sanitized and folded onto one line.
func (p *Printer) Table(header []string, rows [][]string) {
	tw := tabwriter.NewWriter(p.IO.Out, 0, 0, 2, ' ', 0)
	head := p.out.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "8", Dark: "7"})
	cells := make([]string, len(header))
	for i, h := range header {
		cells[i] = head.Render(strings.ToUpper(h))
	}
	fmt.Fprintln(tw, strings.Join(cells, "\t"))
	for _, row := range rows {
		for i := range row {
			row[i] = OneLine(row[i])
		}
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	tw.Flush()
}

// KV is one line of a detail view.
type KV struct {
	Key   string
	Value string
}

// Details writes labelled values, one to a line, with the labels aligned.
func (p *Printer) Details(pairs []KV) {
	width := 0
	for _, kv := range pairs {
		width = max(width, Width(kv.Key))
	}
	label := p.out.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "8", Dark: "245"})
	for _, kv := range pairs {
		pad := strings.Repeat(" ", width-Width(kv.Key))
		fmt.Fprintf(p.IO.Out, "%s%s  %s\n", label.Render(kv.Key+":"), pad, OneLine(kv.Value))
	}
}

// Heading writes a section title.
func (p *Printer) Heading(s string) {
	fmt.Fprintln(p.IO.Out, p.out.NewStyle().Bold(true).Render(s))
}

// Block writes free text — a message, a secret — sanitized but with its
// lines kept.
func (p *Printer) Block(s string) {
	s = Sanitize(s)
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	io.WriteString(p.IO.Out, s)
}

// Println writes a plain line to the output.
func (p *Printer) Println(a ...any) { fmt.Fprintln(p.IO.Out, a...) }

// Success tells the person running the command that it worked. It goes to
// the error stream, so it never lands in a pipe.
func (p *Printer) Success(format string, a ...any) {
	if p.Quiet {
		return
	}
	mark := p.err.NewStyle().Foreground(lipgloss.Color("2")).Render("✓")
	fmt.Fprintf(p.IO.Err, "%s %s\n", mark, fmt.Sprintf(format, a...))
}

// Warn writes a warning to the error stream.
func (p *Printer) Warn(format string, a ...any) {
	if p.Quiet {
		return
	}
	mark := p.err.NewStyle().Foreground(lipgloss.Color("3")).Render("!")
	fmt.Fprintf(p.IO.Err, "%s %s\n", mark, fmt.Sprintf(format, a...))
}

// Info writes a note to the error stream.
func (p *Printer) Info(format string, a ...any) {
	if p.Quiet {
		return
	}
	fmt.Fprintf(p.IO.Err, format+"\n", a...)
}

// Meta writes what a request cost to the error stream, when it cost
// anything or a balance came back with it. Under shared limits a free call
// says nothing at all.
func (p *Printer) Meta(m *teal.Meta) {
	if p.Quiet {
		return
	}
	if line := MetaLine(m); line != "" {
		fmt.Fprintln(p.IO.Err, p.err.NewStyle().Faint(true).Render(line))
	}
}

// MetaLine is the one-line account of what a request cost, or empty when
// there is nothing worth saying.
func MetaLine(m *teal.Meta) string {
	if m == nil || (m.Cost == 0 && !m.HasBalance) {
		return ""
	}
	var parts []string
	if m.Cost != 0 {
		parts = append(parts, "cost "+Credits(m.Cost))
	}
	if m.HasBalance {
		parts = append(parts, "balance "+Credits(m.Balance))
	}
	if m.Billing != "" {
		parts = append(parts, string(m.Billing))
	}
	return strings.Join(parts, " · ")
}

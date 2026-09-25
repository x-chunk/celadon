// Package iostreams is the terminal celadon talks to: where input comes from,
// where output goes, and what either end is.
//
// Every command reads and writes through a Streams rather than os.Std*, so a
// test can run it against buffers and say what it would have seen on a
// terminal.
package iostreams

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// ErrNoTTY is returned when something has to be typed and there is nobody
// at a terminal to type it.
var ErrNoTTY = errors.New("no terminal to prompt on")

// Streams is one set of standard streams.
type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer

	InTTY  bool
	OutTTY bool
	ErrTTY bool

	// Color says whether Out may carry ANSI styling.
	Color bool

	// secret reads one line without echoing it. It is term.ReadPassword on
	// a real terminal and replaceable in tests.
	secret func() (string, error)
	width  func() int

	lines *bufio.Reader
}

// System returns the process's own streams.
func System() *Streams {
	s := &Streams{
		In:     os.Stdin,
		Out:    os.Stdout,
		Err:    os.Stderr,
		InTTY:  isTerminal(os.Stdin),
		OutTTY: isTerminal(os.Stdout),
		ErrTTY: isTerminal(os.Stderr),
	}
	s.Color = s.OutTTY && colorAllowed()
	out := int(os.Stdout.Fd())
	s.width = func() int {
		if !s.OutTTY {
			return 0
		}
		w, _, err := term.GetSize(out)
		if err != nil {
			return 0
		}
		return w
	}
	fd := int(os.Stdin.Fd())
	s.secret = func() (string, error) {
		b, err := term.ReadPassword(fd)
		return string(b), err
	}
	return s
}

// Test returns streams over the given buffers, none of them a terminal. A
// test that needs a terminal sets the TTY fields and SetSecretReader.
func Test(in io.Reader, out, errOut io.Writer) *Streams {
	if in == nil {
		in = strings.NewReader("")
	}
	return &Streams{In: in, Out: out, Err: errOut}
}

// Width is how many columns the output terminal has, or zero when the output
// is not a terminal and lines may be as long as they need to be.
func (s *Streams) Width() int {
	if s.width == nil {
		return 0
	}
	return s.width()
}

// SetWidth fixes what Width reports, for tests.
func (s *Streams) SetWidth(w int) { s.width = func() int { return w } }

// SetSecretReader replaces how a hidden line is read, for tests.
func (s *Streams) SetSecretReader(fn func() (string, error)) { s.secret = fn }

// CanPrompt reports whether somebody can be asked something: input and the
// error stream, where the question is written, are both a terminal.
func (s *Streams) CanPrompt() bool { return s.InTTY && s.ErrTTY }

// Secret asks for a line without echoing it. The question goes to the error
// stream, so that the output stays clean for a pipe.
func (s *Streams) Secret(label string) (string, error) {
	if !s.CanPrompt() || s.secret == nil {
		return "", ErrNoTTY
	}
	fmt.Fprint(s.Err, label)
	v, err := s.secret()
	fmt.Fprintln(s.Err)
	if err != nil {
		return "", fmt.Errorf("reading from the terminal: %w", err)
	}
	return strings.TrimRight(v, "\r\n"), nil
}

// Prompt asks for a line that is echoed as it is typed.
func (s *Streams) Prompt(label string) (string, error) {
	if !s.CanPrompt() {
		return "", ErrNoTTY
	}
	fmt.Fprint(s.Err, label)
	return s.ReadLine()
}

// Confirm asks a yes-or-no question; anything but yes is no.
func (s *Streams) Confirm(label string) (bool, error) {
	answer, err := s.Prompt(label + " [y/N] ")
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

// ReadLine reads one line from input, without its line ending. Successive
// calls read successive lines. The end of input after some text is a line;
// the end of input before any is io.EOF.
func (s *Streams) ReadLine() (string, error) {
	if s.lines == nil {
		s.lines = bufio.NewReader(s.In)
	}
	line, err := s.lines.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// ReadAll reads what is left of input, capped at limit bytes.
func (s *Streams) ReadAll(limit int64) ([]byte, error) {
	var r io.Reader = s.In
	if s.lines != nil {
		r = s.lines
	}
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("input is larger than %d bytes", limit)
	}
	return b, nil
}

func isTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

// colorAllowed honors the NO_COLOR convention (https://no-color.org) and a
// terminal that says it cannot draw any.
func colorAllowed() bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	if os.Getenv("CLICOLOR") == "0" {
		return false
	}
	return os.Getenv("TERM") != "dumb"
}

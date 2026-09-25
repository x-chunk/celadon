package output

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/mattn/go-runewidth"
	"github.com/x-chunk/teal"
)

// Sanitize makes text from the archive safe to write to a terminal.
//
// A message is whatever somebody typed into Telegram, and a terminal treats
// some bytes as instructions: an escape sequence in a message could move the
// cursor, retitle the window, rewrite what was printed above it or, on some
// emulators, do worse. A Windows line ending becomes a newline, and every
// other control character but a newline and a tab is replaced with U+FFFD,
// which is shown for exactly that and is not one. A lone carriage return is
// replaced too: it is how a line is made to print over itself.
func Sanitize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	clean := true
	for _, r := range s {
		if isHostile(r) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	return strings.Map(func(r rune) rune {
		if isHostile(r) {
			return '�'
		}
		return r
	}, s)
}

func isHostile(r rune) bool {
	if r == '\n' || r == '\t' {
		return false
	}
	// C0 and C1 controls, DEL, and the bidirectional overrides that can make
	// text read in an order other than the one it is in.
	return unicode.IsControl(r) || (r >= '‪' && r <= '‮') || (r >= '⁦' && r <= '⁩')
}

// OneLine folds text onto one line, for a table cell.
func OneLine(s string) string {
	s = Sanitize(s)
	if !strings.ContainsAny(s, "\n\t") {
		return s
	}
	return strings.Join(strings.Fields(s), " ")
}

// Truncate cuts s to at most width terminal columns, ending with an ellipsis
// when it was cut. It counts columns, not bytes or runes: a CJK character
// takes two and a combining mark none.
func Truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= width {
		return s
	}
	return runewidth.Truncate(s, width, "…")
}

// Width is how many terminal columns s takes.
func Width(s string) int { return runewidth.StringWidth(s) }

// Dash is what an empty value is shown as.
const Dash = "—"

// Time renders a timestamp in the local zone, or a dash for the zero time.
func Time(t time.Time) string {
	if t.IsZero() {
		return Dash
	}
	return t.Local().Format("2006-01-02 15:04")
}

// Unix renders a Unix timestamp as Time does; zero is no timestamp at all.
func Unix(u teal.Unix) string {
	if u == 0 {
		return Dash
	}
	return Time(u.At())
}

// Seconds renders a count of seconds as the largest two units it spans:
// "90" is "1m 30s", "86400" is "1d". Zero is "off".
func Seconds(n int64) string {
	if n <= 0 {
		return "off"
	}
	return Duration(time.Duration(n) * time.Second)
}

// Duration renders d as the largest two units it spans.
func Duration(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	d = d.Round(time.Second)
	units := []struct {
		n    time.Duration
		name string
	}{
		{24 * time.Hour, "d"}, {time.Hour, "h"}, {time.Minute, "m"}, {time.Second, "s"},
	}
	var parts []string
	for _, u := range units {
		if d >= u.n {
			parts = append(parts, fmt.Sprintf("%d%s", d/u.n, u.name))
			d %= u.n
			if len(parts) == 2 {
				break
			}
		} else if len(parts) == 1 {
			break
		}
	}
	return strings.Join(parts, " ")
}

// Until renders how long remains until t, from now.
func Until(t, now time.Time) string {
	if t.IsZero() {
		return Dash
	}
	d := t.Sub(now)
	if d <= 0 {
		return "now"
	}
	return "in " + Duration(d)
}

// Money renders an amount as the API spelled it, or from its credits when
// it did not.
func Money(m teal.Money) string {
	if m.Display != "" {
		return m.Display
	}
	return Credits(m.Credits)
}

// Credits renders an amount of credits as dollars, to the precision the
// amount needs.
func Credits(c teal.Credits) string {
	return c.String()
}

// Cents renders an amount of US cents as dollars.
func Cents(c int64) string {
	sign := ""
	if c < 0 {
		sign, c = "-", -c
	}
	return fmt.Sprintf("%s$%d.%02d", sign, c/100, c%100)
}

// Quota renders how much of a quota has been used.
func Quota(q teal.Quota) string {
	if q.Unlimited {
		return fmt.Sprintf("%d used · unlimited", q.Used)
	}
	return fmt.Sprintf("%d / %d", q.Used, q.Limit)
}

// Bool renders a flag as a word.
func Bool(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// Or returns s, or the dash when s is empty.
func Or(s string) string {
	if strings.TrimSpace(s) == "" {
		return Dash
	}
	return s
}

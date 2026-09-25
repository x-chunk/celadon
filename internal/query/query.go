// Package query reads what a person types to describe a search or a span of
// time, for the command line and the terminal interface alike.
package query

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/x-chunk/teal"
)

// TextField is the field the free words of a query search.
const TextField = "text"

// ParseCondition reads one filter written as field=value (the field equals
// the value) or field~value (the field contains it). Whichever of the two
// signs comes first splits it, so a value may carry either.
//
// The value is always sent as a string, whatever the field's kind: the API
// reads every value as text and converts it against the field itself, which
// is also where a value the field cannot take is refused.
func ParseCondition(expr string) (teal.Condition, error) {
	i := strings.IndexAny(expr, "=~")
	if i <= 0 {
		return teal.Condition{}, fmt.Errorf("invalid filter %q: write field=value to match exactly or field~value to match a substring", expr)
	}
	field := strings.TrimSpace(expr[:i])
	if !fieldName.MatchString(field) {
		return teal.Condition{}, fmt.Errorf("invalid filter %q: %q is not a field name", expr, field)
	}
	mode := teal.MatchEquals
	if expr[i] == '~' {
		mode = teal.MatchContains
	}
	value := expr[i+1:]
	if value == "" {
		return teal.Condition{}, fmt.Errorf("invalid filter %q: the value is empty", expr)
	}
	return teal.Condition{Field: field, Mode: mode, Value: value}, nil
}

var fieldName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// looksLikeFilter reports whether a token is meant as a filter rather than a
// word: a field name, then = or ~.
func looksLikeFilter(tok string) bool {
	i := strings.IndexAny(tok, "=~")
	return i > 0 && fieldName.MatchString(tok[:i])
}

// ParseLine reads a whole query typed on one line, as the terminal
// interface's search box takes it:
//
//	invoice suser=ann |media=photo "created~2026-09"
//
// Words become one condition on the text, first. A token shaped field=value
// or field~value is a filter joined with AND, or with OR when it starts with
// "|". Double quotes keep spaces inside a word or a value.
func ParseLine(line string) ([]teal.Condition, error) {
	tokens, err := split(line)
	if err != nil {
		return nil, err
	}
	var words []string
	var filters []teal.Condition
	for _, tok := range tokens {
		conn := teal.ConnAnd
		body := tok
		if strings.HasPrefix(tok, "|") && len(tok) > 1 {
			conn, body = teal.ConnOr, tok[1:]
		}
		if !looksLikeFilter(body) {
			words = append(words, tok)
			continue
		}
		c, err := ParseCondition(body)
		if err != nil {
			return nil, err
		}
		c.Conn = conn
		filters = append(filters, c)
	}
	return Combine(words, false, filters), nil
}

// Combine puts the words, as one condition on the text, in front of the
// filters. The first condition joins nothing, so its conn is dropped and the
// body stays what the documentation shows.
func Combine(words []string, exact bool, filters []teal.Condition) []teal.Condition {
	var out []teal.Condition
	if text := strings.TrimSpace(strings.Join(words, " ")); text != "" {
		mode := teal.MatchContains
		if exact {
			mode = teal.MatchEquals
		}
		out = append(out, teal.Condition{Field: TextField, Mode: mode, Value: text})
	}
	out = append(out, filters...)
	if len(out) > 0 {
		out[0].Conn = ""
	}
	return out
}

// split breaks a line into tokens at spaces outside double quotes.
func split(line string) ([]string, error) {
	var (
		tokens []string
		cur    strings.Builder
		quoted bool
		any    bool
	)
	for _, r := range line {
		switch {
		case r == '"':
			quoted = !quoted
			any = true
		case unicode.IsSpace(r) && !quoted:
			if any {
				tokens = append(tokens, cur.String())
				cur.Reset()
				any = false
			}
		default:
			cur.WriteRune(r)
			any = true
		}
	}
	if quoted {
		return nil, errors.New("a quote is not closed")
	}
	if any {
		tokens = append(tokens, cur.String())
	}
	return tokens, nil
}

// Seconds reads a span of time for a setting: a Go duration ("90s", "36h"),
// a count of days ("7d"), a bare count of seconds, or "off"/"0".
func Seconds(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	switch s {
	case "", "0", "off", "none":
		return 0, nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n < 0 {
			return 0, fmt.Errorf("invalid duration %q: it cannot be negative", s)
		}
		return n, nil
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.ParseFloat(days, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return int64(n * 24 * 3600), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid duration %q: use 90s, 12h, 7d or off", s)
	}
	if d%time.Second != 0 {
		return 0, fmt.Errorf("invalid duration %q: the API counts whole seconds", s)
	}
	return int64(d / time.Second), nil
}

package cli

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/pflag"
	"github.com/x-chunk/teal"
)

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
		return teal.Condition{}, fmt.Errorf("invalid filter %q: %q is not a field name (see `celadon fields`)", expr, field)
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

// conditionFlag is a repeatable flag that appends to a list shared with its
// sibling, so --where and --or keep the order they were typed in: the API
// combines conditions left to right, and the order is part of the query.
type conditionFlag struct {
	list *[]teal.Condition
	conn string
}

var _ pflag.Value = conditionFlag{}

func (f conditionFlag) Set(expr string) error {
	c, err := ParseCondition(expr)
	if err != nil {
		return err
	}
	c.Conn = f.conn
	*f.list = append(*f.list, c)
	return nil
}

func (f conditionFlag) String() string { return "" }
func (f conditionFlag) Type() string   { return "filter" }

// queryFlags are the flags search, count and export share.
type queryFlags struct {
	conditions []teal.Condition
	chat       string
	exact      bool
}

func (q *queryFlags) register(fs *pflag.FlagSet) {
	fs.Var(conditionFlag{list: &q.conditions, conn: teal.ConnAnd}, "where", "filter joined with AND: field=value or field~value (repeatable)")
	fs.Var(conditionFlag{list: &q.conditions, conn: teal.ConnOr}, "or", "filter joined with OR to the one before it (repeatable)")
	fs.StringVarP(&q.chat, "chat", "c", "", "only this chat id")
	fs.BoolVar(&q.exact, "exact", false, "match the words as the whole text rather than a part of it")
}

// build turns the words and the flags into a request. The words, when there
// are any, are one condition on the text and come first.
func (q *queryFlags) build(words []string, page int) (teal.SearchRequest, error) {
	req := teal.SearchRequest{Page: page}
	if q.chat != "" {
		id, err := parseChat(q.chat)
		if err != nil {
			return req, err
		}
		req.Chat = &id
	}
	if text := strings.TrimSpace(strings.Join(words, " ")); text != "" {
		mode := teal.MatchContains
		if q.exact {
			mode = teal.MatchEquals
		}
		req.Conditions = append(req.Conditions, teal.Condition{Field: "text", Mode: mode, Value: text})
	}
	req.Conditions = append(req.Conditions, q.conditions...)
	if len(req.Conditions) > 0 {
		// The first condition joins nothing; the API ignores its conn, and
		// leaving it out keeps the body what the documentation shows.
		req.Conditions[0].Conn = ""
	}
	return req, nil
}

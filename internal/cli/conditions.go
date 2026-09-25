package cli

import (
	"github.com/spf13/pflag"
	"github.com/x-chunk/teal"

	"github.com/x-chunk/celadon/internal/query"
)

// conditionFlag is a repeatable flag that appends to a list shared with its
// sibling, so --where and --or keep the order they were typed in: the API
// combines conditions left to right, and the order is part of the query.
type conditionFlag struct {
	list *[]teal.Condition
	conn string
}

var _ pflag.Value = conditionFlag{}

func (f conditionFlag) Set(expr string) error {
	c, err := query.ParseCondition(expr)
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
	req.Conditions = query.Combine(words, q.exact, q.conditions)
	return req, nil
}

package cli

import (
	"testing"

	"github.com/x-chunk/teal"
)

func TestParseCondition(t *testing.T) {
	cases := map[string]teal.Condition{
		"text~invoice":        {Field: "text", Mode: teal.MatchContains, Value: "invoice"},
		"suser=ann":           {Field: "suser", Mode: teal.MatchEquals, Value: "ann"},
		"text~a=b":            {Field: "text", Mode: teal.MatchContains, Value: "a=b"},
		"text=a~b":            {Field: "text", Mode: teal.MatchEquals, Value: "a~b"},
		"created=2026-09-01":  {Field: "created", Mode: teal.MatchEquals, Value: "2026-09-01"},
		"sender=-1001234":     {Field: "sender", Mode: teal.MatchEquals, Value: "-1001234"},
		"text~ spaced value ": {Field: "text", Mode: teal.MatchContains, Value: " spaced value "},
	}
	for in, want := range cases {
		got, err := ParseCondition(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%q = %+v, want %+v", in, got, want)
		}
	}
	for _, bad := range []string{"", "text", "=x", "~x", "text=", "Text=x", "a b=x", "1x=y"} {
		if _, err := ParseCondition(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

package query

import (
	"reflect"
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

func TestParseLine(t *testing.T) {
	got, err := ParseLine(`the invoice suser=ann |media=photo "created~2026 09" 1+1=2`)
	if err != nil {
		t.Fatal(err)
	}
	want := []teal.Condition{
		{Field: "text", Mode: teal.MatchContains, Value: "the invoice 1+1=2"},
		{Field: "suser", Mode: teal.MatchEquals, Value: "ann", Conn: teal.ConnAnd},
		{Field: "media", Mode: teal.MatchEquals, Value: "photo", Conn: teal.ConnOr},
		{Field: "created", Mode: teal.MatchContains, Value: "2026 09", Conn: teal.ConnAnd},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseLine =\n%+v\nwant\n%+v", got, want)
	}

	got, err = ParseLine("|media=photo")
	if err != nil || len(got) != 1 || got[0].Conn != "" {
		t.Errorf("a lone OR filter = %+v, %v", got, err)
	}
	if got, err := ParseLine("   "); err != nil || got != nil {
		t.Errorf("an empty line = %+v, %v", got, err)
	}
	if _, err := ParseLine(`"unclosed`); err == nil {
		t.Error("an unclosed quote was accepted")
	}
}

func TestCombine(t *testing.T) {
	got := Combine([]string{"a", "b"}, true, []teal.Condition{{Field: "x", Mode: "eq", Value: "1", Conn: "or"}})
	if len(got) != 2 || got[0].Mode != teal.MatchEquals || got[0].Value != "a b" || got[0].Conn != "" || got[1].Conn != "or" {
		t.Errorf("Combine = %+v", got)
	}
	filters := []teal.Condition{{Field: "x", Conn: "and"}}
	Combine(nil, false, filters)
	if filters[0].Conn != "and" {
		t.Error("Combine changed the caller's slice")
	}
}

func TestSeconds(t *testing.T) {
	cases := map[string]int64{
		"off": 0, "0": 0, "": 0, "90": 90, "90s": 90, "5m": 300, "12h": 43200, "7d": 604800, "1.5d": 129600,
	}
	for in, want := range cases {
		got, err := Seconds(in)
		if err != nil || got != want {
			t.Errorf("Seconds(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"-5", "soon", "1.5s", "-1d", "-1h"} {
		if _, err := Seconds(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

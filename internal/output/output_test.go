package output

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/x-chunk/teal"

	"github.com/x-chunk/celadon/internal/iostreams"
)

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"plain text":            "plain text",
		"line\nbreak\tand tab":  "line\nbreak\tand tab",
		"\x1b]0;pwned\x07title": "�]0;pwned�title",
		"\x1b[2Jclear":          "�[2Jclear",
		"csi \u009b31m":         "csi �31m",
		"bidi ‮evil‬":           "bidi �evil�",
		"cr\rover":              "cr�over",
		"кириллица и emoji 🙂":   "кириллица и emoji 🙂",
	}
	for in, want := range cases {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOneLine(t *testing.T) {
	if got := OneLine("a\n  b\tc\r\n"); got != "a b c" {
		t.Errorf("OneLine = %q", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("hello", 10); got != "hello" {
		t.Errorf("Truncate short = %q", got)
	}
	if got := Truncate("hello world", 6); Width(got) > 6 || !strings.HasSuffix(got, "…") {
		t.Errorf("Truncate = %q", got)
	}
	if got := Truncate("日本語テキスト", 5); Width(got) > 5 {
		t.Errorf("Truncate wide = %q (%d columns)", got, Width(got))
	}
	if got := Truncate("x", 0); got != "" {
		t.Errorf("Truncate to zero = %q", got)
	}
}

func TestDuration(t *testing.T) {
	cases := map[time.Duration]string{
		90 * time.Second:             "1m 30s",
		24 * time.Hour:               "1d",
		26*time.Hour + 5*time.Minute: "1d 2h",
		time.Hour + 30*time.Second:   "1h",
		45 * time.Second:             "45s",
		7*24*time.Hour + time.Minute: "7d",
		500 * time.Millisecond:       "500ms",
	}
	for in, want := range cases {
		if got := Duration(in); got != want {
			t.Errorf("Duration(%s) = %q, want %q", in, got, want)
		}
	}
	if Seconds(0) != "off" {
		t.Error("zero seconds is not off")
	}
}

func TestCents(t *testing.T) {
	for in, want := range map[int64]string{0: "$0.00", 5: "$0.05", 1234: "$12.34", -250: "-$2.50"} {
		if got := Cents(in); got != want {
			t.Errorf("Cents(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestMetaLine(t *testing.T) {
	if MetaLine(nil) != "" || MetaLine(&teal.Meta{Billing: teal.BillingShared}) != "" {
		t.Error("a free shared call said something")
	}
	got := MetaLine(&teal.Meta{Billing: teal.BillingCredits, Cost: 500, Balance: 4_200_000, HasBalance: true})
	if got != "cost $0.0005 · balance $4.2 · credits" {
		t.Errorf("MetaLine = %q", got)
	}
}

func TestParseFormat(t *testing.T) {
	for in, want := range map[string]Format{"": FormatText, "text": FormatText, "table": FormatText, "JSON": FormatJSON} {
		got, err := ParseFormat(in)
		if err != nil || got != want {
			t.Errorf("ParseFormat(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseFormat("yaml"); err == nil {
		t.Error("yaml was accepted")
	}
}

func TestTableSanitizesCells(t *testing.T) {
	var out bytes.Buffer
	p := New(iostreams.Test(nil, &out, &bytes.Buffer{}), FormatText, false)
	p.Table([]string{"id", "text"}, [][]string{{"1", "hi\x1b[31m\nthere"}})
	got := out.String()
	if strings.Contains(got, "\x1b") {
		t.Errorf("an escape reached the output: %q", got)
	}
	if !strings.Contains(got, "ID") || !strings.Contains(got, "hi�[31m there") {
		t.Errorf("table = %q", got)
	}
}

func TestQuietDropsNotes(t *testing.T) {
	var errOut bytes.Buffer
	p := New(iostreams.Test(nil, &bytes.Buffer{}, &errOut), FormatText, true)
	p.Success("done")
	p.Warn("careful")
	p.Meta(&teal.Meta{Cost: 1, HasBalance: true})
	if errOut.Len() != 0 {
		t.Errorf("quiet wrote %q", errOut.String())
	}
}

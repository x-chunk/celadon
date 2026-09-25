package cli

import (
	"testing"
)

func TestParseSeconds(t *testing.T) {
	cases := map[string]int64{
		"off": 0, "0": 0, "": 0, "90": 90, "90s": 90, "5m": 300, "12h": 43200, "7d": 604800, "1.5d": 129600,
	}
	for in, want := range cases {
		got, err := parseSeconds(in)
		if err != nil || got != want {
			t.Errorf("parseSeconds(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"-5", "soon", "1.5s", "-1d", "-1h"} {
		if _, err := parseSeconds(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestParseIDs(t *testing.T) {
	if id, err := parseID("x", "#12"); err != nil || id != 12 {
		t.Errorf("parseID(#12) = %d, %v", id, err)
	}
	for _, bad := range []string{"0", "-1", "x", ""} {
		if _, err := parseID("x", bad); err == nil {
			t.Errorf("parseID(%q) was accepted", bad)
		}
	}
	if id, err := parseChat("-100"); err != nil || id != -100 {
		t.Errorf("parseChat(-100) = %d, %v", id, err)
	}
	if _, err := parseChat("0"); err == nil {
		t.Error("chat 0 was accepted")
	}
}

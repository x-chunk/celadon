package cli

import (
	"testing"
)

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

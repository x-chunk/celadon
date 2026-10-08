package query

import (
	"strings"
	"testing"
)

func TestActionBody(t *testing.T) {
	for body, want := range map[string]int{"": 0, "Hi [[YOU_FIRST]]!": 17, "привет": 6, "🙂": 2, "a🙂b": 4} {
		if got := ActionBodyLen(body); got != want {
			t.Errorf("ActionBodyLen(%q) = %d, want %d", body, got, want)
		}
	}
	for _, ok := range []string{"Hi!", strings.Repeat("я", MaxActionBody), strings.Repeat("🙂", MaxActionBody/2)} {
		if err := CheckActionBody(ok); err != nil {
			t.Errorf("CheckActionBody(%d units) = %v", ActionBodyLen(ok), err)
		}
	}
	for _, bad := range []string{strings.Repeat("a", MaxActionBody+1), strings.Repeat("🙂", MaxActionBody/2) + "a", "nul\x00here"} {
		if err := CheckActionBody(bad); err == nil {
			t.Errorf("CheckActionBody(%.20q…) was accepted", bad)
		}
	}
}

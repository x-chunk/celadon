package output

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// Emoji with a variation selector or skin tone must be measured as lipgloss
// measures them, or a list row spills onto a second line.
func TestWidthMatchesLipgloss(t *testing.T) {
	for _, s := range []string{"❤️ Love", "☀️ ok", "Чат ✌🏻", "🇷🇺 Чат"} {
		if got, want := Width(s), lipgloss.Width(s); got != want {
			t.Errorf("Width(%q) = %d, lipgloss says %d", s, got, want)
		}
		if w := lipgloss.Width(Truncate(s+" and more", 6)); w > 6 {
			t.Errorf("Truncate(%q, 6) is %d wide", s, w)
		}
	}
}

func TestSanitizeDropsJoiners(t *testing.T) {
	for in, want := range map[string]string{
		"👼🏻😇✌🏻":   "👼😇✌",
		"❤️ Love": "❤ Love",
		"👨‍👩‍👧":   "👨👩👧",
	} {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

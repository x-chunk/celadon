package tui

import "github.com/charmbracelet/lipgloss"

// The palette adapts to the terminal's background: every color is a pair,
// light then dark.
var (
	colorAccent = lipgloss.AdaptiveColor{Light: "#2E7D6B", Dark: "#7FD1B9"} // celadon
	colorMuted  = lipgloss.AdaptiveColor{Light: "#6B6B6B", Dark: "#8A8A8A"}
	colorFaint  = lipgloss.AdaptiveColor{Light: "#A0A0A0", Dark: "#5C5C5C"}
	colorError  = lipgloss.AdaptiveColor{Light: "#B3261E", Dark: "#F2B8B5"}
	colorWarn   = lipgloss.AdaptiveColor{Light: "#8A5A00", Dark: "#F5C26B"}
	colorOK     = lipgloss.AdaptiveColor{Light: "#1B6E3A", Dark: "#8FD19E"}
	colorSelBg  = lipgloss.AdaptiveColor{Light: "#D6EFE7", Dark: "#23433A"}
)

var (
	styleTitle    = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	styleHeading  = lipgloss.NewStyle().Bold(true)
	styleMuted    = lipgloss.NewStyle().Foreground(colorMuted)
	styleFaint    = lipgloss.NewStyle().Foreground(colorFaint)
	styleError    = lipgloss.NewStyle().Foreground(colorError)
	styleWarn     = lipgloss.NewStyle().Foreground(colorWarn)
	styleOK       = lipgloss.NewStyle().Foreground(colorOK)
	styleSelected = lipgloss.NewStyle().Background(colorSelBg).Bold(true)
	styleKey      = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	styleLabel    = lipgloss.NewStyle().Foreground(colorMuted)

	styleTabActive   = lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Underline(true).Padding(0, 1)
	styleTabInactive = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 1)

	stylePane        = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorFaint).Padding(0, 1)
	stylePaneFocused = stylePane.BorderForeground(colorAccent)
)

package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestOverviewLoadsOnStart(t *testing.T) {
	d := newDriver(t)
	d.wantView("Reports", "balance $4.20", "search:daily", "35 / 50", "Ultra", "1 Overview", "work")
}

func TestQuit(t *testing.T) {
	d := newDriver(t)
	d.keys("q")
	if !d.quit {
		t.Error("q did not quit")
	}
}

func TestHelpAndASmallTerminal(t *testing.T) {
	d := newDriver(t)
	d.keys("?")
	d.wantView("Everywhere", "1 Overview")
	d.keys("x")
	d.wantView("Application")

	d.send(tea.WindowSizeMsg{Width: 40, Height: 10})
	d.wantView("at least 60×16")
}

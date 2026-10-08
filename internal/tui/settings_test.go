package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestSettingsToggleAndType(t *testing.T) {
	d := newDriver(t)
	retention := map[string]any{"mode": "keep", "ttl_seconds": 0, "can_ttl": true, "can_in_chat": false}
	d.api.ok("GET", "/v1/settings/retention", retention)
	d.api.ok("PATCH", "/v1/settings/retention", map[string]any{"mode": "rotate", "ttl_seconds": 604800, "can_ttl": true})
	d.api.ok("GET", "/v1/settings/vault", map[string]any{"reveal_ttl_seconds": 60})
	d.api.ok("GET", "/v1/settings/actions", map[string]any{"prefix": ".", "can_use": true})
	d.api.ok("PATCH", "/v1/settings/actions", map[string]any{"prefix": "/", "can_use": true})
	d.api.ok("GET", "/v1/settings/language", map[string]any{"effective": "en", "supported": []string{"en", "ru"}})

	d.open("Settings")
	d.wantView("keep — refuse the newest message", "(the plan does not open this)")

	d.key(tea.KeyEnter)
	if b := d.body("PATCH", "/v1/settings/retention"); b["mode"] != "rotate" || len(b) != 1 {
		t.Errorf("toggle body = %v", b)
	}
	d.wantView("Saved.", "rotate")

	d.key(tea.KeyDown)
	d.key(tea.KeyEnter)
	d.keys("7d")
	d.key(tea.KeyEnter)
	if b := d.body("PATCH", "/v1/settings/retention"); b["ttl_seconds"] != float64(604800) {
		t.Errorf("ttl body = %v", b)
	}

	d.key(tea.KeyDown)
	d.key(tea.KeyDown)
	d.key(tea.KeyDown)
	d.key(tea.KeyRight)
	if b := d.body("PATCH", "/v1/settings/actions"); b["prefix"] != "/" {
		t.Errorf("prefix body = %v", b)
	}
}

func TestSettingsDrawTheirNotesInColorWithoutGarbage(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(old) })

	d := newDriver(t)
	d.api.ok("GET", "/v1/settings/retention", map[string]any{"mode": "keep", "can_ttl": false})
	d.api.ok("GET", "/v1/settings/vault", map[string]any{"reveal_ttl_seconds": 60, "can_reveal_ttl": true})
	d.api.ok("GET", "/v1/settings/actions", map[string]any{"prefix": ".", "can_use": true})
	d.api.ok("GET", "/v1/settings/language", map[string]any{"effective": "en"})
	d.open("Settings")
	v := d.view()
	if !strings.Contains(v, "\x1b[") {
		t.Fatal("nothing was drawn in color")
	}
	if strings.Contains(v, "�") {
		t.Errorf("the escapes of a style were sanitized onto the screen:\n%s", v)
	}
	d.wantView("(the plan does not open this)", "/ , ! $ # > ~")
}

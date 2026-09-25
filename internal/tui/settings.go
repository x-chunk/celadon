package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/x-chunk/teal"

	"github.com/x-chunk/celadon/internal/output"
	"github.com/x-chunk/celadon/internal/query"
)

// settingRow is one line of the settings screen: what it shows and how it
// changes.
type settingRow int

const (
	rowMode settingRow = iota
	rowTTL
	rowInChat
	rowRevealTTL
	rowPrefix
	rowLanguage
	rowCount
)

// actionPrefixes is the closed set the API accepts; the screen cycles
// through it rather than letting anything else be typed.
var actionPrefixes = []string{".", "/", ",", "!", "$", "#", ">", "~"}

// settingsTab shows the four settings and changes them one field at a time:
// a toggle on enter, a value typed where one is needed.
type settingsTab struct {
	be *backend

	retention *teal.Retention
	vault     *teal.VaultSettings
	actions   *teal.ActionSettings
	language  *teal.Language
	err       error
	seq       int

	cur     selection
	editing bool
	input   textinput.Model
	saving  bool
	notice  string
}

func newSettingsTab(be *backend) *settingsTab {
	in := newInput()
	in.Prompt = "› "
	in.CharLimit = 32
	return &settingsTab{be: be, input: in}
}

func (t *settingsTab) title() string   { return "Settings" }
func (t *settingsTab) capturing() bool { return t.editing }

func (t *settingsTab) keys() []keyHelp {
	if t.editing {
		return []keyHelp{{"enter", "save"}, {"esc", "cancel"}}
	}
	return []keyHelp{{"↑/↓", "choose"}, {"enter", "change"}, {"←/→", "cycle"}, {"r", "refresh"}}
}

func (t *settingsTab) init() tea.Cmd { return t.load() }

func (t *settingsTab) load() tea.Cmd {
	t.seq++
	seq := t.seq
	t.err = nil
	return tea.Batch(
		call(t.be, "settings", seq, func(ctx context.Context, c *teal.Client) (teal.Retention, *teal.Meta, error) {
			return c.Settings.Retention(ctx)
		}),
		call(t.be, "settings", seq, func(ctx context.Context, c *teal.Client) (teal.VaultSettings, *teal.Meta, error) {
			return c.Settings.Vault(ctx)
		}),
		call(t.be, "settings", seq, func(ctx context.Context, c *teal.Client) (teal.ActionSettings, *teal.Meta, error) {
			return c.Settings.Actions(ctx)
		}),
		call(t.be, "settings", seq, func(ctx context.Context, c *teal.Client) (teal.Language, *teal.Meta, error) {
			return c.Settings.Language(ctx)
		}),
	)
}

// accept takes an answer to a read or a write of this tab.
func accept[T any](t *settingsTab, d done[T], into **T) {
	switch d.op {
	case "settings":
		if d.seq != t.seq {
			return
		}
	case "settings-save":
		t.saving = false
		if d.err == nil {
			t.notice = "Saved."
		}
	default:
		return
	}
	if d.err != nil {
		t.err = d.err
		return
	}
	v := d.val
	*into = &v
}

func (t *settingsTab) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case done[teal.Retention]:
		accept(t, msg, &t.retention)
	case done[teal.VaultSettings]:
		accept(t, msg, &t.vault)
	case done[teal.ActionSettings]:
		accept(t, msg, &t.actions)
	case done[teal.Language]:
		accept(t, msg, &t.language)
	case tea.KeyMsg:
		return t.key(msg)
	}
	return nil
}

func (t *settingsTab) key(msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	if t.editing {
		switch k {
		case "esc":
			t.editing = false
			t.input.Blur()
			return nil
		case "enter":
			t.editing = false
			t.input.Blur()
			return t.saveTyped(settingRow(t.cur.pos), t.input.Value())
		}
		var cmd tea.Cmd
		t.input, cmd = t.input.Update(msg)
		return cmd
	}
	if t.saving {
		return nil
	}
	switch k {
	case "up", "k":
		t.cur.move(-1, int(rowCount))
	case "down", "j":
		t.cur.move(1, int(rowCount))
	case "r":
		t.notice = ""
		return t.load()
	case "enter", " ":
		return t.change(settingRow(t.cur.pos), 0)
	case "left", "h":
		return t.change(settingRow(t.cur.pos), -1)
	case "right", "l":
		return t.change(settingRow(t.cur.pos), 1)
	}
	return nil
}

// change acts on a row: a toggle flips, a cycle moves by dir (enter moves
// forward), and a value opens the input.
func (t *settingsTab) change(row settingRow, dir int) tea.Cmd {
	t.notice = ""
	t.err = nil
	switch row {
	case rowMode:
		if t.retention == nil {
			return nil
		}
		mode := teal.RetentionRotate
		if t.retention.Mode == teal.RetentionRotate {
			mode = teal.RetentionKeep
		}
		return t.saveRetention(teal.RetentionUpdateRequest{Mode: &mode})
	case rowInChat:
		if t.retention == nil || dir != 0 {
			return nil
		}
		v := !t.retention.InChat
		return t.saveRetention(teal.RetentionUpdateRequest{InChat: &v})
	case rowPrefix:
		if t.actions == nil {
			return nil
		}
		if dir == 0 {
			dir = 1
		}
		i := indexOf(actionPrefixes, t.actions.Prefix)
		next := actionPrefixes[(i+dir+len(actionPrefixes))%len(actionPrefixes)]
		return save(t, func(ctx context.Context, c *teal.Client) (teal.ActionSettings, *teal.Meta, error) {
			return c.Settings.UpdateActions(ctx, teal.ActionSettingsUpdateRequest{Prefix: next})
		})
	case rowLanguage:
		if t.language == nil || len(t.language.Supported) == 0 {
			return nil
		}
		if dir == 0 {
			dir = 1
		}
		// The cycle runs through "auto" and then every supported code.
		options := append([]string{""}, t.language.Supported...)
		i := indexOf(options, t.language.Chosen)
		next := options[(i+dir+len(options))%len(options)]
		return save(t, func(ctx context.Context, c *teal.Client) (teal.Language, *teal.Meta, error) {
			return c.Settings.UpdateLanguage(ctx, teal.LanguageUpdateRequest{Language: next})
		})
	case rowTTL, rowRevealTTL:
		if dir != 0 {
			return nil
		}
		t.editing = true
		t.input.Reset()
		t.input.Placeholder = "30d, 12h, 90s or off"
		if row == rowRevealTTL {
			t.input.Placeholder = `30s, 5m or "default"`
		}
		return t.input.Focus()
	}
	return nil
}

func (t *settingsTab) saveTyped(row settingRow, value string) tea.Cmd {
	v := strings.ToLower(strings.TrimSpace(value))
	switch row {
	case rowTTL:
		secs, err := query.Seconds(v)
		if err != nil {
			t.err = err
			return nil
		}
		return t.saveRetention(teal.RetentionUpdateRequest{TTLSeconds: &secs})
	case rowRevealTTL:
		var secs int64
		if v != "default" {
			var err error
			if secs, err = query.Seconds(v); err != nil {
				t.err = err
				return nil
			}
			if secs == 0 {
				t.err = fmt.Errorf(`the timer cannot be switched off; type "default" for the plan's own`)
				return nil
			}
		}
		return save(t, func(ctx context.Context, c *teal.Client) (teal.VaultSettings, *teal.Meta, error) {
			return c.Settings.UpdateVault(ctx, teal.VaultSettingsUpdateRequest{RevealTTLSeconds: int(secs)})
		})
	}
	return nil
}

func (t *settingsTab) saveRetention(req teal.RetentionUpdateRequest) tea.Cmd {
	return save(t, func(ctx context.Context, c *teal.Client) (teal.Retention, *teal.Meta, error) {
		return c.Settings.UpdateRetention(ctx, req)
	})
}

// save writes one setting and takes back what is in force after it.
func save[T any](t *settingsTab, fn func(context.Context, *teal.Client) (T, *teal.Meta, error)) tea.Cmd {
	t.saving = true
	return call(t.be, "settings-save", 0, fn)
}

func (t *settingsTab) view(width, height int) string {
	inner := width - 4
	var b strings.Builder
	if t.retention == nil && t.vault == nil && t.actions == nil && t.language == nil && t.err == nil {
		return pane("Settings", loading("settings"), width, height, true)
	}
	labelW := 24
	for i := settingRow(0); i < rowCount; i++ {
		label, value, locked := t.row(i)
		// A blank line between the groups: retention, the vault,
		// shortcuts, language.
		if i == rowRevealTTL || i == rowPrefix || i == rowLanguage {
			b.WriteString("\n")
		}
		cell := output.Truncate(output.OneLine(value), max(inner-labelW-2, 4))
		if locked {
			cell += styleFaint.Render("  (the plan does not open this)")
		}
		text := label + strings.Repeat(" ", max(labelW-output.Width(label), 1)) + cell
		if t.editing && int(i) == t.cur.pos {
			text = label + strings.Repeat(" ", max(labelW-output.Width(label), 1)) + t.input.View()
			b.WriteString(text + "\n")
			continue
		}
		b.WriteString(line(text, inner, int(i) == t.cur.pos) + "\n")
	}
	b.WriteString("\n")
	switch {
	case t.saving:
		b.WriteString(styleMuted.Render("Saving…"))
	case t.err != nil:
		b.WriteString(errorView(t.err, inner))
	case t.notice != "":
		b.WriteString(styleOK.Render(t.notice))
	default:
		b.WriteString(styleMuted.Render(wrapText(t.hint(settingRow(t.cur.pos)), inner)))
	}
	return pane("Settings", b.String(), width, height, true)
}

// row is what one setting shows: its label, its value, and whether the
// plan keeps it shut.
func (t *settingsTab) row(r settingRow) (label, value string, locked bool) {
	const unknown = "…"
	switch r {
	case rowMode:
		if t.retention == nil {
			return "Full archive", unknown, false
		}
		v := "keep — refuse the newest message"
		if t.retention.Mode == teal.RetentionRotate {
			v = "rotate — drop the oldest message"
		}
		return "Full archive", v, false
	case rowTTL:
		if t.retention == nil {
			return "Keep messages for", unknown, false
		}
		return "Keep messages for", output.Seconds(t.retention.TTLSeconds), !t.retention.CanTTL
	case rowInChat:
		if t.retention == nil {
			return "Delete in chat too", unknown, false
		}
		return "Delete in chat too", output.Bool(t.retention.InChat), !t.retention.CanInChat
	case rowRevealTTL:
		if t.vault == nil {
			return "Revealed secret stays", unknown, false
		}
		return "Revealed secret stays", output.Seconds(int64(t.vault.RevealTTLSeconds)), !t.vault.CanRevealTTL
	case rowPrefix:
		if t.actions == nil {
			return "Shortcut prefix", unknown, false
		}
		return "Shortcut prefix", t.actions.Prefix + "   " + styleFaint.Render(strings.Join(actionPrefixes, " ")), !t.actions.CanUse
	case rowLanguage:
		if t.language == nil {
			return "Language", unknown, false
		}
		v := output.Or(t.language.Effective)
		if t.language.Chosen == "" {
			v += " (auto, follows Telegram)"
		}
		return "Language", v, false
	}
	return "", "", false
}

func (t *settingsTab) hint(r settingRow) string {
	switch r {
	case rowMode:
		return "What a full archive does with a new message. Enter switches."
	case rowTTL:
		return "Messages older than this leave the archive whether or not it is full. Enter to type a span, or off."
	case rowInChat:
		return "Whether a message leaving the archive is deleted from Telegram too. Enter switches."
	case rowRevealTTL:
		return "How long a secret revealed in Telegram stays in the chat before the bot takes it back. Enter to type a span."
	case rowPrefix:
		return "The character a shortcut is typed behind. ←/→ or enter cycle through the ones allowed."
	case rowLanguage:
		return "The language the bot speaks. ←/→ or enter cycle through auto and the supported languages."
	}
	return ""
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return 0
}

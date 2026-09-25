package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/x-chunk/teal"

	"github.com/x-chunk/celadon/internal/output"
)

// vaultOp is one thing the vault can be asked to do, and the fields it
// needs.
type vaultOp struct {
	name   string
	desc   string
	inputs []vaultInput
	danger bool
}

type vaultInput struct {
	label   string
	secret  bool
	confirm int // index of the input this one must repeat, or -1
}

const (
	opStore = iota
	opReveal
	opRename
	opCodes
	opRecover
	opDelete
	opDeleteID
)

var vaultOps = []vaultOp{
	opStore: {name: "Store", desc: "Encrypt a secret under a new passphrase. Recovery codes are shown once.",
		inputs: []vaultInput{{"Passphrase", true, -1}, {"Repeat passphrase", true, 0}, {"Secret", true, -1}}},
	opReveal: {name: "Reveal", desc: "Decrypt the secret a passphrase opens.",
		inputs: []vaultInput{{"Passphrase", true, -1}}},
	opRename: {name: "Rename", desc: "Move an entry to a new passphrase. The secret is not re-encrypted.",
		inputs: []vaultInput{{"Current passphrase", true, -1}, {"New passphrase", true, -1}, {"Repeat new passphrase", true, 1}}},
	opCodes: {name: "New codes", desc: "Replace every recovery code with a fresh set. The old ones stop working at once.",
		inputs: []vaultInput{{"Passphrase", true, -1}}},
	opRecover: {name: "Recover", desc: "Open an entry with a one-time recovery code. The code is spent, the others are replaced, and the owner is alerted in Telegram. Leave the new passphrase empty to keep the current one.",
		inputs: []vaultInput{{"Recovery code", true, -1}, {"New passphrase (optional)", true, -1}}},
	opDelete: {name: "Delete", desc: "Destroy the entry a passphrase opens. It cannot be recovered.", danger: true,
		inputs: []vaultInput{{"Passphrase", true, -1}}},
	opDeleteID: {name: "Delete by id", desc: "Destroy an entry by the id it was stored under. It cannot be recovered.", danger: true,
		inputs: []vaultInput{{"Entry id", false, -1}}},
}

// vaultTab runs the vault's operations. Everything typed here is masked,
// and whatever the vault hands back — a plaintext, recovery codes — is
// dropped from memory the moment the tab is left.
type vaultTab struct {
	be *backend

	menu    selection
	editing bool
	inputs  []textinput.Model
	focus   int
	confirm bool

	busy   bool
	seq    int
	err    error
	result *vaultResult
}

// vaultResult is what the last operation handed back.
type vaultResult struct {
	title     string
	plaintext string
	codes     []string
	note      string
}

func newVaultTab(be *backend) *vaultTab { return &vaultTab{be: be} }

func (t *vaultTab) title() string   { return "Vault" }
func (t *vaultTab) capturing() bool { return t.editing || t.confirm }

func (t *vaultTab) keys() []keyHelp {
	switch {
	case t.confirm:
		return []keyHelp{{"y", "destroy"}, {"n/esc", "keep"}}
	case t.editing:
		return []keyHelp{{"enter", "next / run"}, {"tab", "next field"}, {"esc", "cancel"}}
	case t.result != nil:
		return []keyHelp{{"x", "clear"}, {"enter", "run another"}}
	}
	return []keyHelp{{"↑/↓", "choose"}, {"enter", "start"}}
}

func (t *vaultTab) init() tea.Cmd { return nil }

// leave forgets everything the vault handed back and everything typed.
func (t *vaultTab) leave() {
	t.clear()
	t.editing, t.confirm = false, false
	t.seq++ // an answer still in flight is not shown on return
	t.busy = false
}

func (t *vaultTab) clear() {
	t.result = nil
	t.err = nil
	for i := range t.inputs {
		t.inputs[i].Reset()
	}
	t.inputs = nil
}

func (t *vaultTab) start() tea.Cmd {
	t.clear()
	op := vaultOps[t.menu.pos]
	t.inputs = make([]textinput.Model, len(op.inputs))
	for i, in := range op.inputs {
		ti := newInput()
		ti.Prompt = ""
		ti.CharLimit = 4096
		if in.secret {
			ti.EchoMode = textinput.EchoPassword
			ti.EchoCharacter = '•'
		}
		t.inputs[i] = ti
	}
	t.editing, t.focus = true, 0
	return t.inputs[0].Focus()
}

func (t *vaultTab) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case done[teal.RecoveryCodes]:
		if msg.seq == t.seq && t.busy {
			t.busy = false
			if t.fail(msg.err) {
				return nil
			}
			title := "Stored"
			if msg.op == "vault-codes" {
				title = "New recovery codes issued; the old ones no longer work"
			}
			t.result = &vaultResult{title: title, codes: msg.val.RecoveryCodes}
		}
	case done[teal.VaultSecret]:
		if msg.seq == t.seq && t.busy {
			t.busy = false
			if t.fail(msg.err) {
				return nil
			}
			t.result = &vaultResult{title: "Revealed", plaintext: msg.val.Plaintext}
		}
	case done[teal.VaultRecovered]:
		if msg.seq == t.seq && t.busy {
			t.busy = false
			if t.fail(msg.err) {
				return nil
			}
			title := "Recovered"
			if msg.val.Rekeyed {
				title += "; the entry now opens with the new passphrase"
			}
			t.result = &vaultResult{title: title, plaintext: msg.val.Plaintext, codes: msg.val.RecoveryCodes,
				note: fmt.Sprintf("%d old codes were revoked.", msg.val.Revoked)}
		}
	case done[ack]:
		if strings.HasPrefix(msg.op, "vault-") && msg.seq == t.seq && t.busy {
			t.busy = false
			if t.fail(msg.err) {
				return nil
			}
			title := "Destroyed the entry"
			if msg.op == "vault-rename" {
				title = "The entry now opens with the new passphrase"
			}
			t.result = &vaultResult{title: title}
		}
	case tea.KeyMsg:
		return t.key(msg)
	}
	return nil
}

// fail records a refusal, and wipes what was typed so it is not left on the
// form. A not_found from the vault is spelled out, since it means a wrong
// passphrase as often as a missing entry.
func (t *vaultTab) fail(err error) bool {
	if err == nil {
		return false
	}
	if teal.IsCode(err, teal.CodeNotFound) {
		err = errors.New("nothing opens with that: a wrong passphrase or code and a missing entry look the same")
	}
	t.err = err
	for i := range t.inputs {
		t.inputs[i].Reset()
	}
	return true
}

func (t *vaultTab) key(msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	if t.confirm {
		switch k {
		case "y", "Y":
			t.confirm = false
			return t.run()
		case "n", "N", "esc":
			t.confirm = false
		}
		return nil
	}
	if t.editing {
		switch k {
		case "esc":
			t.editing = false
			t.clear()
			return nil
		case "tab", "down":
			return t.focusInput(t.focus + 1)
		case "shift+tab", "up":
			return t.focusInput(t.focus - 1)
		case "enter":
			if t.focus < len(t.inputs)-1 {
				return t.focusInput(t.focus + 1)
			}
			return t.submit()
		}
		var cmd tea.Cmd
		t.inputs[t.focus], cmd = t.inputs[t.focus].Update(msg)
		return cmd
	}
	switch k {
	case "up", "k":
		t.menu.move(-1, len(vaultOps))
	case "down", "j":
		t.menu.move(1, len(vaultOps))
	case "x", "esc":
		t.clear()
	case "enter":
		if !t.busy {
			return t.start()
		}
	}
	return nil
}

func (t *vaultTab) focusInput(i int) tea.Cmd {
	i = min(max(i, 0), len(t.inputs)-1)
	t.inputs[t.focus].Blur()
	t.focus = i
	return t.inputs[i].Focus()
}

// submit checks the form and runs the operation, after a confirmation for
// the ones that destroy.
func (t *vaultTab) submit() tea.Cmd {
	op := vaultOps[t.menu.pos]
	for i, in := range op.inputs {
		v := t.inputs[i].Value()
		if v == "" && !strings.Contains(in.label, "optional") {
			t.err = fmt.Errorf("%s is empty", strings.ToLower(in.label))
			return t.focusInput(i)
		}
		if in.confirm >= 0 && v != t.inputs[in.confirm].Value() {
			t.err = fmt.Errorf("the passphrases do not match")
			t.inputs[i].Reset()
			return t.focusInput(i)
		}
	}
	if t.menu.pos == opDeleteID {
		if _, err := strconv.ParseInt(strings.TrimSpace(t.inputs[0].Value()), 10, 64); err != nil {
			t.err = fmt.Errorf("the entry id is a number")
			return nil
		}
	}
	if t.menu.pos == opRename && t.inputs[0].Value() == t.inputs[1].Value() {
		t.err = fmt.Errorf("the new passphrase is the current one")
		return nil
	}
	t.err = nil
	if op.danger {
		t.confirm = true
		return nil
	}
	return t.run()
}

// run sends the operation. The values are copied out of the form and the
// form is wiped before the call leaves, so nothing typed outlives it.
func (t *vaultTab) run() tea.Cmd {
	v := make([]string, len(t.inputs))
	for i := range t.inputs {
		v[i] = t.inputs[i].Value()
		t.inputs[i].Reset()
	}
	t.editing = false
	t.busy = true
	t.seq++
	seq := t.seq
	switch t.menu.pos {
	case opStore:
		return call(t.be, "vault-store", seq, func(ctx context.Context, c *teal.Client) (teal.RecoveryCodes, *teal.Meta, error) {
			return c.Vault.Store(ctx, teal.VaultStoreRequest{Passphrase: v[0], Plaintext: v[2]})
		})
	case opReveal:
		return call(t.be, "vault-reveal", seq, func(ctx context.Context, c *teal.Client) (teal.VaultSecret, *teal.Meta, error) {
			return c.Vault.Reveal(ctx, teal.VaultRevealRequest{Passphrase: v[0]})
		})
	case opRename:
		return callAck(t.be, "vault-rename", seq, func(ctx context.Context, c *teal.Client) (*teal.Meta, error) {
			return c.Vault.Rename(ctx, teal.VaultRenameRequest{Passphrase: v[0], NewPassphrase: v[1]})
		})
	case opCodes:
		return call(t.be, "vault-codes", seq, func(ctx context.Context, c *teal.Client) (teal.RecoveryCodes, *teal.Meta, error) {
			return c.Vault.ReissueCodes(ctx, teal.VaultCodesRequest{Passphrase: v[0]})
		})
	case opRecover:
		return call(t.be, "vault-recover", seq, func(ctx context.Context, c *teal.Client) (teal.VaultRecovered, *teal.Meta, error) {
			return c.Vault.Recover(ctx, teal.VaultRecoverRequest{Code: strings.TrimSpace(v[0]), NewPassphrase: v[1]})
		})
	case opDelete:
		return callAck(t.be, "vault-delete", seq, func(ctx context.Context, c *teal.Client) (*teal.Meta, error) {
			return c.Vault.Delete(ctx, teal.VaultDeleteRequest{Passphrase: v[0]})
		})
	case opDeleteID:
		id, _ := strconv.ParseInt(strings.TrimSpace(v[0]), 10, 64)
		return callAck(t.be, "vault-delete", seq, func(ctx context.Context, c *teal.Client) (*teal.Meta, error) {
			return c.Vault.DeleteByID(ctx, id)
		})
	}
	t.busy = false
	return nil
}

func (t *vaultTab) view(width, height int) string {
	leftW := 24
	rightW := width - leftW
	var menu strings.Builder
	for i, op := range vaultOps {
		menu.WriteString(line(op.name, leftW-4, i == t.menu.pos && !t.editing) + "\n")
	}
	left := pane("Vault", strings.TrimRight(menu.String(), "\n"), leftW, height, !t.editing)
	right := pane(vaultOps[t.menu.pos].name, t.detail(rightW-4, height-3), rightW, height, t.editing)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (t *vaultTab) detail(width, height int) string {
	op := vaultOps[t.menu.pos]
	var b strings.Builder
	b.WriteString(styleMuted.Render(wrapText(op.desc, width)) + "\n\n")

	if t.editing || t.confirm {
		for i, in := range op.inputs {
			if i < len(t.inputs) {
				t.inputs[i].Width = max(width-26, 8)
				label := in.label + strings.Repeat(" ", max(24-output.Width(in.label), 1))
				b.WriteString(styleLabel.Render(label) + t.inputs[i].View() + "\n")
			}
		}
		if t.menu.pos == opStore {
			b.WriteString(styleFaint.Render(wrapText("A secret of more than one line is stored with `celadon vault store --secret-file`.", width)) + "\n")
		}
	}
	switch {
	case t.confirm:
		b.WriteString("\n" + styleWarn.Render("Destroy this entry? It cannot be recovered. y/n"))
	case t.busy:
		b.WriteString(loading("the vault"))
	case t.err != nil:
		b.WriteString("\n" + errorView(t.err, width))
	case t.result != nil:
		b.WriteString(t.resultView(width))
	case !t.editing:
		b.WriteString(styleMuted.Render("Press enter to start. Everything typed is masked, and whatever the vault shows is forgotten when you leave this tab."))
	}
	return clip(b.String(), height)
}

func (t *vaultTab) resultView(width int) string {
	r := t.result
	var b strings.Builder
	b.WriteString(styleOK.Render("✓ "+r.title) + "\n")
	if r.plaintext != "" {
		b.WriteString("\n" + styleHeading.Render("Secret") + "\n" + wrapText(r.plaintext, width) + "\n")
	}
	if len(r.codes) > 0 {
		b.WriteString("\n" + styleWarn.Render("Recovery codes — each opens the entry once. They will not be shown again.") + "\n")
		for _, c := range r.codes {
			b.WriteString("  " + output.Sanitize(c) + "\n")
		}
	}
	if r.note != "" {
		b.WriteString("\n" + styleMuted.Render(r.note) + "\n")
	}
	b.WriteString("\n" + styleFaint.Render("x clears this; leaving the tab does too."))
	return b.String()
}

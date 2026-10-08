package tui

import (
	"net/http"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestVaultRevealIsForgottenOnLeave(t *testing.T) {
	d := newDriver(t)
	d.api.ok("POST", "/v1/vault/entries/reveal", map[string]any{"plaintext": "hunter2-the-secret"})

	d.open("Vault")
	d.key(tea.KeyDown) // Reveal
	d.key(tea.KeyEnter)
	d.keys("correct horse")
	if strings.Contains(d.view(), "correct horse") {
		t.Error("the passphrase is visible while typed")
	}
	d.key(tea.KeyEnter)
	if b := d.body("POST", "/v1/vault/entries/reveal"); b["passphrase"] != "correct horse" {
		t.Errorf("reveal body = %v", b)
	}
	d.wantView("hunter2-the-secret")

	d.open("Overview")
	d.open("Vault")
	if strings.Contains(d.view(), "hunter2-the-secret") {
		t.Error("the secret was still on screen after leaving the tab")
	}
}

func TestVaultStoreChecksTheRepeatAndShowsCodes(t *testing.T) {
	d := newDriver(t)
	d.api.ok("POST", "/v1/vault/entries", map[string]any{"entry_id": 12, "recovery_codes": []string{"AAAA-1111", "BBBB-2222"}})
	d.open("Vault")
	d.key(tea.KeyEnter)
	d.keys("one")
	d.key(tea.KeyEnter)
	d.keys("two")
	d.key(tea.KeyEnter)
	d.keys("secret")
	d.key(tea.KeyEnter)
	if len(d.api.called("POST", "/v1/vault/entries")) != 0 {
		t.Fatal("mismatched passphrases were sent")
	}
	d.wantView("do not match")

	d.keys("one")
	d.key(tea.KeyEnter)
	d.key(tea.KeyEnter)
	if b := d.body("POST", "/v1/vault/entries"); b["passphrase"] != "one" || b["plaintext"] != "secret" {
		t.Errorf("store body = %v", b)
	}
	d.wantView("AAAA-1111", "will not be shown again", "Stored as entry #12")
}

func TestVaultRecoverMovesTheEntryToANewPassphrase(t *testing.T) {
	d := newDriver(t)
	d.api.ok("POST", "/v1/vault/entries/recover", map[string]any{"plaintext": "s3cret", "rekeyed": true, "recovery_codes": []string{"N1"}, "revoked": 4})
	d.open("Vault")
	for range opRecover {
		d.key(tea.KeyDown)
	}
	d.key(tea.KeyEnter)
	d.keys("CODE-1")
	d.key(tea.KeyEnter)
	d.key(tea.KeyEnter)
	d.key(tea.KeyEnter) // submitted from the last field, with no new passphrase
	if len(d.api.called("POST", "/v1/vault/entries/recover")) != 0 {
		t.Fatal("a code without a new passphrase was spent")
	}
	d.wantView("new passphrase is empty")
	d.keys("fresh")
	d.key(tea.KeyEnter)
	d.keys("fresh")
	d.key(tea.KeyEnter)
	if b := d.body("POST", "/v1/vault/entries/recover"); b["code"] != "CODE-1" || b["new_passphrase"] != "fresh" {
		t.Errorf("recover body = %v", b)
	}
	d.wantView("the entry now opens with the new passphrase", "s3cret", "N1", "4 old codes were revoked")
}

func TestVaultDeleteAsksFirst(t *testing.T) {
	d := newDriver(t)
	d.api.ok("POST", "/v1/vault/entries/delete", map[string]any{})
	d.open("Vault")
	for range opDelete {
		d.key(tea.KeyDown)
	}
	d.key(tea.KeyEnter)
	d.keys("pass")
	d.key(tea.KeyEnter)
	d.wantView("Destroy this entry?")
	d.keys("n")
	if len(d.api.called("POST", "/v1/vault/entries/delete")) != 0 {
		t.Fatal("deleted without a yes")
	}
	d.key(tea.KeyEnter)
	d.keys("pass")
	d.key(tea.KeyEnter)
	d.keys("y")
	if len(d.api.called("POST", "/v1/vault/entries/delete")) != 1 {
		t.Fatal("y did not delete")
	}
	d.wantView("Destroyed the entry")
}

func TestVaultExplainsAWrongPassphrase(t *testing.T) {
	d := newDriver(t)
	d.api.on("POST", "/v1/vault/entries/reveal", func(w http.ResponseWriter, _ *http.Request) {
		refuse(w, http.StatusNotFound, "not_found", "not found")
	})
	d.open("Vault")
	d.key(tea.KeyDown)
	d.key(tea.KeyEnter)
	d.keys("wrong")
	d.key(tea.KeyEnter)
	d.wantView("a wrong passphrase or code and a missing entry look the same")
}

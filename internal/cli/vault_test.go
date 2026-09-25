package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/x-chunk/celadon/internal/iostreams"
)

// ── vault ───────────────────────────────────────────────────────────────

func TestVaultStoreReadsEverythingFromStdin(t *testing.T) {
	h := newHarness(t).loggedIn()
	h.api.ok("POST", "/v1/vault/entries", map[string]any{"recovery_codes": []string{"AAAA-1111", "BBBB-2222"}})

	r := h.run("correct horse\nthe secret\nsecond line", "vault", "store", "--passphrase-stdin").wantCode(t, ExitOK)
	body := decodeBody(t, h.api.last().Body)
	if body["passphrase"] != "correct horse" || body["plaintext"] != "the secret\nsecond line" {
		t.Errorf("body = %v", body)
	}
	if r.stdout != "AAAA-1111\nBBBB-2222\n" {
		t.Errorf("stdout = %q", r.stdout)
	}
	if !strings.Contains(r.stderr, "will not be shown again") {
		t.Errorf("stderr = %q", r.stderr)
	}
}

func TestVaultStoreConfirmsAPromptedPassphrase(t *testing.T) {
	h := newHarness(t).loggedIn()
	h.api.ok("POST", "/v1/vault/entries", map[string]any{"recovery_codes": []string{"X"}})
	answers := []string{"one", "two"}
	h.runWith(func(s *iostreams.Streams) {
		s.InTTY, s.ErrTTY = true, true
		s.SetSecretReader(func() (string, error) {
			a := answers[0]
			answers = answers[1:]
			return a, nil
		})
	}, "", "vault", "store").wantCode(t, ExitUsage)
	if h.api.calls() != 0 {
		t.Error("mismatched passphrases reached the API")
	}
}

func TestVaultRevealWritesThePlaintextExactly(t *testing.T) {
	h := newHarness(t).loggedIn()
	h.api.ok("POST", "/v1/vault/entries/reveal", map[string]any{"plaintext": "-----BEGIN KEY-----\nabc\n"})
	r := h.run("pass\n", "vault", "reveal", "--passphrase-stdin").wantCode(t, ExitOK)
	if r.stdout != "-----BEGIN KEY-----\nabc\n" {
		t.Errorf("stdout = %q", r.stdout)
	}
	if decodeBody(t, h.api.last().Body)["passphrase"] != "pass" {
		t.Error("the passphrase was not sent in the body")
	}
	if strings.Contains(h.api.last().Path+h.api.last().Query, "pass") {
		t.Error("the passphrase reached the url")
	}
}

func TestVaultRevealExplainsAWrongPassphrase(t *testing.T) {
	h := newHarness(t).loggedIn()
	h.api.on("POST", "/v1/vault/entries/reveal", func(w http.ResponseWriter, _ *http.Request) {
		refuse(w, http.StatusNotFound, "not_found", "not found")
	})
	r := h.run("wrong\n", "vault", "reveal", "--passphrase-stdin").wantCode(t, ExitError)
	if !strings.Contains(r.stderr, "no entry opens with that passphrase") {
		t.Errorf("stderr = %q", r.stderr)
	}
}

func TestVaultRenameRecoverAndDelete(t *testing.T) {
	h := newHarness(t).loggedIn()
	h.api.ok("POST", "/v1/vault/entries/rename", map[string]any{})
	h.api.ok("POST", "/v1/vault/entries/recover", map[string]any{"plaintext": "s", "rekeyed": true, "recovery_codes": []string{"N1"}, "revoked": 3})
	h.api.ok("POST", "/v1/vault/entries/delete", map[string]any{})
	h.api.ok("DELETE", "/v1/vault/entries/12", map[string]any{})

	h.run("old\nnew\n", "vault", "rename", "--passphrase-stdin").wantCode(t, ExitOK)
	if b := decodeBody(t, h.api.last().Body); b["passphrase"] != "old" || b["new_passphrase"] != "new" {
		t.Errorf("rename body = %v", b)
	}
	h.run("same\nsame\n", "vault", "rename", "--passphrase-stdin").wantCode(t, ExitUsage)

	r := h.run(" CODE-1 \nfresh\n", "vault", "recover", "--code-stdin", "--rekey").wantCode(t, ExitOK)
	if b := decodeBody(t, h.api.last().Body); b["code"] != "CODE-1" || b["new_passphrase"] != "fresh" {
		t.Errorf("recover body = %v", b)
	}
	if !strings.Contains(r.stdout, "N1") || !strings.Contains(r.stderr, "3 old codes were revoked") {
		t.Errorf("recover stdout = %q, stderr = %q", r.stdout, r.stderr)
	}

	h.run("pass\n", "vault", "delete", "--passphrase-stdin").wantCode(t, ExitUsage)
	h.run("pass\n", "vault", "delete", "--passphrase-stdin", "--yes").wantCode(t, ExitOK)
	h.run("", "vault", "delete", "--id", "12", "-y").wantCode(t, ExitOK)
	if h.api.last().Method != "DELETE" {
		t.Errorf("delete by id went to %s %s", h.api.last().Method, h.api.last().Path)
	}
}

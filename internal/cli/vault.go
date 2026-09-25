package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/x-chunk/teal"
)

// maxSecret bounds a secret read from a file or standard input. The API has
// a ceiling of its own; this one only stops a wrong file from being read
// into memory whole.
const maxSecret = 1 << 20

const vaultLong = `Store and open secrets in the Aether vault.

A passphrase is never stored anywhere: a hash of it addresses the entry, and
the key that decrypts it is derived from it. Nobody, Aether included, can open
an entry without its passphrase or one of its one-time recovery codes. A
passphrase that addresses nothing and a wrong one get the same answer.

Passphrases and codes are read from a hidden prompt, or from the first line of
standard input with --passphrase-stdin / --code-stdin. They are never taken as
arguments, which every user of the machine can read through ps and which the
shell writes into its history.`

func newVaultCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "vault",
		Short:   "Store and open secrets behind a passphrase",
		Long:    vaultLong,
		GroupID: groupAccount,
	}
	cmd.AddCommand(
		newVaultStoreCmd(env),
		newVaultRevealCmd(env),
		newVaultRenameCmd(env),
		newVaultCodesCmd(env),
		newVaultRecoverCmd(env),
		newVaultDeleteCmd(env),
	)
	return cmd
}

// secretInput reads what a vault command needs from the person or from
// standard input, and never from an argument.
type secretInput struct {
	env   *Env
	stdin bool
}

// read reads one secret line. With confirm, a hidden prompt asks twice, so a
// typo cannot lock an entry behind a passphrase nobody knows.
func (s secretInput) read(label string, confirmIt bool) (string, error) {
	var v string
	if s.stdin {
		line, err := s.env.IO.ReadLine()
		if err != nil {
			return "", fmt.Errorf("reading the %s from standard input: %w", strings.ToLower(label), err)
		}
		v = line
	} else {
		if !s.env.IO.CanPrompt() {
			return "", usageError(fmt.Errorf("no terminal to ask for the %s on: pass it on standard input", strings.ToLower(label)))
		}
		var err error
		if v, err = s.env.IO.Secret(label + ": "); err != nil {
			return "", err
		}
		if confirmIt {
			again, err := s.env.IO.Secret("Repeat the " + strings.ToLower(label) + ": ")
			if err != nil {
				return "", err
			}
			if again != v {
				return "", usageError(fmt.Errorf("the two %ss do not match", strings.ToLower(label)))
			}
		}
	}
	if v == "" {
		return "", usageError(fmt.Errorf("the %s is empty", strings.ToLower(label)))
	}
	return v, nil
}

func newVaultStoreCmd(env *Env) *cobra.Command {
	var (
		passStdin  bool
		secretFile string
	)
	cmd := &cobra.Command{
		Use:   "store",
		Short: "Encrypt a secret under a new passphrase",
		Long: `Encrypt a secret under a passphrase and print the recovery codes issued
with it. The codes are shown now and never again: keep them somewhere other
than beside the passphrase.

The secret is read from --secret-file (- for standard input), from what is
left of standard input after the passphrase line, or from a hidden prompt.`,
		Example: `  celadon vault store
  celadon vault store --secret-file id_ed25519
  printf '%s\n%s' "$PASS" "$SECRET" | celadon vault store --passphrase-stdin`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in := secretInput{env: env, stdin: passStdin}
			if passStdin && secretFile == "" && env.IO.InTTY {
				return usageError(errors.New("--passphrase-stdin needs the secret from --secret-file or piped after the passphrase"))
			}
			pass, err := in.read("Passphrase", true)
			if err != nil {
				return err
			}
			secret, err := readSecret(env, secretFile, passStdin)
			if err != nil {
				return err
			}
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			codes, meta, err := c.Vault.Store(ctx, teal.VaultStoreRequest{Passphrase: pass, Plaintext: secret})
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(codes, func() error {
				p.Success("Stored the secret")
				printCodes(env, codes.RecoveryCodes)
				p.Meta(meta)
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&passStdin, "passphrase-stdin", false, "read the passphrase from the first line of standard input")
	cmd.Flags().StringVar(&secretFile, "secret-file", "", "read the secret from this file (- for standard input)")
	return cmd
}

// readSecret reads the plaintext to store. Standard input is only read when
// nothing else was asked for from it, or when it was asked for by name.
func readSecret(env *Env, file string, passFromStdin bool) (string, error) {
	var raw []byte
	var err error
	switch {
	case file == "-":
		raw, err = env.IO.ReadAll(maxSecret)
	case file != "":
		raw, err = readFileCapped(file, maxSecret)
	case passFromStdin || !env.IO.InTTY:
		raw, err = env.IO.ReadAll(maxSecret)
	default:
		var s string
		if s, err = env.IO.Secret("Secret: "); err == nil {
			raw = []byte(s)
		}
	}
	if err != nil {
		return "", fmt.Errorf("reading the secret: %w", err)
	}
	if len(raw) == 0 {
		return "", usageError(errors.New("the secret is empty"))
	}
	return string(raw), nil
}

func readFileCapped(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, limit)
	}
	return io.ReadAll(io.LimitReader(f, limit))
}

// printCodes prints recovery codes on standard output and the warning that
// goes with them on the error stream, so a pipe captures the codes alone.
func printCodes(env *Env, codes []string) {
	p := env.Printer()
	p.Warn("Recovery codes — each opens the entry once. They will not be shown again.")
	for _, c := range codes {
		p.Println(c)
	}
}

func newVaultRevealCmd(env *Env) *cobra.Command {
	var passStdin bool
	cmd := &cobra.Command{
		Use:   "reveal",
		Short: "Decrypt the secret a passphrase opens",
		Long: `Decrypt the entry a passphrase addresses and print its plaintext.

On a terminal the plaintext is sanitized before it is printed; into a pipe or a
file it is written exactly as it was stored.`,
		Example: `  celadon vault reveal
  celadon vault reveal > id_ed25519`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pass, err := secretInput{env: env, stdin: passStdin}.read("Passphrase", false)
			if err != nil {
				return err
			}
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			sec, meta, err := c.Vault.Reveal(ctx, teal.VaultRevealRequest{Passphrase: pass})
			if err != nil {
				return notFoundAsWrongPass(err)
			}
			p := env.Printer()
			return p.Result(sec, func() error {
				writePlaintext(env, sec.Plaintext)
				p.Meta(meta)
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&passStdin, "passphrase-stdin", false, "read the passphrase from the first line of standard input")
	return cmd
}

// writePlaintext prints a secret. A terminal gets it sanitized, since a
// secret is text somebody could have planted; a pipe gets it byte for byte.
func writePlaintext(env *Env, s string) {
	if env.IO.OutTTY {
		env.Printer().Block(s)
		return
	}
	fmt.Fprint(env.IO.Out, s)
}

// notFoundAsWrongPass says what a not_found from the vault means: the API
// answers the same for a wrong passphrase as for one that opens nothing.
func notFoundAsWrongPass(err error) error {
	if teal.IsCode(err, teal.CodeNotFound) {
		return errors.New("no entry opens with that passphrase (a wrong passphrase and a missing entry look the same)")
	}
	return err
}

func newVaultRenameCmd(env *Env) *cobra.Command {
	var passStdin bool
	cmd := &cobra.Command{
		Use:   "rename",
		Short: "Move an entry to a new passphrase",
		Long: `Move an entry to a new passphrase. The secret is not re-encrypted: only the
door onto it changes. With --passphrase-stdin, the first line of standard input
is the current passphrase and the second the new one.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in := secretInput{env: env, stdin: passStdin}
			pass, err := in.read("Current passphrase", false)
			if err != nil {
				return err
			}
			next, err := in.read("New passphrase", true)
			if err != nil {
				return err
			}
			if next == pass {
				return usageError(errors.New("the new passphrase is the current one"))
			}
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			meta, err := c.Vault.Rename(ctx, teal.VaultRenameRequest{Passphrase: pass, NewPassphrase: next})
			if err != nil {
				return notFoundAsWrongPass(err)
			}
			p := env.Printer()
			return p.Result(map[string]bool{"ok": true}, func() error {
				p.Success("The entry now opens with the new passphrase")
				p.Meta(meta)
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&passStdin, "passphrase-stdin", false, "read both passphrases from standard input, one per line")
	return cmd
}

func newVaultCodesCmd(env *Env) *cobra.Command {
	var passStdin bool
	cmd := &cobra.Command{
		Use:   "codes",
		Short: "Replace an entry's recovery codes with a fresh set",
		Long: `Throw away every recovery code an entry has and issue a fresh set. Do it when
a set may have leaked: the old codes are worthless the moment this returns.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pass, err := secretInput{env: env, stdin: passStdin}.read("Passphrase", false)
			if err != nil {
				return err
			}
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			codes, meta, err := c.Vault.ReissueCodes(ctx, teal.VaultCodesRequest{Passphrase: pass})
			if err != nil {
				return notFoundAsWrongPass(err)
			}
			p := env.Printer()
			return p.Result(codes, func() error {
				p.Success("Issued new recovery codes; the old ones no longer work")
				printCodes(env, codes.RecoveryCodes)
				p.Meta(meta)
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&passStdin, "passphrase-stdin", false, "read the passphrase from the first line of standard input")
	return cmd
}

func newVaultRecoverCmd(env *Env) *cobra.Command {
	var (
		codeStdin bool
		rekey     bool
	)
	cmd := &cobra.Command{
		Use:   "recover",
		Short: "Open an entry with a one-time recovery code",
		Long: `Open an entry with one of its recovery codes and print the secret, and — with
--rekey — move it to a new passphrase on the way.

The code is spent whatever happens next, every remaining code is replaced with
the fresh set printed here, and the account's owner is alerted in Telegram.
With --code-stdin, the first line of standard input is the code and, with
--rekey, the second is the new passphrase.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in := secretInput{env: env, stdin: codeStdin}
			code, err := in.read("Recovery code", false)
			if err != nil {
				return err
			}
			req := teal.VaultRecoverRequest{Code: strings.TrimSpace(code)}
			if rekey {
				if req.NewPassphrase, err = in.read("New passphrase", true); err != nil {
					return err
				}
			}
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			rec, meta, err := c.Vault.Recover(ctx, req)
			if err != nil {
				if teal.IsCode(err, teal.CodeNotFound) {
					return errors.New("that code opens nothing: it is wrong, or it has already been spent")
				}
				return err
			}
			p := env.Printer()
			return p.Result(rec, func() error {
				if rec.Rekeyed {
					p.Success("Recovered; the entry now opens with the new passphrase")
				} else {
					p.Success("Recovered")
				}
				writePlaintext(env, rec.Plaintext)
				if env.IO.OutTTY && !strings.HasSuffix(rec.Plaintext, "\n") {
					p.Println()
				}
				p.Info("%d old codes were revoked.", rec.Revoked)
				printCodes(env, rec.RecoveryCodes)
				p.Meta(meta)
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&codeStdin, "code-stdin", false, "read the code (and a new passphrase) from standard input")
	cmd.Flags().BoolVar(&rekey, "rekey", false, "move the entry to a new passphrase")
	return cmd
}

func newVaultDeleteCmd(env *Env) *cobra.Command {
	var (
		passStdin bool
		id        string
		yes       bool
	)
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Destroy an entry",
		Long: `Destroy the entry a passphrase addresses, or the one with --id. The row is
destroyed rather than marked, which frees the passphrase to be used again.
Deleting is free on every billing mode.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var (
				entryID int64
				pass    string
				err     error
			)
			if id != "" {
				if passStdin {
					return usageError(errors.New("--id and --passphrase-stdin do not go together"))
				}
				if entryID, err = parseID("entry id", id); err != nil {
					return err
				}
			} else if pass, err = (secretInput{env: env, stdin: passStdin}).read("Passphrase", false); err != nil {
				return err
			}
			if err := confirm(env, yes, "Destroy this vault entry? It cannot be recovered."); err != nil {
				return err
			}
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			var meta *teal.Meta
			if entryID > 0 {
				meta, err = c.Vault.DeleteByID(ctx, entryID)
			} else {
				meta, err = c.Vault.Delete(ctx, teal.VaultDeleteRequest{Passphrase: pass})
			}
			if err != nil {
				return notFoundAsWrongPass(err)
			}
			p := env.Printer()
			return p.Result(map[string]bool{"ok": true}, func() error {
				p.Success("Destroyed the entry")
				p.Meta(meta)
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&passStdin, "passphrase-stdin", false, "read the passphrase from the first line of standard input")
	cmd.Flags().StringVar(&id, "id", "", "destroy the entry with this id instead")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}

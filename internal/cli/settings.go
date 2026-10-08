package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"go.xchunk.org/anvil/v2/result"
	"go.xchunk.org/teal"

	"github.com/x-chunk/celadon/internal/output"
)

// actionPrefixes is the closed set the API accepts for the shortcut prefix.
// It is only used for completion and the error message: the API is what
// refuses anything else.
var actionPrefixes = []string{".", "/", ",", "!", "$", "#", ">", "~"}

func newSettingsCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "settings",
		Short:   "Show and change the account's settings",
		GroupID: groupAccount,
		Long: `Show and change the account's settings. Run a subcommand without flags to
show that setting, and with flags to change it. Everything here is free on
every billing mode.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return showAllSettings(cmd, env)
		},
	}
	cmd.AddCommand(
		newRetentionCmd(env),
		newVaultSettingsCmd(env),
		newActionSettingsCmd(env),
		newLanguageCmd(env),
	)
	return cmd
}

type allSettings struct {
	Retention teal.Retention      `json:"retention"`
	Vault     teal.VaultSettings  `json:"vault"`
	Actions   teal.ActionSettings `json:"actions"`
	Language  teal.Language       `json:"language"`
}

func showAllSettings(cmd *cobra.Command, env *Env) error {
	c, _, err := env.Client()
	if err != nil {
		return err
	}
	ctx, cancel := env.Context(cmd, false)
	defer cancel()
	var all allSettings
	if err := take(c.Settings.Retention(ctx), &all.Retention); err != nil {
		return err
	}
	if err := take(c.Settings.Vault(ctx), &all.Vault); err != nil {
		return err
	}
	if err := take(c.Settings.Actions(ctx), &all.Actions); err != nil {
		return err
	}
	if err := take(c.Settings.Language(ctx), &all.Language); err != nil {
		return err
	}
	p := env.Printer()
	return p.Result(all, func() error {
		p.Heading("Retention")
		printRetention(env, all.Retention)
		p.Println()
		p.Heading("Vault")
		printVaultSettings(env, all.Vault)
		p.Println()
		p.Heading("Shortcuts")
		printActionSettings(env, all.Actions)
		p.Println()
		p.Heading("Language")
		printLanguage(env, all.Language)
		return nil
	})
}

// take puts the payload of a reply into dst, or answers with its refusal.
func take[T any](r reply[T], dst *T) error {
	res, err := r.Value()
	if err == nil {
		*dst = res.Data
	}
	return err
}

// refused is a reply that never left: a value the command refuses itself.
func refused[T any](err error) reply[T] { return result.Err[teal.Response[T]](err) }

// settingCmd is the shape all four settings share: without flags it reads,
// with any of them it writes, and either way it prints what is in force.
func settingCmd[T any](env *Env, use, short, long string,
	get func(context.Context, *teal.Client) reply[T],
	set func(context.Context, *teal.Client, *cobra.Command) reply[T],
	show func(*Env, T),
) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			changing := false
			cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
				if f.Changed {
					changing = true
				}
			})
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			fetch := get
			if changing {
				fetch = func(ctx context.Context, c *teal.Client) reply[T] { return set(ctx, c, cmd) }
			}
			r, err := fetch(ctx, c).Value()
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(r.Data, func() error {
				if changing {
					p.Success("Saved")
				}
				show(env, r.Data)
				p.Meta(r.Meta)
				return nil
			})
		},
	}
}

func newRetentionCmd(env *Env) *cobra.Command {
	var (
		mode   string
		ttl    string
		inChat bool
	)
	cmd := settingCmd(env, "retention",
		"Show or change what happens to the archive over time",
		`Show or change the retention policy: what a full archive does with a new
message, how long a message stays at all, and whether a message leaving the
archive is deleted from Telegram too.

The window and the deletion in the chat are paid settings: a plan that does not
open one refuses the change rather than storing it quietly.`,
		func(ctx context.Context, c *teal.Client) reply[teal.Retention] {
			return c.Settings.Retention(ctx)
		},
		func(ctx context.Context, c *teal.Client, cmd *cobra.Command) reply[teal.Retention] {
			var req teal.RetentionUpdateRequest
			if cmd.Flags().Changed("mode") {
				m := strings.ToLower(strings.TrimSpace(mode))
				if m != teal.RetentionKeep && m != teal.RetentionRotate {
					return refused[teal.Retention](usageError(fmt.Errorf("invalid --mode %q: use keep or rotate", mode)))
				}
				req.Mode = &m
			}
			if cmd.Flags().Changed("ttl") {
				secs, err := parseSeconds(ttl)
				if err != nil {
					return refused[teal.Retention](err)
				}
				req.TTLSeconds = &secs
			}
			if cmd.Flags().Changed("in-chat") {
				req.InChat = &inChat
			}
			return c.Settings.UpdateRetention(ctx, req)
		},
		printRetention,
	)
	cmd.Flags().StringVar(&mode, "mode", "", "what a full archive does: keep (refuse the newest) or rotate (drop the oldest)")
	cmd.Flags().StringVar(&ttl, "ttl", "", "drop messages older than this: 30d, 12h, or off")
	cmd.Flags().BoolVar(&inChat, "in-chat", false, "also delete a message from Telegram when it leaves the archive")
	cmd.Example = `  celadon settings retention
  celadon settings retention --mode rotate --ttl 90d
  celadon settings retention --ttl off --in-chat=false`
	_ = cmd.RegisterFlagCompletionFunc("mode", fixedCompletion(teal.RetentionKeep, teal.RetentionRotate))
	return cmd
}

func printRetention(env *Env, r teal.Retention) {
	mode := r.Mode
	switch r.Mode {
	case teal.RetentionKeep:
		mode = "keep — a full archive refuses the newest message"
	case teal.RetentionRotate:
		mode = "rotate — a full archive drops the oldest message"
	}
	env.Printer().Details([]output.KV{
		{Key: "Mode", Value: mode},
		{Key: "Window", Value: output.Seconds(r.TTLSeconds) + planNote(r.CanTTL)},
		{Key: "Delete in chat", Value: output.Bool(r.InChat) + planNote(r.CanInChat)},
		{Key: "Archive", Value: output.Quota(r.Archive) + " " + r.Archive.Unit},
	})
}

func planNote(open bool) string {
	if open {
		return ""
	}
	return " (the plan does not open this)"
}

func newVaultSettingsCmd(env *Env) *cobra.Command {
	var revealTTL string
	cmd := settingCmd(env, "vault",
		"Show or change how long a revealed secret stays in the chat",
		`Show or change how long a decrypted vault message stays in the Telegram chat
before the bot takes it back. "default" goes back to the plan's own timer.`,
		func(ctx context.Context, c *teal.Client) reply[teal.VaultSettings] {
			return c.Settings.Vault(ctx)
		},
		func(ctx context.Context, c *teal.Client, _ *cobra.Command) reply[teal.VaultSettings] {
			v := strings.ToLower(strings.TrimSpace(revealTTL))
			var secs int64
			if v != "default" {
				var err error
				if secs, err = parseSeconds(v); err != nil {
					return refused[teal.VaultSettings](err)
				}
				if secs == 0 {
					return refused[teal.VaultSettings](usageError(errors.New(`the timer cannot be switched off; use "default" for the plan's own`)))
				}
			}
			return c.Settings.UpdateVault(ctx, teal.VaultSettingsUpdateRequest{RevealTTLSeconds: int(secs)})
		},
		printVaultSettings,
	)
	cmd.Flags().StringVar(&revealTTL, "reveal-ttl", "", `how long a revealed secret stays: 30s, 5m, or "default"`)
	return cmd
}

func printVaultSettings(env *Env, v teal.VaultSettings) {
	env.Printer().Details([]output.KV{
		{Key: "Taken back after", Value: output.Seconds(int64(v.RevealTTLSeconds)) + planNote(v.CanRevealTTL)},
		{Key: "Taken back", Value: output.Bool(v.AutoDeletes)},
	})
}

func newActionSettingsCmd(env *Env) *cobra.Command {
	var prefix string
	cmd := settingCmd(env, "actions",
		"Show or change the character shortcuts are typed behind",
		`Show or change the prefix a shortcut is typed behind, so "kiss" fires on
".kiss". It is one of `+strings.Join(actionPrefixes, " ")+`.`,
		func(ctx context.Context, c *teal.Client) reply[teal.ActionSettings] {
			return c.Settings.Actions(ctx)
		},
		func(ctx context.Context, c *teal.Client, _ *cobra.Command) reply[teal.ActionSettings] {
			return c.Settings.UpdateActions(ctx, teal.ActionSettingsUpdateRequest{Prefix: strings.TrimSpace(prefix)})
		},
		printActionSettings,
	)
	cmd.Flags().StringVar(&prefix, "prefix", "", "the prefix: one of "+strings.Join(actionPrefixes, " "))
	_ = cmd.RegisterFlagCompletionFunc("prefix", fixedCompletion(actionPrefixes...))
	return cmd
}

func printActionSettings(env *Env, a teal.ActionSettings) {
	var families []string
	if a.CanMedia {
		families = append(families, "media")
	}
	if a.CanAdvanced {
		families = append(families, "advanced")
	}
	if a.CanExclusive {
		families = append(families, "exclusive")
	}
	env.Printer().Details([]output.KV{
		{Key: "Prefix", Value: a.Prefix},
		{Key: "Shortcuts", Value: output.Bool(a.CanUse) + " · " + output.Quota(a.Quota)},
		{Key: "Extra placeholders", Value: output.Or(strings.Join(families, ", "))},
	})
}

func newLanguageCmd(env *Env) *cobra.Command {
	var (
		set  string
		auto bool
	)
	cmd := settingCmd(env, "language",
		"Show or change the language the bot speaks",
		`Show or change the language every screen of the bot is drawn in. --auto goes
back to following the Telegram client.`,
		func(ctx context.Context, c *teal.Client) reply[teal.Language] {
			return c.Settings.Language(ctx)
		},
		func(ctx context.Context, c *teal.Client, cmd *cobra.Command) reply[teal.Language] {
			if err := exclusive(cmd, "set", "auto"); err != nil {
				return refused[teal.Language](err)
			}
			code := strings.ToLower(strings.TrimSpace(set))
			if auto {
				code = ""
			} else if code == "" {
				return refused[teal.Language](usageError(errors.New("--set needs a language code; use --auto to follow Telegram")))
			}
			return c.Settings.UpdateLanguage(ctx, teal.LanguageUpdateRequest{Language: code})
		},
		printLanguage,
	)
	cmd.Flags().StringVar(&set, "set", "", "the language code, e.g. en or ru")
	cmd.Flags().BoolVar(&auto, "auto", false, "follow the Telegram client's language")
	return cmd
}

func printLanguage(env *Env, l teal.Language) {
	chosen := l.Chosen
	if chosen == "" {
		chosen = "auto (follows Telegram)"
	}
	env.Printer().Details([]output.KV{
		{Key: "In force", Value: output.Or(l.Effective)},
		{Key: "Chosen", Value: chosen},
		{Key: "Telegram reports", Value: output.Or(l.Detected)},
		{Key: "Supported", Value: output.Or(strings.Join(l.Supported, ", "))},
	})
}

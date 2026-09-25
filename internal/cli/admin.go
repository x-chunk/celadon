package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/x-chunk/celadon/internal/admin"
	"github.com/x-chunk/celadon/internal/api"
	"github.com/x-chunk/celadon/internal/config"
	"github.com/x-chunk/celadon/internal/output"
)

func newAdminCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "admin",
		Short:   "Administer a deployment: promotions campaigns and codes",
		GroupID: groupAdmin,
		Long: `Administer a deployment through its private admin API — for now the
promotions: campaigns everybody gets, codes whoever redeems them gets, and the
reference both are written against.

The admin API is opened by the deployment's ADMIN_TOKEN, not by an application
key. Store it with ` + "`celadon admin login`" + `; it is kept in
~/.celadon/admin/<profile>, readable by you alone, beside the profile that names
the deployment. $CELADON_ADMIN_TOKEN bypasses the file, and
$CELADON_ADMIN_BASE_URL (or the profile's admin_base_url) points at the admin API
when a proxy serves it somewhere other than the Plug-In API.

Run ` + "`celadon admin tui`" + ` for the full-screen interface.`,
	}
	cmd.AddCommand(
		newAdminLoginCmd(env),
		newAdminLogoutCmd(env),
		newAdminStatusCmd(env),
		newAdminReferenceCmd(env),
		newAdminTUICmd(env),
	)
	return cmd
}

func newAdminLoginCmd(env *Env) *cobra.Command {
	var (
		withToken    bool
		skipVerify   bool
		adminBaseURL string
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Store the deployment's admin token in a profile",
		Long: `Store the deployment's ADMIN_TOKEN in a profile, after checking that it opens
the admin API (GET /api/admin/promo/reference, which changes nothing).

The token is read from a hidden prompt, or from standard input with
--with-token; never from an argument. The profile is chosen as everywhere else,
and it may hold an admin token without an application key. --base-url sets the
profile's deployment when it has none yet; --admin-base-url records a separate
address for the admin API, for a deployment whose proxy serves it elsewhere.`,
		Example: `  celadon admin login
  celadon admin login --profile ops --base-url https://aether.example.com
  celadon admin login --with-token < admin-token.txt`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := env.Store()
			if err != nil {
				return err
			}
			cfg, err := store.Load()
			if err != nil {
				return err
			}
			name := profileFor(env, cfg)
			if err := config.ValidateProfileName(name); err != nil {
				return usageError(err)
			}
			profile := cfg.Profiles[name]
			if env.baseURL != "" && profile.BaseURL == "" {
				profile.BaseURL = strings.TrimRight(env.baseURL, "/")
			}
			if adminBaseURL != "" {
				if err := config.ValidateBaseURL(adminBaseURL); err != nil {
					return usageError(err)
				}
				profile.AdminBaseURL = strings.TrimRight(adminBaseURL, "/")
			}
			if profile.BaseURL != "" {
				if err := config.ValidateBaseURL(profile.BaseURL); err != nil {
					return usageError(err)
				}
			}

			token, err := readAdminToken(env, withToken)
			if err != nil {
				return err
			}
			if err := config.ValidateAdminToken(token); err != nil {
				return usageError(err)
			}

			// Where the check goes is where the stored profile will point,
			// with --base-url on top when it was given.
			target := firstNonEmpty(env.baseURL, profile.AdminBaseURL, profile.BaseURL, admin.DefaultBaseURL)
			if !skipVerify {
				ctx, cancel := env.Context(cmd, false)
				defer cancel()
				c, err := api.NewAdmin(config.AdminResolved{BaseURL: target, Token: token},
					api.Options{Retries: env.retries, HTTPClient: env.HTTPClient})
				if err != nil {
					return err
				}
				if _, _, err := c.Promo.Reference(ctx); err != nil {
					return fmt.Errorf("checking the token against %s: %w", target, err)
				}
			}

			if err := store.SaveAdminToken(name, token); err != nil {
				return err
			}
			err = store.Update(func(c *config.Config) error {
				c.Profiles[name] = profile
				if c.DefaultProfile == "" {
					c.DefaultProfile = name
				}
				return nil
			})
			if err != nil {
				return err
			}
			p := env.Printer()
			if skipVerify {
				p.Success("Stored the admin token for %s without checking it", target)
			} else {
				p.Success("The admin API at %s accepts the token", target)
			}
			p.Info("  profile %s · token %s", name, store.AdminTokenPath(name))
			return nil
		},
	}
	cmd.Flags().BoolVar(&withToken, "with-token", false, "read the token from standard input")
	cmd.Flags().BoolVar(&skipVerify, "skip-verify", false, "store the token without checking it against the API")
	cmd.Flags().StringVar(&adminBaseURL, "admin-base-url", "", "where the admin API is, when not at the profile's base URL")
	return cmd
}

func readAdminToken(env *Env, fromStdin bool) (string, error) {
	if fromStdin {
		line, err := env.IO.ReadLine()
		if err != nil {
			return "", fmt.Errorf("reading the token from standard input: %w", err)
		}
		return strings.TrimSpace(line), nil
	}
	if !env.IO.CanPrompt() {
		return "", usageError(errors.New("no terminal to ask for the token on: pass it on standard input with --with-token"))
	}
	env.Printer().Info("Paste the deployment's ADMIN_TOKEN.")
	token, err := env.IO.Secret("Admin token: ")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(token), nil
}

func newAdminLogoutCmd(env *Env) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Delete the admin token of a profile",
		Long: `Delete the admin token of a profile from this machine. The profile and its
application key, if it has one, stay.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := env.Store()
			if err != nil {
				return err
			}
			cfg, err := store.Load()
			if err != nil {
				return err
			}
			name := profileFor(env, cfg)
			if err := config.ValidateProfileName(name); err != nil {
				return usageError(err)
			}
			if !store.HasAdminToken(name) {
				return fmt.Errorf("%w for profile %q", config.ErrNoAdminToken, name)
			}
			if err := confirm(env, yes, fmt.Sprintf("Delete the admin token of profile %q?", name)); err != nil {
				return err
			}
			if err := store.DeleteAdminToken(name); err != nil {
				return err
			}
			env.Printer().Success("Deleted the admin token of profile %s", name)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}

// adminStatus is `admin status`, and its JSON.
type adminStatus struct {
	Profile     string `json:"profile"`
	BaseURL     string `json:"base_url"`
	BaseURLFrom string `json:"base_url_source"`
	Token       string `json:"token,omitempty"`
	TokenFrom   string `json:"token_source,omitempty"`
	State       string `json:"state"`
	Detail      string `json:"detail,omitempty"`
}

func newAdminStatusCmd(env *Env) *cobra.Command {
	var offline bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show which deployment and token the admin commands use",
		Long: `Show the profile, the admin API's address and where the token comes from, and
— unless --offline — whether the deployment accepts it. Exits 4 when there is no
token or it is refused.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, rerr := env.AdminResolve()
			if rerr != nil && r.Profile == "" {
				return rerr
			}
			st := adminStatus{Profile: r.Profile, BaseURL: r.BaseURL, BaseURLFrom: r.BaseURLSource, State: stateUnchecked}
			switch {
			case errors.Is(rerr, config.ErrNoAdminToken):
				st.State = stateNoKey
			case rerr != nil:
				st.State, st.Detail = stateError, rerr.Error()
			default:
				st.Token, st.TokenFrom = config.MaskKey(r.Token), r.TokenSource
				if !offline {
					ctx, cancel := env.Context(cmd, false)
					defer cancel()
					c, err := api.NewAdmin(r, api.Options{HTTPClient: env.HTTPClient})
					if err != nil {
						return err
					}
					if _, _, err := c.Promo.Reference(ctx); err != nil {
						st.State = stateError
						if admin.IsCode(err, admin.CodeUnauthorized) {
							st.State = stateRejected
						}
						p := api.Explain(err, env.now())
						st.Detail = p.Title
						if p.Hint != "" {
							st.Detail += " — " + p.Hint
						}
					} else {
						st.State = stateValid
					}
				}
			}

			p := env.Printer()
			err := p.Result(st, func() error {
				token := output.Dash
				if st.Token != "" {
					token = fmt.Sprintf("%s (from %s)", st.Token, st.TokenFrom)
				}
				state := stateMark(st.State)
				if st.Detail != "" {
					state += " — " + st.Detail
				}
				p.Details([]output.KV{
					{Key: "Profile", Value: st.Profile},
					{Key: "Admin API", Value: fmt.Sprintf("%s (from %s)", st.BaseURL, st.BaseURLFrom)},
					{Key: "Token", Value: token},
					{Key: "State", Value: state},
				})
				return nil
			})
			if err != nil {
				return err
			}
			if st.State != stateValid && st.State != stateUnchecked {
				return silentError{code: ExitAuth}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&offline, "offline", false, "do not check the token against the API")
	return cmd
}

func newAdminReferenceCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:     "reference",
		Aliases: []string{"ref"},
		Short:   "List the plans, quotas and amounts a promotion is written against",
		Long: `List what a promotion may name, by the names the API accepts: the plans (only
a paid one can be discounted), the quotas that can be raised, the top-up amounts
that can carry a bonus, and the ceilings on a discount and a bonus.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := env.AdminClient()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			ref, _, err := c.Promo.Reference(ctx)
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(ref, func() error {
				printReference(env, ref)
				return nil
			})
		},
	}
}

func printReference(env *Env, ref admin.Reference) {
	p := env.Printer()
	p.Heading("Plans")
	rows := make([][]string, 0, len(ref.Tiers))
	for _, t := range ref.Tiers {
		discount := "no (not sold)"
		if t.Paid {
			discount = "yes"
		}
		rows = append(rows, []string{t.Tier, t.Name, output.Cents(t.PriceCents), discount})
	}
	p.Table([]string{"tier", "name", "price", "discountable"}, rows)

	p.Println()
	p.Heading("Quotas")
	rows = rows[:0]
	for _, l := range ref.Limits {
		rows = append(rows, []string{l.Key, l.Unit, l.Label})
	}
	p.Table([]string{"limit", "unit", "label"}, rows)

	p.Println()
	amounts := make([]string, 0, len(ref.TopUpAmountsCents))
	for _, c := range ref.TopUpAmountsCents {
		amounts = append(amounts, output.Cents(c))
	}
	p.Details([]output.KV{
		{Key: "Top-up amounts", Value: output.Or(strings.Join(amounts, ", "))},
		{Key: "Campaign kinds", Value: output.Or(strings.Join(ref.CampaignKinds, ", "))},
		{Key: "Largest discount", Value: fmt.Sprintf("%d%%", ref.MaxDiscountPercent)},
		{Key: "Largest bonus", Value: fmt.Sprintf("%d%%", ref.MaxBonusPercent)},
		{Key: "No ceiling", Value: fmt.Sprintf("%d (written as unlimited)", ref.Unlimited)},
	})
}

func newAdminTUICmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Open the full-screen admin interface",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if env.RunAdminTUI == nil {
				return errors.New("this build has no terminal interface")
			}
			if !env.IO.OutTTY || !env.IO.InTTY {
				return usageError(errors.New("the interface needs a terminal on stdin and stdout"))
			}
			return env.RunAdminTUI(cmd.Context(), env)
		},
	}
}

// adminNow is the clock the admin commands read moments against.
func adminNow(env *Env) time.Time { return env.now().Local() }

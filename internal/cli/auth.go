package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/x-chunk/teal"

	"github.com/x-chunk/celadon/internal/api"
	"github.com/x-chunk/celadon/internal/config"
	"github.com/x-chunk/celadon/internal/output"
)

func newAuthCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "auth",
		Short:   "Store, check and remove application keys",
		GroupID: groupCore,
		Long: `Manage the application keys celadon calls the API with.

A key is issued once, by the bot, when an application is created or its key is
regenerated. celadon keeps each one in ~/.celadon/keys/<profile>, readable by
you alone, and the rest of the profile in ~/.celadon/config.toml. Set
$CELADON_HOME to keep them somewhere else, or $CELADON_API_KEY to bypass the
file altogether.`,
	}
	cmd.AddCommand(
		newAuthLoginCmd(env),
		newAuthLogoutCmd(env),
		newAuthStatusCmd(env),
		newAuthSwitchCmd(env),
		newAuthTokenCmd(env),
	)
	return cmd
}

func newAuthLoginCmd(env *Env) *cobra.Command {
	var (
		withToken   bool
		skipVerify  bool
		makeDefault bool
		authHeader  string
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Store an application key in a profile",
		Long: `Store an application key in a profile, after checking that it opens an
application.

The key is read from a hidden prompt, or from standard input with --with-token.
It is never taken as an argument, where every user of the machine could read it
through ps and your shell would write it into its history.

The profile is the one --profile names, then $CELADON_PROFILE, then the default
profile, then "default". The first profile stored becomes the default.`,
		Example: `  celadon auth login
  celadon auth login --profile work --base-url https://aether.example.com
  celadon auth login --with-token < key.txt`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := config.ValidateAuthHeader(authHeader); err != nil {
				return usageError(err)
			}
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

			baseURL := env.baseURL
			if baseURL == "" {
				baseURL = profile.BaseURL
			}
			if baseURL == "" && !withToken && env.IO.CanPrompt() {
				answer, err := env.IO.Prompt(fmt.Sprintf("API base URL [%s]: ", teal.DefaultBaseURL))
				if err != nil {
					return err
				}
				baseURL = strings.TrimSpace(answer)
			}
			if baseURL == "" {
				baseURL = teal.DefaultBaseURL
			}
			baseURL = strings.TrimRight(baseURL, "/")
			if err := config.ValidateBaseURL(baseURL); err != nil {
				return usageError(err)
			}
			if authHeader != "" {
				profile.AuthHeader = authHeader
			}

			key, err := readKey(env, withToken)
			if err != nil {
				return err
			}
			if err := config.ValidateKey(key); err != nil {
				return usageError(err)
			}

			profile.BaseURL = baseURL
			if !skipVerify {
				ctx, cancel := env.Context(cmd, false)
				defer cancel()
				c, err := api.New(config.Resolved{BaseURL: baseURL, Key: key, AuthHeader: profile.AuthHeader},
					api.Options{Retries: env.retries, HTTPClient: env.HTTPClient})
				if err != nil {
					return err
				}
				app, _, err := c.App.Get(ctx)
				if err != nil {
					return fmt.Errorf("checking the key against %s: %w", baseURL, err)
				}
				profile.AppID, profile.AppName, profile.KeyPrefix = app.ID, app.Name, app.KeyPrefix
				if app.Disabled {
					env.Printer().Warn("application %q is disabled: its account's plan has lapsed, and calls will be refused until it is renewed", app.Name)
				}
			} else {
				profile.AppID, profile.AppName, profile.KeyPrefix = 0, "", ""
			}

			if err := store.SaveKey(name, key); err != nil {
				return err
			}
			err = store.Update(func(c *config.Config) error {
				c.Profiles[name] = profile
				if makeDefault || c.DefaultProfile == "" || len(c.Profiles) == 1 {
					c.DefaultProfile = name
				}
				return nil
			})
			if err != nil {
				return err
			}

			p := env.Printer()
			if profile.AppName != "" {
				p.Success("Logged in to %s as application %q (#%d)", baseURL, output.Sanitize(profile.AppName), profile.AppID)
			} else {
				p.Success("Stored the key for %s without checking it", baseURL)
			}
			p.Info("  profile %s · key %s", name, store.KeyPath(name))
			return nil
		},
	}
	cmd.Flags().BoolVar(&withToken, "with-token", false, "read the key from standard input")
	cmd.Flags().BoolVar(&skipVerify, "skip-verify", false, "store the key without checking it against the API")
	cmd.Flags().BoolVar(&makeDefault, "default", false, "make this profile the default")
	cmd.Flags().StringVar(&authHeader, "auth-header", "", "how the key is sent: bearer (default), bare or x-aether-key")
	_ = cmd.RegisterFlagCompletionFunc("auth-header", fixedCompletion(config.AuthBearer, config.AuthBare, config.AuthCustom))
	return cmd
}

func readKey(env *Env, fromStdin bool) (string, error) {
	if fromStdin {
		line, err := env.IO.ReadLine()
		if err != nil {
			return "", fmt.Errorf("reading the key from standard input: %w", err)
		}
		return strings.TrimSpace(line), nil
	}
	if !env.IO.CanPrompt() {
		return "", usageError(errors.New("no terminal to ask for the key on: pass it on standard input with --with-token"))
	}
	env.Printer().Info("Paste the application key the bot showed you (it starts with %s).", config.KeyPrefix)
	key, err := env.IO.Secret("Key: ")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(key), nil
}

// profileFor is the profile a command that writes one acts on. It is the
// one Resolve would choose, without requiring it to exist.
func profileFor(env *Env, cfg *config.Config) string {
	if env.profile != "" {
		return env.profile
	}
	if p := strings.TrimSpace(getenv(config.EnvProfile)); p != "" {
		return p
	}
	if cfg.DefaultProfile != "" {
		return cfg.DefaultProfile
	}
	return config.DefaultProfile
}

func newAuthLogoutCmd(env *Env) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove a profile and its key",
		Long: `Remove a profile and delete its key from this machine.

Aether keeps no copy of a key it can show again: once deleted here, the only
way back is to regenerate the key in the bot, which retires this one
everywhere.`,
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
			if _, ok := cfg.Profiles[name]; !ok && !store.HasKey(name) {
				return fmt.Errorf("%w: %s", config.ErrNoProfile, name)
			}
			if err := confirm(env, yes, fmt.Sprintf("Delete profile %q and its key? The key cannot be shown again.", name)); err != nil {
				return err
			}
			if err := store.RemoveProfile(name); err != nil {
				return err
			}
			env.Printer().Success("Removed profile %s", name)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}

// profileStatus is one row of `auth status`, and its JSON.
type profileStatus struct {
	Profile   string `json:"profile"`
	Default   bool   `json:"default"`
	Active    bool   `json:"active"`
	BaseURL   string `json:"base_url"`
	App       string `json:"app,omitempty"`
	AppID     int64  `json:"app_id,omitempty"`
	Key       string `json:"key,omitempty"`
	KeySource string `json:"key_source,omitempty"`
	State     string `json:"state"`
	Detail    string `json:"detail,omitempty"`
	Billing   string `json:"billing,omitempty"`
	Balance   string `json:"balance,omitempty"`
}

const (
	stateValid     = "valid"
	stateRejected  = "rejected"
	stateError     = "error"
	stateNoKey     = "no key"
	stateUnchecked = "unchecked"
)

func newAuthStatusCmd(env *Env) *cobra.Command {
	var offline bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the profiles and whether their keys still work",
		Long: `Show every profile, which one is in use, and — unless --offline — whether
its key still opens its application. Checking reads GET /v1/app, which is free
on every billing mode.

Exits 4 when the active profile has no key or its key is refused.`,
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
			active, _ := store.Resolve(env.Overrides(), teal.DefaultBaseURL)

			names := cfg.Names()
			if active.Profile != "" && !contains(names, active.Profile) {
				names = append(names, active.Profile)
			}
			if env.profile != "" {
				names = []string{env.profile}
			}

			ctx, cancel := env.Context(cmd, false)
			defer cancel()

			var rows []profileStatus
			activeOK := true
			for _, name := range names {
				row := profileStatus{Profile: name, Default: name == cfg.DefaultProfile, Active: name == active.Profile}
				r, rerr := store.Resolve(config.Overrides{Profile: name, BaseURL: env.baseURL}, teal.DefaultBaseURL)
				row.BaseURL = r.BaseURL
				row.App, row.AppID = r.Stored.AppName, r.Stored.AppID
				switch {
				case errors.Is(rerr, config.ErrNoKey):
					row.State = stateNoKey
				case rerr != nil:
					row.State, row.Detail = stateError, rerr.Error()
				default:
					row.Key, row.KeySource = config.MaskKey(r.Key), r.KeySource
					row.State = stateUnchecked
					if !offline {
						checkProfile(ctx, env, r, &row)
					}
				}
				if row.Active && row.State != stateValid && row.State != stateUnchecked {
					activeOK = false
				}
				rows = append(rows, row)
			}

			p := env.Printer()
			if p.JSONMode() {
				if rows == nil {
					rows = []profileStatus{}
				}
				if err := p.JSON(rows); err != nil {
					return err
				}
			} else if len(rows) == 0 {
				p.Info("No profiles. Run `celadon auth login` to add one.")
			} else {
				printStatus(env, rows)
			}
			if getenv(config.EnvAPIKey) != "" {
				p.Warn("$%s is set and overrides every stored key", config.EnvAPIKey)
			}
			if !activeOK {
				return silentError{code: ExitAuth}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&offline, "offline", false, "do not check the keys against the API")
	return cmd
}

func checkProfile(ctx context.Context, env *Env, r config.Resolved, row *profileStatus) {
	c, err := api.New(r, api.Options{Retries: 0, HTTPClient: env.HTTPClient})
	if err != nil {
		row.State, row.Detail = stateError, err.Error()
		return
	}
	app, _, err := c.App.Get(ctx)
	if err != nil {
		if teal.IsCode(err, teal.CodeUnauthorized, teal.CodeAccountBlocked) {
			row.State = stateRejected
		} else {
			row.State = stateError
		}
		row.Detail = api.Explain(err, time.Now()).Title
		return
	}
	row.State = stateValid
	row.App, row.AppID = app.Name, app.ID
	row.Billing = string(app.Billing)
	row.Balance = output.Money(app.Balance)
	if app.Disabled {
		row.Detail = "disabled: the account's plan has lapsed"
	}
}

func printStatus(env *Env, rows []profileStatus) {
	p := env.Printer()
	for i, row := range rows {
		if i > 0 {
			p.Println()
		}
		title := row.Profile
		var tags []string
		if row.Active {
			tags = append(tags, "active")
		}
		if row.Default {
			tags = append(tags, "default")
		}
		if len(tags) > 0 {
			title += " (" + strings.Join(tags, ", ") + ")"
		}
		p.Heading(title)
		pairs := []output.KV{{Key: "  API", Value: row.BaseURL}}
		if row.App != "" {
			pairs = append(pairs, output.KV{Key: "  Application", Value: fmt.Sprintf("%s (#%d)", row.App, row.AppID)})
		}
		if row.Key != "" {
			pairs = append(pairs, output.KV{Key: "  Key", Value: fmt.Sprintf("%s (from %s)", row.Key, row.KeySource)})
		}
		if row.Billing != "" {
			pairs = append(pairs, output.KV{Key: "  Billing", Value: row.Billing + " · balance " + row.Balance})
		}
		state := stateMark(row.State)
		if row.Detail != "" {
			state += " — " + row.Detail
		}
		pairs = append(pairs, output.KV{Key: "  State", Value: state})
		p.Details(pairs)
	}
}

func stateMark(state string) string {
	switch state {
	case stateValid:
		return "✓ " + state
	case stateUnchecked:
		return "· " + state
	default:
		return "✗ " + state
	}
}

func newAuthSwitchCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:               "switch <profile>",
		Short:             "Make a profile the default",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: profileCompletion(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			store, err := env.Store()
			if err != nil {
				return err
			}
			err = store.Update(func(c *config.Config) error {
				if _, ok := c.Profiles[name]; !ok {
					return fmt.Errorf("%w: %s (known: %s)", config.ErrNoProfile, name, orNone(c.Names()))
				}
				c.DefaultProfile = name
				return nil
			})
			if err != nil {
				return err
			}
			env.Printer().Success("Default profile is now %s", name)
			return nil
		},
	}
}

func newAuthTokenCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "token",
		Short: "Print the key of the active profile",
		Long: `Print the key of the active profile to standard output, for handing to
another program. Anything that captures the output captures the key.`,
		Example: `  curl -H "Authorization: Bearer $(celadon auth token)" https://aether.example.com/v1/app`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := env.Resolve()
			if errors.Is(err, config.ErrNoKey) {
				return authError(fmt.Errorf("not logged in to profile %q", r.Profile))
			}
			if err != nil {
				return err
			}
			fmt.Fprintln(env.IO.Out, r.Key)
			return nil
		},
	}
}

func orNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

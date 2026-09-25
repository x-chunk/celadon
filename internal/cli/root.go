// Package cli is celadon's command line: one cobra command per API
// operation, grouped the way the API documentation groups them.
package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/x-chunk/teal"

	"github.com/x-chunk/celadon/internal/api"
	"github.com/x-chunk/celadon/internal/config"
	"github.com/x-chunk/celadon/internal/iostreams"
	"github.com/x-chunk/celadon/internal/output"
	"github.com/x-chunk/celadon/internal/version"
)

// Exit codes. They follow the GitHub CLI's, so scripts written against one
// read the other.
const (
	ExitOK       = 0
	ExitError    = 1
	ExitUsage    = 2
	ExitAuth     = 4
	ExitCanceled = 130
)

// DefaultTimeout bounds a command that does not set --timeout. An export and
// the TUI are not bounded by it: one is a stream of unknown length, and the
// other bounds every request of its own.
const DefaultTimeout = time.Minute

// Env is what every command shares: the streams, the store, the global
// flags, and the seams a test replaces.
type Env struct {
	IO *iostreams.Streams

	// ConfigDir overrides where the store is; empty means the default.
	ConfigDir string
	// HTTPClient replaces the client's transport, for tests.
	HTTPClient *http.Client
	// Now is the clock, for tests.
	Now func() time.Time
	// RunTUI starts the full-screen interface. It is a seam so that the
	// cli package does not import the tui one, and a test can stub it.
	RunTUI func(ctx context.Context, env *Env) error

	profile string
	baseURL string
	format  string
	quiet   bool
	noColor bool
	timeout time.Duration
	retries int

	printer *output.Printer
	store   *config.Store
}

// Store opens the config store once.
func (e *Env) Store() (*config.Store, error) {
	if e.store != nil {
		return e.store, nil
	}
	s, err := config.Open(e.ConfigDir)
	if err != nil {
		return nil, err
	}
	e.store = s
	return s, nil
}

// Printer is the printer for the global --output and --quiet.
func (e *Env) Printer() *output.Printer {
	if e.printer == nil {
		f, _ := output.ParseFormat(e.format)
		e.printer = output.New(e.IO, f, e.quiet)
	}
	return e.printer
}

// Overrides are the global flags that bear on which profile is in use.
func (e *Env) Overrides() config.Overrides {
	return config.Overrides{Profile: e.profile, BaseURL: e.baseURL}
}

// Resolve settles the profile in use.
func (e *Env) Resolve() (config.Resolved, error) {
	s, err := e.Store()
	if err != nil {
		return config.Resolved{}, err
	}
	return s.Resolve(e.Overrides(), teal.DefaultBaseURL)
}

// Client builds a client for the profile in use. A missing key is an
// authentication problem, and is reported as one.
func (e *Env) Client() (*teal.Client, config.Resolved, error) {
	r, err := e.Resolve()
	if errors.Is(err, config.ErrNoKey) {
		return nil, r, authError(fmt.Errorf("not logged in to profile %q: run `celadon auth login`, or set $%s", r.Profile, config.EnvAPIKey))
	}
	if err != nil {
		return nil, r, err
	}
	c, err := api.New(r, api.Options{Retries: e.retries, HTTPClient: e.HTTPClient})
	return c, r, err
}

// Context bounds one command by --timeout, or by DefaultTimeout when the
// flag was not given. Unbounded commands pass unbounded.
func (e *Env) Context(cmd *cobra.Command, unbounded bool) (context.Context, context.CancelFunc) {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	d := e.timeout
	if !cmd.Flags().Changed("timeout") && unbounded {
		d = 0
	}
	if d <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d)
}

func (e *Env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// NewRootCmd builds the whole command tree over env.
func NewRootCmd(env *Env) *cobra.Command {
	root := &cobra.Command{
		Use:   "celadon",
		Short: "Work with the Aether Plug-In API from the terminal",
		Long: `celadon is a command line and a full-screen terminal interface for the
Aether Plug-In API: the archive, the vault, the shortcuts and the settings of
the account an application key opens.

Run it without arguments on a terminal to open the interface; run a command
to script it. Start with ` + "`celadon auth login`" + `.`,
		Example: `  celadon auth login
  celadon search invoice --where 'suser=ann'
  celadon actions list
  celadon                      # open the full-screen interface`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Get().Version,
		Args:          cobra.NoArgs,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := output.ParseFormat(env.format); err != nil {
				return usageError(err)
			}
			if env.noColor {
				env.IO.Color = false
			}
			if env.timeout < 0 {
				return usageError(fmt.Errorf("--timeout cannot be negative"))
			}
			if env.retries < 0 {
				return usageError(fmt.Errorf("--retries cannot be negative"))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !env.IO.OutTTY || !env.IO.InTTY || env.RunTUI == nil {
				return cmd.Help()
			}
			return env.RunTUI(cmd.Context(), env)
		},
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.SetIn(env.IO.In)
	root.SetOut(env.IO.Out)
	root.SetErr(env.IO.Err)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError(err) })

	pf := root.PersistentFlags()
	pf.StringVarP(&env.profile, "profile", "p", "", "profile to use (default: $CELADON_PROFILE, then the default profile)")
	pf.StringVar(&env.baseURL, "base-url", "", "API base URL, overriding the profile ($CELADON_BASE_URL)")
	pf.StringVarP(&env.format, "output", "o", "text", "output format: text or json")
	pf.BoolVarP(&env.quiet, "quiet", "q", false, "print nothing but the result and errors")
	pf.BoolVar(&env.noColor, "no-color", false, "disable colored output ($NO_COLOR)")
	pf.DurationVar(&env.timeout, "timeout", DefaultTimeout, "give up on a command after this long (0 for never)")
	pf.IntVar(&env.retries, "retries", api.DefaultRetries, "retries after a rate refusal or a failed read")
	_ = root.RegisterFlagCompletionFunc("output", fixedCompletion("text", "json"))
	_ = root.RegisterFlagCompletionFunc("profile", profileCompletion(env))

	root.AddGroup(
		&cobra.Group{ID: groupCore, Title: "Core commands:"},
		&cobra.Group{ID: groupArchive, Title: "Archive commands:"},
		&cobra.Group{ID: groupAccount, Title: "Account commands:"},
	)
	root.SetHelpCommandGroupID(groupCore)
	root.SetCompletionCommandGroupID(groupCore)

	root.AddCommand(
		newAuthCmd(env),
		newTUICmd(env),
		newVersionCmd(env),

		newSearchCmd(env),
		newCountCmd(env),
		newExportCmd(env),
		newChatsCmd(env),
		newMessageCmd(env),
		newFieldsCmd(env),
		newInsightsCmd(env),
		newPortraitCmd(env),

		newAppCmd(env),
		newAccountCmd(env),
		newVaultCmd(env),
		newActionsCmd(env),
		newSettingsCmd(env),
	)
	markArgErrors(root)
	return root
}

// markArgErrors makes a wrong number of arguments a usage error, which cobra
// reports as a plain one. It walks the tree once, after it is built.
func markArgErrors(cmd *cobra.Command) {
	if validate := cmd.Args; validate != nil {
		cmd.Args = func(c *cobra.Command, args []string) error {
			if err := validate(c, args); err != nil {
				return usageError(err)
			}
			return nil
		}
	}
	for _, sub := range cmd.Commands() {
		markArgErrors(sub)
	}
}

// exclusive refuses a command given more than one of the named flags.
// cobra's own flag groups report the same with a plain error, which would
// not exit as a usage error.
func exclusive(cmd *cobra.Command, names ...string) error {
	var set []string
	for _, n := range names {
		if cmd.Flags().Changed(n) {
			set = append(set, "--"+n)
		}
	}
	if len(set) > 1 {
		return usageError(fmt.Errorf("%s cannot be used together", strings.Join(set, " and ")))
	}
	return nil
}

const (
	groupCore    = "core"
	groupArchive = "archive"
	groupAccount = "account"
)

// Execute runs the command line and returns the process's exit code.
func Execute(ctx context.Context, env *Env, args []string) int {
	root := NewRootCmd(env)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	return report(env, err)
}

// report writes err to the error stream and chooses the exit code for it.
func report(env *Env, err error) int {
	var silent silentError
	if errors.As(err, &silent) {
		return silent.code
	}
	if errors.Is(err, context.Canceled) {
		return ExitCanceled
	}

	code := ExitError
	var ue usageErr
	var ae authErr
	switch {
	case errors.As(err, &ue):
		code = ExitUsage
	case errors.As(err, &ae):
		code = ExitAuth
	}

	p := api.Explain(err, env.now())
	if p.Auth {
		code = ExitAuth
	}
	fmt.Fprintf(env.IO.Err, "error: %s\n", output.Sanitize(p.Title))
	if p.Hint != "" {
		fmt.Fprintf(env.IO.Err, "hint: %s\n", p.Hint)
	}
	if code == ExitUsage {
		fmt.Fprintln(env.IO.Err, "run with --help for usage")
	}
	if e, ok := teal.AsError(err); ok {
		env.Printer().Meta(e.Meta)
	}
	return code
}

type usageErr struct{ error }

func (e usageErr) Unwrap() error { return e.error }

func usageError(err error) error { return usageErr{err} }

type authErr struct{ error }

func (e authErr) Unwrap() error { return e.error }

func authError(err error) error { return authErr{err} }

// silentError ends a command with a code and nothing more to say: whatever
// there was to say has been said.
type silentError struct{ code int }

func (e silentError) Error() string { return fmt.Sprintf("exit %d", e.code) }

func fixedCompletion(values ...string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return values, cobra.ShellCompDirectiveNoFileComp
	}
}

func profileCompletion(env *Env) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		s, err := env.Store()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		cfg, err := s.Load()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return cfg.Names(), cobra.ShellCompDirectiveNoFileComp
	}
}

func newVersionCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Print the version of celadon",
		GroupID: groupCore,
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			info := version.Get()
			return env.Printer().Result(map[string]string{
				"version": info.Version, "commit": info.Commit, "date": info.Date, "go": info.Go,
			}, func() error {
				env.Printer().Println(info.String())
				return nil
			})
		},
	}
}

func newTUICmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:     "tui",
		Short:   "Open the full-screen terminal interface",
		Long:    "Open the full-screen terminal interface. Running celadon with no arguments on a terminal does the same.",
		GroupID: groupCore,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if env.RunTUI == nil {
				return errors.New("this build has no terminal interface")
			}
			if !env.IO.OutTTY || !env.IO.InTTY {
				return usageError(errors.New("the interface needs a terminal on stdin and stdout"))
			}
			return env.RunTUI(cmd.Context(), env)
		},
	}
}

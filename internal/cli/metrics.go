package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/x-chunk/celadon/internal/api"
	"github.com/x-chunk/celadon/internal/config"
	"github.com/x-chunk/celadon/internal/metrics"
	"github.com/x-chunk/celadon/internal/output"
)

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// tunnelHint is what usually gets a remote listener within reach.
const tunnelHint = "the listener is published on the deployment's loopback address only: open a tunnel with `ssh -N -L 9090:127.0.0.1:9090 user@app-host`, or point --metrics-url at it"

func newMetricsCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "metrics",
		Aliases: []string{"m"},
		Short:   "Watch the process: health, live values, history and charts",
		GroupID: groupAdmin,
		Long: `Read Aether's metrics listener — the API its built-in dashboard is drawn from:
health and readiness, the catalog of series, their live values and the stream of
them, range queries over the history, and the Prometheus text.

The listener is a port of its own (:9090), published on the deployment's
loopback address. celadon reaches it at http://127.0.0.1:9090 unless told
otherwise, which is also where an SSH tunnel puts a remote one:

  ssh -N -L 9090:127.0.0.1:9090 user@app-host

--metrics-url, $CELADON_METRICS_URL or the profile's metrics_url point
elsewhere. Its METRICS_TOKEN is optional; store one with ` + "`celadon metrics login`" + `.

A counter is shown as its rate per second, a histogram as its _avg, _p50 and
_p99 series. Run ` + "`celadon metrics tui`" + ` for the full-screen dashboard.`,
		Example: `  celadon metrics health
  celadon metrics overview
  celadon metrics get 'db_*'
  celadon metrics query process_cpu_percent host_cpu_percent --range 6h
  celadon metrics watch
  celadon metrics tui`,
	}
	cmd.PersistentFlags().StringVar(&env.metricsURL, "metrics-url", "", "the metrics listener (default: $CELADON_METRICS_URL, the profile, then "+config.DefaultMetricsURL+")")
	cmd.AddCommand(
		newMetricsLoginCmd(env),
		newMetricsLogoutCmd(env),
		newMetricsStatusCmd(env),
		newMetricsHealthCmd(env),
		newMetricsOverviewCmd(env),
		newMetricsListCmd(env),
		newMetricsGetCmd(env),
		newMetricsQueryCmd(env),
		newMetricsWatchCmd(env),
		newMetricsPromCmd(env),
		newMetricsTUICmd(env),
	)
	return cmd
}

// metricsCall wraps a failure to reach the listener at all with the hint
// that usually fixes it.
func metricsCall(r config.MetricsResolved, err error) error {
	return api.MarkUnreachable(err, "the metrics listener", r.BaseURL, tunnelHint)
}

func newMetricsLoginCmd(env *Env) *cobra.Command {
	var (
		withToken  bool
		noToken    bool
		skipVerify bool
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Store the listener's address and its METRICS_TOKEN in a profile",
		Long: `Store where the metrics listener is (--metrics-url) and, unless --no-token, its
METRICS_TOKEN, after checking that the listener answers GET /api/meta with it.

The token is read from a hidden prompt or from standard input with
--with-token; never from an argument. A deployment without a METRICS_TOKEN
serves the listener to anybody who reaches it: log in with --no-token.`,
		Example: `  celadon metrics login
  celadon metrics login --metrics-url http://127.0.0.1:19090 --with-token < metrics-token.txt
  celadon metrics login --no-token`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if withToken && noToken {
				return usageError(errors.New("--with-token and --no-token cannot be used together"))
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
			if env.metricsURL != "" {
				if err := config.ValidateBaseURL(env.metricsURL); err != nil {
					return usageError(err)
				}
				profile.MetricsURL = strings.TrimRight(env.metricsURL, "/")
			}
			target := firstNonEmpty(profile.MetricsURL, config.DefaultMetricsURL)

			token := ""
			if !noToken {
				if token, err = readMetricsToken(env, withToken); err != nil {
					return err
				}
				if err := config.ValidateMetricsToken(token); err != nil {
					return usageError(err)
				}
			}

			r := config.MetricsResolved{Profile: name, BaseURL: target, Token: token}
			var info metrics.Info
			if !skipVerify {
				ctx, cancel := env.Context(cmd, false)
				defer cancel()
				c, err := api.NewMetrics(r, api.Options{Retries: env.retries, HTTPClient: env.HTTPClient})
				if err != nil {
					return err
				}
				if info, _, err = c.Series.Info(ctx); err != nil {
					return fmt.Errorf("checking the listener at %s: %w", target, metricsCall(r, err))
				}
			}

			if noToken {
				if err := store.DeleteMetricsToken(name); err != nil {
					return err
				}
			} else if err := store.SaveMetricsToken(name, token); err != nil {
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
				p.Success("Stored the metrics listener %s without checking it", target)
			} else {
				p.Success("The metrics listener at %s answers: %d series, up %s", target, len(info.Catalog), output.Duration(info.Uptime()))
			}
			if noToken {
				p.Info("  profile %s · no token", name)
			} else {
				p.Info("  profile %s · token %s", name, store.MetricsTokenPath(name))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&withToken, "with-token", false, "read the token from standard input")
	cmd.Flags().BoolVar(&noToken, "no-token", false, "the listener has no METRICS_TOKEN; forget any stored one")
	cmd.Flags().BoolVar(&skipVerify, "skip-verify", false, "store without checking against the listener")
	return cmd
}

func readMetricsToken(env *Env, fromStdin bool) (string, error) {
	if fromStdin {
		line, err := env.IO.ReadLine()
		if err != nil {
			return "", fmt.Errorf("reading the token from standard input: %w", err)
		}
		return strings.TrimSpace(line), nil
	}
	if !env.IO.CanPrompt() {
		return "", usageError(errors.New("no terminal to ask for the token on: pass it with --with-token, or use --no-token"))
	}
	env.Printer().Info("Paste the deployment's METRICS_TOKEN.")
	token, err := env.IO.Secret("Metrics token: ")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(token), nil
}

func newMetricsLogoutCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Delete the stored METRICS_TOKEN of a profile",
		Long:  "Delete the stored METRICS_TOKEN of a profile. Its metrics_url, and the rest of the profile, stay.",
		Args:  cobra.NoArgs,
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
			if !store.HasMetricsToken(name) {
				return fmt.Errorf("%w for profile %q", config.ErrNoMetricsToken, name)
			}
			if err := store.DeleteMetricsToken(name); err != nil {
				return err
			}
			env.Printer().Success("Deleted the metrics token of profile %s", name)
			return nil
		},
	}
}

// metricsStatus is `metrics status`, and its JSON.
type metricsStatus struct {
	Profile     string            `json:"profile"`
	URL         string            `json:"url"`
	URLSource   string            `json:"url_source"`
	Token       string            `json:"token,omitempty"`
	TokenSource string            `json:"token_source"`
	Live        bool              `json:"live"`
	Ready       *bool             `json:"ready,omitempty"`
	Database    string            `json:"database,omitempty"`
	NATS        map[string]string `json:"nats,omitempty"`
	Access      string            `json:"access"`
	Series      int               `json:"series,omitempty"`
	Uptime      string            `json:"uptime,omitempty"`
	Persisted   *bool             `json:"persisted,omitempty"`
	Detail      string            `json:"detail,omitempty"`
}

func newMetricsStatusCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show which listener and token are used, and whether they work",
		Long: `Show the listener's address, where the token comes from, and — asking the
listener — whether it is up, whether the process is ready, and whether the token
opens the API. Exits 1 when the listener cannot be reached and 4 when it
refuses the token.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, r, err := env.MetricsClient()
			if err != nil {
				return err
			}
			st := metricsStatus{Profile: r.Profile, URL: r.BaseURL, URLSource: r.BaseURLSource, TokenSource: r.TokenSource}
			if r.Token != "" {
				st.Token = config.MaskKey(r.Token)
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()

			code := ExitOK
			if _, _, err := c.Health.Live(ctx); err != nil {
				st.Access = "unreachable"
				st.Detail = api.Explain(metricsCall(r, err), env.now()).Title
				code = ExitError
			} else {
				st.Live = true
				if ready, _, err := c.Health.Ready(ctx); err == nil {
					st.Ready, st.Database, st.NATS = &ready.Ready, ready.Database, ready.NATS
				}
				info, _, err := c.Series.Info(ctx)
				switch {
				case metrics.IsCode(err, metrics.CodeUnauthorized):
					st.Access = "refused"
					st.Detail = api.Explain(err, env.now()).Title
					code = ExitAuth
				case err != nil:
					st.Access = "error"
					st.Detail = api.Explain(err, env.now()).Title
					code = ExitError
				default:
					st.Access = "open"
					st.Series, st.Uptime, st.Persisted = len(info.Catalog), output.Duration(info.Uptime()), &info.Persisted
				}
			}

			p := env.Printer()
			err = p.Result(st, func() error {
				token := "none"
				if st.Token != "" {
					token = fmt.Sprintf("%s (from %s)", st.Token, st.TokenSource)
				}
				pairs := []output.KV{
					{Key: "Profile", Value: st.Profile},
					{Key: "Listener", Value: fmt.Sprintf("%s (from %s)", st.URL, st.URLSource)},
					{Key: "Token", Value: token},
				}
				if !st.Live {
					pairs = append(pairs, output.KV{Key: "State", Value: "✗ unreachable — " + st.Detail})
					p.Details(pairs)
					p.Info("hint: %s", tunnelHint)
					return nil
				}
				if st.Ready != nil {
					pairs = append(pairs, output.KV{Key: "Ready", Value: readyWord(*st.Ready) + " · database " + st.Database})
				}
				switch st.Access {
				case "open":
					history := "live only"
					if st.Persisted != nil && *st.Persisted {
						history = "persisted to Postgres"
					}
					pairs = append(pairs,
						output.KV{Key: "API", Value: fmt.Sprintf("✓ %d series · up %s · %s", st.Series, st.Uptime, history)})
				default:
					pairs = append(pairs, output.KV{Key: "API", Value: "✗ " + st.Access + " — " + st.Detail})
				}
				p.Details(pairs)
				return nil
			})
			if err != nil {
				return err
			}
			if code != ExitOK {
				return silentError{code: code}
			}
			return nil
		},
	}
}

func readyWord(ready bool) string {
	if ready {
		return "✓ ready"
	}
	return "✗ not ready"
}

// healthReport is `metrics health`, and its JSON.
type healthReport struct {
	Live      bool              `json:"live"`
	Ready     bool              `json:"ready"`
	Database  string            `json:"database,omitempty"`
	NATS      map[string]string `json:"nats,omitempty"`
	LatencyMs int64             `json:"latency_ms"`
}

// checkHealth asks for liveness and readiness once. err is set only when the
// listener could not be asked at all.
func checkHealth(ctx context.Context, c *metrics.Client) (healthReport, error) {
	var h healthReport
	_, meta, err := c.Health.Live(ctx)
	if err != nil {
		return h, err
	}
	h.Live, h.LatencyMs = true, meta.Elapsed.Milliseconds()
	ready, _, err := c.Health.Ready(ctx)
	if err != nil {
		return h, err
	}
	h.Ready, h.Database, h.NATS = ready.Ready, ready.Database, ready.NATS
	return h, nil
}

func newMetricsHealthCmd(env *Env) *cobra.Command {
	var wait time.Duration
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Show whether the process is up and ready",
		Long: `Show liveness (the process serves the listener) and readiness (the database
answers and every NATS connection is up). Neither needs a token.

Exits 0 when ready and 1 otherwise, so a deploy script can wait on it; --wait
keeps asking every two seconds until the process is ready or the time runs out.`,
		Example: `  celadon metrics health
  celadon metrics health --wait 2m && echo deployed`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if wait < 0 {
				return usageError(errors.New("--wait cannot be negative"))
			}
			c, r, err := env.MetricsClient()
			if err != nil {
				return err
			}
			var (
				ctx    context.Context
				cancel context.CancelFunc
			)
			if wait > 0 {
				ctx, cancel = context.WithTimeout(cmd.Context(), wait)
			} else {
				ctx, cancel = env.Context(cmd, false)
			}
			defer cancel()

			for {
				h, err := checkHealth(ctx, c)
				if err == nil && h.Ready || wait == 0 {
					if err != nil && !h.Live {
						return metricsCall(r, err)
					}
					return printHealth(env, h)
				}
				env.Printer().Info("not ready yet — asking again in 2s…")
				t := time.NewTimer(2 * time.Second)
				select {
				case <-ctx.Done():
					t.Stop()
					env.Printer().Warn("gave up waiting after %s", output.Duration(wait))
					if err != nil && !h.Live {
						return metricsCall(r, err)
					}
					return printHealth(env, h)
				case <-t.C:
				}
			}
		},
	}
	cmd.Flags().DurationVar(&wait, "wait", 0, "keep asking until ready, for up to this long")
	return cmd
}

func printHealth(env *Env, h healthReport) error {
	p := env.Printer()
	err := p.Result(h, func() error {
		pairs := []output.KV{
			{Key: "Live", Value: fmt.Sprintf("%s (answered in %dms)", mark(h.Live), h.LatencyMs)},
			{Key: "Ready", Value: readyWord(h.Ready)},
			{Key: "Database", Value: dependency(h.Database)},
		}
		for _, name := range sortedKeys(h.NATS) {
			pairs = append(pairs, output.KV{Key: "NATS " + name, Value: dependency(h.NATS[name])})
		}
		p.Details(pairs)
		return nil
	})
	if err != nil {
		return err
	}
	if !h.Ready {
		return silentError{code: ExitError}
	}
	return nil
}

func mark(ok bool) string {
	if ok {
		return "✓ up"
	}
	return "✗ down"
}

func dependency(state string) string {
	switch state {
	case "ok":
		return "✓ ok"
	case "", "not configured":
		return "· " + output.Or(state)
	}
	return "✗ " + state
}

func newMetricsTUICmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Open the full-screen metrics dashboard",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if env.RunMetricsTUI == nil {
				return errors.New("this build has no terminal interface")
			}
			if !env.IO.OutTTY || !env.IO.InTTY {
				return usageError(errors.New("the dashboard needs a terminal on stdin and stdout"))
			}
			return env.RunMetricsTUI(cmd.Context(), env)
		},
	}
}

package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/x-chunk/teal"

	"github.com/x-chunk/celadon/internal/output"
)

func newAppCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "app",
		Short:   "Show the application, what it has spent and the price list",
		GroupID: groupAccount,
		Long: `Show the application the key belongs to. Everything here is free on every
billing mode: an application that has run out of credits can still find out
that it has.`,
	}
	cmd.AddCommand(newAppViewCmd(env), newAppUsageCmd(env), newAppPricesCmd(env))
	return cmd
}

func newAppViewCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "view",
		Short: "Show the application: billing mode, balance, spending",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			app, meta, err := c.App.Get(ctx)
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(app, func() error {
				p.Heading(app.Name)
				state := "active"
				if app.Disabled {
					state = "disabled — the account's plan has lapsed"
				}
				p.Details([]output.KV{
					{Key: "ID", Value: fmt.Sprint(app.ID)},
					{Key: "Account", Value: fmt.Sprint(app.AccountID)},
					{Key: "Billing", Value: billingLabel(app.Billing)},
					{Key: "Balance", Value: output.Money(app.Balance)},
					{Key: "Funded", Value: output.Money(app.Funded)},
					{Key: "Spent", Value: output.Money(app.Spent)},
					{Key: "Requests", Value: fmt.Sprint(app.Requests)},
					{Key: "Last used", Value: output.Time(app.LastUsedAt)},
					{Key: "Key", Value: app.KeyPrefix + "… issued " + output.Time(app.KeyIssuedAt)},
					{Key: "Created", Value: output.Time(app.CreatedAt)},
					{Key: "State", Value: state},
				})
				p.Meta(meta)
				return nil
			})
		},
	}
}

// billingLabel says what a billing mode means, not only what it is called.
func billingLabel(b teal.BillingMode) string {
	switch b {
	case teal.BillingShared:
		return "shared — the plan pays, quotas apply"
	case teal.BillingCredits:
		return "credits — the application's balance pays for every priced call"
	case teal.BillingHybrid:
		return "hybrid — the plan pays until a quota refuses, then credits"
	}
	return output.Or(string(b))
}

func newAppUsageCmd(env *Env) *cobra.Command {
	var (
		days  int
		byDay bool
	)
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Show what the application has spent, by operation",
		Example: `  celadon app usage
  celadon app usage --days 7 --by-day`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if days < 0 || days > 365 {
				return usageError(fmt.Errorf("--days must be between 1 and 365"))
			}
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			u, meta, err := c.App.Usage(ctx, &teal.UsageRequest{Days: days, ByDay: byDay})
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(u, func() error {
				p.Heading(fmt.Sprintf("%s → %s: %d calls, %s", u.From, u.To, u.Calls, output.Money(u.Credits)))
				if len(u.Ops) == 0 {
					p.Info("Nothing was billed in this window.")
				} else {
					p.Table([]string{"operation", "calls", "units", "spent"}, usageRows(u.Ops))
				}
				if byDay && len(u.Days) > 0 {
					p.Println()
					rows := make([][]string, 0, len(u.Days))
					for _, d := range u.Days {
						rows = append(rows, []string{d.Day, fmt.Sprint(d.Calls), output.Money(d.Credits)})
					}
					p.Table([]string{"day", "calls", "spent"}, rows)
				}
				p.Meta(meta)
				return nil
			})
		},
	}
	cmd.Flags().IntVar(&days, "days", 0, "how many days back to cover, 1-365 (default 30)")
	cmd.Flags().BoolVar(&byDay, "by-day", false, "add a day-by-day breakdown")
	return cmd
}

func usageRows(ops []teal.UsageOp) [][]string {
	rows := make([][]string, 0, len(ops))
	for _, op := range ops {
		rows = append(rows, []string{op.Op, fmt.Sprint(op.Calls), fmt.Sprint(op.Units), output.Money(op.Credits)})
	}
	return rows
}

func newAppPricesCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "prices",
		Short: "Show what every operation costs on this deployment",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			pr, meta, err := c.App.Prices(ctx)
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(pr, func() error {
				rows := make([][]string, 0, len(pr.Prices))
				for _, x := range pr.Prices {
					rows = append(rows, []string{x.Op, output.Money(x.Price), x.Unit, x.Charging, output.Or(x.Limit)})
				}
				p.Table([]string{"operation", "price", "per", "charged", "stands in for"}, rows)
				p.Println()
				modes := make([]string, 0, len(pr.Rates))
				for m := range pr.Rates {
					modes = append(modes, string(m))
				}
				sort.Strings(modes)
				var rates []string
				for _, m := range modes {
					r := pr.Rates[teal.BillingMode(m)]
					rates = append(rates, fmt.Sprintf("%s %d/s (burst %d)", m, r.PerSecond, r.Burst))
				}
				p.Details([]output.KV{
					{Key: "1 cent buys", Value: fmt.Sprintf("%d credits", pr.CreditsPerCent)},
					{Key: "Rate limits", Value: output.Or(strings.Join(rates, " · "))},
				})
				p.Meta(meta)
				return nil
			})
		},
	}
}

func newAccountCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "account",
		Short:   "Show the account behind the key: plan and quotas",
		GroupID: groupAccount,
	}
	cmd.AddCommand(newAccountViewCmd(env), newAccountQuotasCmd(env))
	return cmd
}

func newAccountViewCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "view",
		Short: "Show the plan, its permissions and every quota",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			a, meta, err := c.App.Account(ctx)
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(a, func() error {
				p.Details([]output.KV{
					{Key: "Account", Value: fmt.Sprint(a.AccountID)},
					{Key: "Plan", Value: fmt.Sprintf("%s (%s, %s)", output.Or(a.Plan.Name), a.Plan.Tier, output.Cents(a.Plan.PriceCents))},
					{Key: "Paid until", Value: output.Time(a.Plan.Until)},
					{Key: "Balance", Value: output.Cents(a.BalanceCents) + " (the account's own; not spendable with a key)"},
					{Key: "Permissions", Value: output.Or(strings.Join(a.Plan.Permissions, ", "))},
				})
				if len(a.Quotas) > 0 {
					p.Println()
					printQuotas(env, a.Quotas)
				}
				p.Meta(meta)
				return nil
			})
		},
	}
}

func newAccountQuotasCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "quotas",
		Short: "Show every quota against what has been used of it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			qs, meta, err := c.App.Quotas(ctx)
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(qs, func() error {
				printQuotas(env, qs)
				p.Meta(meta)
				return nil
			})
		},
	}
}

func printQuotas(env *Env, qs []teal.Quota) {
	now := env.now()
	rows := make([][]string, 0, len(qs))
	for _, q := range qs {
		window, resets := "ceiling", output.Dash
		if q.Window != "" {
			window = "per " + q.Window
			resets = output.Until(q.ResetAt, now)
		}
		rows = append(rows, []string{q.Key, output.Quota(q), q.Unit, window, resets})
	}
	env.Printer().Table([]string{"quota", "used", "unit", "window", "resets"}, rows)
}

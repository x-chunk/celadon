package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/x-chunk/celadon/internal/admin"
	"github.com/x-chunk/celadon/internal/output"
	"github.com/x-chunk/celadon/internal/query"
)

const benefitsHelp = `Benefits are given as repeatable flags, or all at once:

  --discount TIER:PERCENT        25% off Pro: pro:25; every plan that is sold: all:25
  --bonus    AMOUNT:PERCENT      +20% on a $50 top-up: 50:20; every amount: any:20
  --grant    TIER:LIMIT=VALUE    free:search:daily=200; no ceiling: …=unlimited
  --benefits LINE                "discount pro:25, bonus 50:20, grant free:search:daily=200"
  --benefits-file FILE           the benefits object as JSON (- for standard input)

` + "`celadon admin reference`" + ` lists the tiers, quotas and amounts by the names accepted.

Times take now, never, 2026-10-01, "2026-10-01 18:00" (local time), RFC 3339, or
a span from now such as +7d; --for sets the end as a span from the start.`

// maxBenefitsFile bounds a benefits file; the server reads a megabyte at most.
const maxBenefitsFile = 1 << 20

// promoFlags are the flags campaigns and codes share.
type promoFlags struct {
	name, description string
	discounts         []string
	bonuses           []string
	grants            []string
	benefitsLine      string
	benefitsFile      string
	starts, ends, dur string
	dryRun            bool
}

func (f *promoFlags) register(cmd *cobra.Command) {
	fs := cmd.Flags()
	fs.StringVar(&f.name, "name", "", "the name (up to 64 characters)")
	fs.StringVar(&f.description, "description", "", "what it is, as users will read it (up to 512 characters)")
	fs.StringArrayVar(&f.discounts, "discount", nil, "a discount, TIER:PERCENT (repeatable)")
	fs.StringArrayVar(&f.bonuses, "bonus", nil, "a top-up bonus, AMOUNT:PERCENT with the amount in dollars (repeatable)")
	fs.StringArrayVar(&f.grants, "grant", nil, "a raised ceiling, TIER:LIMIT=VALUE (repeatable)")
	fs.StringVar(&f.benefitsLine, "benefits", "", "every benefit on one line, comma-separated")
	fs.StringVar(&f.benefitsFile, "benefits-file", "", "the benefits as JSON (- for standard input)")
	fs.StringVar(&f.starts, "starts", "", "when it starts (default now)")
	fs.StringVar(&f.ends, "ends", "", "when it ends (default never)")
	fs.StringVar(&f.dur, "for", "", "how long it runs, from --starts or from now: 7d, 36h")
	fs.BoolVar(&f.dryRun, "dry-run", false, "print the request body instead of sending it")
}

// benefitsGiven reports whether any benefit flag was given.
func (f *promoFlags) benefitsGiven() bool {
	return len(f.discounts)+len(f.bonuses)+len(f.grants) > 0 || f.benefitsLine != "" || f.benefitsFile != ""
}

// benefits reads the benefit flags into one set, nil when none was given.
func (f *promoFlags) benefits(env *Env) (*admin.Benefits, error) {
	if !f.benefitsGiven() {
		return nil, nil
	}
	if f.benefitsFile != "" {
		if len(f.discounts)+len(f.bonuses)+len(f.grants) > 0 || f.benefitsLine != "" {
			return nil, usageError(errors.New("--benefits-file cannot be combined with the other benefit flags"))
		}
		var raw []byte
		var err error
		if f.benefitsFile == "-" {
			raw, err = env.IO.ReadAll(maxBenefitsFile)
		} else {
			raw, err = readFileCapped(f.benefitsFile, maxBenefitsFile)
		}
		if err != nil {
			return nil, fmt.Errorf("reading the benefits: %w", err)
		}
		var b admin.Benefits
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&b); err != nil {
			return nil, usageError(fmt.Errorf("the benefits file is not a benefits object: %w", err))
		}
		return &b, nil
	}

	b, err := query.ParseBenefits(f.benefitsLine)
	if err != nil {
		return nil, usageError(err)
	}
	for _, s := range f.discounts {
		d, err := query.ParseDiscount(s)
		if err != nil {
			return nil, usageError(err)
		}
		b.Discounts = append(b.Discounts, d)
	}
	for _, s := range f.bonuses {
		x, err := query.ParseBonus(s)
		if err != nil {
			return nil, usageError(err)
		}
		b.TopUps = append(b.TopUps, x)
	}
	for _, s := range f.grants {
		g, err := query.ParseGrant(s)
		if err != nil {
			return nil, usageError(err)
		}
		b.Limits = append(b.Limits, g)
	}
	return &b, nil
}

// window reads --starts, --ends and --for into the request's two fields.
//
// On creation a start of "now" is left out, so the server's own clock
// decides it; an edit has no such default, and sends this machine's now.
// An end of "never" is sent as zero, which is what the API reads it as.
func (f *promoFlags) window(cmd *cobra.Command, creating bool, now time.Time) (starts, ends *admin.Unix, err error) {
	if err := exclusive(cmd, "ends", "for"); err != nil {
		return nil, nil, err
	}
	base := now
	if cmd.Flags().Changed("starts") {
		t, err := query.ParseMoment(f.starts, now)
		if err != nil {
			return nil, nil, usageError(err)
		}
		if t.IsZero() {
			return nil, nil, usageError(errors.New("--starts cannot be never; stop it with --inactive or `stop` instead"))
		}
		base = t
		if !(creating && strings.EqualFold(strings.TrimSpace(f.starts), "now")) {
			starts = admin.Ptr(admin.UnixOf(t))
		}
	}
	switch {
	case cmd.Flags().Changed("ends"):
		t, err := query.ParseMoment(f.ends, now)
		if err != nil {
			return nil, nil, usageError(err)
		}
		ends = admin.Ptr(admin.UnixOf(t))
		if !t.IsZero() && !t.After(base) {
			return nil, nil, usageError(errors.New("--ends is not after the start"))
		}
	case cmd.Flags().Changed("for"):
		secs, err := query.Seconds(f.dur)
		if err != nil || secs == 0 {
			return nil, nil, usageError(fmt.Errorf("invalid --for %q: write a span such as 7d or 36h", f.dur))
		}
		ends = admin.Ptr(admin.UnixOf(base.Add(time.Duration(secs) * time.Second)))
	}
	return starts, ends, nil
}

// optional returns a pointer to the flag's value when the flag was given.
func optional(cmd *cobra.Command, name, value string) *string {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	return &value
}

// dryRun prints the body a request would carry.
func dryRun(env *Env, method, path string, body any) error {
	env.Printer().Info("%s %s", method, path)
	return env.Printer().JSON(body)
}

// ── Campaigns ──────────────────────────────────────────────────────────────

func newAdminCampaignsCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "campaigns",
		Aliases: []string{"campaign"},
		Short:   "Create and manage promotion campaigns",
		Long: `Campaigns apply to everybody while they run. An offer is a standing campaign;
an event announces itself to every account when it starts.`,
	}
	cmd.AddCommand(
		newCampaignListCmd(env),
		newCampaignViewCmd(env),
		newCampaignCreateCmd(env),
		newCampaignEditCmd(env),
		newCampaignActiveCmd(env, "stop", false),
		newCampaignActiveCmd(env, "start", true),
		newCampaignDeleteCmd(env),
	)
	return cmd
}

func newCampaignListCmd(env *Env) *cobra.Command {
	var (
		kind          string
		all           bool
		limit, offset int
	)
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List campaigns",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if kind != "" && kind != admin.KindOffer && kind != admin.KindEvent {
				return usageError(fmt.Errorf("invalid --kind %q: use offer or event", kind))
			}
			if limit < 0 || limit > 500 || offset < 0 {
				return usageError(errors.New("--limit is 1 to 500 and --offset is not negative"))
			}
			c, _, err := env.AdminClient()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			list, _, err := c.Promo.Campaigns(ctx, &admin.CampaignListRequest{Kind: kind, Inactive: all, Limit: limit, Offset: offset})
			if err != nil {
				return err
			}
			p := env.Printer()
			if list == nil {
				list = []admin.Campaign{}
			}
			return p.Result(list, func() error {
				if len(list) == 0 {
					p.Info("No campaigns. Create one with `celadon admin campaigns create`.")
					return nil
				}
				rows := make([][]string, 0, len(list))
				for _, c := range list {
					rows = append(rows, []string{
						strconv.FormatInt(c.ID, 10), c.Kind, c.State, c.Name,
						window(c.StartsAt, c.EndsAt), fit(env, query.DescribeBenefits(c.Benefits), 90),
					})
				}
				p.Table([]string{"id", "kind", "state", "name", "runs", "grants"}, rows)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "", "only offers or only events")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "include stopped campaigns")
	cmd.Flags().IntVar(&limit, "limit", 0, "how many to list, up to 500 (default 50)")
	cmd.Flags().IntVar(&offset, "offset", 0, "how many to skip")
	_ = cmd.RegisterFlagCompletionFunc("kind", fixedCompletion(admin.KindOffer, admin.KindEvent))
	return cmd
}

// window renders when a promotion runs.
func window(starts, ends admin.Unix) string {
	from := "now"
	if starts != 0 {
		from = output.Time(starts.At())
	}
	to := "never"
	if ends != 0 {
		to = output.Time(ends.At())
	}
	return from + " → " + to
}

// fit cuts a cell to what the terminal leaves for it, when it is one.
func fit(env *Env, s string, reserve int) string {
	if w := env.IO.Width(); w > 0 {
		return output.Truncate(s, max(w-reserve, 20))
	}
	return s
}

func newCampaignViewCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "view <id>",
		Short: "Show one campaign",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID("campaign id", args[0])
			if err != nil {
				return err
			}
			c, _, err := env.AdminClient()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			camp, _, err := c.Promo.Campaign(ctx, id)
			if err != nil {
				return err
			}
			return env.Printer().Result(camp, func() error {
				printCampaign(env, camp)
				return nil
			})
		},
	}
}

func printCampaign(env *Env, c admin.Campaign) {
	p := env.Printer()
	p.Heading(fmt.Sprintf("%s (#%d)", c.Name, c.ID))
	announced := output.Dash
	if c.AnnouncedAt != 0 {
		announced = output.Time(c.AnnouncedAt.At())
	} else if c.Kind == admin.KindEvent {
		announced = "not yet"
	}
	p.Details([]output.KV{
		{Key: "Kind", Value: c.Kind},
		{Key: "State", Value: c.State},
		{Key: "Runs", Value: window(c.StartsAt, c.EndsAt)},
		{Key: "Announced", Value: announced},
		{Key: "Description", Value: output.Or(c.Description)},
		{Key: "Announcement", Value: output.Or(c.Announcement)},
		{Key: "Created", Value: output.Time(c.CreatedAt)},
		{Key: "Updated", Value: output.Time(c.UpdatedAt)},
	})
	printBenefits(env, c.Benefits)
}

func printBenefits(env *Env, b admin.Benefits) {
	p := env.Printer()
	p.Println()
	p.Heading("Grants")
	if b.Empty() {
		p.Println("  nothing")
		return
	}
	for _, item := range strings.Split(query.DescribeBenefits(b), " · ") {
		p.Println("  •", item)
	}
}

func newCampaignCreateCmd(env *Env) *cobra.Command {
	var (
		f            promoFlags
		kind         string
		announcement string
		inactive     bool
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a campaign",
		Long:  "Create a campaign. It starts now and never ends unless told otherwise.\n\n" + benefitsHelp,
		Example: `  celadon admin campaigns create --kind event --name "Summer week" \
    --starts 2026-10-01 --for 7d \
    --discount pro:25 --bonus 50:20 --grant free:search:daily=200 \
    --announcement "Tell a friend — the link in your profile pays you back."
  celadon admin campaigns create --name "Autumn offer" --discount all:10 --dry-run`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if kind != admin.KindOffer && kind != admin.KindEvent {
				return usageError(fmt.Errorf("invalid --kind %q: use offer or event", kind))
			}
			if strings.TrimSpace(f.name) == "" {
				return usageError(errors.New("a campaign needs a --name"))
			}
			b, err := f.benefits(env)
			if err != nil {
				return err
			}
			if b == nil || b.Empty() {
				return usageError(errors.New("a campaign has to grant something: give --discount, --bonus or --grant"))
			}
			starts, ends, err := f.window(cmd, true, adminNow(env))
			if err != nil {
				return err
			}
			req := admin.CampaignCreateRequest{
				Kind: kind, Name: strings.TrimSpace(f.name), Benefits: *b,
				Description:  optional(cmd, "description", f.description),
				Announcement: optional(cmd, "announcement", announcement),
				StartsAt:     starts, EndsAt: ends,
			}
			if inactive {
				req.Active = admin.Ptr(false)
			}
			if f.dryRun {
				return dryRun(env, "POST", "/api/admin/promo/campaigns", req)
			}
			c, _, err := env.AdminClient()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			camp, _, err := c.Promo.CreateCampaign(ctx, req)
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(camp, func() error {
				p.Success("Created campaign %q (#%d), %s", camp.Name, camp.ID, camp.State)
				return nil
			})
		},
	}
	f.register(cmd)
	cmd.Flags().StringVar(&kind, "kind", admin.KindOffer, "offer (quiet) or event (announced when it starts)")
	cmd.Flags().StringVar(&announcement, "announcement", "", "a line added to an event's announcement")
	cmd.Flags().BoolVar(&inactive, "inactive", false, "create it stopped")
	_ = cmd.RegisterFlagCompletionFunc("kind", fixedCompletion(admin.KindOffer, admin.KindEvent))
	return cmd
}

func newCampaignEditCmd(env *Env) *cobra.Command {
	var (
		f            promoFlags
		kind         string
		announcement string
		active       bool
	)
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Change a campaign",
		Long: "Change a campaign. Only what is given is written; benefit flags replace the\n" +
			"campaign's benefits as a whole. Moving the start of an event into the future\n" +
			"has it announce itself again when it arrives.\n\n" + benefitsHelp,
		Example: `  celadon admin campaigns edit 7 --ends +3d
  celadon admin campaigns edit 7 --discount pro:30 --discount go:15`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID("campaign id", args[0])
			if err != nil {
				return err
			}
			b, err := f.benefits(env)
			if err != nil {
				return err
			}
			starts, ends, err := f.window(cmd, false, adminNow(env))
			if err != nil {
				return err
			}
			req := admin.CampaignUpdateRequest{
				Kind:         optional(cmd, "kind", kind),
				Name:         optional(cmd, "name", strings.TrimSpace(f.name)),
				Description:  optional(cmd, "description", f.description),
				Announcement: optional(cmd, "announcement", announcement),
				Benefits:     b,
				StartsAt:     starts, EndsAt: ends,
			}
			if req.Kind != nil && *req.Kind != admin.KindOffer && *req.Kind != admin.KindEvent {
				return usageError(fmt.Errorf("invalid --kind %q: use offer or event", kind))
			}
			if cmd.Flags().Changed("active") {
				req.Active = &active
			}
			if req == (admin.CampaignUpdateRequest{}) {
				return usageError(errors.New("nothing to change: give at least one flag"))
			}
			if f.dryRun {
				return dryRun(env, "PATCH", fmt.Sprintf("/api/admin/promo/campaigns/%d", id), req)
			}
			c, _, err := env.AdminClient()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			camp, _, err := c.Promo.UpdateCampaign(ctx, id, req)
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(camp, func() error {
				p.Success("Updated campaign %q (#%d), %s", camp.Name, camp.ID, camp.State)
				return nil
			})
		},
	}
	f.register(cmd)
	cmd.Flags().StringVar(&kind, "kind", "", "offer or event")
	cmd.Flags().StringVar(&announcement, "announcement", "", "a line added to an event's announcement")
	cmd.Flags().BoolVar(&active, "active", true, "run it (true) or stop it (false)")
	return cmd
}

func newCampaignActiveCmd(env *Env, verb string, active bool) *cobra.Command {
	short := "Stop a campaign without deleting it"
	if active {
		short = "Start a stopped campaign again"
	}
	return &cobra.Command{
		Use:   verb + " <id>",
		Short: short,
		Long:  short + ". Its history and what it has granted stay.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID("campaign id", args[0])
			if err != nil {
				return err
			}
			c, _, err := env.AdminClient()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			camp, _, err := c.Promo.UpdateCampaign(ctx, id, admin.CampaignUpdateRequest{Active: admin.Ptr(active)})
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(camp, func() error {
				p.Success("Campaign %q (#%d) is %s", camp.Name, camp.ID, camp.State)
				return nil
			})
		},
	}
}

func newCampaignDeleteCmd(env *Env) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a campaign",
		Long:    "Delete a campaign for good. `stop` keeps it and its history instead.",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID("campaign id", args[0])
			if err != nil {
				return err
			}
			if err := confirm(env, yes, fmt.Sprintf("Delete campaign #%d for good?", id)); err != nil {
				return err
			}
			c, _, err := env.AdminClient()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			if _, err := c.Promo.DeleteCampaign(ctx, id); err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(map[string]any{"ok": true, "id": id}, func() error {
				p.Success("Deleted campaign #%d", id)
				return nil
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}

// ── Codes ──────────────────────────────────────────────────────────────────

func newAdminCodesCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "codes",
		Aliases: []string{"code"},
		Short:   "Issue and manage promo codes",
		Long: `Promo codes grant their benefits to whoever redeems them, for as long as the
code says. A code is looked up the way a user types it: "summer-25" finds
SUMMER25.`,
	}
	cmd.AddCommand(
		newCodeListCmd(env),
		newCodeViewCmd(env),
		newCodeCreateCmd(env),
		newCodeEditCmd(env),
		newCodeActiveCmd(env, "disable", false),
		newCodeActiveCmd(env, "enable", true),
		newCodeDeleteCmd(env),
		newCodeRedemptionsCmd(env),
	)
	return cmd
}

func newCodeListCmd(env *Env) *cobra.Command {
	var (
		all           bool
		limit, offset int
	)
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List promo codes",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit < 0 || limit > 500 || offset < 0 {
				return usageError(errors.New("--limit is 1 to 500 and --offset is not negative"))
			}
			c, _, err := env.AdminClient()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			list, _, err := c.Promo.Codes(ctx, &admin.CodeListRequest{Inactive: all, Limit: limit, Offset: offset})
			if err != nil {
				return err
			}
			if list == nil {
				list = []admin.Code{}
			}
			p := env.Printer()
			return p.Result(list, func() error {
				if len(list) == 0 {
					p.Info("No codes. Issue one with `celadon admin codes create`.")
					return nil
				}
				rows := make([][]string, 0, len(list))
				for _, c := range list {
					rows = append(rows, []string{
						c.Code, c.Name, redeemed(c), activeWord(c.Active), window(c.StartsAt, c.EndsAt),
						lasts(c.Duration), fit(env, query.DescribeBenefits(c.Benefits), 100),
					})
				}
				p.Table([]string{"code", "name", "redeemed", "state", "valid", "lasts", "grants"}, rows)
				return nil
			})
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "include disabled codes")
	cmd.Flags().IntVar(&limit, "limit", 0, "how many to list, up to 500 (default 50)")
	cmd.Flags().IntVar(&offset, "offset", 0, "how many to skip")
	return cmd
}

func redeemed(c admin.Code) string {
	if c.MaxRedemptions == 0 {
		return fmt.Sprintf("%d / ∞", c.Redemptions)
	}
	return fmt.Sprintf("%d / %d", c.Redemptions, c.MaxRedemptions)
}

func activeWord(active bool) string {
	if active {
		return "active"
	}
	return "disabled"
}

// lasts renders how long a code's benefits last once redeemed.
func lasts(secs int64) string {
	if secs == 0 {
		return "while valid"
	}
	return output.Seconds(secs)
}

func newCodeViewCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "view <code>",
		Short: "Show one promo code",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := env.AdminClient()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			code, _, err := c.Promo.Code(ctx, args[0])
			if err != nil {
				return err
			}
			return env.Printer().Result(code, func() error {
				printCode(env, code)
				return nil
			})
		},
	}
}

func printCode(env *Env, c admin.Code) {
	p := env.Printer()
	p.Heading(fmt.Sprintf("%s — %s", c.Code, c.Name))
	p.Details([]output.KV{
		{Key: "State", Value: activeWord(c.Active)},
		{Key: "Valid", Value: window(c.StartsAt, c.EndsAt)},
		{Key: "Redeemed", Value: redeemed(c)},
		{Key: "Lasts", Value: lasts(c.Duration) + " once redeemed"},
		{Key: "Description", Value: output.Or(c.Description)},
		{Key: "Created", Value: output.Time(c.CreatedAt)},
		{Key: "Updated", Value: output.Time(c.UpdatedAt)},
	})
	printBenefits(env, c.Benefits)
}

// codeFlags are what a code has beyond what every promotion has.
type codeFlags struct {
	max   int64
	lasts string
}

func (c *codeFlags) register(cmd *cobra.Command) {
	cmd.Flags().Int64Var(&c.max, "max", 0, "how many accounts may redeem it (0 for no cap)")
	cmd.Flags().StringVar(&c.lasts, "lasts", "", "how long the benefits last once redeemed: 30d, or 0 for as long as the code is valid")
}

func (c *codeFlags) apply(cmd *cobra.Command) (maxRed, duration *int64, err error) {
	if cmd.Flags().Changed("max") {
		if c.max < 0 {
			return nil, nil, usageError(errors.New("--max cannot be negative"))
		}
		maxRed = admin.Ptr(c.max)
	}
	if cmd.Flags().Changed("lasts") {
		secs, err := query.Seconds(c.lasts)
		if err != nil {
			return nil, nil, usageError(err)
		}
		duration = &secs
	}
	return maxRed, duration, nil
}

func newCodeCreateCmd(env *Env) *cobra.Command {
	var (
		f        promoFlags
		cf       codeFlags
		inactive bool
	)
	cmd := &cobra.Command{
		Use:   "create [code]",
		Short: "Issue a promo code",
		Long: "Issue a promo code. Leave the code out to have one drawn (ten characters\n" +
			"nobody misreads). It is valid from now, forever, for everybody, and its\n" +
			"benefits last as long as it does, unless told otherwise.\n\n" + benefitsHelp,
		Example: `  celadon admin codes create SUMMER25 --name "Summer sale" --max 100 --lasts 30d --discount all:25
  celadon admin codes create --name "Support goodwill" --grant all:search:daily=unlimited --lasts 7d`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(f.name) == "" {
				return usageError(errors.New("a code needs a --name"))
			}
			b, err := f.benefits(env)
			if err != nil {
				return err
			}
			if b == nil || b.Empty() {
				return usageError(errors.New("a code has to grant something: give --discount, --bonus or --grant"))
			}
			starts, ends, err := f.window(cmd, true, adminNow(env))
			if err != nil {
				return err
			}
			maxRed, duration, err := cf.apply(cmd)
			if err != nil {
				return err
			}
			req := admin.CodeCreateRequest{
				Name: strings.TrimSpace(f.name), Benefits: *b,
				Description: optional(cmd, "description", f.description),
				StartsAt:    starts, EndsAt: ends, MaxRedemptions: maxRed, Duration: duration,
			}
			if len(args) == 1 {
				req.Code = admin.Ptr(strings.TrimSpace(args[0]))
			}
			if inactive {
				req.Active = admin.Ptr(false)
			}
			if f.dryRun {
				return dryRun(env, "POST", "/api/admin/promo/codes", req)
			}
			c, _, err := env.AdminClient()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			code, _, err := c.Promo.CreateCode(ctx, req)
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(code, func() error {
				// The code itself is the result — a script may want it.
				p.Println(code.Code)
				p.Success("Issued %s (%s)", code.Code, code.Name)
				return nil
			})
		},
	}
	f.register(cmd)
	cf.register(cmd)
	cmd.Flags().BoolVar(&inactive, "inactive", false, "issue it disabled")
	return cmd
}

func newCodeEditCmd(env *Env) *cobra.Command {
	var (
		f      promoFlags
		cf     codeFlags
		rename string
		active bool
	)
	cmd := &cobra.Command{
		Use:   "edit <code>",
		Short: "Change a promo code",
		Long: "Change a promo code. Only what is given is written; benefit flags replace the\n" +
			"code's benefits as a whole. What was already redeemed stays as it was granted.\n\n" + benefitsHelp,
		Example: `  celadon admin codes edit SUMMER25 --max 200
  celadon admin codes edit SUMMER25 --rename AUTUMN25 --ends 2026-11-30`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := f.benefits(env)
			if err != nil {
				return err
			}
			starts, ends, err := f.window(cmd, false, adminNow(env))
			if err != nil {
				return err
			}
			maxRed, duration, err := cf.apply(cmd)
			if err != nil {
				return err
			}
			req := admin.CodeUpdateRequest{
				Code:        optional(cmd, "rename", strings.TrimSpace(rename)),
				Name:        optional(cmd, "name", strings.TrimSpace(f.name)),
				Description: optional(cmd, "description", f.description),
				Benefits:    b, StartsAt: starts, EndsAt: ends,
				MaxRedemptions: maxRed, Duration: duration,
			}
			if cmd.Flags().Changed("active") {
				req.Active = &active
			}
			if req == (admin.CodeUpdateRequest{}) {
				return usageError(errors.New("nothing to change: give at least one flag"))
			}
			if f.dryRun {
				return dryRun(env, "PATCH", "/api/admin/promo/codes/"+args[0], req)
			}
			c, _, err := env.AdminClient()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			code, _, err := c.Promo.UpdateCode(ctx, args[0], req)
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(code, func() error {
				p.Success("Updated %s (%s)", code.Code, code.Name)
				return nil
			})
		},
	}
	f.register(cmd)
	cf.register(cmd)
	cmd.Flags().StringVar(&rename, "rename", "", "a new code for it")
	cmd.Flags().BoolVar(&active, "active", true, "accept it (true) or refuse it (false)")
	return cmd
}

func newCodeActiveCmd(env *Env, verb string, active bool) *cobra.Command {
	short := "Stop accepting a promo code"
	if active {
		short = "Accept a disabled promo code again"
	}
	return &cobra.Command{
		Use:   verb + " <code>",
		Short: short,
		Long:  short + ". What it has already granted stays granted.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := env.AdminClient()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			code, _, err := c.Promo.UpdateCode(ctx, args[0], admin.CodeUpdateRequest{Active: admin.Ptr(active)})
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(code, func() error {
				p.Success("%s is %s", code.Code, activeWord(code.Active))
				return nil
			})
		},
	}
}

func newCodeDeleteCmd(env *Env) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <code>",
		Aliases: []string{"rm"},
		Short:   "Delete a promo code",
		Long:    "Delete a promo code for good. What it has already granted stays granted; `disable` keeps its history.",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := confirm(env, yes, fmt.Sprintf("Delete code %s for good?", args[0])); err != nil {
				return err
			}
			c, _, err := env.AdminClient()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			if _, err := c.Promo.DeleteCode(ctx, args[0]); err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(map[string]any{"ok": true, "code": args[0]}, func() error {
				p.Success("Deleted code %s", args[0])
				return nil
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}

func newCodeRedemptionsCmd(env *Env) *cobra.Command {
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "redemptions <code>",
		Short: "List the accounts that redeemed a code",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 0 || limit > 500 || offset < 0 {
				return usageError(errors.New("--limit is 1 to 500 and --offset is not negative"))
			}
			c, _, err := env.AdminClient()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			list, _, err := c.Promo.Redemptions(ctx, args[0], &admin.RedemptionListRequest{Limit: limit, Offset: offset})
			if err != nil {
				return err
			}
			if list == nil {
				list = []admin.Redemption{}
			}
			p := env.Printer()
			return p.Result(list, func() error {
				if len(list) == 0 {
					p.Info("Nobody has redeemed it yet.")
					return nil
				}
				now := env.now()
				rows := make([][]string, 0, len(list))
				for _, r := range list {
					expires := "with the code"
					if r.ExpiresAt != 0 {
						expires = output.Time(r.ExpiresAt.At())
						if r.ExpiresAt.At().Before(now) {
							expires += " (expired)"
						}
					}
					rows = append(rows, []string{strconv.FormatInt(r.AccountID, 10), output.Time(r.CreatedAt), expires})
				}
				p.Table([]string{"account", "redeemed", "expires"}, rows)
				return nil
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "how many to list, up to 500 (default 50)")
	cmd.Flags().IntVar(&offset, "offset", 0, "how many to skip")
	return cmd
}

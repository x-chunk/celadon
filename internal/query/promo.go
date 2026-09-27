package query

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/x-chunk/celadon/internal/admin"
)

// The promotion syntax, shared by the command line's flags and the admin
// interface's fields. What a promotion grants while it is in force:
//
//	discount pro:25          25% off Pro          (all:25 — every plan that is sold)
//	bonus    50:20           +20% on a $50 top-up (any:20 — every amount)
//	grant    free:search:daily=200                (all:…, and =unlimited for no ceiling)
//
// A whole set of benefits is those items joined with commas:
//
//	discount pro:25, bonus 50:20, grant free:search:daily=200
//
// The trial length an event offers belongs to its benefits too, but it is
// one number rather than a list, so it has a field and a flag of its own
// (ParseTrial) instead of a place in the line.
//
// What a promotion gives once — its gifts, the API's "grants" — is a line of
// its own, since it is a value of its own:
//
//	balance all:5            $5.00 to every plan  (pro:10 for Pro alone; ultra:0 leaves Ultra out)
//	plan    pro:30           30 days of Pro       (codes only)
//
//	balance all:1, balance pro:2, plan pro:30
//
// "none" is a line that grants nothing, which is how an edit empties one.
// Names are passed through as typed: the API checks them against the
// reference and says which one it does not know.

// anyWords are what "every plan" and "every amount" may be written as.
var anyWords = map[string]bool{"": true, "all": true, "any": true, "*": true, "every": true}

// ParseDiscount reads TIER:PERCENT.
func ParseDiscount(s string) (admin.Discount, error) {
	tier, pct, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok {
		return admin.Discount{}, fmt.Errorf("invalid discount %q: write TIER:PERCENT, e.g. pro:25 or all:10", s)
	}
	p, err := percent(pct)
	if err != nil {
		return admin.Discount{}, fmt.Errorf("invalid discount %q: %w", s, err)
	}
	return admin.Discount{Tier: tierOf(tier), Percent: p}, nil
}

// ParseBonus reads AMOUNT:PERCENT, the amount in dollars.
func ParseBonus(s string) (admin.TopUpBonus, error) {
	amount, pct, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok {
		return admin.TopUpBonus{}, fmt.Errorf("invalid bonus %q: write AMOUNT:PERCENT in dollars, e.g. 50:20 or any:10", s)
	}
	cents, err := dollars(amount)
	if err != nil {
		return admin.TopUpBonus{}, fmt.Errorf("invalid bonus %q: %w", s, err)
	}
	p, err := percent(pct)
	if err != nil {
		return admin.TopUpBonus{}, fmt.Errorf("invalid bonus %q: %w", s, err)
	}
	return admin.TopUpBonus{AmountCents: cents, Percent: p}, nil
}

// ParseGrant reads TIER:LIMIT=VALUE. The tier is what comes before the
// first colon, since a limit's own key carries one ("search:daily").
func ParseGrant(s string) (admin.LimitGrant, error) {
	s = strings.TrimSpace(s)
	head, value, ok := strings.Cut(s, "=")
	tier, limit, ok2 := strings.Cut(head, ":")
	if !ok || !ok2 || strings.TrimSpace(limit) == "" {
		return admin.LimitGrant{}, fmt.Errorf("invalid grant %q: write TIER:LIMIT=VALUE, e.g. free:search:daily=200 or all:messages:stored=unlimited", s)
	}
	v := strings.ToLower(strings.TrimSpace(value))
	var n int64
	switch v {
	case "unlimited", "∞", "-1", "inf":
		n = admin.Unlimited
	default:
		var err error
		n, err = strconv.ParseInt(strings.ReplaceAll(v, "_", ""), 10, 64)
		if err != nil || n <= 0 {
			return admin.LimitGrant{}, fmt.Errorf("invalid grant %q: the value is a positive number or unlimited", s)
		}
	}
	return admin.LimitGrant{Tier: tierOf(tier), Limit: strings.ToLower(strings.TrimSpace(limit)), Value: n}, nil
}

// ParseBenefits reads a comma-separated set of benefits. An empty line, or
// "none", is no benefits at all.
func ParseBenefits(line string) (admin.Benefits, error) {
	var b admin.Benefits
	if isNone(line) {
		return b, nil
	}
	for item := range strings.SplitSeq(line, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		word, spec, _ := strings.Cut(item, " ")
		spec = strings.TrimSpace(spec)
		switch strings.ToLower(word) {
		case "discount", "off":
			d, err := ParseDiscount(spec)
			if err != nil {
				return b, err
			}
			b.Discounts = append(b.Discounts, d)
		case "bonus", "topup":
			x, err := ParseBonus(spec)
			if err != nil {
				return b, err
			}
			b.TopUps = append(b.TopUps, x)
		case "grant", "limit":
			g, err := ParseGrant(spec)
			if err != nil {
				return b, err
			}
			b.Limits = append(b.Limits, g)
		default:
			return b, fmt.Errorf("invalid benefit %q: start it with discount, bonus or grant", item)
		}
	}
	return b, nil
}

// FormatBenefits writes benefits in the syntax ParseBenefits reads, so a
// form can be filled with what a promotion already grants. The trial length
// is not part of the line; FormatTrial writes it.
func FormatBenefits(b admin.Benefits) string {
	var items []string
	for _, d := range b.Discounts {
		items = append(items, fmt.Sprintf("discount %s:%d", tierName(d.Tier), d.Percent))
	}
	for _, x := range b.TopUps {
		items = append(items, fmt.Sprintf("bonus %s:%d", amountName(x.AmountCents), x.Percent))
	}
	for _, g := range b.Limits {
		items = append(items, fmt.Sprintf("grant %s:%s=%s", tierName(g.Tier), g.Limit, valueName(g.Value)))
	}
	return strings.Join(items, ", ")
}

// DescribeBenefits says what benefits grant, for a person: "−25% Pro ·
// +20% on $50.00 · search:daily → 200 (free)".
func DescribeBenefits(b admin.Benefits) string {
	var items []string
	for _, d := range b.Discounts {
		items = append(items, fmt.Sprintf("−%d%% %s", d.Percent, planWord(d.Tier)))
	}
	for _, x := range b.TopUps {
		on := "every top-up"
		if x.AmountCents != admin.AnyAmount {
			on = "a " + centsString(x.AmountCents) + " top-up"
		}
		items = append(items, fmt.Sprintf("+%d%% on %s", x.Percent, on))
	}
	for _, g := range b.Limits {
		items = append(items, fmt.Sprintf("%s → %s (%s)", g.Limit, valueName(g.Value), planWord(g.Tier)))
	}
	if b.TrialDays > 0 {
		items = append(items, fmt.Sprintf("a %d-day trial", b.TrialDays))
	}
	if len(items) == 0 {
		return "nothing"
	}
	return strings.Join(items, " · ")
}

// ParseTrial reads the trial length an event offers, in days: "30" or
// "30d". "0", "off" and "none" leave the trial as it is. Which lengths are
// offered is the API's to say (Reference.TrialDays).
func ParseTrial(s string) (int, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", "0", "off", "none", "no":
		return 0, nil
	}
	days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
	if err != nil || days <= 0 {
		return 0, fmt.Errorf("invalid trial %q: write a number of days, e.g. 30, or off", s)
	}
	return days, nil
}

// FormatTrial writes a trial length the way ParseTrial reads it back.
func FormatTrial(days int) string {
	if days == 0 {
		return "off"
	}
	return strconv.Itoa(days)
}

// ParseBalanceGift reads TIER:AMOUNT, the amount in dollars, into g. A plan
// may be named once; zero leaves it out of a gift written for every plan.
func ParseBalanceGift(g *admin.Grants, s string) error {
	tier, amount, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok {
		return fmt.Errorf("invalid balance gift %q: write TIER:AMOUNT in dollars, e.g. all:5 or pro:10", s)
	}
	cents, err := giftDollars(amount)
	if err != nil {
		return fmt.Errorf("invalid balance gift %q: %w", s, err)
	}
	t := tierOf(tier)
	if _, dup := g.BalanceCents[t]; dup {
		return fmt.Errorf("invalid balance gift %q: %s is given a balance twice", s, planWord(t))
	}
	if g.BalanceCents == nil {
		g.BalanceCents = map[string]int64{}
	}
	g.BalanceCents[t] = cents
	return nil
}

// ParsePlanGift reads TIER:DAYS, a term of a paid plan, into g. A promotion
// gives one plan at most.
func ParsePlanGift(g *admin.Grants, s string) error {
	tier, days, ok := strings.Cut(strings.TrimSpace(s), ":")
	tier = strings.ToLower(strings.TrimSpace(tier))
	if !ok || anyWords[tier] {
		return fmt.Errorf("invalid plan gift %q: write TIER:DAYS, e.g. pro:30", s)
	}
	n, err := strconv.Atoi(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(days)), "d"))
	if err != nil || n <= 0 {
		return fmt.Errorf("invalid plan gift %q: the term is a positive number of days", s)
	}
	if g.Subscription != nil {
		return fmt.Errorf("invalid plan gift %q: a promotion gives one plan at most", s)
	}
	g.Subscription = &admin.SubscriptionGrant{Tier: tier, Days: n}
	return nil
}

// ParseGifts reads a comma-separated set of gifts. An empty line, or "none",
// gives nothing.
func ParseGifts(line string) (admin.Grants, error) {
	var g admin.Grants
	if isNone(line) {
		return g, nil
	}
	for item := range strings.SplitSeq(line, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		word, spec, _ := strings.Cut(item, " ")
		spec = strings.TrimSpace(spec)
		var err error
		switch strings.ToLower(word) {
		case "balance", "credit":
			err = ParseBalanceGift(&g, spec)
		case "plan", "subscription":
			err = ParsePlanGift(&g, spec)
		default:
			err = fmt.Errorf("invalid gift %q: start it with balance or plan", item)
		}
		if err != nil {
			return g, err
		}
	}
	return g, nil
}

// FormatGifts writes gifts in the syntax ParseGifts reads, every plan first
// and the rest by name, so the same gifts are always written the same way.
func FormatGifts(g admin.Grants) string {
	var items []string
	for _, tier := range balanceTiers(g) {
		items = append(items, fmt.Sprintf("balance %s:%s", tierName(tier), giftAmountName(g.BalanceCents[tier])))
	}
	if s := g.Subscription; s != nil {
		items = append(items, fmt.Sprintf("plan %s:%d", s.Tier, s.Days))
	}
	return strings.Join(items, ", ")
}

// DescribeGifts says what gifts give, for a person: "+$1.00 balance (every
// other plan) · +$2.00 balance (pro) · 30 days of pro".
func DescribeGifts(g admin.Grants) string {
	var items []string
	_, blanket := g.BalanceCents[admin.AnyTier]
	for _, tier := range balanceTiers(g) {
		who := planWord(tier)
		if tier == admin.AnyTier && len(g.BalanceCents) > 1 {
			who = "every other plan"
		}
		cents := g.BalanceCents[tier]
		switch {
		case cents > 0:
			items = append(items, fmt.Sprintf("+%s balance (%s)", centsString(cents), who))
		case blanket:
			// A zero is only worth saying where it carves a plan out of a
			// gift to everybody; alone it is the absence of a gift.
			items = append(items, fmt.Sprintf("no balance (%s)", who))
		}
	}
	if s := g.Subscription; s != nil {
		items = append(items, fmt.Sprintf("%d days of %s", s.Days, s.Tier))
	}
	if len(items) == 0 {
		return "nothing"
	}
	return strings.Join(items, " · ")
}

// DescribePromotion says everything a promotion gives, in force and once, on
// one line.
func DescribePromotion(b admin.Benefits, g admin.Grants) string {
	var parts []string
	for _, desc := range []string{DescribeBenefits(b), DescribeGifts(g)} {
		if desc != "nothing" {
			parts = append(parts, desc)
		}
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, " · ")
}

// balanceTiers are the plans a balance gift names, every plan first and the
// rest by name.
func balanceTiers(g admin.Grants) []string {
	return slices.SortedFunc(maps.Keys(g.BalanceCents), func(a, b string) int {
		if (a == admin.AnyTier) != (b == admin.AnyTier) {
			if a == admin.AnyTier {
				return -1
			}
			return 1
		}
		return cmp.Compare(a, b)
	})
}

// isNone reports whether a line says, in so many words, that it grants
// nothing.
func isNone(line string) bool {
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "none", "nothing":
		return true
	}
	return false
}

// ParseMoment reads a point in time as a person types one: "now", a date
// ("2026-10-01", midnight local time), a local date and time
// ("2026-10-01 18:00" or with a T), RFC 3339, or a span from now ("+7d",
// "in 12h"). "never", "none" and "0" are the zero time, which is what the
// API reads as "never" for an end.
func ParseMoment(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "now":
		return now, nil
	case "never", "none", "0", "":
		return time.Time{}, nil
	}
	lower := strings.ToLower(s)
	if span, ok := strings.CutPrefix(lower, "+"); ok {
		return fromNow(span, now, s)
	}
	if span, ok := strings.CutPrefix(lower, "in "); ok {
		return fromNow(span, now, s)
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, now.Location()); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time %q: use now, 2026-10-01, \"2026-10-01 18:00\", RFC 3339, +7d or never", s)
}

func fromNow(span string, now time.Time, raw string) (time.Time, error) {
	secs, err := Seconds(span)
	if err != nil || secs == 0 {
		return time.Time{}, fmt.Errorf("invalid time %q: a span is written like +7d, +12h or +90m", raw)
	}
	return now.Add(time.Duration(secs) * time.Second), nil
}

func percent(s string) (int, error) {
	s = strings.TrimSuffix(strings.TrimSpace(s), "%")
	p, err := strconv.Atoi(s)
	if err != nil || p <= 0 {
		return 0, errors.New("the percentage is a positive whole number")
	}
	return p, nil
}

func dollars(s string) (int64, error) {
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "$")
	if anyWords[s] {
		return admin.AnyAmount, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || !(f > 0) || math.IsInf(f, 0) {
		return 0, errors.New("the amount is in dollars, e.g. 50 or 9.99, or any")
	}
	cents := math.Round(f * 100)
	if math.Abs(f*100-cents) > 1e-6 {
		return 0, errors.New("the amount has more than two decimals")
	}
	return int64(cents), nil
}

// giftDollars reads the amount of a balance gift: dollars with at most two
// decimals, and zero allowed, since zero is how a plan is left out.
func giftDollars(s string) (int64, error) {
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "$")
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || !(f >= 0) || math.IsInf(f, 0) {
		return 0, errors.New("the amount is in dollars, e.g. 5 or 2.50, and 0 leaves the plan out")
	}
	cents := math.Round(f * 100)
	if math.Abs(f*100-cents) > 1e-6 {
		return 0, errors.New("the amount has more than two decimals")
	}
	if cents > math.MaxInt64/2 {
		return 0, errors.New("the amount is too large")
	}
	return int64(cents), nil
}

func tierOf(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if anyWords[s] {
		return admin.AnyTier
	}
	return s
}

func tierName(t string) string {
	if t == admin.AnyTier {
		return "all"
	}
	return t
}

func planWord(t string) string {
	if t == admin.AnyTier {
		return "every plan"
	}
	return t
}

func amountName(cents int64) string {
	if cents == admin.AnyAmount {
		return "any"
	}
	if cents%100 == 0 {
		return strconv.FormatInt(cents/100, 10)
	}
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}

// giftAmountName is amountName for a gift, where zero is an amount — the
// one that leaves a plan out — rather than "any".
func giftAmountName(cents int64) string {
	if cents == 0 {
		return "0"
	}
	return amountName(cents)
}

func valueName(v int64) string {
	if v == admin.Unlimited {
		return "unlimited"
	}
	return strconv.FormatInt(v, 10)
}

func centsString(c int64) string { return fmt.Sprintf("$%d.%02d", c/100, c%100) }

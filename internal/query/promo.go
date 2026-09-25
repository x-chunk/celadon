package query

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/x-chunk/celadon/internal/admin"
)

// The promotion syntax, shared by the command line's flags and the admin
// interface's fields:
//
//	discount pro:25          25% off Pro          (all:25 — every plan that is sold)
//	bonus    50:20           +20% on a $50 top-up (any:20 — every amount)
//	grant    free:search:daily=200                (all:…, and =unlimited for no ceiling)
//
// A whole set of benefits is those items joined with commas:
//
//	discount pro:25, bonus 50:20, grant free:search:daily=200
//
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

// ParseBenefits reads a comma-separated set of benefits. An empty line is no
// benefits at all.
func ParseBenefits(line string) (admin.Benefits, error) {
	var b admin.Benefits
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
// form can be filled with what a promotion already grants.
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
	if len(items) == 0 {
		return "nothing"
	}
	return strings.Join(items, " · ")
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
	if err != nil || f <= 0 || math.IsInf(f, 0) {
		return 0, errors.New("the amount is in dollars, e.g. 50 or 9.99, or any")
	}
	cents := math.Round(f * 100)
	if math.Abs(f*100-cents) > 1e-6 {
		return 0, errors.New("the amount has more than two decimals")
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

func valueName(v int64) string {
	if v == admin.Unlimited {
		return "unlimited"
	}
	return strconv.FormatInt(v, 10)
}

func centsString(c int64) string { return fmt.Sprintf("$%d.%02d", c/100, c%100) }

package query

import (
	"reflect"
	"testing"
	"time"

	"github.com/x-chunk/celadon/internal/admin"
)

func TestParseDiscount(t *testing.T) {
	for in, want := range map[string]admin.Discount{
		"pro:25":   {Tier: "pro", Percent: 25},
		"Pro:25%":  {Tier: "pro", Percent: 25},
		"all:10":   {Tier: admin.AnyTier, Percent: 10},
		"*:10":     {Tier: admin.AnyTier, Percent: 10},
		":5":       {Tier: admin.AnyTier, Percent: 5},
		" go : 30": {Tier: "go", Percent: 30},
	} {
		got, err := ParseDiscount(in)
		if err != nil || got != want {
			t.Errorf("ParseDiscount(%q) = %+v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"pro", "pro:", "pro:x", "pro:-5", "pro:0"} {
		if _, err := ParseDiscount(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestParseBonus(t *testing.T) {
	for in, want := range map[string]admin.TopUpBonus{
		"50:20":    {AmountCents: 5000, Percent: 20},
		"$9.99:5":  {AmountCents: 999, Percent: 5},
		"any:10":   {AmountCents: admin.AnyAmount, Percent: 10},
		"12.5:100": {AmountCents: 1250, Percent: 100},
	} {
		got, err := ParseBonus(in)
		if err != nil || got != want {
			t.Errorf("ParseBonus(%q) = %+v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"50", "x:20", "-5:20", "1.234:5", "50:0", "0:5"} {
		if _, err := ParseBonus(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestParseGrant(t *testing.T) {
	for in, want := range map[string]admin.LimitGrant{
		"free:search:daily=200":         {Tier: "free", Limit: "search:daily", Value: 200},
		"all:messages:stored=unlimited": {Tier: admin.AnyTier, Limit: "messages:stored", Value: admin.Unlimited},
		"pro:exports:weekly=1_000":      {Tier: "pro", Limit: "exports:weekly", Value: 1000},
		"Go : Search:Daily = -1":        {Tier: "go", Limit: "search:daily", Value: admin.Unlimited},
	} {
		got, err := ParseGrant(in)
		if err != nil || got != want {
			t.Errorf("ParseGrant(%q) = %+v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"free=200", "free:=200", "free:search:daily", "free:search:daily=0", "free:search:daily=x"} {
		if _, err := ParseGrant(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestBenefitsRoundTrip(t *testing.T) {
	line := "discount pro:25, bonus 50:20, bonus any:5, grant free:search:daily=200, grant all:messages:stored=unlimited, discount all:10"
	b, err := ParseBenefits(line)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Discounts) != 2 || len(b.TopUps) != 2 || len(b.Limits) != 2 {
		t.Fatalf("benefits = %+v", b)
	}
	again, err := ParseBenefits(FormatBenefits(b))
	if err != nil || !reflect.DeepEqual(again, b) {
		t.Errorf("round trip: %q → %+v, %v", FormatBenefits(b), again, err)
	}
	if empty, err := ParseBenefits("  "); err != nil || !empty.Empty() {
		t.Errorf("an empty line = %+v, %v", empty, err)
	}
	if _, err := ParseBenefits("gift pro:25"); err == nil {
		t.Error("an unknown benefit was accepted")
	}
	if got := DescribeBenefits(b); got == "" || got == "nothing" {
		t.Errorf("DescribeBenefits = %q", got)
	}
	if DescribeBenefits(admin.Benefits{}) != "nothing" {
		t.Error("empty benefits are not described as nothing")
	}
}

func TestParseMoment(t *testing.T) {
	loc := time.FixedZone("UTC+3", 3*3600)
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, loc)
	cases := map[string]time.Time{
		"now":                  now,
		"never":                {},
		"":                     {},
		"+7d":                  now.Add(7 * 24 * time.Hour),
		"in 12h":               now.Add(12 * time.Hour),
		"2026-10-01":           time.Date(2026, 10, 1, 0, 0, 0, 0, loc),
		"2026-10-01 18:00":     time.Date(2026, 10, 1, 18, 0, 0, 0, loc),
		"2026-10-01T18:00":     time.Date(2026, 10, 1, 18, 0, 0, 0, loc),
		"2026-10-01T18:00:00Z": time.Date(2026, 10, 1, 18, 0, 0, 0, time.UTC),
	}
	for in, want := range cases {
		got, err := ParseMoment(in, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("ParseMoment(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"tomorrow", "+soon", "+0", "2026-13-01"} {
		if _, err := ParseMoment(bad, now); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestParseTrial(t *testing.T) {
	for in, want := range map[string]int{"30": 30, "90d": 90, " 14 ": 14, "off": 0, "0": 0, "": 0, "none": 0} {
		got, err := ParseTrial(in)
		if err != nil || got != want {
			t.Errorf("ParseTrial(%q) = %d, %v", in, got, err)
		}
	}
	for _, bad := range []string{"-30", "a month", "30w"} {
		if _, err := ParseTrial(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	if FormatTrial(0) != "off" || FormatTrial(30) != "30" {
		t.Error("FormatTrial does not write what ParseTrial reads")
	}
}

func TestParseGifts(t *testing.T) {
	g, err := ParseGifts("balance all:1, balance Pro:2.50, balance ultra:0, plan pro:30d")
	if err != nil {
		t.Fatal(err)
	}
	want := admin.Grants{
		BalanceCents: map[string]int64{admin.AnyTier: 100, "pro": 250, "ultra": 0},
		Subscription: &admin.SubscriptionGrant{Tier: "pro", Days: 30},
	}
	if !reflect.DeepEqual(g, want) {
		t.Fatalf("ParseGifts = %+v", g)
	}
	line := FormatGifts(g)
	if line != "balance all:1, balance pro:2.50, balance ultra:0, plan pro:30" {
		t.Errorf("FormatGifts = %q", line)
	}
	if again, err := ParseGifts(line); err != nil || !reflect.DeepEqual(again, g) {
		t.Errorf("round trip: %q → %+v, %v", line, again, err)
	}
	if got := DescribeGifts(g); got != "+$1.00 balance (every other plan) · +$2.50 balance (pro) · no balance (ultra) · 30 days of pro" {
		t.Errorf("DescribeGifts = %q", got)
	}
	if got := DescribeGifts(admin.Grants{BalanceCents: map[string]int64{"": 500}}); got != "+$5.00 balance (every plan)" {
		t.Errorf("DescribeGifts of a blanket gift = %q", got)
	}

	for _, none := range []string{"", "  ", "none", "None"} {
		if g, err := ParseGifts(none); err != nil || !reflect.DeepEqual(g, admin.Grants{}) {
			t.Errorf("ParseGifts(%q) = %+v, %v", none, g, err)
		}
	}
	for _, bad := range []string{
		"balance pro",                  // no amount
		"balance pro:-1",               // negative
		"balance pro:1.234",            // more than cents
		"balance pro:nan",              // not a number
		"balance pro:1, balance PRO:2", // one plan twice
		"balance all:1, balance any:2", // every plan twice
		"plan pro",                     // no term
		"plan all:30",                  // a plan has to be named
		"plan pro:0",                   // no term at all
		"plan pro:30, plan go:7",       // two plans
		"gift pro:30",                  // unknown word
	} {
		if _, err := ParseGifts(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestDescribePromotion(t *testing.T) {
	b := admin.Benefits{Discounts: []admin.Discount{{Tier: "pro", Percent: 25}}, TrialDays: 30}
	if got := DescribeBenefits(b); got != "−25% pro · a 30-day trial" {
		t.Errorf("DescribeBenefits = %q", got)
	}
	g := admin.Grants{BalanceCents: map[string]int64{"": 100}}
	if got := DescribePromotion(b, g); got != "−25% pro · a 30-day trial · +$1.00 balance (every plan)" {
		t.Errorf("DescribePromotion = %q", got)
	}
	if got := DescribePromotion(admin.Benefits{}, g); got != "+$1.00 balance (every plan)" {
		t.Errorf("DescribePromotion of gifts alone = %q", got)
	}
	if DescribePromotion(admin.Benefits{}, admin.Grants{}) != "nothing" {
		t.Error("a promotion giving nothing is not described as nothing")
	}
	if b, err := ParseBenefits("none"); err != nil || !b.Empty() {
		t.Errorf("ParseBenefits(none) = %+v, %v", b, err)
	}
}

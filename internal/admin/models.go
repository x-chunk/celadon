package admin

import (
	"net/url"
	"strconv"
	"time"
)

// The payload types of the promotions API, and one request type per call.
//
// The conventions are teal's. Required fields are values and optional ones
// pointers with omitempty, so that leaving a field alone and setting it to
// its zero value are different things — it matters wherever zero means
// something: an ends_at of zero is "never", and a max_redemptions of zero is
// "no cap". Timestamps are Unix seconds, kept as Unix with an At() helper.

// Unix is a timestamp sent as seconds since the epoch. Zero is no moment at
// all: "now" for a start, "never" for an end.
type Unix int64

// At renders the timestamp as a time.Time in UTC, or the zero time.
func (u Unix) At() time.Time {
	if u == 0 {
		return time.Time{}
	}
	return time.Unix(int64(u), 0).UTC()
}

// UnixOf is t as Unix seconds, or zero for the zero time.
func UnixOf(t time.Time) Unix {
	if t.IsZero() {
		return 0
	}
	return Unix(t.Unix())
}

// Ptr returns a pointer to v, for the optional fields of a request.
func Ptr[T any](v T) *T { return &v }

// --- Benefits ---------------------------------------------------------------

// AnyTier is the tier of a benefit that applies to every plan: a discount on
// every plan that is sold, a raised ceiling on every plan including Free. In
// Grants.BalanceCents it is the key for every plan not named. The reference
// says so too, in Reference.AnyTier.
const AnyTier = ""

// AnyAmount is the amount of a top-up bonus that applies to every amount the
// store offers.
const AnyAmount int64 = 0

// Unlimited is the value a limit grant carries to mean "no ceiling". The
// reference says so too, in Reference.Unlimited.
const Unlimited int64 = -1

// Benefits is everything one promotion grants while it is in force. A
// promotion that grants nothing — neither here nor in its Grants — is
// refused.
type Benefits struct {
	Discounts []Discount   `json:"discounts,omitempty"`
	TopUps    []TopUpBonus `json:"top_up_bonuses,omitempty"`
	Limits    []LimitGrant `json:"limits,omitempty"`
	// TrialDays is how long the free trial lasts while the promotion is in
	// force, one of Reference.TrialDays; zero leaves it as it is. Events
	// only.
	TrialDays int `json:"trial_days,omitempty"`
}

// Empty reports whether the benefits grant nothing.
func (b Benefits) Empty() bool {
	return len(b.Discounts) == 0 && len(b.TopUps) == 0 && len(b.Limits) == 0 && b.TrialDays == 0
}

// Discount is a percentage off one plan's list price.
type Discount struct {
	Tier    string `json:"tier"`    // "pro", …, or AnyTier
	Percent int    `json:"percent"` // 1 to Reference.MaxDiscountPercent
}

// TopUpBonus is a percentage added to the balance a top-up buys.
type TopUpBonus struct {
	AmountCents int64 `json:"amount_cents"` // one of Reference.TopUpAmountsCents, or AnyAmount
	Percent     int   `json:"percent"`      // 1 to Reference.MaxBonusPercent
}

// LimitGrant raises one ceiling above what a plan grants. It never lowers one.
type LimitGrant struct {
	Tier  string `json:"tier"`  // or AnyTier
	Limit string `json:"limit"` // a Reference.Limits key: "search:daily", …
	Value int64  `json:"value"` // a positive ceiling, or Unlimited
}

// --- Grants -----------------------------------------------------------------

// Grants is what a promotion gives once: when a code is redeemed, or when an
// event reaches an account. Unlike Benefits it does not end with the
// promotion — a gift, once given, stays.
//
// An offer gives nothing once, an event gives only a balance, and a code
// gives a balance, a subscription term or both. What an event gives is
// frozen from its first announcement on (Campaign.Frozen).
type Grants struct {
	// BalanceCents is the credit, in cents, per plan the account is on at the
	// moment of the gift, keyed by the plan's name or AnyTier for every plan
	// not named. A plan named explicitly wins over AnyTier, and zero leaves
	// it out: {"": 100, "ultra": 0} gives $1.00 to everybody but Ultra.
	BalanceCents map[string]int64 `json:"balance_cents,omitempty"`
	// Subscription is a term of a paid plan. Codes only.
	Subscription *SubscriptionGrant `json:"subscription,omitempty"`
}

// SubscriptionGrant is a term of one paid plan.
type SubscriptionGrant struct {
	Tier string `json:"tier"` // "go", "pro", "ultra": a paid Reference.Tiers entry
	Days int    `json:"days"` // one of Reference.SubscriptionDays
}

// Empty reports whether the grants give nothing: no positive balance for any
// plan and no subscription.
func (g Grants) Empty() bool {
	if g.Subscription != nil {
		return false
	}
	for _, cents := range g.BalanceCents {
		if cents > 0 {
			return false
		}
	}
	return true
}

// --- Reference --------------------------------------------------------------

// Reference is what a promotion is written against: the plans, the quotas
// and the top-up amounts, by the names the API accepts them under.
type Reference struct {
	Tiers              []ReferenceTier  `json:"tiers"`
	Limits             []ReferenceLimit `json:"limits"`
	TopUpAmountsCents  []int64          `json:"top_up_amounts_cents"`
	CampaignKinds      []string         `json:"campaign_kinds"`
	MaxDiscountPercent int              `json:"max_discount_percent"`
	MaxBonusPercent    int              `json:"max_bonus_percent"`
	Unlimited          int64            `json:"unlimited"`
	// MaxGiftCents is the most one balance gift may credit one account.
	MaxGiftCents int64 `json:"max_gift_cents"`
	// SubscriptionDays are the terms a code may give, in days.
	SubscriptionDays []int `json:"subscription_days"`
	// TrialDays are the trial lengths an event may offer, in days.
	TrialDays []int `json:"trial_days"`
	// AnyTier is the Grants.BalanceCents key for every plan not named.
	AnyTier string `json:"any_tier"`
}

// ReferenceTier is one plan. Only a paid one can be discounted.
type ReferenceTier struct {
	Tier       string `json:"tier"`
	Name       string `json:"name"`
	PriceCents int64  `json:"price_cents"`
	Paid       bool   `json:"paid"`
}

// ReferenceLimit is one quota a promotion may raise.
type ReferenceLimit struct {
	Key   string `json:"limit"`
	Label string `json:"label"`
	Unit  string `json:"unit"`
}

// --- Campaigns --------------------------------------------------------------

// Campaign kinds.
const (
	// KindOffer is a standing campaign nobody is told about.
	KindOffer = "offer"
	// KindEvent is a campaign that announces itself to every account when
	// it starts.
	KindEvent = "event"
)

// Campaign states. The state is computed on every request, never stored.
const (
	StateScheduled = "scheduled"
	StateRunning   = "running"
	StateEnded     = "ended"
	StateStopped   = "stopped"
)

// Campaign is one campaign.
type Campaign struct {
	ID           int64    `json:"id"`
	Kind         string   `json:"kind"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Announcement string   `json:"announcement"`
	Benefits     Benefits `json:"benefits"`
	// Grants is what an event gives once to every account it reaches.
	Grants   Grants `json:"grants"`
	StartsAt Unix   `json:"starts_at"`
	EndsAt   Unix   `json:"ends_at"`
	// AnnouncedAt is when an event's current announcement went out, zero
	// until it has. Moving the start into the future clears it, and the
	// event announces itself again when it arrives.
	AnnouncedAt Unix `json:"announced_at"`
	// FirstAnnouncedAt is when the event was first announced, zero when it
	// never was. Nothing clears it: from then on its grants and its trial
	// length cannot change.
	FirstAnnouncedAt Unix `json:"first_announced_at"`
	// Generation counts the event's announcements; each reschedule into the
	// future makes the next one a new generation.
	Generation int64 `json:"generation"`
	Active     bool  `json:"active"`
	// State is where it stands as of the request: StateScheduled,
	// StateRunning, StateEnded or StateStopped.
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Frozen reports whether what the campaign gives once and the trial it
// offers can no longer be changed: an event that has been announced.
func (c Campaign) Frozen() bool { return c.FirstAnnouncedAt != 0 }

// CampaignListRequest narrows GET /admin/promo/campaigns. A nil request
// lists the active campaigns of both kinds, fifty at a time.
type CampaignListRequest struct {
	// Kind is KindOffer or KindEvent; empty for both.
	Kind string
	// Inactive includes the campaigns that were stopped.
	Inactive bool
	// Limit is how many to return, up to 500. Zero is the API's 50.
	Limit int
	// Offset skips that many.
	Offset int
}

func (r *CampaignListRequest) query() url.Values {
	if r == nil {
		return nil
	}
	q := url.Values{}
	if r.Kind != "" {
		q.Set("kind", r.Kind)
	}
	if r.Inactive {
		q.Set("inactive", "true")
	}
	page(q, r.Limit, r.Offset)
	return q
}

// CampaignCreateRequest is one campaign to create. Every field but Name,
// Kind and Benefits may be left out: a campaign starts now, never ends, gives
// nothing once and is active unless it says otherwise. It has to grant
// something, in Benefits or in Grants.
type CampaignCreateRequest struct {
	Kind         string   `json:"kind"`
	Name         string   `json:"name"`
	Description  *string  `json:"description,omitempty"`
	Announcement *string  `json:"announcement,omitempty"`
	Benefits     Benefits `json:"benefits"`
	Grants       *Grants  `json:"grants,omitempty"`
	StartsAt     *Unix    `json:"starts_at,omitempty"`
	EndsAt       *Unix    `json:"ends_at,omitempty"`
	Active       *bool    `json:"active,omitempty"`
}

// CampaignUpdateRequest is a partial edit: only the fields set are written.
// Benefits and Grants, when set, replace the campaign's as a whole — the
// trial length included, which lives in Benefits. An announced event refuses
// any change to its grants or its trial length.
type CampaignUpdateRequest struct {
	Kind         *string   `json:"kind,omitempty"`
	Name         *string   `json:"name,omitempty"`
	Description  *string   `json:"description,omitempty"`
	Announcement *string   `json:"announcement,omitempty"`
	Benefits     *Benefits `json:"benefits,omitempty"`
	Grants       *Grants   `json:"grants,omitempty"`
	StartsAt     *Unix     `json:"starts_at,omitempty"`
	EndsAt       *Unix     `json:"ends_at,omitempty"`
	Active       *bool     `json:"active,omitempty"`
}

// --- Codes ------------------------------------------------------------------

// Code is one promo code.
type Code struct {
	ID          int64    `json:"id"`
	Code        string   `json:"code"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Benefits    Benefits `json:"benefits"`
	// Grants is what the code gives once, when it is redeemed.
	Grants   Grants `json:"grants"`
	StartsAt Unix   `json:"starts_at"`
	EndsAt   Unix   `json:"ends_at"`
	// MaxRedemptions caps how many accounts may redeem it; zero is no cap.
	MaxRedemptions int64 `json:"max_redemptions"`
	Redemptions    int64 `json:"redemptions"`
	// Duration is how long the benefits last once redeemed, in seconds;
	// zero is as long as the code itself does.
	Duration  int64     `json:"duration"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CodeListRequest pages through GET /admin/promo/codes. A nil request
// lists the first fifty active codes.
type CodeListRequest struct {
	Inactive bool
	Limit    int
	Offset   int
}

func (r *CodeListRequest) query() url.Values {
	if r == nil {
		return nil
	}
	q := url.Values{}
	if r.Inactive {
		q.Set("inactive", "true")
	}
	page(q, r.Limit, r.Offset)
	return q
}

// CodeCreateRequest is one code to issue. Leaving Code nil has one drawn. It
// has to grant something, in Benefits or in Grants.
type CodeCreateRequest struct {
	Code           *string  `json:"code,omitempty"`
	Name           string   `json:"name"`
	Description    *string  `json:"description,omitempty"`
	Benefits       Benefits `json:"benefits"`
	Grants         *Grants  `json:"grants,omitempty"`
	StartsAt       *Unix    `json:"starts_at,omitempty"`
	EndsAt         *Unix    `json:"ends_at,omitempty"`
	MaxRedemptions *int64   `json:"max_redemptions,omitempty"`
	Duration       *int64   `json:"duration,omitempty"`
	Active         *bool    `json:"active,omitempty"`
}

// CodeUpdateRequest is a partial edit of a code. Code renames it; Benefits
// and Grants, when set, replace the code's as a whole. What was already
// redeemed keeps what it was given.
type CodeUpdateRequest struct {
	Code           *string   `json:"code,omitempty"`
	Name           *string   `json:"name,omitempty"`
	Description    *string   `json:"description,omitempty"`
	Benefits       *Benefits `json:"benefits,omitempty"`
	Grants         *Grants   `json:"grants,omitempty"`
	StartsAt       *Unix     `json:"starts_at,omitempty"`
	EndsAt         *Unix     `json:"ends_at,omitempty"`
	MaxRedemptions *int64    `json:"max_redemptions,omitempty"`
	Duration       *int64    `json:"duration,omitempty"`
	Active         *bool     `json:"active,omitempty"`
}

// Redemption is one account holding one code.
type Redemption struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	AccountID int64  `json:"account_id"`
	ExpiresAt Unix   `json:"expires_at"`
	// RevokedAt is when the account's erasure took the benefits back, zero
	// when it did not. The redemption is kept as the record of what the
	// code gave.
	RevokedAt Unix      `json:"benefits_revoked_at,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Live reports whether the redemption still grants its benefits at now.
func (r Redemption) Live(now time.Time) bool {
	if r.RevokedAt != 0 {
		return false
	}
	return r.ExpiresAt == 0 || r.ExpiresAt.At().After(now)
}

// RedemptionListRequest pages through a code's redemptions.
type RedemptionListRequest struct {
	Limit  int
	Offset int
}

func (r *RedemptionListRequest) query() url.Values {
	if r == nil {
		return nil
	}
	q := url.Values{}
	page(q, r.Limit, r.Offset)
	return q
}

func page(q url.Values, limit, offset int) {
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
}

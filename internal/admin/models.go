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
// every plan that is sold, a raised ceiling on every plan including Free.
const AnyTier = ""

// AnyAmount is the amount of a top-up bonus that applies to every amount the
// store offers.
const AnyAmount int64 = 0

// Unlimited is the value a limit grant carries to mean "no ceiling". The
// reference says so too, in Reference.Unlimited.
const Unlimited int64 = -1

// Benefits is everything one promotion grants. A promotion that grants
// nothing is refused.
type Benefits struct {
	Discounts []Discount   `json:"discounts,omitempty"`
	TopUps    []TopUpBonus `json:"top_up_bonuses,omitempty"`
	Limits    []LimitGrant `json:"limits,omitempty"`
}

// Empty reports whether the benefits grant nothing.
func (b Benefits) Empty() bool {
	return len(b.Discounts) == 0 && len(b.TopUps) == 0 && len(b.Limits) == 0
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
	StartsAt     Unix     `json:"starts_at"`
	EndsAt       Unix     `json:"ends_at"`
	// AnnouncedAt is when an event told everybody it had started, zero
	// until it has.
	AnnouncedAt Unix `json:"announced_at"`
	Active      bool `json:"active"`
	// State is where it stands as of the request: StateScheduled,
	// StateRunning, StateEnded or StateStopped.
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

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
// Kind and Benefits may be left out: a campaign starts now, never ends and
// is active unless it says otherwise.
type CampaignCreateRequest struct {
	Kind         string   `json:"kind"`
	Name         string   `json:"name"`
	Description  *string  `json:"description,omitempty"`
	Announcement *string  `json:"announcement,omitempty"`
	Benefits     Benefits `json:"benefits"`
	StartsAt     *Unix    `json:"starts_at,omitempty"`
	EndsAt       *Unix    `json:"ends_at,omitempty"`
	Active       *bool    `json:"active,omitempty"`
}

// CampaignUpdateRequest is a partial edit: only the fields set are written.
// Benefits, when set, replace the campaign's benefits as a whole.
type CampaignUpdateRequest struct {
	Kind         *string   `json:"kind,omitempty"`
	Name         *string   `json:"name,omitempty"`
	Description  *string   `json:"description,omitempty"`
	Announcement *string   `json:"announcement,omitempty"`
	Benefits     *Benefits `json:"benefits,omitempty"`
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
	StartsAt    Unix     `json:"starts_at"`
	EndsAt      Unix     `json:"ends_at"`
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

// CodeCreateRequest is one code to issue. Leaving Code nil has one drawn.
type CodeCreateRequest struct {
	Code           *string  `json:"code,omitempty"`
	Name           string   `json:"name"`
	Description    *string  `json:"description,omitempty"`
	Benefits       Benefits `json:"benefits"`
	StartsAt       *Unix    `json:"starts_at,omitempty"`
	EndsAt         *Unix    `json:"ends_at,omitempty"`
	MaxRedemptions *int64   `json:"max_redemptions,omitempty"`
	Duration       *int64   `json:"duration,omitempty"`
	Active         *bool    `json:"active,omitempty"`
}

// CodeUpdateRequest is a partial edit of a code. Code renames it.
type CodeUpdateRequest struct {
	Code           *string   `json:"code,omitempty"`
	Name           *string   `json:"name,omitempty"`
	Description    *string   `json:"description,omitempty"`
	Benefits       *Benefits `json:"benefits,omitempty"`
	StartsAt       *Unix     `json:"starts_at,omitempty"`
	EndsAt         *Unix     `json:"ends_at,omitempty"`
	MaxRedemptions *int64    `json:"max_redemptions,omitempty"`
	Duration       *int64    `json:"duration,omitempty"`
	Active         *bool     `json:"active,omitempty"`
}

// Redemption is one account holding one code.
type Redemption struct {
	ID        int64     `json:"id"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	AccountID int64     `json:"account_id"`
	ExpiresAt Unix      `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
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

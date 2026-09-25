package admin

import (
	"context"
	"net/url"
	"strconv"
)

// PromoService writes promotions: campaigns, which apply to everybody, and
// codes, which apply to whoever redeems them. Both carry the same Benefits.
type PromoService struct{ base }

const promoPath = "admin/promo/"

// Reference returns the plans, quotas and top-up amounts a promotion is
// written against, by the names the API accepts. Read it rather than copying
// the catalog into a script.
//
// GET /admin/promo/reference.
func (s *PromoService) Reference(ctx context.Context) (Reference, *Meta, error) {
	return s.get[Reference](ctx, promoPath+"reference", nil)
}

// Campaigns lists campaigns. A nil request lists the active ones.
//
// GET /admin/promo/campaigns.
func (s *PromoService) Campaigns(ctx context.Context, req *CampaignListRequest) ([]Campaign, *Meta, error) {
	return s.get[[]Campaign](ctx, promoPath+"campaigns", req.query())
}

// Campaign reads one campaign.
//
// GET /admin/promo/campaigns/{id}.
func (s *PromoService) Campaign(ctx context.Context, id int64) (Campaign, *Meta, error) {
	return s.get[Campaign](ctx, campaignPath(id), nil)
}

// CreateCampaign creates a campaign. An event given a start in the future
// announces itself when it arrives.
//
// POST /admin/promo/campaigns.
func (s *PromoService) CreateCampaign(ctx context.Context, req CampaignCreateRequest) (Campaign, *Meta, error) {
	return s.post[Campaign](ctx, promoPath+"campaigns", req)
}

// UpdateCampaign edits a campaign. Setting Active to false stops it without
// deleting it: its history and its redemptions stay.
//
// PATCH /admin/promo/campaigns/{id}.
func (s *PromoService) UpdateCampaign(ctx context.Context, id int64, req CampaignUpdateRequest) (Campaign, *Meta, error) {
	return s.patch[Campaign](ctx, campaignPath(id), req)
}

// DeleteCampaign removes a campaign.
//
// DELETE /admin/promo/campaigns/{id}.
func (s *PromoService) DeleteCampaign(ctx context.Context, id int64) (*Meta, error) {
	_, meta, err := s.del[none](ctx, campaignPath(id))
	return meta, err
}

// Codes lists promo codes. A nil request lists the active ones.
//
// GET /admin/promo/codes.
func (s *PromoService) Codes(ctx context.Context, req *CodeListRequest) ([]Code, *Meta, error) {
	return s.get[[]Code](ctx, promoPath+"codes", req.query())
}

// Code reads one code. The API looks it up the way a user types it —
// "summer-25" finds SUMMER25.
//
// GET /admin/promo/codes/{code}.
func (s *PromoService) Code(ctx context.Context, code string) (Code, *Meta, error) {
	return s.get[Code](ctx, codePath(code), nil)
}

// CreateCode issues a code.
//
// POST /admin/promo/codes.
func (s *PromoService) CreateCode(ctx context.Context, req CodeCreateRequest) (Code, *Meta, error) {
	return s.post[Code](ctx, promoPath+"codes", req)
}

// UpdateCode edits a code.
//
// PATCH /admin/promo/codes/{code}.
func (s *PromoService) UpdateCode(ctx context.Context, code string, req CodeUpdateRequest) (Code, *Meta, error) {
	return s.patch[Code](ctx, codePath(code), req)
}

// DeleteCode removes a code. What it has already granted stays granted.
//
// DELETE /admin/promo/codes/{code}.
func (s *PromoService) DeleteCode(ctx context.Context, code string) (*Meta, error) {
	_, meta, err := s.del[none](ctx, codePath(code))
	return meta, err
}

// Redemptions lists the accounts that have redeemed a code.
//
// GET /admin/promo/codes/{code}/redemptions.
func (s *PromoService) Redemptions(ctx context.Context, code string, req *RedemptionListRequest) ([]Redemption, *Meta, error) {
	return s.get[[]Redemption](ctx, codePath(code)+"/redemptions", req.query())
}

func campaignPath(id int64) string { return promoPath + "campaigns/" + strconv.FormatInt(id, 10) }

// codePath escapes the code, which is typed by a person and may carry
// anything, into one path segment.
func codePath(code string) string { return promoPath + "codes/" + url.PathEscape(code) }

# Administering a deployment

`celadon admin` drives a deployment's private admin API — for now the
promotions under `/admin/promo`: **campaigns**, which apply to everybody
while they run, and **promo codes**, which apply to whoever redeems them. Both
grant the same kind of benefits while they are in force, and both may give
**gifts** once — a balance, and for a code a term of a paid plan.

It is opened by the deployment's `ADMIN_TOKEN`, not by an application key. A
deployment without one (or with one shorter than 24 characters) does not
register the admin routes at all, and celadon says so rather than reporting a
missing campaign.

## Logging in

```sh
celadon admin login                                     # hidden prompt
celadon admin login --profile ops --base-url https://aether.example.com
celadon admin login --with-token < admin-token.txt
celadon admin login --admin-base-url https://internal.example.com   # a proxy serves /admin elsewhere
celadon admin status                                    # which address and token, and whether it is accepted
celadon admin logout                                    # delete the token; the profile stays
```

The token is checked against `GET /admin/promo/reference`, which changes
nothing, before it is stored in `~/.celadon/admin/<profile>` (mode 0600). A
profile may hold an admin token without an application key. In CI, set
`CELADON_ADMIN_TOKEN` and `CELADON_BASE_URL` (or `CELADON_ADMIN_BASE_URL`)
instead. See [configuration.md](configuration.md) for the resolution order.

## Writing a promotion

Start with the reference: it lists the plans, quotas and top-up amounts by the
names the API accepts, the ceilings on a discount, a bonus and a balance gift,
the plan terms a code may give and the trial lengths an event may offer.

```sh
celadon admin reference
```

Benefits are written the same way on the command line and in the interface:

| Benefit  | Syntax             | Example                                          |
|----------|--------------------|--------------------------------------------------|
| discount | `TIER:PERCENT`     | `pro:25`; every plan that is sold: `all:25`      |
| bonus    | `AMOUNT:PERCENT`   | `50:20` (a $50 top-up); every amount: `any:20`   |
| grant    | `TIER:LIMIT=VALUE` | `free:search:daily=200`; no ceiling: `=unlimited` |

On the command line they are repeatable `--discount`, `--bonus` and `--grant`
flags, or one `--benefits "discount pro:25, bonus 50:20"` line, or the JSON
object in `--benefits-file` (`-` for standard input). A grant only ever raises a
ceiling. `--benefits none` is no benefits at all, for a promotion that only
gives gifts.

An event may also lengthen the free trial while it runs: `--trial 30` (one of
the lengths the reference lists; `off` for the usual length). The API keeps the
trial inside the benefits object as `trial_days`, so a `--benefits-file` carries
it too.

### Gifts

What a promotion gives **once** — when a code is redeemed, or when an event
reaches an account — is its gifts (the API's `grants`). Unlike the benefits,
they do not end with the promotion.

| Gift    | Syntax         | Example                                                   |
|---------|----------------|-----------------------------------------------------------|
| balance | `TIER:AMOUNT`  | `all:5` ($5 to every plan); `pro:10`; `ultra:0` leaves Ultra out |
| plan    | `TIER:DAYS`    | `pro:30` — 30 days of Pro (codes only)                    |

A balance is given per plan the account is on at the moment of the gift: a
plan named explicitly wins over `all`, and `0` excludes it — `all:1` with
`pro:2` gives Pro $2 and everybody else $1. On the command line they are
repeatable `--gift-balance` flags and one `--gift-plan` (codes), or a
`--gifts "balance all:1, balance pro:2"` line (`none` gives nothing), or the
grants object in `--gifts-file`.

Not every promotion may give everything; the API refuses the rest:

|           | benefits | `--trial` | balance | plan |
|-----------|----------|-----------|---------|------|
| **offer** | yes      | —         | —       | —    |
| **event** | yes      | yes       | yes     | —    |
| **code**  | yes      | —         | yes     | yes  |

A promotion has to grant something, in force or once.

Times take `now`, `never`, `2026-10-01`, `"2026-10-01 18:00"` (local time), RFC
3339, or a span from now such as `+7d`. `--for 7d` sets the end as a span from
the start.

## Campaigns

```sh
celadon admin campaigns list [--kind offer|event] [--all] [--limit N] [--offset N]
celadon admin campaigns view 7
celadon admin campaigns create --kind event --name "Summer week" \
  --starts 2026-10-01 --for 7d \
  --discount pro:25 --bonus 50:20 --grant free:search:daily=200 \
  --description "Seven days of lower prices." \
  --announcement "Tell a friend — the link in your profile pays you back."
celadon admin campaigns create --kind event --name "Birthday" --for 3d \
  --trial 30 --gift-balance all:1 --gift-balance pro:2
celadon admin campaigns edit 7 --ends +3d
celadon admin campaigns stop 7      # keeps it and its history; start 7 undoes it
celadon admin campaigns delete 7    # for good, after a confirmation
```

An **offer** runs quietly; an **event** announces itself to every account when
it starts, and moving its start into the future has it announce itself again,
as a new generation (`view` shows which). A campaign's state — scheduled,
running, ended or stopped — is computed on every request.

An event's balance goes to every account that existed when it started; a later
account is told about the event and not paid by it. **Once an event has been
announced its gifts and its trial length are frozen**, for good — rescheduling
it does not thaw them. Its words, its dates and its switch stay editable, and
`view` says when a campaign is frozen. A different gift is a different
campaign.

## Promo codes

```sh
celadon admin codes list [--all]
celadon admin codes create SUMMER25 --name "Summer sale" --max 100 --lasts 30d --discount all:25
celadon admin codes create --name "Support goodwill" --grant all:search:daily=unlimited --lasts 7d
celadon admin codes create MONTHOFPRO --name "A month of Pro" --gift-plan pro:30 --gift-balance all:5
celadon admin codes edit SUMMER25 --max 200 --ends 2026-11-30
celadon admin codes edit SUMMER25 --rename AUTUMN25
celadon admin codes disable SUMMER25    # enable undoes it
celadon admin codes redemptions SUMMER25
celadon admin codes delete SUMMER25
```

Leave the code out to have one drawn. `--max 0` is no cap, and `--lasts 0`
makes the benefits last as long as the code is valid; gifts are given at
redemption and stay. A code is looked up the way a user types it: `summer-25`
finds `SUMMER25`. `codes create` prints the code alone on standard output, for
a script to keep. `redemptions` shows when an account's erasure revoked what a
redemption granted; the redemption itself is kept as the record of the gift.

## Edits are patches

Only the flags given are sent. `--ends never` sends an end of zero, which the
API reads as "never", and `--max 0` lifts the cap — leaving either flag out
leaves the value alone. Benefit flags on an edit **replace** the discounts,
bonuses and ceilings as a whole, and gift flags the gifts (`--gifts none`
empties them).

The API stores the trial inside the benefits, so on a campaign edit benefit
flags keep the campaign's trial and `--trial` keeps its other benefits:
celadon reads the campaign first and sends the two together. That is also what
lets a frozen event's discount change without touching its trial. A
`--benefits-file` is the whole object and is sent as it is.

`--dry-run` prints the body a create or an edit would send, and writes nothing
(an edit that has to keep part of the benefits still reads the campaign).

## The interface

`celadon admin tui` opens three tabs:

- **Campaigns** — the list colored by state, `f` to filter by kind, `a` to
  include stopped ones, and a card of what the selected campaign grants while
  in force and gives once, and whether it is frozen. `n` and `e` open a form
  (with a Trial and a Gifts field), `s` stops or starts, `d` deletes after a
  `y`.
- **Codes** — the list with seats taken, a card with what the code grants and
  gives, and on `enter` the accounts that redeemed the code (and whose
  benefits were revoked). `n`, `e`, `s` and `d` as above.
- **Reference** — the plans, quotas, amounts, plan terms and trial lengths.

In a form, `tab` and the arrows move between fields, `enter` moves on and
submits from the last, `ctrl+s` submits from anywhere and `esc` cancels. An
edit sends only the fields whose text changed — so a frozen event can be
renamed without its gifts being sent back — and a refusal stays on the form
with the server's reason.

## Errors

| Status | Means | celadon exits |
|--------|-------|---------------|
| 400 | refused — the message says why (an unknown plan, a discount over the ceiling, a gift an offer cannot give, a frozen event's gifts) | 1 |
| 401 | the token is wrong | 4 |
| 404 | no such campaign or code | 1 |
| 404, not the API's answer | the deployment serves no admin API | 1 |
| 409 | a code with that name exists | 1 |
| 500 | the server failed, or promotions are not configured on it | 1 |

Reads and deletes are retried after a failure on the server's side; creates
and edits never are, because a write whose connection failed may still have
arrived.

## From Go

Inside celadon the client is `internal/admin`, shaped like
[teal](https://github.com/x-chunk/teal):

```go
c, err := admin.New(token, admin.WithBaseURL("https://aether.example.com"))
campaigns, meta, err := c.Promo.Campaigns(ctx, &admin.CampaignListRequest{Kind: admin.KindEvent})
code, _, err := c.Promo.CreateCode(ctx, admin.CodeCreateRequest{
	Name:     "Summer sale",
	Benefits: admin.Benefits{Discounts: []admin.Discount{{Tier: admin.AnyTier, Percent: 25}}},
	Grants: &admin.Grants{
		BalanceCents: map[string]int64{admin.AnyTier: 500},
		Subscription: &admin.SubscriptionGrant{Tier: "pro", Days: 30},
	},
	Duration: admin.Ptr(int64(30 * 24 * 3600)),
})
if admin.IsCode(err, admin.CodeConflict) { … }
```

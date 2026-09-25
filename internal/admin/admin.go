// Package admin is a Go client for Aether's private admin API — for now the
// promotions API under /admin/promo — written to be called exactly the way
// teal calls the public one:
//
//	c, err := admin.New(token, admin.WithBaseURL("https://your-aether-host"))
//	if err != nil {
//		return err
//	}
//	campaigns, meta, err := c.Promo.Campaigns(ctx, nil)
//
// Every endpoint hangs off a service on the client, and every method returns
// its payload, a *Meta and an error — or only the last two when the endpoint
// answers with nothing.
//
// The admin API is not the Plug-In API, and three things differ underneath:
//
//   - It is opened by the deployment's ADMIN_TOKEN, sent as a bearer token,
//     not by an application key. Nothing it does is billed, so a Meta carries
//     the status and the headers but no cost.
//   - It answers in {"ok":…,"message":…,"data":…} and says what went wrong in
//     the status line and the message rather than in an error code. *Error
//     derives a code from the status so callers can branch on IsCode all the
//     same, and the message is kept for a person to read.
//   - A deployment without an ADMIN_TOKEN does not register the routes at all.
//     That answers a plain 404 that is not the envelope, which is reported as
//     CodeNotServed rather than confused with a missing campaign.
//
// Reads, deletes and nothing else are retried after a failure on the server's
// side: a write may have taken effect before the process failed, and a second
// campaign is worse than an error.
package admin

import "github.com/x-chunk/teal"

// DefaultBaseURL is used when no other base URL is configured. The admin API
// is served on the same listener as the Plug-In API, so it is teal's.
const DefaultBaseURL = teal.DefaultBaseURL

// MinTokenLength is the shortest token a deployment will run with. A shorter
// one is never accepted by the server, so it is refused before it is sent.
const MinTokenLength = 24

package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/x-chunk/teal"
)

// Problem is a refusal explained: what happened, and what to do about it.
type Problem struct {
	// Title is one line saying what went wrong.
	Title string
	// Hint is what to do next, empty when there is nothing to do.
	Hint string
	// Auth marks a problem with the credential itself, which the CLI exits
	// with a code of its own for.
	Auth bool
}

// Explain turns an error from teal into something to show a person. It
// branches on the API's error code, never on its message, as teal asks.
func Explain(err error, now time.Time) Problem {
	if err == nil {
		return Problem{}
	}
	if errors.Is(err, context.Canceled) {
		return Problem{Title: "cancelled"}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Problem{Title: "the request timed out", Hint: "raise --timeout, or try again"}
	}

	e, ok := teal.AsError(err)
	if !ok {
		var urlErr *url.Error
		var netErr net.Error
		if errors.As(err, &urlErr) || errors.As(err, &netErr) {
			return Problem{
				Title: "could not reach the API: " + rootCause(err).Error(),
				Hint:  "check the base url (`celadon auth status`) and your connection",
			}
		}
		return Problem{Title: err.Error()}
	}

	p := Problem{Title: e.Message}
	if p.Title == "" {
		p.Title = fmt.Sprintf("the API refused the request (%s, http %d)", e.Code, e.StatusCode)
	}
	switch e.Code {
	case teal.CodeUnauthorized:
		p.Auth = true
		p.Hint = "the key was not accepted — it may have been regenerated in the bot; run `celadon auth login` with the new one"
	case teal.CodeInsufficientCredit:
		p.Hint = "the application's balance cannot cover this call; fund it from the bot's Plug-In screen"
		if e.Meta != nil && e.Meta.HasBalance {
			p.Hint += " (balance " + e.Meta.Balance.String() + ")"
		}
	case teal.CodeForbidden:
		p.Hint = "the account's plan does not open this; the Plug-In needs Ultra"
	case teal.CodeAccountBlocked:
		p.Auth = true
		p.Hint = "the account behind this key is blocked"
	case teal.CodeQuotaExhausted:
		if e.Limit != "" {
			p.Title = fmt.Sprintf("quota %s is used up (%d of %d)", e.Limit, e.Used, e.LimitValue)
		}
		if !e.ResetAt.IsZero() {
			p.Hint = "it resets " + e.ResetAt.Local().Format("2006-01-02 15:04") + " (in " + e.ResetAt.Sub(now).Round(time.Minute).String() + ")"
		}
	case teal.CodeRateLimited:
		if e.RetryAfter > 0 {
			p.Hint = "slow down; retry in " + e.RetryAfter.String()
		}
	case teal.CodeUnavailable:
		p.Hint = "the Plug-In API is not enabled on this deployment"
	case teal.CodeInternal:
		p.Hint = "the server failed; if it keeps happening, report it with the time of the request"
	}
	return p
}

func rootCause(err error) error {
	for {
		next := errors.Unwrap(err)
		if next == nil {
			return err
		}
		err = next
	}
}

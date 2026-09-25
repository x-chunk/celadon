package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// The error codes an *Error carries. The admin API says what went wrong in
// its status line, so the code is derived from the status — and from whether
// the body was the API's envelope at all, which is what tells a route that
// is not served apart from a campaign that does not exist.
const (
	CodeBadRequest   = "bad_request"  // 400: refused, with the reason in the message
	CodeUnauthorized = "unauthorized" // 401: the token is wrong
	CodeNotFound     = "not_found"    // 404: no such campaign or code
	CodeConflict     = "conflict"     // 409: a code that already exists
	CodeRateLimited  = "rate_limited" // 429
	CodeInternal     = "internal"     // 500, and anything else unexpected
	CodeUnavailable  = "unavailable"  // 502, 503, 504
	CodeNotServed    = "not_served"   // 404/405 outside the envelope: the deployment has no ADMIN_TOKEN
)

// Error is a refusal: the status it arrived with, the code derived from it,
// and the API's message.
type Error struct {
	StatusCode int
	Code       string
	Message    string
	RetryAfter time.Duration

	Meta *Meta
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("admin: %s (http %d)", e.Code, e.StatusCode)
	}
	return fmt.Sprintf("admin: %s: %s (http %d)", e.Code, e.Message, e.StatusCode)
}

// IsCode reports whether err is an *Error carrying one of the given codes.
func IsCode(err error, codes ...string) bool {
	e, ok := AsError(err)
	if !ok {
		return false
	}
	for _, c := range codes {
		if e.Code == c {
			return true
		}
	}
	return false
}

// AsError extracts the *Error from err, if there is one.
func AsError(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

// retryable reports whether a request refused this way is worth sending
// again: a rate refusal always, since nothing was done; a failure on the
// server's side only when repeating the request is safe.
func (e *Error) retryable(idempotent bool) bool {
	switch e.Code {
	case CodeRateLimited:
		return true
	case CodeInternal, CodeUnavailable:
		return idempotent
	}
	return false
}

// errorFrom builds the *Error for a refusal, reading the message out of the
// envelope when the body is one.
func errorFrom(raw []byte, status int, meta *Meta) *Error {
	e := &Error{StatusCode: status, Code: codeFor(status), Meta: meta}
	if meta != nil {
		e.RetryAfter = meta.RetryAfter
	}
	var env struct {
		OK      *bool  `json:"ok"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.OK == nil {
		// Not the envelope. On a 404 or a 405 that is the router saying
		// the route does not exist — which, under /admin, is what a
		// deployment without an ADMIN_TOKEN answers.
		if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
			e.Code = CodeNotServed
			e.Message = "the admin API is not served by this deployment (is ADMIN_TOKEN set on it?)"
		}
		return e
	}
	e.Message = env.Message
	if meta != nil {
		meta.Message = env.Message
	}
	if status < 300 {
		// ok:false with a success status says nothing but its message.
		e.Code = CodeInternal
	}
	return e
}

func codeFor(status int) string {
	switch status {
	case http.StatusBadRequest:
		return CodeBadRequest
	case http.StatusUnauthorized, http.StatusForbidden:
		return CodeUnauthorized
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusConflict:
		return CodeConflict
	case http.StatusTooManyRequests:
		return CodeRateLimited
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return CodeUnavailable
	}
	return CodeInternal
}

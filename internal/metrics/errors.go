package metrics

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// The error codes an *Error carries, derived from the status: the listener
// says what went wrong in its status line and a message, never in a code.
const (
	CodeBadRequest   = "bad_request"  // 400: a malformed query — the message says which part
	CodeUnauthorized = "unauthorized" // 401: the token is wrong or missing
	CodeNotFound     = "not_found"    // 404: not a metrics listener, or not this path
	CodeInternal     = "internal"     // 500
	CodeUnavailable  = "unavailable"  // 502, 503, 504
)

// Error is a refusal: the status, the code derived from it, and the message.
type Error struct {
	StatusCode int
	Code       string
	Message    string
	Meta       *Meta
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("metrics: %s (http %d)", e.Code, e.StatusCode)
	}
	return fmt.Sprintf("metrics: %s: %s (http %d)", e.Code, e.Message, e.StatusCode)
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

func (e *Error) retryable() bool {
	return e.Code == CodeInternal || e.Code == CodeUnavailable
}

func errorFrom(raw []byte, status int, meta *Meta) *Error {
	e := &Error{StatusCode: status, Code: codeFor(status), Meta: meta}
	var body struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &body) == nil {
		e.Message = body.Message
	}
	if e.Message == "" && status == http.StatusNotFound {
		e.Message = "nothing is served at this path; is the base URL the metrics listener (:9090)?"
	}
	return e
}

func codeFor(status int) string {
	switch status {
	case http.StatusBadRequest:
		return CodeBadRequest
	case http.StatusUnauthorized, http.StatusForbidden:
		return CodeUnauthorized
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return CodeNotFound
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return CodeUnavailable
	}
	return CodeInternal
}

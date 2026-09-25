package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Request is one call to the API, before it becomes an *http.Request.
type Request struct {
	Method string     // http.MethodGet, http.MethodPost, …
	Path   string     // relative to the base URL, e.g. "admin/promo/codes"
	Query  url.Values // optional
	Body   any        // marshalled as JSON when not nil

	// Idempotent says that sending this request twice does what sending it
	// once does, which is what decides whether it may be retried after a
	// failure on the server's side. Reads and deletes set it.
	Idempotent bool
}

// Meta is what came back besides the payload. The admin API bills nothing,
// so it is the status and the headers.
//
// A method returns a nil *Meta only when the request never reached the API.
type Meta struct {
	StatusCode int
	// Message is the envelope's message: "success" on success, the reason
	// on a refusal.
	Message    string
	RetryAfter time.Duration
	Header     http.Header
}

// envelope is what every admin endpoint answers in.
type envelope struct {
	OK      bool            `json:"ok"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// Do sends req and decodes the envelope's data field into a T.
//
// T is in the result and not in the arguments, so it is always given
// explicitly:
//
//	ref, meta, err := c.Do[Reference](ctx, Request{Method: http.MethodGet, Path: "admin/promo/reference"})
func (c *Client) Do[T any](ctx context.Context, req Request) (T, *Meta, error) {
	var out T
	resp, meta, err := c.send(ctx, req)
	if err != nil {
		return out, meta, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return out, meta, fmt.Errorf("admin: reading %s %s: %w", req.Method, req.Path, err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return out, meta, fmt.Errorf("admin: decoding %s %s: %w", req.Method, req.Path, err)
	}
	meta.Message = env.Message
	// A refusal normally arrives with a status to match; one that arrives
	// with a 2xx would otherwise read as an empty answer.
	if !env.OK {
		return out, meta, errorFrom(raw, resp.StatusCode, meta)
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return out, meta, nil
	}
	if err := json.Unmarshal(env.Data, &out); err != nil {
		return out, meta, fmt.Errorf("admin: decoding %s %s: %w", req.Method, req.Path, err)
	}
	return out, meta, nil
}

// send performs the request, retrying what is worth retrying, and turns a
// refusal into an *Error. On a nil error the body is open and undrained.
func (c *Client) send(ctx context.Context, req Request) (*http.Response, *Meta, error) {
	var body []byte
	if req.Body != nil {
		var err error
		if body, err = json.Marshal(req.Body); err != nil {
			return nil, nil, fmt.Errorf("admin: encoding %s %s: %w", req.Method, req.Path, err)
		}
	}

	for attempt := 0; ; attempt++ {
		httpReq, err := c.newRequest(ctx, req, body)
		if err != nil {
			return nil, nil, err
		}

		resp, err := c.http.Do(httpReq)
		if err != nil {
			err = fmt.Errorf("admin: %s %s: %w", req.Method, req.Path, err)
			// A write whose connection failed may still have arrived, so
			// only an idempotent request is sent again.
			if !req.Idempotent || attempt >= c.maxRetries || ctx.Err() != nil {
				return nil, nil, err
			}
			if werr := wait(ctx, c.backoff(attempt, 0)); werr != nil {
				return nil, nil, werr
			}
			continue
		}

		meta := &Meta{
			StatusCode: resp.StatusCode,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
			Header:     resp.Header,
		}
		if resp.StatusCode < 300 {
			return resp, meta, nil
		}

		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		apiErr := errorFrom(raw, resp.StatusCode, meta)
		if attempt >= c.maxRetries || !apiErr.retryable(req.Idempotent) {
			return nil, meta, apiErr
		}
		if err := wait(ctx, c.backoff(attempt, meta.RetryAfter)); err != nil {
			return nil, meta, err
		}
	}
}

func (c *Client) newRequest(ctx context.Context, req Request, body []byte) (*http.Request, error) {
	u := c.baseURL.JoinPath(req.Path)
	if len(req.Query) > 0 {
		u.RawQuery = req.Query.Encode()
	}
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, u.String(), r)
	if err != nil {
		return nil, fmt.Errorf("admin: building %s %s: %w", req.Method, req.Path, err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.token)
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", c.userAgent)
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	return httpReq, nil
}

// backoff prefers what the server asked for, and doubles its own wait
// otherwise.
func (c *Client) backoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	if c.retryBase <= 0 {
		return 0
	}
	return time.Duration(float64(c.retryBase) * math.Pow(2, float64(attempt)))
}

func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// parseRetryAfter reads the header in both of the forms RFC 9110 allows.
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil {
		if n < 0 {
			return 0
		}
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// maxBody caps what is read from one answer.
const maxBody = 8 << 20

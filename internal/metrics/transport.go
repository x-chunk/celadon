package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"time"
)

// Request is one call to the listener, before it becomes an *http.Request.
// Every endpoint is a GET, so there is no body.
type Request struct {
	Path  string     // relative to the base URL, e.g. "api/live"
	Query url.Values // optional
	// Accept is the media type asked for; JSON when empty.
	Accept string
	// Answers are statuses besides 2xx whose body is the payload rather than
	// a refusal — the 503 that /readyz answers a not-ready process with.
	Answers []int
	// NoRetry sends the request once, for a stream a caller reconnects
	// itself.
	NoRetry bool
}

// Meta is what came back besides the payload: the status and the headers.
// A method returns a nil *Meta only when the request never reached the
// listener.
type Meta struct {
	StatusCode int
	Header     http.Header
	// Elapsed is how long the listener took to answer, headers included.
	Elapsed time.Duration
}

// Do sends req and decodes the body into a T. T is in the result, so it is
// always given explicitly:
//
//	snap, meta, err := c.Do[Snapshot](ctx, Request{Path: "api/live"})
func (c *Client) Do[T any](ctx context.Context, req Request) (T, *Meta, error) {
	var out T
	resp, meta, err := c.send(ctx, req)
	if err != nil {
		return out, meta, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return out, meta, fmt.Errorf("metrics: reading %s: %w", req.Path, err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, meta, fmt.Errorf("metrics: decoding %s: %w", req.Path, err)
	}
	return out, meta, nil
}

// DoRaw sends req and hands back the undecoded body, for the exposition and
// the stream. The caller closes it.
func (c *Client) DoRaw(ctx context.Context, req Request) (io.ReadCloser, *Meta, error) {
	resp, meta, err := c.send(ctx, req)
	if err != nil {
		return nil, meta, err
	}
	return resp.Body, meta, nil
}

// send performs the request, retrying failures — every endpoint is a read —
// and turns a refusal into an *Error. On a nil error the body is open.
func (c *Client) send(ctx context.Context, req Request) (*http.Response, *Meta, error) {
	retries := c.maxRetries
	if req.NoRetry {
		retries = 0
	}
	for attempt := 0; ; attempt++ {
		httpReq, err := c.newRequest(ctx, req)
		if err != nil {
			return nil, nil, err
		}
		start := time.Now()
		resp, err := c.http.Do(httpReq)
		if err != nil {
			err = fmt.Errorf("metrics: GET %s: %w", req.Path, err)
			if attempt >= retries || ctx.Err() != nil {
				return nil, nil, err
			}
			if werr := wait(ctx, c.backoff(attempt)); werr != nil {
				return nil, nil, werr
			}
			continue
		}
		meta := &Meta{StatusCode: resp.StatusCode, Header: resp.Header, Elapsed: time.Since(start)}
		if resp.StatusCode < 300 || slices.Contains(req.Answers, resp.StatusCode) {
			return resp, meta, nil
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		apiErr := errorFrom(raw, resp.StatusCode, meta)
		if attempt >= retries || !apiErr.retryable() {
			return nil, meta, apiErr
		}
		if err := wait(ctx, c.backoff(attempt)); err != nil {
			return nil, meta, err
		}
	}
}

func (c *Client) newRequest(ctx context.Context, req Request) (*http.Request, error) {
	u := c.baseURL.JoinPath(req.Path)
	if len(req.Query) > 0 {
		u.RawQuery = req.Query.Encode()
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("metrics: building GET %s: %w", req.Path, err)
	}
	if c.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.token)
	}
	accept := req.Accept
	if accept == "" {
		// Never text/html: the listener sends a browser to its sign-in
		// page, and a client wants the 401.
		accept = "application/json"
	}
	httpReq.Header.Set("Accept", accept)
	httpReq.Header.Set("User-Agent", c.userAgent)
	return httpReq, nil
}

func (c *Client) backoff(attempt int) time.Duration {
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

// maxBody caps what is read from one answer. A range query over every
// series at a fine step is the largest thing the listener sends.
const maxBody = 64 << 20

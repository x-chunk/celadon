package admin

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to one deployment with its admin token. It is safe for
// concurrent use and is meant to be made once and kept.
type Client struct {
	baseURL    *url.URL
	token      string
	http       *http.Client
	userAgent  string
	maxRetries int
	retryBase  time.Duration
	timeout    time.Duration

	// Promo is the promotions API: campaigns, codes and the reference they
	// are written against.
	Promo *PromoService
}

// New builds a client for the given admin token — the deployment's
// ADMIN_TOKEN.
func New(token string, opts ...Option) (*Client, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("admin: empty token")
	}
	if len(token) < MinTokenLength {
		return nil, fmt.Errorf("admin: the token is %d characters long; a deployment never accepts one shorter than %d", len(token), MinTokenLength)
	}

	c := &Client{
		token:      token,
		http:       &http.Client{Transport: defaultTransport()},
		userAgent:  "celadon-admin",
		maxRetries: 2,
		retryBase:  500 * time.Millisecond,
	}
	if err := WithBaseURL(DefaultBaseURL)(c); err != nil {
		return nil, err
	}
	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}
	if c.timeout > 0 {
		// Copied rather than set, so that a caller's own http.Client is not
		// mutated behind its back and the order of the options cannot matter.
		hc := *c.http
		hc.Timeout = c.timeout
		c.http = &hc
	}

	c.Promo = &PromoService{base{c}}
	return c, nil
}

// defaultTransport bounds the parts of a request that can hang.
func defaultTransport() http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = 30 * time.Second
	t.ExpectContinueTimeout = 1 * time.Second
	return t
}

// Option configures a Client. Options are applied in order by New.
type Option func(*Client) error

// WithBaseURL points the client at a deployment. A path in raw is kept, so a
// host behind a prefix ("https://example.com/aether") works.
func WithBaseURL(raw string) Option {
	return func(c *Client) error {
		u, err := url.Parse(strings.TrimRight(raw, "/") + "/")
		if err != nil {
			return fmt.Errorf("admin: bad base url %q: %w", raw, err)
		}
		if u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("admin: base url %q needs a scheme and a host", raw)
		}
		c.baseURL = u
		return nil
	}
}

// WithHTTPClient replaces the underlying *http.Client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) error {
		if h == nil {
			return fmt.Errorf("admin: nil http client")
		}
		c.http = h
		return nil
	}
}

// WithTimeout puts a deadline on the whole of every request. It is off by
// default; a per-call context is the finer instrument.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) error {
		if d < 0 {
			return fmt.Errorf("admin: negative timeout %s", d)
		}
		c.timeout = d
		return nil
	}
}

// WithUserAgent prepends a caller's own identifier to the User-Agent.
func WithUserAgent(ua string) Option {
	return func(c *Client) error {
		c.userAgent = strings.TrimSpace(ua) + " " + c.userAgent
		return nil
	}
}

// WithRetry sets how many times a request is sent again after a failure on
// the server's side. Zero disables retrying.
func WithRetry(attempts int, base time.Duration) Option {
	return func(c *Client) error {
		if attempts < 0 {
			return fmt.Errorf("admin: negative retry count %d", attempts)
		}
		c.maxRetries, c.retryBase = attempts, base
		return nil
	}
}

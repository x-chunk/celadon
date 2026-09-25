package metrics

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to one metrics listener. It is safe for concurrent use and is
// meant to be made once and kept.
type Client struct {
	baseURL    *url.URL
	token      string
	http       *http.Client
	userAgent  string
	maxRetries int
	retryBase  time.Duration
	timeout    time.Duration

	// Health is liveness and readiness.
	Health *HealthService
	// Series is the catalog, the live values, the stream and range queries.
	Series *SeriesService
	// Prometheus is the text exposition.
	Prometheus *PrometheusService
}

// New builds a client. Without WithToken it sends no token, which is what a
// listener without a METRICS_TOKEN expects.
func New(opts ...Option) (*Client, error) {
	c := &Client{
		http:       &http.Client{Transport: defaultTransport()},
		userAgent:  "celadon-metrics",
		maxRetries: 2,
		retryBase:  250 * time.Millisecond,
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
		hc := *c.http
		hc.Timeout = c.timeout
		c.http = &hc
	}
	c.Health = &HealthService{base{c}}
	c.Series = &SeriesService{base{c}}
	c.Prometheus = &PrometheusService{base{c}}
	return c, nil
}

// defaultTransport bounds the parts of a request that can hang. There is no
// Timeout on the http.Client: the stream is a response that never ends.
func defaultTransport() http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = 20 * time.Second
	return t
}

// Option configures a Client. Options are applied in order by New.
type Option func(*Client) error

// WithBaseURL points the client at a listener. A path in raw is kept, so a
// listener behind a prefix works.
func WithBaseURL(raw string) Option {
	return func(c *Client) error {
		u, err := url.Parse(strings.TrimRight(raw, "/") + "/")
		if err != nil {
			return fmt.Errorf("metrics: bad base url %q: %w", raw, err)
		}
		if u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("metrics: base url %q needs a scheme and a host", raw)
		}
		c.baseURL = u
		return nil
	}
}

// WithToken sets the METRICS_TOKEN, sent as a bearer token. An empty one
// sends none.
func WithToken(token string) Option {
	return func(c *Client) error {
		c.token = strings.TrimSpace(token)
		return nil
	}
}

// WithHTTPClient replaces the underlying *http.Client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) error {
		if h == nil {
			return fmt.Errorf("metrics: nil http client")
		}
		c.http = h
		return nil
	}
}

// WithTimeout puts a deadline on the whole of every request — the stream
// included, so leave it off for a client that streams.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) error {
		if d < 0 {
			return fmt.Errorf("metrics: negative timeout %s", d)
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

// WithRetry sets how many times a failed request is sent again. Every
// endpoint here is a read, so every failure on the server's side or of the
// connection is worth another try. Zero disables retrying.
func WithRetry(attempts int, base time.Duration) Option {
	return func(c *Client) error {
		if attempts < 0 {
			return fmt.Errorf("metrics: negative retry count %d", attempts)
		}
		c.maxRetries, c.retryBase = attempts, base
		return nil
	}
}

// HasToken reports whether the client sends a token.
func (c *Client) HasToken() bool { return c.token != "" }

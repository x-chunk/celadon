// Package api builds the teal client a command talks through and explains
// what the API says when it refuses.
package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	neturl "net/url"
	"time"

	"github.com/x-chunk/teal"

	"github.com/x-chunk/celadon/internal/admin"
	"github.com/x-chunk/celadon/internal/config"
	"github.com/x-chunk/celadon/internal/metrics"
	"github.com/x-chunk/celadon/internal/version"
)

// Options tune the client beyond what the profile says.
type Options struct {
	// Retries is how many times a rate refusal or a failed read is sent
	// again. Teal decides which requests are safe to repeat.
	Retries int
	// HTTPClient replaces the transport, for tests.
	HTTPClient *http.Client
}

// DefaultRetries is what a command retries when nothing says otherwise.
const DefaultRetries = 2

// New builds a client for a resolved profile.
func New(r config.Resolved, o Options) (*teal.Client, error) {
	opts := []teal.Option{
		teal.WithBaseURL(r.BaseURL),
		teal.WithUserAgent("celadon/" + version.Get().Version),
		teal.WithHeaderAuth(AuthHeader(r.AuthHeader)),
		teal.WithRetry(o.Retries, 500*time.Millisecond),
	}
	if o.HTTPClient != nil {
		opts = append(opts, teal.WithHTTPClient(o.HTTPClient))
	}
	c, err := teal.New(r.Key, opts...)
	if err != nil {
		return nil, fmt.Errorf("building the client: %w", err)
	}
	return c, nil
}

// AuthHeader maps the name config.toml uses onto teal's.
func AuthHeader(name string) teal.AuthHeader {
	switch name {
	case config.AuthBare:
		return teal.AuthBare
	case config.AuthCustom:
		return teal.AuthCustomHeader
	default:
		return teal.AuthBearer
	}
}

// NewAdmin builds an admin client for a resolved profile.
func NewAdmin(r config.AdminResolved, o Options) (*admin.Client, error) {
	opts := []admin.Option{
		admin.WithBaseURL(r.BaseURL),
		admin.WithUserAgent("celadon/" + version.Get().Version),
		admin.WithRetry(o.Retries, 500*time.Millisecond),
	}
	if o.HTTPClient != nil {
		opts = append(opts, admin.WithHTTPClient(o.HTTPClient))
	}
	c, err := admin.New(r.Token, opts...)
	if err != nil {
		return nil, fmt.Errorf("building the admin client: %w", err)
	}
	return c, nil
}

// NewMetrics builds a metrics client for a resolved profile. It sends a
// token only when the profile has one.
func NewMetrics(r config.MetricsResolved, o Options) (*metrics.Client, error) {
	opts := []metrics.Option{
		metrics.WithBaseURL(r.BaseURL),
		metrics.WithToken(r.Token),
		metrics.WithUserAgent("celadon/" + version.Get().Version),
		metrics.WithRetry(o.Retries, 250*time.Millisecond),
	}
	if o.HTTPClient != nil {
		opts = append(opts, metrics.WithHTTPClient(o.HTTPClient))
	}
	c, err := metrics.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("building the metrics client: %w", err)
	}
	return c, nil
}

// Unreachable says which service could not be reached and what usually
// fixes it, around the transport's own error. Explain reads it before
// anything else.
type Unreachable struct {
	Service string
	URL     string
	Hint    string
	Err     error
}

func (u *Unreachable) Error() string {
	return fmt.Sprintf("could not reach %s at %s: %v", u.Service, u.URL, u.Err)
}

func (u *Unreachable) Unwrap() error { return u.Err }

// MarkUnreachable wraps err in an Unreachable when it is a failure to reach
// the service at all — a refused connection, a failed lookup — and returns
// it unchanged otherwise.
func MarkUnreachable(err error, service, url, hint string) error {
	if err == nil {
		return nil
	}
	var urlErr *neturl.Error
	var netErr net.Error
	if !errors.As(err, &urlErr) && !errors.As(err, &netErr) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &Unreachable{Service: service, URL: url, Hint: hint, Err: err}
}

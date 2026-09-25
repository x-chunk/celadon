// Package api builds the teal client a command talks through and explains
// what the API says when it refuses.
package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/x-chunk/teal"

	"github.com/x-chunk/celadon/internal/admin"
	"github.com/x-chunk/celadon/internal/config"
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

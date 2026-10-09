package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.xchunk.org/teal"

	"github.com/x-chunk/celadon/internal/config"
)

// elsewhere stands in for a host a redirect points away to, and counts what
// reaches it.
func elsewhere(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var reached atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		w.Write([]byte(`{"ok":true,"data":{}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &reached
}

// redirecting answers every request with a redirect to the same path on to.
func redirecting(t *testing.T, to string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, to+r.URL.Path, status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNoCredentialLeavesTheOriginOnARedirect(t *testing.T) {
	other, reached := elsewhere(t)
	from := redirecting(t, other.URL, http.StatusTemporaryRedirect)

	c, err := New(config.Resolved{BaseURL: from.URL, Key: testKey, AuthHeader: config.AuthCustom}, Options{Retries: 0})
	if err != nil {
		t.Fatal(err)
	}
	// A 307 sends the body again: a passphrase and the secret itself.
	err = c.Vault.Store(context.Background(), teal.VaultStoreRequest{Passphrase: "pass", Plaintext: "secret"}).Error()
	var redirected *Redirected
	if !errors.As(err, &redirected) {
		t.Fatalf("err = %v, want a refused redirect", err)
	}
	if p := Explain(err, time.Now()); !strings.Contains(p.Title, "refused a redirect") || !strings.Contains(p.Hint, other.URL) {
		t.Errorf("Explain = %+v", p)
	}

	a, err := NewAdmin(config.AdminResolved{BaseURL: from.URL, Token: strings.Repeat("t", 32)}, Options{Retries: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Promo.DeleteCode(context.Background(), "SUMMER25"); !errors.As(err, &redirected) {
		t.Errorf("admin err = %v, want a refused redirect", err)
	}

	m, err := NewMetrics(config.MetricsResolved{BaseURL: from.URL, Token: "tok"}, Options{Retries: 0})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = m.Series.Live(context.Background())
	err = MarkUnreachable(err, "the metrics listener", from.URL, "open a tunnel")
	if !errors.As(err, &redirected) || strings.Contains(Explain(err, time.Now()).Title, "could not reach") {
		t.Errorf("metrics err = %v, want a refused redirect, not an unreachable listener", err)
	}

	if n := reached.Load(); n != 0 {
		t.Errorf("%d requests reached the other host", n)
	}
}

func TestARedirectWithinTheOriginIsFollowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/app" {
			http.Redirect(w, r, "/v1/app/", http.StatusPermanentRedirect)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+testKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"ok":true,"data":{"id":7,"name":"Reports"}}`))
	}))
	defer srv.Close()

	// A test's own client is copied and given the policy, not changed.
	given := &http.Client{Timeout: time.Minute}
	c, err := New(config.Resolved{BaseURL: srv.URL, Key: testKey}, Options{HTTPClient: given})
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.App.Get(context.Background()).Value()
	if err != nil || r.Data.Name != "Reports" {
		t.Fatalf("app = %+v, %v", r.Data, err)
	}
	if given.CheckRedirect != nil {
		t.Error("the given client was changed")
	}
}

func TestSameOrigin(t *testing.T) {
	via := func(raw string) []*http.Request {
		u, _ := url.Parse(raw)
		return []*http.Request{{URL: u}}
	}
	to := func(raw string) *http.Request {
		u, _ := url.Parse(raw)
		return &http.Request{URL: u}
	}
	for _, c := range []struct {
		from, to string
		ok       bool
	}{
		{"https://aether.example.com/v1/app", "https://aether.example.com/v1/app/", true},
		{"https://aether.example.com/v1/app", "https://AETHER.example.com:443/v1/app", true},
		{"http://127.0.0.1:9090/api/live", "http://127.0.0.1:9090/login", true},
		{"https://aether.example.com/v1/app", "http://aether.example.com/v1/app", false},
		{"https://aether.example.com/v1/app", "https://cdn.aether.example.com/v1/app", false},
		{"https://aether.example.com/v1/app", "https://aether.example.com:8443/v1/app", false},
		{"http://127.0.0.1:9090/api/live", "http://127.0.0.1:9091/api/live", false},
	} {
		err := sameOrigin(to(c.to), via(c.from))
		if (err == nil) != c.ok {
			t.Errorf("%s → %s: %v", c.from, c.to, err)
		}
	}
	many := make([]*http.Request, maxRedirects)
	for i := range many {
		many[i] = via("https://aether.example.com/")[0]
	}
	if sameOrigin(to("https://aether.example.com/"), many) == nil {
		t.Error("a loop of redirects was followed without end")
	}
}

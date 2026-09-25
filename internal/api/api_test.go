package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/x-chunk/teal"

	"github.com/x-chunk/celadon/internal/config"
)

const testKey = "aek_AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_abcd"

func TestNewSendsTheKeyTheWayTheProfileSays(t *testing.T) {
	for name, check := range map[string]func(*http.Request) bool{
		config.AuthBearer: func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer "+testKey },
		config.AuthBare:   func(r *http.Request) bool { return r.Header.Get("Authorization") == testKey },
		config.AuthCustom: func(r *http.Request) bool { return r.Header.Get("X-Aether-Key") == testKey },
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !check(r) {
					t.Errorf("headers: %v", r.Header)
				}
				if !strings.HasPrefix(r.Header.Get("User-Agent"), "celadon/") {
					t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
				}
				json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]any{"id": 7, "name": "Reports"}})
			}))
			defer srv.Close()

			c, err := New(config.Resolved{BaseURL: srv.URL, Key: testKey, AuthHeader: name}, Options{})
			if err != nil {
				t.Fatal(err)
			}
			app, _, err := c.App.Get(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if app.Name != "Reports" {
				t.Errorf("app = %+v", app)
			}
		})
	}
}

func TestExplain(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		err   error
		title string
		hint  string
		auth  bool
	}{
		{&teal.Error{Code: teal.CodeUnauthorized, StatusCode: 401, Message: "invalid key"}, "invalid key", "auth login", true},
		{&teal.Error{Code: teal.CodeInsufficientCredit, StatusCode: 402, Meta: &teal.Meta{Balance: 10, HasBalance: true}}, "insufficient_credits", "balance $0.00001", false},
		{&teal.Error{Code: teal.CodeQuotaExhausted, StatusCode: 429, Limit: "search:daily", Used: 50, LimitValue: 50, ResetAt: now.Add(3 * time.Hour)}, "quota search:daily is used up (50 of 50)", "in 3h0m0s", false},
		{&teal.Error{Code: teal.CodeRateLimited, StatusCode: 429, RetryAfter: 2 * time.Second}, "rate_limited", "retry in 2s", false},
		{&teal.Error{Code: teal.CodeUnavailable, StatusCode: 503}, "unavailable", "not enabled", false},
		{fmt.Errorf("wrapped: %w", &teal.Error{Code: teal.CodeAccountBlocked, StatusCode: 403, Message: "blocked"}), "blocked", "blocked", true},
		{context.DeadlineExceeded, "timed out", "--timeout", false},
		{errors.New("plain"), "plain", "", false},
	}
	for _, c := range cases {
		p := Explain(c.err, now)
		if !strings.Contains(p.Title, c.title) || !strings.Contains(p.Hint, c.hint) || p.Auth != c.auth {
			t.Errorf("Explain(%v) = %+v", c.err, p)
		}
	}
}

func TestExplainANetworkFailure(t *testing.T) {
	c, err := New(config.Resolved{BaseURL: "http://127.0.0.1:1", Key: testKey}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = c.App.Get(context.Background())
	p := Explain(err, time.Now())
	if !strings.Contains(p.Title, "could not reach the API") || !strings.Contains(p.Hint, "auth status") {
		t.Errorf("Explain = %+v", p)
	}
}

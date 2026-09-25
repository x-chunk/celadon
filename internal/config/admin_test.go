package config

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
)

const testAdminToken = "0123456789abcdef0123456789abcdef"

func TestValidateAdminToken(t *testing.T) {
	if err := ValidateAdminToken(testAdminToken); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "short", strings.Repeat("a", 23), "0123456789abcdef 123456789abcdef", strings.Repeat("a", 600)} {
		if err := ValidateAdminToken(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestAdminTokenRoundTrip(t *testing.T) {
	s := newStore(t)
	if err := s.SaveAdminToken("prod", testAdminToken); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadAdminToken("prod")
	if err != nil || got != testAdminToken {
		t.Fatalf("LoadAdminToken = %q, %v", got, err)
	}
	if s.HasKey("prod") {
		t.Error("an admin token was stored as an application key")
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(s.AdminTokenPath("prod"))
		if info.Mode().Perm() != 0o600 {
			t.Errorf("admin token mode = %04o", info.Mode().Perm())
		}
		if err := os.Chmod(s.AdminTokenPath("prod"), 0o640); err != nil {
			t.Fatal(err)
		}
		if _, err := s.LoadAdminToken("prod"); err == nil || !strings.Contains(err.Error(), "chmod 600") {
			t.Errorf("a group-readable token = %v", err)
		}
		os.Chmod(s.AdminTokenPath("prod"), 0o600)
	}
	if err := s.SaveAdminToken("prod", "short"); err == nil {
		t.Error("a short token was stored")
	}
	if err := s.DeleteAdminToken("prod"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadAdminToken("prod"); !errors.Is(err, ErrNoAdminToken) {
		t.Errorf("after delete = %v", err)
	}
}

func TestResolveAdmin(t *testing.T) {
	s := newStore(t)
	err := s.Update(func(c *Config) error {
		c.DefaultProfile = "prod"
		c.Profiles["prod"] = Profile{BaseURL: "https://prod.example"}
		c.Profiles["edge"] = Profile{BaseURL: "https://edge.example", AdminBaseURL: "https://internal.example"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// An admin-only profile: a token and no application key.
	if err := s.SaveAdminToken("prod", testAdminToken); err != nil {
		t.Fatal(err)
	}
	r, err := s.ResolveAdmin(Overrides{}, "http://fallback")
	if err != nil {
		t.Fatal(err)
	}
	if r.Profile != "prod" || r.BaseURL != "https://prod.example" || r.BaseURLSource != SourceProfile ||
		r.Token != testAdminToken || r.TokenSource != SourceFile {
		t.Errorf("admin-only profile: %+v", r)
	}

	r, err = s.ResolveAdmin(Overrides{Profile: "edge"}, "http://fallback")
	if !errors.Is(err, ErrNoAdminToken) {
		t.Fatalf("edge has no token, err = %v", err)
	}
	if r.BaseURL != "https://internal.example" {
		t.Errorf("admin_base_url was not used: %+v", r)
	}

	t.Setenv(EnvBaseURL, "https://env-public.example")
	r, _ = s.ResolveAdmin(Overrides{Profile: "prod"}, "http://fallback")
	if r.BaseURL != "https://env-public.example" {
		t.Errorf("$%s did not reach the admin API: %+v", EnvBaseURL, r)
	}
	t.Setenv(EnvAdminBaseURL, "https://env-admin.example")
	t.Setenv(EnvAdminToken, strings.Repeat("z", 30))
	t.Setenv(EnvAPIKey, "garbage")
	r, err = s.ResolveAdmin(Overrides{Profile: "edge"}, "http://fallback")
	if err != nil {
		t.Fatalf("a malformed application key stopped the admin API: %v", err)
	}
	if r.BaseURL != "https://env-admin.example" || r.TokenSource != SourceEnv {
		t.Errorf("env resolution: %+v", r)
	}
	r, _ = s.ResolveAdmin(Overrides{Profile: "edge", BaseURL: "https://flag.example"}, "http://fallback")
	if r.BaseURL != "https://flag.example" || r.BaseURLSource != SourceFlag {
		t.Errorf("flag resolution: %+v", r)
	}

	t.Setenv(EnvAdminToken, "short")
	if _, err := s.ResolveAdmin(Overrides{}, "http://fallback"); err == nil {
		t.Error("a malformed admin token in the environment was accepted")
	}
	if _, err := s.ResolveAdmin(Overrides{Profile: "../x"}, "http://fallback"); err == nil {
		t.Error("a hostile profile name was resolved")
	}
}

func TestRemoveProfileTakesTheAdminTokenToo(t *testing.T) {
	s := newStore(t)
	if err := s.SaveAdminToken("ops", testAdminToken); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveProfile("ops"); err != nil {
		t.Fatalf("an admin-only profile could not be removed: %v", err)
	}
	if s.HasAdminToken("ops") {
		t.Error("the admin token outlived its profile")
	}
}

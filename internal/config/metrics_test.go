package config

import (
	"os"
	"runtime"
	"testing"
)

func TestValidateMetricsToken(t *testing.T) {
	if err := ValidateMetricsToken("short-is-fine"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "has space", "tab\there"} {
		if err := ValidateMetricsToken(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestResolveMetrics(t *testing.T) {
	s := newStore(t)
	err := s.Update(func(c *Config) error {
		c.DefaultProfile = "prod"
		c.Profiles["prod"] = Profile{BaseURL: "https://prod.example"}
		c.Profiles["tunnel"] = Profile{MetricsURL: "http://127.0.0.1:19090"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// No token is a configuration, not a failure; and the Plug-In API's
	// base URL is never the listener's.
	r, err := s.ResolveMetrics(Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if r.BaseURL != DefaultMetricsURL || r.BaseURLSource != SourceDefault || r.Token != "" || r.TokenSource != SourceNone {
		t.Errorf("default: %+v", r)
	}

	if err := s.SaveMetricsToken("tunnel", "tok"); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(s.MetricsTokenPath("tunnel"))
		if info.Mode().Perm() != 0o600 {
			t.Errorf("mode = %04o", info.Mode().Perm())
		}
	}
	r, err = s.ResolveMetrics(Overrides{Profile: "tunnel"})
	if err != nil {
		t.Fatal(err)
	}
	if r.BaseURL != "http://127.0.0.1:19090" || r.BaseURLSource != SourceProfile || r.Token != "tok" || r.TokenSource != SourceFile {
		t.Errorf("profile: %+v", r)
	}

	t.Setenv(EnvMetricsURL, "http://env:9090")
	t.Setenv(EnvMetricsToken, "envtok")
	r, _ = s.ResolveMetrics(Overrides{Profile: "tunnel"})
	if r.BaseURL != "http://env:9090" || r.TokenSource != SourceEnv {
		t.Errorf("env: %+v", r)
	}
	r, _ = s.ResolveMetrics(Overrides{Profile: "tunnel", MetricsURL: "http://flag:9090"})
	if r.BaseURL != "http://flag:9090" || r.BaseURLSource != SourceFlag {
		t.Errorf("flag: %+v", r)
	}
	t.Setenv(EnvMetricsToken, "bad token")
	if _, err := s.ResolveMetrics(Overrides{}); err == nil {
		t.Error("a malformed token in the environment was accepted")
	}
	t.Setenv(EnvMetricsToken, "")
	if _, err := s.ResolveMetrics(Overrides{MetricsURL: "ftp://x"}); err == nil {
		t.Error("a bad listener url was accepted")
	}

	if err := s.RemoveProfile("tunnel"); err != nil {
		t.Fatal(err)
	}
	if s.HasMetricsToken("tunnel") {
		t.Error("the metrics token outlived its profile")
	}
}

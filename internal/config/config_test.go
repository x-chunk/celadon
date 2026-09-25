package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testKey = "aek_AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_abcd"

func newStore(t *testing.T) *Store {
	t.Helper()
	t.Setenv(EnvHome, "")
	t.Setenv(EnvProfile, "")
	t.Setenv(EnvAPIKey, "")
	t.Setenv(EnvBaseURL, "")
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestValidateProfileName(t *testing.T) {
	for _, ok := range []string{"default", "prod", "a", "work-2", "my_app"} {
		if err := ValidateProfileName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "..", "../x", "a/b", "Prod", "-x", "a.b", strings.Repeat("a", 33)} {
		if err := ValidateProfileName(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestValidateKey(t *testing.T) {
	if err := ValidateKey(testKey); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "aek_", "sk_live_123", "aek_abc def", "aek_abc/def", "aek_" + strings.Repeat("a", 300)} {
		if err := ValidateKey(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestValidateBaseURL(t *testing.T) {
	for _, ok := range []string{"http://localhost:8080", "https://example.com/aether"} {
		if err := ValidateBaseURL(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "example.com", "ftp://example.com", "https://", "https://u:p@example.com"} {
		if err := ValidateBaseURL(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestMaskKey(t *testing.T) {
	if got := MaskKey(testKey); got != "aek_AbCdEfGh…" {
		t.Errorf("MaskKey = %q", got)
	}
	if got := MaskKey("short"); strings.Contains(got, "short") {
		t.Errorf("a short key was shown: %q", got)
	}
}

func TestKeyRoundTrip(t *testing.T) {
	s := newStore(t)
	if err := s.SaveKey("prod", testKey); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadKey("prod")
	if err != nil {
		t.Fatal(err)
	}
	if got != testKey {
		t.Errorf("LoadKey = %q", got)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(s.KeyPath("prod"))
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("key file mode = %04o, want 0600", perm)
		}
		dir, err := os.Stat(filepath.Dir(s.KeyPath("prod")))
		if err != nil {
			t.Fatal(err)
		}
		if perm := dir.Mode().Perm(); perm != 0o700 {
			t.Errorf("keys dir mode = %04o, want 0700", perm)
		}
	}
	if !s.HasKey("prod") {
		t.Error("HasKey = false after SaveKey")
	}
	if err := s.DeleteKey("prod"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteKey("prod"); err != nil {
		t.Errorf("a second delete failed: %v", err)
	}
	if _, err := s.LoadKey("prod"); !errors.Is(err, ErrNoKey) {
		t.Errorf("LoadKey after delete = %v, want ErrNoKey", err)
	}
}

func TestLoadKeyRefusesAReadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits do not describe NTFS permissions")
	}
	s := newStore(t)
	if err := s.SaveKey("prod", testKey); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s.KeyPath("prod"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := s.LoadKey("prod")
	if err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("LoadKey = %v, want a refusal naming chmod", err)
	}
}

func TestLoadKeyRejectsGarbage(t *testing.T) {
	s := newStore(t)
	if err := os.MkdirAll(filepath.Dir(s.KeyPath("prod")), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.KeyPath("prod"), []byte("not a key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadKey("prod"); err == nil {
		t.Error("a file that is not a key was accepted")
	}
}

func TestConfigRoundTrip(t *testing.T) {
	s := newStore(t)
	cfg, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Profiles) != 0 {
		t.Fatalf("a missing config loaded %d profiles", len(cfg.Profiles))
	}
	err = s.Update(func(c *Config) error {
		c.DefaultProfile = "prod"
		c.Profiles["prod"] = Profile{BaseURL: "https://aether.example", AppName: "Reports", AppID: 7}
		c.Profiles["dev"] = Profile{BaseURL: "http://localhost:8080"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultProfile != "prod" || cfg.Profiles["prod"].AppName != "Reports" || cfg.Profiles["prod"].AppID != 7 {
		t.Errorf("round trip lost data: %+v", cfg)
	}
	if got := strings.Join(cfg.Names(), ","); got != "dev,prod" {
		t.Errorf("Names = %q", got)
	}
	raw, err := os.ReadFile(s.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "aek_") {
		t.Error("config.toml holds a key")
	}
}

func TestUpdateSavesNothingOnError(t *testing.T) {
	s := newStore(t)
	boom := errors.New("boom")
	err := s.Update(func(c *Config) error {
		c.DefaultProfile = "x"
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Update = %v", err)
	}
	if _, err := os.Stat(s.ConfigPath()); !errors.Is(err, os.ErrNotExist) {
		t.Error("a failed update wrote the config")
	}
}

func TestLoadRejectsAHostileProfileName(t *testing.T) {
	s := newStore(t)
	if err := os.MkdirAll(s.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	raw := "[profiles.\"../../etc\"]\nbase_url = \"http://x\"\n"
	if err := os.WriteFile(s.ConfigPath(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err == nil {
		t.Error("a profile name with a path in it was loaded")
	}
}

func TestRemoveProfileMovesTheDefault(t *testing.T) {
	s := newStore(t)
	for _, p := range []string{"a", "b"} {
		if err := s.SaveKey(p, testKey); err != nil {
			t.Fatal(err)
		}
	}
	err := s.Update(func(c *Config) error {
		c.DefaultProfile = "a"
		c.Profiles["a"] = Profile{}
		c.Profiles["b"] = Profile{}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveProfile("a"); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultProfile != "b" {
		t.Errorf("default = %q, want b", cfg.DefaultProfile)
	}
	if s.HasKey("a") {
		t.Error("the key of a removed profile is still there")
	}
	if err := s.RemoveProfile("nope"); !errors.Is(err, ErrNoProfile) {
		t.Errorf("removing an unknown profile = %v", err)
	}
}

func TestResolvePrecedence(t *testing.T) {
	s := newStore(t)
	err := s.Update(func(c *Config) error {
		c.DefaultProfile = "prod"
		c.Profiles["prod"] = Profile{BaseURL: "https://prod.example", AuthHeader: AuthCustom}
		c.Profiles["dev"] = Profile{BaseURL: "http://dev.example"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveKey("prod", testKey); err != nil {
		t.Fatal(err)
	}

	r, err := s.Resolve(Overrides{}, "http://fallback")
	if err != nil {
		t.Fatal(err)
	}
	if r.Profile != "prod" || r.ProfileSource != SourceProfile || r.BaseURL != "https://prod.example" ||
		r.Key != testKey || r.KeySource != SourceFile || r.AuthHeader != AuthCustom || !r.Known {
		t.Errorf("config resolution: %+v", r)
	}

	t.Setenv(EnvProfile, "dev")
	r, err = s.Resolve(Overrides{}, "http://fallback")
	if !errors.Is(err, ErrNoKey) {
		t.Fatalf("dev has no key, Resolve = %v", err)
	}
	if r.Profile != "dev" || r.ProfileSource != SourceEnv || r.BaseURL != "http://dev.example" {
		t.Errorf("env profile: %+v", r)
	}

	t.Setenv(EnvAPIKey, testKey)
	t.Setenv(EnvBaseURL, "https://env.example")
	r, err = s.Resolve(Overrides{Profile: "prod", BaseURL: "https://flag.example"}, "http://fallback")
	if err != nil {
		t.Fatal(err)
	}
	if r.Profile != "prod" || r.ProfileSource != SourceFlag || r.BaseURL != "https://flag.example" ||
		r.BaseURLSource != SourceFlag || r.KeySource != SourceEnv {
		t.Errorf("flag resolution: %+v", r)
	}

	r, err = s.Resolve(Overrides{Profile: "fresh"}, "http://fallback")
	if err != nil {
		t.Fatal(err)
	}
	if r.Known || r.BaseURL != "https://env.example" || r.BaseURLSource != SourceEnv {
		t.Errorf("unknown profile with env key: %+v", r)
	}

	t.Setenv(EnvAPIKey, "garbage")
	if _, err := s.Resolve(Overrides{}, "http://fallback"); err == nil {
		t.Error("a malformed key in the environment was accepted")
	}

	if _, err := s.Resolve(Overrides{Profile: "../x"}, "http://fallback"); err == nil {
		t.Error("a hostile profile name was resolved")
	}
}

func TestDefaultDirHonoursTheEnvironment(t *testing.T) {
	t.Setenv(EnvHome, "/tmp/celadon-test-home")
	d, err := DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	if d != "/tmp/celadon-test-home" {
		t.Errorf("DefaultDir = %q", d)
	}
}

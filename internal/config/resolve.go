package config

import (
	"fmt"
	"os"
	"strings"
)

// Auth header names as they are written in config.toml and on the command
// line. They map one to one onto teal.AuthHeader.
const (
	AuthBearer = "bearer"
	AuthBare   = "bare"
	AuthCustom = "x-aether-key"
)

// ValidateAuthHeader reports whether h names a way the API accepts a key.
func ValidateAuthHeader(h string) error {
	switch h {
	case "", AuthBearer, AuthBare, AuthCustom:
		return nil
	}
	return fmt.Errorf("invalid auth header %q: use %s, %s or %s", h, AuthBearer, AuthBare, AuthCustom)
}

// Overrides are what the command line says, which beats everything else.
type Overrides struct {
	Profile string
	BaseURL string
	// MetricsURL is --metrics-url, which only the metrics listener reads.
	MetricsURL string
}

// Where a value came from, for `auth status` to report.
const (
	SourceFlag    = "flag"
	SourceEnv     = "env"
	SourceProfile = "profile"
	SourceFile    = "file"
	SourceDefault = "default"
)

// Resolved is everything a client needs, settled.
type Resolved struct {
	Profile       string
	ProfileSource string
	// Known says whether the profile exists in config.toml. An unknown
	// profile can still be used when the key comes from the environment.
	Known bool

	BaseURL       string
	BaseURLSource string
	AuthHeader    string

	Key       string
	KeySource string

	// Stored is the profile as config.toml has it.
	Stored Profile
}

// Resolve settles which profile is in use and what it resolves to.
//
// Precedence, highest first — the command line, then the environment, then
// config.toml, then the built-in defaults:
//
//	profile   --profile, $CELADON_PROFILE, default_profile, "default"
//	base url  --base-url, $CELADON_BASE_URL, the profile's base_url, fallback
//	key       $CELADON_API_KEY, keys/<profile>
//
// There is deliberately no flag for the key: an argument is visible to every
// user on the machine through ps and is written into shell history.
//
// When the key cannot be found Resolve still returns everything else, with
// the error, so that a caller reporting the state can report it.
func (s *Store) Resolve(o Overrides, fallbackURL string) (Resolved, error) {
	cfg, err := s.Load()
	if err != nil {
		return Resolved{}, err
	}

	var r Resolved
	switch {
	case o.Profile != "":
		r.Profile, r.ProfileSource = o.Profile, SourceFlag
	case strings.TrimSpace(os.Getenv(EnvProfile)) != "":
		r.Profile, r.ProfileSource = strings.TrimSpace(os.Getenv(EnvProfile)), SourceEnv
	case cfg.DefaultProfile != "":
		r.Profile, r.ProfileSource = cfg.DefaultProfile, SourceProfile
	default:
		r.Profile, r.ProfileSource = DefaultProfile, SourceDefault
	}
	if err := ValidateProfileName(r.Profile); err != nil {
		return Resolved{}, err
	}
	r.Stored, r.Known = cfg.Profiles[r.Profile]
	r.AuthHeader = r.Stored.AuthHeader
	if err := ValidateAuthHeader(r.AuthHeader); err != nil {
		return Resolved{}, fmt.Errorf("profile %q: %w", r.Profile, err)
	}

	switch {
	case o.BaseURL != "":
		r.BaseURL, r.BaseURLSource = o.BaseURL, SourceFlag
	case strings.TrimSpace(os.Getenv(EnvBaseURL)) != "":
		r.BaseURL, r.BaseURLSource = strings.TrimSpace(os.Getenv(EnvBaseURL)), SourceEnv
	case r.Stored.BaseURL != "":
		r.BaseURL, r.BaseURLSource = r.Stored.BaseURL, SourceProfile
	default:
		r.BaseURL, r.BaseURLSource = fallbackURL, SourceDefault
	}
	if err := ValidateBaseURL(r.BaseURL); err != nil {
		return Resolved{}, err
	}

	if k := strings.TrimSpace(os.Getenv(EnvAPIKey)); k != "" {
		if err := ValidateKey(k); err != nil {
			return r, fmt.Errorf("$%s: %w", EnvAPIKey, err)
		}
		r.Key, r.KeySource = k, SourceEnv
		return r, nil
	}
	key, err := s.LoadKey(r.Profile)
	if err != nil {
		return r, err
	}
	r.Key, r.KeySource = key, SourceFile
	return r, nil
}

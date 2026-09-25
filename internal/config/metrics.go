package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"
)

// The metrics listener is the third thing a profile can point at. It is a
// port of its own (:9090), published on the loopback address, so it has an
// address of its own — metrics_url, the loopback by default, which is also
// where an SSH tunnel puts it — and its METRICS_TOKEN is optional: a
// deployment without one serves the listener to anybody who reaches the port.
// A token, when there is one, is kept in metrics/<profile> under the same
// rules as a key.

// Environment variables for the metrics listener.
const (
	EnvMetricsToken = "CELADON_METRICS_TOKEN" // the token, bypassing the file
	EnvMetricsURL   = "CELADON_METRICS_URL"   // the listener, bypassing the profile
)

// DefaultMetricsURL is where the listener is on its own machine, and where
// `ssh -N -L 9090:127.0.0.1:9090 host` puts a remote one.
const DefaultMetricsURL = "http://127.0.0.1:9090"

// SourceNone says a credential is absent, which for the metrics listener is
// a configuration rather than a failure.
const SourceNone = "none"

const (
	metricsDir       = "metrics"
	maxMetricsTokLen = 512
)

// ErrNoMetricsToken is returned when a profile has no metrics token stored.
var ErrNoMetricsToken = errors.New("no metrics token stored")

var metricsToken = secret{dir: metricsDir, name: "metrics token", missing: ErrNoMetricsToken, validate: ValidateMetricsToken, maxLen: maxMetricsTokLen}

// ValidateMetricsToken reports whether token could be a METRICS_TOKEN. The
// listener sets no minimum length, so this checks only that it is one line
// of printable characters.
func ValidateMetricsToken(token string) error {
	if token == "" {
		return errors.New("the metrics token is empty")
	}
	if len(token) > maxMetricsTokLen {
		return fmt.Errorf("the metrics token is %d characters long, which is longer than this accepts", len(token))
	}
	for _, r := range token {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return errors.New("the metrics token contains whitespace or a control character")
		}
	}
	return nil
}

// MetricsTokenPath is where the metrics token of the named profile is.
func (s *Store) MetricsTokenPath(profile string) string { return s.secretPath(metricsToken, profile) }

// SaveMetricsToken stores the metrics token of a profile.
func (s *Store) SaveMetricsToken(profile, token string) error {
	return s.saveSecret(metricsToken, profile, token)
}

// LoadMetricsToken reads the metrics token of a profile.
func (s *Store) LoadMetricsToken(profile string) (string, error) {
	return s.loadSecret(metricsToken, profile)
}

// HasMetricsToken reports whether a metrics token file exists.
func (s *Store) HasMetricsToken(profile string) bool { return s.hasSecret(metricsToken, profile) }

// DeleteMetricsToken removes the metrics token of a profile.
func (s *Store) DeleteMetricsToken(profile string) error {
	return s.deleteSecret(metricsToken, profile)
}

// MetricsResolved is everything a metrics client needs, settled.
type MetricsResolved struct {
	Profile       string
	ProfileSource string
	Known         bool

	BaseURL       string
	BaseURLSource string

	// Token is empty, with TokenSource SourceNone, when there is none —
	// which a listener without a METRICS_TOKEN expects.
	Token       string
	TokenSource string

	Stored Profile
}

// ResolveMetrics settles the profile, the listener and its token.
//
// The profile is chosen as Resolve chooses it. The listener is, highest
// first: o.MetricsURL (--metrics-url), $CELADON_METRICS_URL, the profile's
// metrics_url, then DefaultMetricsURL. It is never the Plug-In API's base
// URL: the listener is another port, and usually another route to it. The
// token is $CELADON_METRICS_TOKEN or metrics/<profile>, and its absence is
// not an error.
func (s *Store) ResolveMetrics(o Overrides) (MetricsResolved, error) {
	pub, err := s.Resolve(Overrides{Profile: o.Profile}, DefaultMetricsURL)
	if err != nil && pub.Profile == "" {
		return MetricsResolved{}, err
	}
	r := MetricsResolved{Profile: pub.Profile, ProfileSource: pub.ProfileSource, Known: pub.Known, Stored: pub.Stored}
	switch {
	case o.MetricsURL != "":
		r.BaseURL, r.BaseURLSource = o.MetricsURL, SourceFlag
	case strings.TrimSpace(os.Getenv(EnvMetricsURL)) != "":
		r.BaseURL, r.BaseURLSource = strings.TrimSpace(os.Getenv(EnvMetricsURL)), SourceEnv
	case r.Stored.MetricsURL != "":
		r.BaseURL, r.BaseURLSource = r.Stored.MetricsURL, SourceProfile
	default:
		r.BaseURL, r.BaseURLSource = DefaultMetricsURL, SourceDefault
	}
	if err := ValidateBaseURL(r.BaseURL); err != nil {
		return MetricsResolved{}, err
	}

	if t := strings.TrimSpace(os.Getenv(EnvMetricsToken)); t != "" {
		if err := ValidateMetricsToken(t); err != nil {
			return r, fmt.Errorf("$%s: %w", EnvMetricsToken, err)
		}
		r.Token, r.TokenSource = t, SourceEnv
		return r, nil
	}
	token, err := s.LoadMetricsToken(r.Profile)
	switch {
	case errors.Is(err, ErrNoMetricsToken):
		r.TokenSource = SourceNone
	case err != nil:
		return r, err
	default:
		r.Token, r.TokenSource = token, SourceFile
	}
	return r, nil
}

package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"
)

// The admin token is the deployment's ADMIN_TOKEN: it opens the private admin
// API of one deployment, not an application. It is kept beside the profile
// that names the deployment, in admin/<profile>, under the same rules as a
// key — mode 0600 in a 0700 directory, written atomically, refused when
// anybody else can read it — and a profile may hold one without holding an
// application key at all.

// Environment variables for the admin API.
const (
	EnvAdminToken   = "CELADON_ADMIN_TOKEN"    // the token, bypassing the file
	EnvAdminBaseURL = "CELADON_ADMIN_BASE_URL" // where the admin API is, when not where the profile's API is
)

// MinAdminTokenLength is the shortest token a deployment runs with; the
// server refuses to register its admin routes behind anything shorter.
const MinAdminTokenLength = 24

const (
	adminDir       = "admin"
	maxAdminTokLen = 512
)

// ErrNoAdminToken is returned when a profile has no admin token stored.
var ErrNoAdminToken = errors.New("no admin token stored")

var adminToken = secret{dir: adminDir, name: "admin token", missing: ErrNoAdminToken, validate: ValidateAdminToken, maxLen: maxAdminTokLen}

// ValidateAdminToken reports whether token could be a deployment's
// ADMIN_TOKEN. It checks the shape only; whether it opens anything is the
// server's to say.
func ValidateAdminToken(token string) error {
	if len(token) < MinAdminTokenLength {
		return fmt.Errorf("the admin token is %d characters long; a deployment never runs with one shorter than %d", len(token), MinAdminTokenLength)
	}
	if len(token) > maxAdminTokLen {
		return fmt.Errorf("the admin token is %d characters long, which is longer than this accepts", len(token))
	}
	for _, r := range token {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return errors.New("the admin token contains whitespace or a control character")
		}
	}
	return nil
}

// AdminTokenPath is where the admin token of the named profile is.
func (s *Store) AdminTokenPath(profile string) string { return s.secretPath(adminToken, profile) }

// SaveAdminToken stores the admin token of a profile.
func (s *Store) SaveAdminToken(profile, token string) error {
	return s.saveSecret(adminToken, profile, token)
}

// LoadAdminToken reads the admin token of a profile.
func (s *Store) LoadAdminToken(profile string) (string, error) {
	return s.loadSecret(adminToken, profile)
}

// HasAdminToken reports whether an admin token file exists for the profile.
func (s *Store) HasAdminToken(profile string) bool { return s.hasSecret(adminToken, profile) }

// DeleteAdminToken removes the admin token of a profile.
func (s *Store) DeleteAdminToken(profile string) error { return s.deleteSecret(adminToken, profile) }

// AdminResolved is everything an admin client needs, settled.
type AdminResolved struct {
	Profile       string
	ProfileSource string
	Known         bool

	BaseURL       string
	BaseURLSource string

	Token       string
	TokenSource string

	Stored Profile
}

// ResolveAdmin settles the profile, the deployment and the admin token.
//
// The profile is chosen exactly as Resolve chooses it. The base URL is,
// highest first: --base-url, $CELADON_ADMIN_BASE_URL, the profile's
// admin_base_url, then whatever Resolve would use for the Plug-In API — the
// admin API is served on the same listener unless a proxy in front of the
// deployment says otherwise. The token is $CELADON_ADMIN_TOKEN or
// admin/<profile>; there is no flag for it, for the same reason there is none
// for a key.
//
// When the token cannot be found everything else is still returned, with the
// error, so a caller reporting the state can report it.
func (s *Store) ResolveAdmin(o Overrides, fallbackURL string) (AdminResolved, error) {
	// The profile and the public base URL come from Resolve. Whatever it
	// says about the application key — missing, malformed, unreadable — is
	// no concern of the admin API: Resolve fills in everything else before
	// it looks at the key, and only a failure before that leaves the profile
	// empty.
	pub, err := s.Resolve(Overrides{Profile: o.Profile}, fallbackURL)
	if err != nil && pub.Profile == "" {
		return AdminResolved{}, err
	}

	r := AdminResolved{
		Profile: pub.Profile, ProfileSource: pub.ProfileSource, Known: pub.Known, Stored: pub.Stored,
	}
	switch {
	case o.BaseURL != "":
		r.BaseURL, r.BaseURLSource = o.BaseURL, SourceFlag
	case strings.TrimSpace(os.Getenv(EnvAdminBaseURL)) != "":
		r.BaseURL, r.BaseURLSource = strings.TrimSpace(os.Getenv(EnvAdminBaseURL)), SourceEnv
	case r.Stored.AdminBaseURL != "":
		r.BaseURL, r.BaseURLSource = r.Stored.AdminBaseURL, SourceProfile
	default:
		r.BaseURL, r.BaseURLSource = pub.BaseURL, pub.BaseURLSource
	}
	if err := ValidateBaseURL(r.BaseURL); err != nil {
		return AdminResolved{}, err
	}

	if t := strings.TrimSpace(os.Getenv(EnvAdminToken)); t != "" {
		if err := ValidateAdminToken(t); err != nil {
			return r, fmt.Errorf("$%s: %w", EnvAdminToken, err)
		}
		r.Token, r.TokenSource = t, SourceEnv
		return r, nil
	}
	token, err := s.LoadAdminToken(r.Profile)
	if err != nil {
		return r, err
	}
	r.Token, r.TokenSource = token, SourceFile
	return r, nil
}

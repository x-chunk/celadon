// Package config owns everything celadon keeps on disk: the profiles in
// config.toml and the application keys beside them.
//
// The layout under the home directory (~/.celadon, or $CELADON_HOME):
//
//	config.toml        profiles, the default one, and nothing secret
//	keys/<profile>     one application key per file, mode 0600
//
// Keys are kept out of config.toml on purpose. The config is something a
// person opens in an editor, pastes into an issue and syncs between
// machines; a key is none of those things, and keeping it in a file of its
// own lets its permissions be checked on every read.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Environment variables that override what is on disk.
const (
	EnvHome    = "CELADON_HOME"     // the directory everything lives in
	EnvProfile = "CELADON_PROFILE"  // the profile to use
	EnvAPIKey  = "CELADON_API_KEY"  // the key, bypassing the key file
	EnvBaseURL = "CELADON_BASE_URL" // the deployment, bypassing the profile
)

// DefaultProfile is the name a profile gets when nobody names one.
const DefaultProfile = "default"

const (
	configFile = "config.toml"
	keysDir    = "keys"
	dirMode    = 0o700
	fileMode   = 0o600
)

// ErrNoProfile is returned when a named profile does not exist.
var ErrNoProfile = errors.New("profile not found")

var profileName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// ValidateProfileName reports whether name may be used as a profile name. It
// becomes a file name under keys/, so it is held to a closed alphabet: no
// separators, no dots, nothing a path could be built out of.
func ValidateProfileName(name string) error {
	if !profileName.MatchString(name) {
		return fmt.Errorf("invalid profile name %q: use 1-32 lowercase letters, digits, '-' or '_', starting with a letter or digit", name)
	}
	return nil
}

// ValidateBaseURL reports whether raw can address a deployment.
func ValidateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid base url %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("invalid base url %q: the scheme must be http or https", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("invalid base url %q: it has no host", raw)
	}
	if u.User != nil {
		return fmt.Errorf("invalid base url %q: credentials do not belong in the url", raw)
	}
	return nil
}

// Profile is one deployment and one application on it. The key is not here:
// it is in keys/<name>.
type Profile struct {
	BaseURL    string `toml:"base_url,omitempty"`
	AuthHeader string `toml:"auth_header,omitempty"`

	// What the key opened when it was stored, so that `auth status` can say
	// which application a profile is without spending a request on it.
	AppID     int64  `toml:"app_id,omitempty"`
	AppName   string `toml:"app_name,omitempty"`
	KeyPrefix string `toml:"key_prefix,omitempty"`
}

// Config is the whole of config.toml.
type Config struct {
	DefaultProfile string             `toml:"default_profile,omitempty"`
	Profiles       map[string]Profile `toml:"profiles,omitempty"`
}

// Store reads and writes one celadon home directory.
type Store struct {
	dir string
}

// Open returns the store at dir, or at the default location when dir is
// empty. Nothing is created until something is written.
func Open(dir string) (*Store, error) {
	if dir == "" {
		var err error
		if dir, err = DefaultDir(); err != nil {
			return nil, err
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", dir, err)
	}
	return &Store{dir: abs}, nil
}

// DefaultDir is $CELADON_HOME when it is set, ~/.celadon otherwise.
func DefaultDir() (string, error) {
	if d := strings.TrimSpace(os.Getenv(EnvHome)); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating the home directory (set %s to choose one): %w", EnvHome, err)
	}
	return filepath.Join(home, ".celadon"), nil
}

// Dir is the directory this store reads and writes.
func (s *Store) Dir() string { return s.dir }

// ConfigPath is where config.toml is.
func (s *Store) ConfigPath() string { return filepath.Join(s.dir, configFile) }

// KeyPath is where the key of the named profile is.
func (s *Store) KeyPath(profile string) string { return filepath.Join(s.dir, keysDir, profile) }

// Load reads config.toml. A missing file is an empty config, not an error.
func (s *Store) Load() (*Config, error) {
	cfg := &Config{Profiles: map[string]Profile{}}
	raw, err := os.ReadFile(s.ConfigPath())
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.ConfigPath(), err)
	}
	if _, err := toml.Decode(string(raw), cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", s.ConfigPath(), err)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}
	for name := range cfg.Profiles {
		if err := ValidateProfileName(name); err != nil {
			return nil, fmt.Errorf("%s: %w", s.ConfigPath(), err)
		}
	}
	return cfg, nil
}

// Save writes config.toml atomically: a reader sees the old file or the new
// one and never half of either.
func (s *Store) Save(cfg *Config) error {
	var b strings.Builder
	b.WriteString("# Written by celadon. Keys are not kept here: they are in keys/<profile>.\n\n")
	if err := toml.NewEncoder(&b).Encode(cfg); err != nil {
		return fmt.Errorf("encoding the config: %w", err)
	}
	if err := os.MkdirAll(s.dir, dirMode); err != nil {
		return fmt.Errorf("creating %s: %w", s.dir, err)
	}
	return writeFileAtomic(s.ConfigPath(), []byte(b.String()), fileMode)
}

// Names returns the profile names in order.
func (c *Config) Names() []string {
	names := make([]string, 0, len(c.Profiles))
	for n := range c.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Update loads the config, hands it to fn and saves what fn left behind. A
// failing fn saves nothing.
func (s *Store) Update(fn func(*Config) error) error {
	cfg, err := s.Load()
	if err != nil {
		return err
	}
	if err := fn(cfg); err != nil {
		return err
	}
	return s.Save(cfg)
}

// RemoveProfile deletes a profile and its key. When it was the default, the
// default moves to the first profile left, if any.
func (s *Store) RemoveProfile(name string) error {
	if err := ValidateProfileName(name); err != nil {
		return err
	}
	err := s.Update(func(cfg *Config) error {
		if _, ok := cfg.Profiles[name]; !ok {
			if _, kerr := os.Stat(s.KeyPath(name)); kerr != nil {
				return fmt.Errorf("%w: %s", ErrNoProfile, name)
			}
		}
		delete(cfg.Profiles, name)
		if cfg.DefaultProfile == name {
			cfg.DefaultProfile = ""
			if names := cfg.Names(); len(names) > 0 {
				cfg.DefaultProfile = names[0]
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.DeleteKey(name)
}

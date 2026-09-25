package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// KeyPrefix is how every Aether application key begins.
const KeyPrefix = "aek_"

// maxKeyLen bounds what is read from a key file, so a file that is not a key
// at all cannot make the CLI read a gigabyte into memory.
const maxKeyLen = 256

// ErrNoKey is returned when a profile has no key stored.
var ErrNoKey = errors.New("no api key stored")

// ValidateKey reports whether key looks like an application key. It checks
// the shape only: whether the key opens anything is the API's to say.
func ValidateKey(key string) error {
	if !strings.HasPrefix(key, KeyPrefix) {
		return fmt.Errorf("the key does not look like an Aether application key: it should start with %q", KeyPrefix)
	}
	if len(key) > maxKeyLen {
		return fmt.Errorf("the key is %d characters long, which is longer than any application key", len(key))
	}
	for _, r := range key[len(KeyPrefix):] {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return fmt.Errorf("the key contains %q, which an application key never does", r)
		}
	}
	if len(key) == len(KeyPrefix) {
		return fmt.Errorf("the key is empty after its %q prefix", KeyPrefix)
	}
	return nil
}

// MaskKey renders a key the way the bot does: the first twelve characters,
// then an ellipsis. It is what may be printed.
func MaskKey(key string) string {
	const shown = 12
	if len(key) <= shown {
		return strings.Repeat("•", len(key))
	}
	return key[:shown] + "…"
}

// secret is one kind of credential kept a file per profile: where the files
// are, what a valid one looks like, and what it is called in a message.
type secret struct {
	dir      string
	name     string
	missing  error
	validate func(string) error
	maxLen   int
}

var appKey = secret{dir: keysDir, name: "key", missing: ErrNoKey, validate: ValidateKey, maxLen: maxKeyLen}

func (s *Store) secretPath(k secret, profile string) string {
	return filepath.Join(s.dir, k.dir, profile)
}

// saveSecret stores a credential of a profile, readable by its owner alone.
func (s *Store) saveSecret(k secret, profile, value string) error {
	if err := ValidateProfileName(profile); err != nil {
		return err
	}
	if err := k.validate(value); err != nil {
		return err
	}
	dir := filepath.Join(s.dir, k.dir)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	// MkdirAll leaves an existing directory's mode alone; one that was made
	// by hand with a looser mode is tightened here, where it matters.
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dir, dirMode); err != nil {
			return fmt.Errorf("securing %s: %w", dir, err)
		}
	}
	return writeFileAtomic(s.secretPath(k, profile), []byte(value+"\n"), fileMode)
}

// loadSecret reads a credential of a profile.
//
// A file anyone but its owner can read is refused, as ssh refuses a private
// key: a credential somebody else may already have copied is not one to go
// on sending.
func (s *Store) loadSecret(k secret, profile string) (string, error) {
	if err := ValidateProfileName(profile); err != nil {
		return "", err
	}
	path := s.secretPath(k, profile)
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%w for profile %q", k.missing, profile)
	}
	if err != nil {
		return "", fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", path)
	}
	if err := checkPrivate(path, info); err != nil {
		return "", err
	}

	// Bounded, so a file that is not a credential at all is not read into
	// memory whole.
	raw, err := io.ReadAll(io.LimitReader(f, int64(k.maxLen)+2))
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	value := strings.TrimSpace(string(raw))
	if err := k.validate(value); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return value, nil
}

func (s *Store) hasSecret(k secret, profile string) bool {
	if ValidateProfileName(profile) != nil {
		return false
	}
	_, err := os.Stat(s.secretPath(k, profile))
	return err == nil
}

// deleteSecret removes a credential of a profile. One that is not there is
// not an error: the outcome asked for is already the case.
func (s *Store) deleteSecret(k secret, profile string) error {
	if err := ValidateProfileName(profile); err != nil {
		return err
	}
	if err := os.Remove(s.secretPath(k, profile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing the %s of %q: %w", k.name, profile, err)
	}
	return nil
}

// SaveKey stores the application key of a profile.
func (s *Store) SaveKey(profile, key string) error { return s.saveSecret(appKey, profile, key) }

// LoadKey reads the application key of a profile.
func (s *Store) LoadKey(profile string) (string, error) { return s.loadSecret(appKey, profile) }

// HasKey reports whether a key file exists for the profile.
func (s *Store) HasKey(profile string) bool { return s.hasSecret(appKey, profile) }

// DeleteKey removes the application key of a profile.
func (s *Store) DeleteKey(profile string) error { return s.deleteSecret(appKey, profile) }

func checkPrivate(path string, info os.FileInfo) error {
	if runtime.GOOS == "windows" {
		// NTFS permissions are ACLs, which the mode bits do not describe.
		return nil
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("%s is accessible by other users (mode %04o); run `chmod 600 %s`", path, perm, path)
	}
	return nil
}

// writeFileAtomic writes data to a temporary file beside path and renames it
// into place, so that path holds either what it held or all of data.
func writeFileAtomic(path string, data []byte, mode os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if runtime.GOOS != "windows" {
		if err = tmp.Chmod(mode); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

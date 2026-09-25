package config

import (
	"errors"
	"fmt"
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

// SaveKey stores the key of a profile, readable by its owner alone.
func (s *Store) SaveKey(profile, key string) error {
	if err := ValidateProfileName(profile); err != nil {
		return err
	}
	if err := ValidateKey(key); err != nil {
		return err
	}
	dir := filepath.Join(s.dir, keysDir)
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
	return writeFileAtomic(s.KeyPath(profile), []byte(key+"\n"), fileMode)
}

// LoadKey reads the key of a profile.
//
// A key file anyone but its owner can read is refused, as ssh refuses a
// private key: a key somebody else may already have copied is not one to go
// on sending.
func (s *Store) LoadKey(profile string) (string, error) {
	if err := ValidateProfileName(profile); err != nil {
		return "", err
	}
	path := s.KeyPath(profile)
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%w for profile %q", ErrNoKey, profile)
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

	buf := make([]byte, maxKeyLen+2)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	key := strings.TrimSpace(string(buf[:n]))
	if err := ValidateKey(key); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return key, nil
}

// HasKey reports whether a key file exists for the profile.
func (s *Store) HasKey(profile string) bool {
	if ValidateProfileName(profile) != nil {
		return false
	}
	_, err := os.Stat(s.KeyPath(profile))
	return err == nil
}

// DeleteKey removes the key of a profile. A key that is not there is not an
// error: the outcome asked for is already the case.
func (s *Store) DeleteKey(profile string) error {
	if err := ValidateProfileName(profile); err != nil {
		return err
	}
	if err := os.Remove(s.KeyPath(profile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing the key of %q: %w", profile, err)
	}
	return nil
}

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

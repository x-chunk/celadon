package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// getenv is os.Getenv with the surrounding space taken off.
func getenv(key string) string { return strings.TrimSpace(os.Getenv(key)) }

// confirm asks before something that cannot be undone. --yes answers for a
// script; a script without it is refused rather than left waiting for an
// answer nobody will type.
func confirm(env *Env, yes bool, question string) error {
	if yes {
		return nil
	}
	if !env.IO.CanPrompt() {
		return usageError(errors.New("refusing to go ahead without confirmation: pass --yes"))
	}
	ok, err := env.IO.Confirm(question)
	if err != nil {
		return err
	}
	if !ok {
		env.Printer().Info("Aborted.")
		return silentError{code: ExitError}
	}
	return nil
}

// parseID reads a positive id from an argument.
func parseID(what, arg string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(arg), "#"), 10, 64)
	if err != nil || id <= 0 {
		return 0, usageError(fmt.Errorf("invalid %s %q: expected a positive number", what, arg))
	}
	return id, nil
}

// parseChat reads a chat id, which is negative for a group.
func parseChat(arg string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(arg), 10, 64)
	if err != nil || id == 0 {
		return 0, usageError(fmt.Errorf("invalid chat id %q: expected a non-zero number", arg))
	}
	return id, nil
}

// parseSeconds reads a span of time for a setting: a Go duration ("90s",
// "36h"), a count of days ("7d"), a bare count of seconds, or "off"/"0".
func parseSeconds(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	switch s {
	case "", "0", "off", "none":
		return 0, nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n < 0 {
			return 0, fmt.Errorf("invalid duration %q: it cannot be negative", s)
		}
		return n, nil
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.ParseFloat(days, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return int64(n * 24 * 3600), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid duration %q: use 90s, 12h, 7d or off", s)
	}
	if d%time.Second != 0 {
		return 0, fmt.Errorf("invalid duration %q: the API counts whole seconds", s)
	}
	return int64(d / time.Second), nil
}

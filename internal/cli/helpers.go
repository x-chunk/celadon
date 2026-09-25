package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/x-chunk/celadon/internal/query"
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

// parseSeconds reads a span of time for a setting, as a usage error when it
// cannot.
func parseSeconds(s string) (int64, error) {
	n, err := query.Seconds(s)
	if err != nil {
		return 0, usageError(err)
	}
	return n, nil
}

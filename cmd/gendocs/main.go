// Command gendocs writes celadon's man pages and shell completion scripts,
// for packaging. It is run by `make docs` and by the release build.
//
//	go run ./cmd/gendocs -out dist
//
// writes dist/man/*.1 and dist/completions/celadon.{bash,zsh,fish,ps1}.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	"github.com/x-chunk/celadon/internal/cli"
	"github.com/x-chunk/celadon/internal/iostreams"
)

func main() {
	out := flag.String("out", ".", "directory to write man/ and completions/ into")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "gendocs:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	var discard bytes.Buffer
	root := cli.NewRootCmd(&cli.Env{IO: iostreams.Test(nil, &discard, &discard)})
	root.DisableAutoGenTag = true

	manDir := filepath.Join(out, "man")
	if err := os.MkdirAll(manDir, 0o755); err != nil {
		return err
	}
	header := &doc.GenManHeader{
		Title:   "CELADON",
		Section: "1",
		Source:  "celadon",
		Manual:  "celadon manual",
		Date:    releaseDate(),
	}
	if err := doc.GenManTree(root, header, manDir); err != nil {
		return fmt.Errorf("man pages: %w", err)
	}

	compDir := filepath.Join(out, "completions")
	if err := os.MkdirAll(compDir, 0o755); err != nil {
		return err
	}
	for name, gen := range map[string]func(*cobra.Command, string) error{
		"celadon.bash": func(c *cobra.Command, p string) error { return c.GenBashCompletionFileV2(p, true) },
		"celadon.zsh":  func(c *cobra.Command, p string) error { return c.GenZshCompletionFile(p) },
		"celadon.fish": func(c *cobra.Command, p string) error { return c.GenFishCompletionFile(p, true) },
		"celadon.ps1":  func(c *cobra.Command, p string) error { return c.GenPowerShellCompletionFileWithDesc(p) },
	} {
		if err := gen(root, filepath.Join(compDir, name)); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

// releaseDate pins the date in the man pages to SOURCE_DATE_EPOCH when it is
// set, so that a release build is reproducible.
func releaseDate() *time.Time {
	if s := os.Getenv("SOURCE_DATE_EPOCH"); s != "" {
		var sec int64
		if _, err := fmt.Sscan(s, &sec); err == nil {
			t := time.Unix(sec, 0).UTC()
			return &t
		}
	}
	t := time.Now().UTC()
	return &t
}

// Command celadon is a command line and a terminal interface for the Aether
// Plug-In API.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/x-chunk/celadon/internal/cli"
	"github.com/x-chunk/celadon/internal/iostreams"
	"github.com/x-chunk/celadon/internal/tui"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	env := &cli.Env{IO: iostreams.System(), RunTUI: runTUI}
	return cli.Execute(ctx, env, os.Args[1:])
}

// runTUI opens the interface on the profile the command line resolved.
func runTUI(ctx context.Context, env *cli.Env) error {
	client, r, err := env.Client()
	if err != nil {
		return err
	}
	return tui.Run(ctx, client, tui.Options{Profile: r.Profile, BaseURL: r.BaseURL})
}

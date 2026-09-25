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
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	env := &cli.Env{IO: iostreams.System()}
	return cli.Execute(ctx, env, os.Args[1:])
}

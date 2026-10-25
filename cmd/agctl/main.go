package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/ccsert/artifact-gateway/internal/agctl"
)

func main() {
	// SIGINT belongs to the active RunCLI command (or the shell's line editor).
	// Cancelling the root on Ctrl-C would poison every subsequent shell command.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	code := agctl.RunCLI(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

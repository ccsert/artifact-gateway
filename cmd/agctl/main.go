package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/ccsert/artifact-gateway/internal/agctl"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := agctl.RunCLI(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

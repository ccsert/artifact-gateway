package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/artifact-gateway/artifact-gateway/internal/agctl"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := agctl.RunCLI(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
)

func main() {
	if len(os.Args) != 3 || (os.Args[1] != "present" && os.Args[1] != "absent") {
		fatal("usage: rustfs-object-state <present|absent> <object-key>")
	}
	store, err := objectstore.NewRustFSStore(
		os.Getenv("GATEWAY_RUSTFS_ENDPOINT"),
		os.Getenv("RUSTFS_ACCESS_KEY"),
		os.Getenv("RUSTFS_SECRET_KEY"),
		os.Getenv("GATEWAY_RUSTFS_BUCKET"),
	)
	if err != nil {
		fatal("configure RustFS object check: %v", err)
	}
	_, err = store.Stat(context.Background(), os.Args[2])
	present := err == nil
	if err != nil && !errors.Is(err, objectstore.ErrNotFound) {
		fatal("stat RustFS object %q: %v", os.Args[2], err)
	}
	if wantPresent := os.Args[1] == "present"; present != wantPresent {
		fatal("RustFS object %q state is present=%t, expected present=%t", os.Args[2], present, wantPresent)
	}
}

func fatal(format string, values ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", values...)
	os.Exit(1)
}

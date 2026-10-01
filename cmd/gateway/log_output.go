package main

import "github.com/artifact-gateway/artifact-gateway/internal/operationalog"

// The runtime callback returns only after its HTTP and resource cleanup defers
// have run. Log output therefore stays available throughout that cleanup.
func runWithLogOutput(output *operationalog.Output, run func() int) (code int, cleanupErr error) {
	defer func() { cleanupErr = output.Close() }()
	return run(), nil
}

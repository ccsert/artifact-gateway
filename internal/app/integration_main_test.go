//go:build integration

package app

import (
	"fmt"
	"os"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/testsupport"
)

func TestMain(m *testing.M) {
	if err := testsupport.ValidateIsolatedDatabaseURL(os.Getenv("TEST_DATABASE_URL")); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(m.Run())
}

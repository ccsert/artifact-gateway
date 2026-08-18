package testsupport

import (
	"fmt"
	"net/url"
	"strings"
)

// ValidateIsolatedDatabaseURL prevents integration tests from writing fixtures
// into a development or operator-visible Artifact Gateway database.
func ValidateIsolatedDatabaseURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("TEST_DATABASE_URL is invalid")
	}
	databaseName := strings.Trim(strings.TrimSpace(parsed.Path), "/")
	if databaseName == "" || !strings.HasSuffix(databaseName, "_test") {
		return fmt.Errorf("TEST_DATABASE_URL must select a database whose name ends in _test")
	}
	return nil
}

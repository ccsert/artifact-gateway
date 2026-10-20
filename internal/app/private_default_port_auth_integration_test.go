//go:build integration

package app

import "testing"

func TestPrivateFormatDefaultPortCredentialsPostgres(t *testing.T) {
	privateDefaultPortCredentials(t, newPrivateProxyPostgresBackend)
}

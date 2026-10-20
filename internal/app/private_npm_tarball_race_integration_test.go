//go:build integration

package app

import "testing"

func TestPrivateFormatNPMTarballPublicationPostgres(t *testing.T) {
	for _, change := range []string{"credential-rotation", "metadata-revalidation"} {
		t.Run(change, func(t *testing.T) {
			privateNPMTarballPublication(t, change, newPrivateProxyPostgresBackend(t))
		})
	}
}

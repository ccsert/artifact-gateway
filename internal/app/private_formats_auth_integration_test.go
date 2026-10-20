//go:build integration

package app

import (
	"fmt"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/repository"
)

func TestPostgresRustFSPrivateFormatCredentials(t *testing.T) {
	for _, format := range []repository.Format{repository.FormatNPM, repository.FormatPyPI, repository.FormatCargo, repository.FormatAPT, repository.FormatRaw, repository.FormatConan} {
		for _, scheme := range []repository.UpstreamAuthScheme{repository.UpstreamAuthSchemeBasic, repository.UpstreamAuthSchemeBearer} {
			for _, group := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/group=%v", format, scheme, group), func(t *testing.T) {
					backend := newPrivateProxyPostgresBackend(t)
					switch format {
					case repository.FormatRaw:
						privateRawCredentialsContract(t, scheme, group, backend)
					case repository.FormatConan:
						privateConanCredentialsContract(t, scheme, group, backend)
					default:
						privateNativeCredentialsContract(t, format, scheme, group, backend)
					}
				})
			}
		}
	}
}

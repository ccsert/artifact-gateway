package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// RawMessage distinguishes an omitted PATCH field (preserve) from JSON null
// (clear). The issuer/audience object is replaced atomically, never merged.
func resolveOCIBearer(request json.RawMessage, existing *repository.OCIBearer, format repository.Format, repoType repository.RepositoryType) (*repository.OCIBearer, error) {
	if request == nil {
		return existing, nil
	}
	if format != repository.FormatOCI || repoType != repository.RepositoryTypeProxy {
		return nil, errors.New("ociBearer is supported for OCI Proxy repositories only")
	}
	if bytes.Equal(bytes.TrimSpace(request), []byte("null")) {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(request))
	decoder.DisallowUnknownFields()
	var config repository.OCIBearer
	if err := decoder.Decode(&config); err != nil {
		return nil, errors.New("ociBearer must contain only realm and service")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("ociBearer must be a single configuration object")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &config, nil
}

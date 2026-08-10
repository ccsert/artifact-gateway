// Package scanning defines the format-neutral artifact scanning seam.
// Concrete adapters receive immutable artifact identity plus verified assets
// and return bounded security metadata without mutating repository state.
package scanning

import (
	"context"
	"io"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const SchemaVersion = "v1"

// OpenAsset opens one immutable object from the Gateway object store. Callers
// retain ownership of object lookup; scanners only receive a streaming reader.
type OpenAsset func(context.Context) (io.ReadCloser, error)

// Asset is one named object that belongs to a logical artifact. Complex
// formats such as OCI, Maven, and Conan can provide more than one asset.
type Asset struct {
	Path      string
	Digest    string
	Size      int64
	MediaType string
	Open      OpenAsset
}

// Artifact is the immutable logical identity presented to a scanner.
type Artifact struct {
	RepositoryID string
	Format       repository.Format
	Coordinate   string
	Digest       string
	Assets       []Asset
}

// Report is the scanner-owned portion of artifact intelligence. Signatures
// and provenance are intentionally excluded because a vulnerability scanner
// must not replace publisher-supplied trust evidence.
type Report struct {
	SBOMs         []repository.ArtifactSBOM
	Licenses      []repository.ArtifactLicense
	Vulnerability *repository.ArtifactVulnerabilitySummary
}

// Scanner hides transport and scanner-specific behavior behind one operation.
type Scanner interface {
	Scan(context.Context, Artifact) (Report, error)
}

// ScannerFunc adapts an in-process scanner or focused test implementation.
type ScannerFunc func(context.Context, Artifact) (Report, error)

func (f ScannerFunc) Scan(ctx context.Context, artifact Artifact) (Report, error) {
	return f(ctx, artifact)
}

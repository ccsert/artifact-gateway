package raw

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	rawprotocol "github.com/ccsert/artifact-gateway/internal/protocol/raw"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

// serveAsset binds every interval to the selected immutable object, even when
// the mutable path changes during a multipart response. Delayed reclamation
// retains replaced objects; an unavailable interval produces a truncated
// response rather than falling back to the path's new object.
func (h *Module) serveAsset(w http.ResponseWriter, r *http.Request, path string, asset repository.RawAsset) {
	rawprotocol.ServeContent(w, r, path, rawprotocol.Content{
		Digest: strings.TrimPrefix(asset.Digest, "sha256:"), ContentType: asset.ContentType, Size: asset.Size,
		Source: immutableSource{store: h.objects, key: asset.ObjectKey},
	})
}

type immutableSource struct {
	store objectstore.Store
	key   string
}

func (s immutableSource) Open(ctx context.Context) (io.ReadCloser, int64, error) {
	return s.store.Open(ctx, s.key)
}

func (s immutableSource) OpenRange(ctx context.Context, start, length int64) (io.ReadCloser, int64, error) {
	return s.store.OpenRange(ctx, s.key, start, length)
}

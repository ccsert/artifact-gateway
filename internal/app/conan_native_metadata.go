package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	conanprotocol "github.com/ccsert/artifact-gateway/internal/protocol/conan"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

func (h ConanHandler) nativeConanPackageSearch(ctx context.Context, repositoryID, reference, recipeRevision string) ([]byte, error) {
	ids, err := h.NativeStore.ListConanPackageIDs(ctx, repositoryID, reference, recipeRevision)
	if err != nil {
		return nil, err
	}
	packages := make(map[string]map[string]string, len(ids))
	for _, id := range ids {
		revisions, err := h.NativeStore.ListConanPackageRevisions(ctx, repositoryID, reference, recipeRevision, id)
		if err != nil || len(revisions) == 0 {
			return nil, errors.New("conan package revision unavailable")
		}
		asset, err := h.NativeStore.GetConanPackageAsset(ctx, repositoryID, reference, recipeRevision, id, revisions[0].Revision, "conaninfo.txt")
		if errors.Is(err, repository.ErrNotFound) {
			// Older management sessions can carry arbitrary declared assets.
			// They retain their readable identity but have no package settings.
			packages[id] = map[string]string{"content": ""}
			continue
		}
		if err != nil || asset.Size > conanprotocol.ManifestMaxBytes {
			return nil, errors.New("conan package information unavailable")
		}
		body, _, err := h.NativeObjects.Open(ctx, asset.ObjectKey)
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(body, conanprotocol.ManifestMaxBytes+1))
		_ = body.Close()
		sum := sha256.Sum256(data)
		if readErr != nil || int64(len(data)) != asset.Size || "sha256:"+hex.EncodeToString(sum[:]) != asset.Digest {
			return nil, errors.New("conan package information is incomplete")
		}
		packages[id] = map[string]string{"content": string(data)}
	}
	return json.Marshal(packages)
}

func validConanPackageSearch(body []byte) bool {
	var packages map[string]struct {
		Content *string `json:"content"`
	}
	if json.Unmarshal(body, &packages) != nil || packages == nil {
		return false
	}
	for id, info := range packages {
		if decoded, err := hex.DecodeString(id); err != nil || len(decoded) != 20 || strings.ToLower(id) != id || info.Content == nil || len(*info.Content) > conanprotocol.ManifestMaxBytes {
			return false
		}
	}
	return true
}

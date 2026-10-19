package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"

	conanprotocol "github.com/ccsert/artifact-gateway/internal/protocol/conan"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

func validateConanPublication(ctx context.Context, objects ObjectStore, session repository.ConanPublishSession, uploads map[string]string) error {
	declaration := nativeConanPublishRequest{Kind: session.Kind, Reference: session.Reference, RecipeRevision: session.RecipeRevision, PackageID: session.PackageID, PackageRevision: session.PackageRevision, Objects: session.Objects}
	if !validConanPublishRequest(declaration) {
		return errors.New("conan publication identity or declared assets are invalid")
	}
	if len(uploads) != len(session.Objects) {
		return errors.New("conan publication snapshot has changed")
	}
	names := make([]string, 0, len(session.Objects))
	for _, object := range session.Objects {
		body, _, err := objects.Open(ctx, uploads[object.Name])
		if err != nil {
			return errors.New("conan staged asset is unavailable")
		}
		hash := sha256.New()
		n, readErr := io.Copy(hash, io.LimitReader(body, object.Size+1))
		_ = body.Close()
		if readErr != nil || n != object.Size || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != object.Digest {
			return errors.New("conan staged asset bytes no longer match their declaration")
		}
		names = append(names, object.Name)
	}
	reader, _, err := objects.Open(ctx, uploads[conanprotocol.ManifestName])
	if err != nil {
		return errors.New("conan manifest is unavailable")
	}
	manifest, readErr := io.ReadAll(io.LimitReader(reader, conanprotocol.ManifestMaxBytes+1))
	_ = reader.Close()
	if readErr != nil || len(manifest) > conanprotocol.ManifestMaxBytes {
		return errors.New("conan manifest is unavailable or exceeds its byte limit")
	}
	revision := session.RecipeRevision
	if session.Kind == "package" {
		revision = session.PackageRevision
	}
	return conanprotocol.ValidatePublication(ctx, session.Kind, revision, session.PackageID, manifest, names, func(name string) (io.ReadCloser, error) {
		body, _, err := objects.Open(ctx, uploads[name])
		return body, err
	})
}

package raw

import (
	"context"
	"errors"
	"io"
	"strings"

	formatmodule "github.com/ccsert/artifact-gateway/internal/format"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/v2contract"
)

func (m *Module) ValidatePublication(identity string, assets []formatmodule.NativeAsset) error {
	if len(assets) != 1 || identity != assets[0].Path {
		return formatmodule.ErrPublicationIdentity
	}
	canonical, ok := v2contract.CanonicalRawPath(assets[0].Path)
	if !ok || canonical != assets[0].Path {
		return formatmodule.ErrPublicationPath
	}
	return nil
}
func (m *Module) LockPublication(ctx context.Context, assets []formatmodule.NativeAsset) (context.Context, func(), error) {
	if m.publication == nil || len(assets) != 1 {
		return ctx, nil, repository.ErrDisabled
	}
	return repository.LockObjectKeys(ctx, []string{assets[0].Digest}, m.publication, repository.FormatRaw, m.publication.LockRawObject)
}

// StageVerifiedPublication accepts bytes already size/hash-verified by the
// publication coordinator. Object-store PutVerifiedReader records that fact;
// it does not itself verify a digest. Visibility remains a separate operation.
func (m *Module) StageVerifiedPublication(ctx context.Context, asset formatmodule.NativeAsset, reader io.Reader) error {
	if m.publication == nil || m.objects == nil {
		return repository.ErrDisabled
	}
	if err := m.publication.StageRawObject(ctx, repository.RawObject{RepositoryID: asset.RepositoryID, Digest: asset.Digest, ObjectKey: asset.ObjectKey, Size: asset.Size}); err != nil {
		return errors.Join(formatmodule.ErrPublicationStage, err)
	}
	if err := m.objects.PutVerifiedReader(ctx, asset.ObjectKey, reader, asset.Size, asset.Digest); err != nil {
		return errors.Join(formatmodule.ErrPublicationUpload, err)
	}
	return nil
}
func (m *Module) PublishPublication(ctx context.Context, asset formatmodule.NativeAsset) error {
	if m.publication == nil {
		return repository.ErrDisabled
	}
	_, err := m.publication.PutRawAssetIf(ctx, repository.RawAsset{RepositoryID: asset.RepositoryID, Path: asset.Path, ObjectKey: asset.ObjectKey, Digest: asset.Digest, Size: asset.Size, ContentType: "application/octet-stream"}, repository.RawWriteCondition{RequireAbsent: true})
	return err
}
func (m *Module) ReadPublication(ctx context.Context, repositoryID, path string) (formatmodule.NativeAsset, error) {
	if m.publication == nil {
		return formatmodule.NativeAsset{}, repository.ErrDisabled
	}
	asset, err := m.publication.GetRawAsset(ctx, repositoryID, path)
	return formatmodule.NativeAsset{RepositoryID: asset.RepositoryID, Path: asset.Path, ObjectKey: asset.ObjectKey, Digest: asset.Digest, Size: asset.Size}, err
}
func NativeObjectKey(digest string) string {
	return "native/raw/sha256/" + strings.TrimPrefix(digest, "sha256:")
}

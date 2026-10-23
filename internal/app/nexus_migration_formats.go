package app

import (
	"context"
	"errors"
	"io"

	"github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

// MigrationPublicationAsset binds a native logical reference to immutable bytes.
// A staged key may differ from an already published key with the same content.
type MigrationPublicationAsset struct {
	Path      string
	ObjectKey string
	Digest    string
	Size      int64
}

// NexusFormatMigrationPublication supplies only format-owned publication and
// readback. The batch coordinator owns inventory, capacity and the durable lease.
type NexusFormatMigrationPublication interface {
	Lock(context.Context, nexusmigration.Entry) (context.Context, func(), error)
	Readback(context.Context, nexusmigration.Entry) (nexusmigration.Readback, error)
	Begin(context.Context, nexusmigration.Entry) (string, error)
	Stage(context.Context, nexusmigration.Entry, nexusmigration.Asset, io.ReadSeeker) error
	Verify(context.Context, nexusmigration.Entry) error
	Publish(context.Context, nexusmigration.Entry) error
	NativeAssets(nexusmigration.Entry) ([]MigrationPublicationAsset, error)
	NativeAsset(context.Context, nexusmigration.Entry, MigrationPublicationAsset) (MigrationPublicationAsset, error)
}

func (p *NexusMigrationPublication) WithOCI(publication NexusFormatMigrationPublication) *NexusMigrationPublication {
	p.oci = publication
	return p
}

func (p *NexusMigrationPublication) WithConan(publication NexusFormatMigrationPublication) *NexusMigrationPublication {
	p.conan = publication
	return p
}

func (p *NexusMigrationPublication) WithPyPI(publication NexusFormatMigrationPublication) *NexusMigrationPublication {
	p.pypi = publication
	return p
}

func (p *NexusMigrationPublication) WithNPM(publication NexusFormatMigrationPublication) *NexusMigrationPublication {
	p.npm = publication
	return p
}

func (p *NexusMigrationPublication) formatPublication(format string) NexusFormatMigrationPublication {
	if p == nil {
		return nil
	}
	var publication NexusFormatMigrationPublication
	switch format {
	case "oci":
		publication = p.oci
	case "conan":
		publication = p.conan
	case "npm":
		publication = p.npm
	case "pypi":
		publication = p.pypi
	}
	if nilDependency(publication) {
		return nil
	}
	return publication
}

func (p *NexusMigrationPublication) supportsFormat(format repository.Format) bool {
	return format == repository.FormatMaven || supportsMigratedPublication(format) || p.formatPublication(string(format)) != nil
}

func (p *NexusMigrationPublication) capacityAssets(entry nexusmigration.Entry) ([]MigrationPublicationAsset, error) {
	if publication := p.formatPublication(entry.Format); publication != nil {
		return publication.NativeAssets(entry)
	}
	assets, err := nexusNativeAssets(entry)
	if err != nil {
		return nil, err
	}
	out := make([]MigrationPublicationAsset, len(assets))
	for i, asset := range assets {
		out[i] = MigrationPublicationAsset{Path: asset.Path, ObjectKey: asset.ObjectKey, Digest: asset.Digest, Size: asset.Size}
	}
	return out, nil
}

func (p *NexusMigrationPublication) capacityAsset(ctx context.Context, entry nexusmigration.Entry, planned MigrationPublicationAsset) (MigrationPublicationAsset, error) {
	if publication := p.formatPublication(entry.Format); publication != nil {
		return publication.NativeAsset(ctx, entry, planned)
	}
	asset, err := p.nativeAsset(ctx, entry.Format, entry.TargetRepositoryID, planned.Path)
	if err != nil {
		return MigrationPublicationAsset{}, err
	}
	return MigrationPublicationAsset{Path: asset.Path, ObjectKey: asset.ObjectKey, Digest: asset.Digest, Size: asset.Size}, nil
}

// ValidateMigrationActor rejects non-durable/expired authority before writing a
// PyPI batch. Legacy format admission remains separate.
func (p *NexusMigrationPublication) ValidateMigrationActor(ctx context.Context, actor, kind, ownerID string, mappings []nexusmigration.Mapping) (bool, error) {
	required := false
	for _, mapping := range mappings {
		repo, err := p.store.GetHostedRepository(ctx, mapping.TargetRepositoryID)
		if errors.Is(err, repository.ErrNotFound) {
			// Target existence belongs to inventory binding and target validation.
			// Do not mask a resumed scan's mapping drift with an actor lookup error.
			continue
		}
		if err != nil {
			return required, err
		}
		if repo.Format != repository.FormatPyPI {
			continue
		}
		required = true
		check, ok := p.pypi.(interface {
			CheckActor(context.Context, string, string, string) error
		})
		if !ok {
			return required, nexusmigration.Failed("pypi_actor_not_current_admin")
		}
		if err = check.CheckActor(ctx, actor, kind, ownerID); err != nil {
			return required, err
		}
	}
	return required, nil
}

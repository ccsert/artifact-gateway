package repository

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/ccsert/artifact-gateway/internal/ocigraph"
)

// OCIImportStore is an explicit capability outside NativeOCIStore/GatewayStore.
// The lock spans complete byte verification and the visibility transaction.
type OCIImportStore interface {
	LockOCIImport(context.Context, OCIImportClosure) (context.Context, func(), error)
	StageOCIImportObject(context.Context, OCIObjectIntent) error
	PublishOCIImportClosure(context.Context, OCIImportClosure) error
}

type OCIImportManifest struct {
	OCIManifest
	Document ocigraph.Document
}

type OCIImportClosure struct {
	RepositoryID, Name, Reference, RootDigest, Actor string
	Manifests                                        []OCIImportManifest
	Blobs                                            []OCIBlob
}

func (c OCIImportClosure) objects() []OCIObjectIntent {
	out := make([]OCIObjectIntent, 0, len(c.Manifests)+len(c.Blobs))
	for _, m := range c.Manifests {
		out = append(out, OCIObjectIntent{RepositoryID: c.RepositoryID, ObjectKey: m.ObjectKey, Digest: m.Digest, Size: m.Size})
	}
	for _, b := range c.Blobs {
		out = append(out, OCIObjectIntent{RepositoryID: c.RepositoryID, ObjectKey: b.ObjectKey, Digest: b.Digest, Size: b.Size})
	}
	return out
}

func validateOCIImport(c OCIImportClosure) error {
	if c.RepositoryID == "" || !ocigraph.ValidName(c.Name) || !ocigraph.ValidReference(c.Reference) || !ocigraph.ValidDigest(c.RootDigest) || c.Actor == "" || ocigraph.ValidDigest(c.Reference) && c.Reference != c.RootDigest {
		return ErrDisabled
	}
	objects := make([]ocigraph.Object, 0, len(c.Manifests)+len(c.Blobs))
	for _, m := range c.Manifests {
		key := "native/oci/manifests/" + c.RepositoryID + "/" + url.PathEscape(c.Name) + "/" + strings.TrimPrefix(m.Digest, "sha256:")
		if m.RepositoryID != c.RepositoryID || m.Name != c.Name || m.ObjectKey != key || m.SubjectDigest != "" || m.ArtifactType != "" {
			return ErrDisabled
		}
		objects = append(objects, ocigraph.Object{Digest: m.Digest, Size: m.Size, MediaType: m.MediaType, Document: &m.Document})
	}
	for _, b := range c.Blobs {
		if b.ObjectKey != "native/oci/blobs/sha256/"+strings.TrimPrefix(b.Digest, "sha256:") {
			return ErrDisabled
		}
		mediaType := ""
		for _, m := range c.Manifests {
			for _, d := range m.Document.Dependencies() {
				if d.Digest == b.Digest {
					if mediaType != "" && mediaType != d.MediaType {
						return ErrDisabled
					}
					mediaType = d.MediaType
				}
			}
		}
		objects = append(objects, ocigraph.Object{Digest: b.Digest, Size: b.Size, MediaType: mediaType})
	}
	if ocigraph.Validate(c.RootDigest, objects) != nil {
		return ErrDisabled
	}
	return nil
}

func ociImportAudit(c OCIImportClosure, repoName string) AuditRecord {
	return AuditRecord{Repository: repoName, GroupName: repoName, Actor: c.Actor, Format: "oci", Resource: c.Name, Representation: c.RootDigest, Operation: "nexus.migration.publish", Status: 201, Outcome: AuditResolved, OccurredAt: time.Now().UTC(), Evidence: map[string]string{"reference": c.Reference, "rootDigest": c.RootDigest}}
}

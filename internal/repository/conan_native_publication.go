package repository

import (
	"context"
	"strconv"
)

// ConanNativePublicationStore adds the manifest-last wire workflow to the
// existing durable publish sessions. It is composed explicitly and does not
// expand the legacy aggregate GatewayStore.
type ConanNativePublicationStore interface {
	BeginConanNativeSession(context.Context, ConanPublishSession) (ConanPublishSession, error)
	StageConanNativeObject(context.Context, string, MavenDeclaredObject, string) (ConanPublishSession, error)
	CommitConanNativeSession(context.Context, string, int64, string) (ConanPublishSession, bool, error)
}

func cloneConanSession(session ConanPublishSession) ConanPublishSession {
	session.Objects = append([]MavenDeclaredObject(nil), session.Objects...)
	return session
}

func sameConanSessionIdentity(a, b ConanPublishSession) bool {
	return a.RepositoryID == b.RepositoryID && a.Publisher == b.Publisher && a.Kind == b.Kind && a.Reference == b.Reference && a.RecipeRevision == b.RecipeRevision && a.PackageID == b.PackageID && a.PackageRevision == b.PackageRevision
}

func conanSessionAssets(session ConanPublishSession, uploads map[string]string) ([]ConanAsset, error) {
	if len(session.Objects) == 0 || len(session.Objects) != len(uploads) {
		return nil, ErrDisabled
	}
	assets := make([]ConanAsset, 0, len(session.Objects))
	names := make(map[string]bool, len(session.Objects))
	for _, object := range session.Objects {
		key := uploads[object.Name]
		if key == "" {
			return nil, ErrDisabled
		}
		assets = append(assets, ConanAsset{RepositoryID: session.RepositoryID, Reference: session.Reference, RecipeRevision: session.RecipeRevision, PackageID: session.PackageID, PackageRevision: session.PackageRevision, Path: object.Name, ObjectKey: key, Digest: object.Digest, Size: object.Size})
		names[object.Name] = true
	}
	if session.Native && (!names["conanmanifest.txt"] || session.Kind == "recipe" && !names["conanfile.py"] && !names["conan_export.tgz"] || session.Kind == "package" && (!names["conaninfo.txt"] || !names["conan_package.tgz"])) {
		return nil, ErrDisabled
	}
	return assets, nil
}

func conanNativePublicationAudit(repo HostedRepository, session ConanPublishSession, digest string, replay bool) AuditRecord {
	coordinate := session.Reference + "#" + session.RecipeRevision
	if session.Kind == "package" {
		coordinate += "/" + session.PackageID + "#" + session.PackageRevision
	}
	return AuditRecord{GroupName: repo.Name, Repository: repo.Name, MemberName: "native", MemberType: "hosted", Actor: session.Publisher,
		Format: "conan", Resource: coordinate, Operation: "publish", Outcome: AuditResolved, Status: 201,
		Evidence: map[string]string{"publicationSessionId": session.ID, "publicationState": "committed", "digest": digest, "replay": strconv.FormatBool(replay)}}
}

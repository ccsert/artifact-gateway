package repository

import (
	"context"
	"strconv"
)

// ConanPublicationStore commits verified management and native publication
// snapshots through the same durable boundary. It is composed explicitly.
type ConanPublicationStore interface {
	BeginConanNativeSession(context.Context, ConanPublishSession) (ConanPublishSession, error)
	StageConanPublicationObject(context.Context, string, MavenDeclaredObject, string) (ConanPublishSession, error)
	CommitConanPublicationSession(context.Context, string, int64, string, string) (ConanPublishSession, bool, error)
}

func stageConanPublication(session ConanPublishSession, object MavenDeclaredObject) (ConanPublishSession, error) {
	session = cloneConanSession(session)
	for i, declared := range session.Objects {
		if declared.Name != object.Name {
			continue
		}
		if !session.Native && (declared.Digest != object.Digest || declared.Size != object.Size) {
			return ConanPublishSession{}, ErrDisabled
		}
		session.Objects[i] = object
		session.Version++
		return session, nil
	}
	if !session.Native {
		return ConanPublishSession{}, ErrDisabled
	}
	session.Objects = append(session.Objects, object)
	session.Version++
	return session, nil
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
	if !names["conanmanifest.txt"] || session.Kind == "recipe" && !names["conanfile.py"] && !names["conan_export.tgz"] || session.Kind == "package" && (!names["conaninfo.txt"] || !names["conan_package.tgz"]) {
		return nil, ErrDisabled
	}
	return assets, nil
}

func conanPublicationAudit(repo HostedRepository, session ConanPublishSession, digest, actor string, replay bool) AuditRecord {
	coordinate := session.Reference + "#" + session.RecipeRevision
	if session.Kind == "package" {
		coordinate += "/" + session.PackageID + "#" + session.PackageRevision
	}
	member, status := "native", 201
	if !session.Native {
		member, status = "management", 200
	}
	return AuditRecord{GroupName: repo.Name, Repository: repo.Name, MemberName: member, MemberType: "hosted", Actor: actor,
		Format: "conan", Resource: coordinate, Operation: "publish", Outcome: AuditResolved, Status: status,
		Evidence: map[string]string{"publicationSessionId": session.ID, "publicationState": "committed", "digest": digest, "replay": strconv.FormatBool(replay)}}
}

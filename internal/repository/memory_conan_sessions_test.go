package repository

import (
	"context"
	"testing"
	"time"
)

func TestMemoryConanPublishSessionRequiresEveryDeclaredObject(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.CreateHostedRepository(ctx, HostedRepository{ID: "repo", Name: "repo", Format: FormatConan}); err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateConanPublishSession(ctx, ConanPublishSession{
		ID: "session", RepositoryID: "repo", Publisher: "publisher", Kind: "recipe",
		Reference: "pkg/1.0/user/stable", RecipeRevision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", State: "open", ExpiresAt: time.Now().Add(time.Hour),
		Objects: []MavenDeclaredObject{{Name: "conanfile.py", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 1}, {Name: "conanmanifest.txt", Digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Size: 1}},
	})
	if err != nil || session.State != "open" || session.Version != 1 || session.Native {
		t.Fatalf("create session=%#v err=%v", session, err)
	}
	for i, object := range session.Objects {
		key := "native/conan/session/" + object.Name
		if err = store.StageConanObject(ctx, ConanObjectIntent{RepositoryID: "repo", ObjectKey: key, Digest: object.Digest, Size: object.Size}); err != nil {
			t.Fatal(err)
		}
		session, err = store.StageConanPublicationObject(ctx, session.ID, object, key)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if _, _, err = store.CommitConanPublicationSession(ctx, session.ID, session.Version, object.Digest, "committer"); err != ErrDisabled {
				t.Fatalf("incomplete commit error=%v, want %v", err, ErrDisabled)
			}
		}
	}
	committed, _, err := store.CommitConanPublicationSession(ctx, session.ID, session.Version, "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", "committer")
	if err != nil || committed.State != "committed" || committed.Publisher != "publisher" {
		t.Fatalf("committed session=%#v err=%v", committed, err)
	}
}

func TestTombstoneConanRecipeAlsoHidesPackages(t *testing.T) {
	store := NewMemoryStore()
	for _, id := range []string{"repo"} {
		if _, err := store.CreateHostedRepository(context.Background(), HostedRepository{ID: id, Name: id, Format: FormatConan}); err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()
	if err := store.StageConanObject(ctx, ConanObjectIntent{RepositoryID: "repo", ObjectKey: "recipe", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutConanRecipeRevision(ctx, ConanRecipeRevision{RepositoryID: "repo", Reference: "pkg/1/user/stable", Revision: "rrev", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, []ConanAsset{{RepositoryID: "repo", Reference: "pkg/1/user/stable", RecipeRevision: "rrev", Path: "conanfile.py", ObjectKey: "recipe", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := store.StageConanObject(ctx, ConanObjectIntent{RepositoryID: "repo", ObjectKey: "package", Digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutConanPackageRevision(ctx, ConanPackageRevision{RepositoryID: "repo", Reference: "pkg/1/user/stable", RecipeRevision: "rrev", PackageID: "id", Revision: "prev", Digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}, []ConanAsset{{RepositoryID: "repo", Reference: "pkg/1/user/stable", RecipeRevision: "rrev", PackageID: "id", PackageRevision: "prev", Path: "package.tgz", ObjectKey: "package", Digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Size: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TombstoneConanRecipeRevision(ctx, "repo", "pkg/1/user/stable", "rrev"); err != nil {
		t.Fatal(err)
	}
	pkg, err := store.GetConanPackageRevision(ctx, "repo", "pkg/1/user/stable", "rrev", "id", "prev")
	if err != nil || pkg.State != "deleted" {
		t.Fatalf("package=%#v err=%v", pkg, err)
	}
}

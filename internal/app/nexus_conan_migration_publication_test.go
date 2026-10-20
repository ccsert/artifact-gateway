package app

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func nexusConanInventory(t *testing.T, repo repository.HostedRepository) ([]nexusmigration.Entry, map[string][]byte) {
	t.Helper()
	recipe := []byte("# original producer input " + uuid.NewString() + "\nfrom conan import ConanFile\nclass Demo(ConanFile):\n    name='demo'\n    version='1.0'\n")
	rrev, manifest := conanPublicationManifestFixture(map[string][]byte{"conanfile.py": recipe})
	info := []byte("[settings]\narch=x86_64\nos=Linux\n\n[options]\n\n[requires]\n\n")
	header := []byte("#define MIGRATED_CONAN 425\n")
	prev, packageManifest := conanPublicationManifestFixture(map[string][]byte{"conaninfo.txt": info, "include/demo.h": header})
	packageID := fmt.Sprintf("%x", sha1.Sum(info))
	root := "conans/demo/1.0/_/_/revisions/" + rrev
	packageRoot := root + "/packages/" + packageID + "/revisions/" + prev
	bodies := map[string][]byte{root + "/files/conanfile.py": recipe, root + "/files/conanmanifest.txt": manifest,
		packageRoot + "/files/conaninfo.txt": info, packageRoot + "/files/conanmanifest.txt": packageManifest,
		packageRoot + "/files/conan_package.tgz": conanPublicationArchiveFixture(t, map[string][]byte{"include/demo.h": header})}
	byIdentity := map[string]*nexusmigration.Entry{}
	for source, body := range bodies {
		identity, name, err := nexusmigration.ConanAssetIdentity(source)
		if err != nil {
			t.Fatal(err)
		}
		entry := byIdentity[identity]
		if entry == nil {
			entry = &nexusmigration.Entry{ID: uuid.NewString(), BatchID: uuid.NewString(), SourceRepository: "source", TargetRepositoryID: repo.ID,
				Format: "conan", Identity: identity, State: "pending", Actor: "actual-migration-admin"}
			byIdentity[identity] = entry
		}
		entry.Assets = append(entry.Assets, nexusmigration.Asset{SourcePath: source, TargetPath: name, Size: int64(len(body)), SHA256: fmt.Sprintf("%x", sha256.Sum256(body))})
	}
	entries := make([]nexusmigration.Entry, 0, len(byIdentity))
	for _, entry := range byIdentity {
		sort.Slice(entry.Assets, func(i, j int) bool { return entry.Assets[i].TargetPath < entry.Assets[j].TargetPath })
		entries = append(entries, *entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Identity < entries[j].Identity })
	return entries, bodies
}

type nexusConanMigrationFixture struct {
	store       *repository.MemoryStore
	objects     *objectstore.MemoryStore
	repo        repository.HostedRepository
	publication *NexusConanMigrationPublication
	entries     []nexusmigration.Entry
	bodies      map[string][]byte
}

func newNexusConanMigrationFixture(t *testing.T) nexusConanMigrationFixture {
	t.Helper()
	store, objects := repository.NewMemoryStore(), objectstore.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "conan-migration", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	entries, bodies := nexusConanInventory(t, repo)
	return nexusConanMigrationFixture{store, objects, repo, NewNexusConanMigrationPublication(store, store, objects), entries, bodies}
}

func stageNexusConan(t *testing.T, ctx context.Context, publication *NexusConanMigrationPublication, entry *nexusmigration.Entry, bodies map[string][]byte) {
	t.Helper()
	var err error
	entry.SessionID, err = publication.Begin(ctx, *entry)
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range entry.Assets {
		if err = publication.Stage(ctx, *entry, asset, bytes.NewReader(bodies[asset.SourcePath])); err != nil {
			t.Fatal(err)
		}
	}
}

func requireNexusConanCode(t *testing.T, err error, want string) {
	t.Helper()
	var failure *nexusmigration.Failure
	if !errors.As(err, &failure) || failure.Code != want {
		t.Fatalf("error=%v want=%s", err, want)
	}
}

func TestNexusConanMigrationPublicationPreservesCompleteBytesAndReopen(t *testing.T) {
	f := newNexusConanMigrationFixture(t)
	for i := range f.entries {
		entry := &f.entries[i]
		ctx, release, err := f.publication.Lock(context.Background(), *entry)
		if err != nil {
			t.Fatal(err)
		}
		stageNexusConan(t, ctx, f.publication, entry, f.bodies)
		if state, err := f.publication.Readback(ctx, *entry); err != nil || state != nexusmigration.Absent {
			t.Fatalf("staged revision became visible=%s %v", state, err)
		}
		requireNexusConanCode(t, f.publication.Publish(ctx, *entry), "target_verification_required")
		// Recreating the format service reuses the durable session, not a
		// process-local pending map or a second partially visible publication.
		other := NewNexusConanMigrationPublication(f.store, f.store, f.objects)
		otherCtx, otherRelease, err := other.Lock(context.Background(), *entry)
		if err != nil {
			t.Fatal(err)
		}
		session, err := other.Begin(otherCtx, *entry)
		if err != nil || session != entry.SessionID {
			t.Fatalf("resume session=%s expected=%s %v", session, entry.SessionID, err)
		}
		if err = other.Verify(otherCtx, *entry); err != nil {
			t.Fatal(err)
		}
		if err = other.Publish(otherCtx, *entry); err != nil {
			t.Fatal(err)
		}
		otherRelease()
		release()
		if state, err := f.publication.Readback(context.Background(), *entry); err != nil || state != nexusmigration.Identical {
			t.Fatalf("committed complete revision=%s %v", state, err)
		}
	}
	handler := newTestGateway(t, GatewayOptions{Store: f.store, ConanPublications: f.store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatConan: f.objects}})
	assertNexusConanNativeBytes(t, handler, f.repo, f.entries, f.bodies)
	audits, err := f.store.ListAudits(context.Background(), repository.AuditQuery{Repository: f.repo.Name, Operation: "publish", Limit: 100})
	if err != nil || len(audits) != 2 {
		t.Fatalf("publication audits=%+v %v", audits, err)
	}
	for _, audit := range audits {
		if audit.Actor != "actual-migration-admin" || audit.Evidence["publicationState"] != "committed" || audit.Evidence["publicationSessionId"] == "" {
			t.Fatalf("actual actor/transaction evidence=%+v", audit)
		}
	}
}

func assertNexusConanNativeBytes(t *testing.T, handler http.Handler, repo repository.HostedRepository, entries []nexusmigration.Entry, bodies map[string][]byte) {
	t.Helper()
	for _, entry := range entries {
		for _, asset := range entry.Assets {
			path := "/conan/" + repo.Name + "/v2/" + asset.SourcePath
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.SetBasicAuth("reader", "resolver-secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), bodies[asset.SourcePath]) {
				t.Fatalf("native exact source bytes %s=%d len=%d want=%d", asset.SourcePath, response.Code, response.Body.Len(), len(bodies[asset.SourcePath]))
			}
		}
	}
}

func TestNexusConanMigrationPublicationRejectsBytesManifestAndStaleSnapshot(t *testing.T) {
	for _, mode := range []string{"wrong-source-bytes", "invalid-manifest", "stale-session-version", "no-context", "quota"} {
		t.Run(mode, func(t *testing.T) {
			f := newNexusConanMigrationFixture(t)
			entry := &f.entries[0]
			if mode == "no-context" {
				requireNexusConanCode(t, f.publication.Publish(context.Background(), *entry), "target_coordination_required")
				return
			}
			if mode == "invalid-manifest" {
				for i, asset := range entry.Assets {
					if asset.TargetPath == "conanmanifest.txt" {
						body := []byte("1700000000\nconanfile.py: " + strings.Repeat("a", 32) + "\n")
						entry.Assets[i].SHA256, entry.Assets[i].Size = fmt.Sprintf("%x", sha256.Sum256(body)), int64(len(body))
						f.bodies[asset.SourcePath] = body
					}
				}
			}
			ctx, release, err := f.publication.Lock(context.Background(), *entry)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if mode == "wrong-source-bytes" {
				entry.SessionID, err = f.publication.Begin(ctx, *entry)
				if err != nil {
					t.Fatal(err)
				}
				requireNexusConanCode(t, f.publication.Stage(ctx, *entry, entry.Assets[0], bytes.NewReader([]byte("changed"))), "source_bytes_mismatch")
				return
			}
			stageNexusConan(t, ctx, f.publication, entry, f.bodies)
			if mode == "invalid-manifest" {
				requireNexusConanCode(t, f.publication.Verify(ctx, *entry), "conan_manifest_invalid")
				return
			}
			if err = f.publication.Verify(ctx, *entry); err != nil {
				t.Fatal(err)
			}
			if mode == "stale-session-version" {
				asset := entry.Assets[0]
				if _, err = f.store.StageConanPublicationObject(ctx, entry.SessionID, repository.MavenDeclaredObject{Name: asset.TargetPath, Digest: "sha256:" + asset.SHA256, Size: asset.Size}, nexusConanObjectKey(*entry, asset)); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "quota" {
				if _, err = f.store.ReplaceRepositoryCapacityQuota(ctx, f.repo.ID, 1); err != nil {
					t.Fatal(err)
				}
				requireNexusConanCode(t, f.publication.Publish(ctx, *entry), "target_quota_exceeded")
			} else {
				requireNexusConanCode(t, f.publication.Publish(ctx, *entry), "target_conflict")
			}
			if state, err := f.publication.Readback(ctx, *entry); err != nil || state != nexusmigration.Absent {
				t.Fatalf("rejected commit leaked visibility=%s %v", state, err)
			}
		})
	}
}

func TestNexusConanMigrationReadbackRejectsCorruptionAndTombstone(t *testing.T) {
	for _, mode := range []string{"corrupt-object", "tombstone", "same-revision-different-bytes"} {
		t.Run(mode, func(t *testing.T) {
			f := newNexusConanMigrationFixture(t)
			entry := &f.entries[0]
			ctx, release, err := f.publication.Lock(context.Background(), *entry)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			stageNexusConan(t, ctx, f.publication, entry, f.bodies)
			if err = f.publication.Verify(ctx, *entry); err != nil {
				t.Fatal(err)
			}
			if err = f.publication.Publish(ctx, *entry); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "corrupt-object":
				asset := entry.Assets[0]
				if err = f.objects.Put(ctx, nexusConanObjectKey(*entry, asset), []byte("corrupt")); err != nil {
					t.Fatal(err)
				}
			case "tombstone":
				c, _ := nexusmigration.ParseConanIdentity(entry.Identity)
				if _, err = f.store.TombstoneConanRecipeRevision(ctx, f.repo.ID, c.Reference, c.RecipeRevision); err != nil {
					t.Fatal(err)
				}
			case "same-revision-different-bytes":
				for i, asset := range entry.Assets {
					if asset.TargetPath == "conanmanifest.txt" {
						body := bytes.Replace(f.bodies[asset.SourcePath], []byte("1700000000"), []byte("1700000001"), 1)
						entry.Assets[i].SHA256 = fmt.Sprintf("%x", sha256.Sum256(body))
					}
				}
			}
			if state, err := f.publication.Readback(context.Background(), *entry); err != nil || state != nexusmigration.Conflict {
				t.Fatalf("bad visible identity=%s %v", state, err)
			}
		})
	}
}

func TestNexusConanMigrationMissingExplicitPorts(t *testing.T) {
	f := newNexusConanMigrationFixture(t)
	var nilStore *repository.MemoryStore
	for _, publication := range []*NexusConanMigrationPublication{
		NewNexusConanMigrationPublication(nilStore, f.store, f.objects),
		NewNexusConanMigrationPublication(f.store, nilStore, f.objects),
		NewNexusConanMigrationPublication(f.store, f.store, nil),
	} {
		if _, _, err := publication.Lock(context.Background(), f.entries[0]); err == nil {
			t.Fatal("missing explicit Conan dependency fell back to an aggregate store")
		}
	}
}

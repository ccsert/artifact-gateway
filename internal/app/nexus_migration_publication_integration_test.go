//go:build integration

package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/capacityplan"
	"github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type nexusPGPublicationFixture struct {
	storeA, storeB     *repository.PostgresStore
	objectsA, objectsB *objectstore.RustFSStore
	publication        *NexusMigrationPublication
	repo               repository.HostedRepository
	entry              nexusmigration.Entry
	bodies             map[string][]byte
}

func newNexusPGPublicationFixture(t *testing.T, format repository.Format) nexusPGPublicationFixture {
	t.Helper()
	if os.Getenv("TEST_DATABASE_URL") == "" || os.Getenv("TEST_RUSTFS_ENDPOINT") == "" {
		t.Skip("isolated PostgreSQL and S3 environment required")
	}
	var f nexusPGPublicationFixture
	var err error
	f.storeA, err = repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.storeA.Close() })
	f.storeB, err = repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.storeB.Close() })
	bucket := "nexus-publication-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
	f.objectsA, err = objectstore.NewRustFSStore(os.Getenv("TEST_RUSTFS_ENDPOINT"), os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.objectsA.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.objectsB, err = objectstore.NewRustFSStore(os.Getenv("TEST_RUSTFS_ENDPOINT"), os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), bucket)
	if err != nil {
		t.Fatal(err)
	}
	f.repo, err = f.storeA.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "nexus-publication-" + uuid.NewString(), Format: format, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	f.entry, f.bodies = nexusPublicationInventory(f.repo)
	// Every case owns its repository, bucket, entries and native publisher.
	// Repeated test runs cannot share a digest row with a previous S3 bucket.
	if format == repository.FormatRaw {
		f.bodies[f.entry.Assets[0].SourcePath] = []byte("synthetic Raw " + uuid.NewString())
		f.entry.Assets[0] = nexusPublicationAsset(f.entry.Assets[0].SourcePath, f.bodies[f.entry.Assets[0].SourcePath])
	} else {
		for i, asset := range f.entry.Assets {
			body := append([]byte(nil), f.bodies[asset.SourcePath]...)
			if strings.HasSuffix(asset.SourcePath, ".pom") {
				body = bytes.Replace(body, []byte("</project>"), []byte("<!-- "+uuid.NewString()+" --></project>"), 1)
			} else {
				body = append(body, []byte(uuid.NewString())...)
			}
			f.bodies[asset.SourcePath] = body
			f.entry.Assets[i] = nexusPublicationAsset(asset.SourcePath, body)
		}
	}
	f.publication = NewNexusMigrationPublication(f.storeA, ObjectStores{format: f.objectsA}, "owned-target", "owned-pool")
	return f
}

func TestPostgresRustFSNexusPublicationNativeReadback(t *testing.T) {
	for _, format := range []repository.Format{repository.FormatRaw, repository.FormatMaven} {
		t.Run(string(format), func(t *testing.T) {
			f := newNexusPGPublicationFixture(t, format)
			ctx, release, err := f.publication.Lock(context.Background(), f.entry)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			nexusPublicationStage(t, ctx, f.publication, &f.entry, f.bodies)
			other := NewNexusMigrationPublication(f.storeB, ObjectStores{format: f.objectsB}, "owned-target", "owned-pool")
			if state, err := other.Readback(ctx, f.entry); err != nil || state != nexusmigration.Absent {
				t.Fatalf("uncommitted native identity=%s err=%v", state, err)
			}
			if err = f.publication.Verify(ctx, f.entry); err != nil {
				t.Fatal(err)
			}
			if err = f.publication.Publish(ctx, f.entry); err != nil {
				t.Fatal(err)
			}
			if state, err := other.Readback(context.Background(), f.entry); err != nil || state != nexusmigration.Identical {
				t.Fatalf("other-instance native readback=%s err=%v", state, err)
			}
			handler := newTestGateway(t, GatewayOptions{Store: f.storeB, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{format: f.objectsB}})
			nexusPublicationNativeReads(t, handler, f.repo, f.entry, f.bodies)
			if format == repository.FormatRaw {
				candidate := f.entry
				candidate.Identity += ".sha256"
				candidate.Assets = []nexusmigration.Asset{nexusPublicationAsset(candidate.Identity, []byte("Nexus checksum with source-specific formatting"))}
				if _, _, err = other.Lock(context.Background(), candidate); err == nil {
					t.Fatal("virtual checksum URL accepted as original Raw bytes")
				}
			}
		})
	}
}

func TestPostgresRustFSNexusPublicationRawCASPreservesConcurrentWinner(t *testing.T) {
	f := newNexusPGPublicationFixture(t, repository.FormatRaw)
	other := NewNexusMigrationPublication(f.storeB, ObjectStores{repository.FormatRaw: f.objectsB}, "owned-target", "owned-pool")
	entries := []nexusmigration.Entry{f.entry, f.entry}
	entries[1].ID = uuid.NewString()
	competing := []byte("concurrent Raw " + uuid.NewString())
	entries[1].Assets = []nexusmigration.Asset{nexusPublicationAsset(f.entry.Identity, competing)}
	publications := []*NexusMigrationPublication{f.publication, other}
	contexts := make([]context.Context, 2)
	for i := range entries {
		ctx, release, err := publications[i].Lock(context.Background(), entries[i])
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(release)
		contexts[i] = ctx
		bodies := f.bodies
		if i == 1 {
			bodies = map[string][]byte{entries[i].Identity: competing}
		}
		nexusPublicationStage(t, ctx, publications[i], &entries[i], bodies)
		if err = publications[i].Verify(ctx, entries[i]); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	errorsByWriter := make([]error, 2)
	for i := range entries {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; errorsByWriter[i] = publications[i].Publish(contexts[i], entries[i]) }()
	}
	close(start)
	wg.Wait()
	winners := 0
	for i, err := range errorsByWriter {
		if err == nil {
			winners++
			if state, err := other.Readback(context.Background(), entries[i]); err != nil || state != nexusmigration.Identical {
				t.Fatalf("winner bytes=%s err=%v", state, err)
			}
		} else {
			var failure *nexusmigration.Failure
			if !errors.As(err, &failure) || failure.Code != "target_conflict" {
				t.Fatalf("loser must retain winner: %v", err)
			}
			if state, err := other.Readback(context.Background(), entries[i]); err != nil || state != nexusmigration.Conflict {
				t.Fatalf("losing identity=%s err=%v", state, err)
			}
		}
	}
	if winners != 1 {
		t.Fatalf("CAS admitted %d writers, errors=%v", winners, errorsByWriter)
	}
}

func TestPostgresRustFSNexusPublicationRawIntentLocksReclaimWindow(t *testing.T) {
	f := newNexusPGPublicationFixture(t, repository.FormatRaw)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, release, err := f.publication.Lock(ctx, f.entry)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	nexusPublicationStage(t, ctx, f.publication, &f.entry, f.bodies)
	candidates, err := f.storeB.ListUnreferencedRawObjects(ctx, time.Now().Add(time.Hour), 10000)
	if err != nil {
		t.Fatal(err)
	}
	digest, found := "sha256:"+f.entry.Assets[0].SHA256, false
	for _, candidate := range candidates {
		found = found || candidate.Digest == digest
	}
	if !found {
		t.Fatal("uploaded bytes lack durable Raw reclamation intent")
	}
	acquired := make(chan bool, 1)
	gcErrors := make(chan error, 1)
	go func() {
		unlock, err := f.storeB.LockRawObject(ctx, digest)
		if err != nil {
			gcErrors <- err
			return
		}
		defer unlock()
		unreferenced, err := f.storeB.RawObjectIsUnreferenced(ctx, digest)
		if err != nil {
			gcErrors <- err
			return
		}
		acquired <- unreferenced
	}()
	select {
	case state := <-acquired:
		t.Fatalf("GC object lock escaped before publication: unreferenced=%t", state)
	case err := <-gcErrors:
		t.Fatal(err)
	case <-time.After(75 * time.Millisecond):
	}
	if err = f.publication.Verify(ctx, f.entry); err != nil {
		t.Fatal(err)
	}
	if err = f.publication.Publish(ctx, f.entry); err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case unreferenced := <-acquired:
		if unreferenced {
			t.Fatal("GC reference recheck lost the newly visible Raw path")
		}
	case err := <-gcErrors:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = nexusVerifyObject(ctx, f.objectsB, repository.MavenAsset{ObjectKey: "native/raw/sha256/" + f.entry.Assets[0].SHA256, Digest: digest, Size: f.entry.Assets[0].Size}); err != nil {
		t.Fatalf("GC window lost target bytes: %v", err)
	}
}

type nexusLostCommitResponse struct {
	NexusMigrationPublicationStore
	lost atomic.Bool
}

func (s *nexusLostCommitResponse) CommitMavenPublishSessionIdempotently(ctx context.Context, id, key, payload string, assets []repository.MavenAsset) (repository.MavenArtifact, bool, error) {
	artifact, replayed, err := s.NexusMigrationPublicationStore.CommitMavenPublishSessionIdempotently(ctx, id, key, payload, assets)
	if err == nil && !s.lost.Swap(true) {
		return repository.MavenArtifact{}, false, errors.New("synthetic commit response lost")
	}
	return artifact, replayed, err
}

func TestPostgresRustFSNexusPublicationMavenAtomicExpiryAndLostResponse(t *testing.T) {
	f := newNexusPGPublicationFixture(t, repository.FormatMaven)
	lost := &nexusLostCommitResponse{NexusMigrationPublicationStore: f.storeA}
	publication := NewNexusMigrationPublication(lost, ObjectStores{repository.FormatMaven: f.objectsA}, "owned-target", "owned-pool")
	ctx, release, err := publication.Lock(context.Background(), f.entry)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	f.entry.SessionID, err = publication.Begin(ctx, f.entry)
	if err != nil {
		t.Fatal(err)
	}
	first := f.entry.Assets[0]
	if err = publication.Stage(ctx, f.entry, first, bytes.NewReader(f.bodies[first.SourcePath])); err != nil {
		t.Fatal(err)
	}
	if err = publication.Verify(ctx, f.entry); err == nil {
		t.Fatal("half GAV passed byte verification")
	}
	if state, err := publication.Readback(ctx, f.entry); err != nil || state != nexusmigration.Absent {
		t.Fatalf("half GAV visible=%s err=%v", state, err)
	}
	db, err := sql.Open("pgx", os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.ExecContext(ctx, `UPDATE native_maven_publish_sessions SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, f.entry.SessionID); err != nil {
		t.Fatal(err)
	}
	expired := f.entry.SessionID
	f.entry.SessionID, err = publication.Begin(ctx, f.entry)
	if err != nil || expired == f.entry.SessionID {
		t.Fatalf("expired native session not recreated: old=%s new=%s err=%v", expired, f.entry.SessionID, err)
	}
	nexusPublicationStage(t, ctx, publication, &f.entry, f.bodies)
	if err = publication.Verify(ctx, f.entry); err != nil {
		t.Fatal(err)
	}
	if err = publication.Publish(ctx, f.entry); err == nil {
		t.Fatal("lost commit response did not interrupt the caller")
	}
	other := NewNexusMigrationPublication(f.storeB, ObjectStores{repository.FormatMaven: f.objectsB}, "owned-target", "owned-pool")
	if state, err := other.Readback(context.Background(), f.entry); err != nil || state != nexusmigration.Identical {
		t.Fatalf("committed native state lost on response failure=%s err=%v", state, err)
	}
	if err = publication.Publish(ctx, f.entry); err != nil {
		t.Fatalf("same native commit did not replay: %v", err)
	}
	artifacts, err := f.storeB.ListMavenArtifacts(ctx, f.repo.ID)
	if err != nil || len(artifacts) != 1 {
		t.Fatalf("response recovery duplicated release=%v err=%v", artifacts, err)
	}
	handler := newTestGateway(t, GatewayOptions{Store: f.storeB, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatMaven: f.objectsB}})
	nexusPublicationNativeReads(t, handler, f.repo, f.entry, f.bodies)
}

func TestPostgresRustFSNexusPublicationMavenSidecarQuotaAndWrongTargetBytes(t *testing.T) {
	f := newNexusPGPublicationFixture(t, repository.FormatMaven)
	ctx, release, err := f.publication.Lock(context.Background(), f.entry)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	planned, err := nexusNativeAssets(f.entry)
	if err != nil {
		t.Fatal(err)
	}
	var primaryBytes, fullBytes int64
	for _, asset := range f.entry.Assets {
		primaryBytes += asset.Size
	}
	for _, asset := range planned {
		fullBytes += asset.Size
	}
	if fullBytes <= primaryBytes {
		t.Fatal("fixture lacks generated checksum storage")
	}
	if _, err = f.storeA.ReplaceRepositoryCapacityQuota(ctx, f.repo.ID, fullBytes-1); err != nil {
		t.Fatal(err)
	}
	report, err := f.publication.Capacity(ctx, nexusPublicationBatch(f.entry), []nexusmigration.Entry{f.entry})
	if err != nil || report.Status != capacityplan.Insufficient || len(report.Repositories) != 1 || *report.Repositories[0].AdditionalBytes != fullBytes {
		t.Fatalf("generated sidecar quota=%+v err=%v", report, err)
	}
	nexusPublicationStage(t, ctx, f.publication, &f.entry, f.bodies)
	if err = f.publication.Verify(ctx, f.entry); err != nil {
		t.Fatal(err)
	}
	if err = f.publication.Publish(ctx, f.entry); err == nil || err.Error() != "target_quota_exceeded" {
		t.Fatalf("native transaction ignored sidecar quota: %v", err)
	}
	if state, err := f.publication.Readback(ctx, f.entry); err != nil || state != nexusmigration.Absent {
		t.Fatalf("quota failure made partial GAV visible=%s err=%v", state, err)
	}
	if _, err = f.storeA.ReplaceRepositoryCapacityQuota(ctx, f.repo.ID, fullBytes); err != nil {
		t.Fatal(err)
	}
	if err = f.publication.Publish(ctx, f.entry); err != nil {
		t.Fatal(err)
	}
	handler := newTestGateway(t, GatewayOptions{Store: f.storeB, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatMaven: f.objectsB}})
	nexusPublicationNativeReads(t, handler, f.repo, f.entry, f.bodies)
	asset := f.entry.Assets[0]
	key, digest := "native/maven/sha256/"+asset.SHA256, "sha256:"+asset.SHA256
	// Keep the length and verified-digest metadata correct while replacing the
	// actual S3 payload. Readback must hash bytes rather than trusting HEAD.
	if err = f.objectsB.PutVerifiedReader(ctx, key, bytes.NewReader(bytes.Repeat([]byte("x"), int(asset.Size))), asset.Size, digest); err != nil {
		t.Fatal(err)
	}
	info, err := f.objectsB.Stat(ctx, key)
	if err != nil || info.Size != asset.Size || info.Digest != digest {
		t.Fatalf("corruption fixture metadata=%+v err=%v", info, err)
	}
	other := NewNexusMigrationPublication(f.storeB, ObjectStores{repository.FormatMaven: f.objectsB}, "owned-target", "owned-pool")
	if state, err := other.Readback(context.Background(), f.entry); err != nil || state != nexusmigration.Conflict {
		t.Fatalf("same-size wrong bytes accepted=%s err=%v", state, err)
	}
	report, err = other.Capacity(context.Background(), nexusPublicationBatch(f.entry), []nexusmigration.Entry{f.entry})
	if err != nil || report.Status != capacityplan.Unknown {
		t.Fatalf("unverified physical evidence became sufficient=%+v err=%v", report, err)
	}
}

func TestPostgresRustFSNexusPublicationMavenRequiredReleaseAssets(t *testing.T) {
	cases := []struct {
		name, packaging, want string
		main                  bool
	}{
		{name: "default jar without main", want: "maven_required_asset_missing"},
		{name: "jar without main", packaging: "jar", want: "maven_required_asset_missing"},
		{name: "classifier does not replace main", packaging: "jar", want: "maven_required_asset_missing"},
		{name: "pom only", packaging: "pom"},
		{name: "unknown lifecycle", packaging: "custom-archive", main: true, want: "maven_release_closure_unsupported"},
		{name: "maven plugin", packaging: "maven-plugin", main: true},
		{name: "ejb", packaging: "ejb", main: true},
		{name: "bundle", packaging: "bundle", main: true},
		{name: "war without main", packaging: "war", want: "maven_required_asset_missing"},
		{name: "war", packaging: "war", main: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newNexusPGPublicationFixture(t, repository.FormatMaven)
			f.entry.Assets = nil
			bodies := map[string][]byte{}
			for assetPath, body := range f.bodies {
				if strings.HasSuffix(assetPath, ".pom") {
					if tc.packaging != "" {
						body = bytes.Replace(body, []byte("</project>"), []byte("<packaging>"+tc.packaging+"</packaging></project>"), 1)
					}
				} else if tc.name == "classifier does not replace main" {
					assetPath = strings.TrimSuffix(assetPath, ".jar") + "-sources.jar"
				} else if !tc.main {
					continue
				} else if tc.packaging == "war" {
					assetPath = strings.TrimSuffix(assetPath, ".jar") + ".war"
				}
				bodies[assetPath] = body
				f.entry.Assets = append(f.entry.Assets, nexusPublicationAsset(assetPath, body))
			}
			ctx, release, err := f.publication.Lock(context.Background(), f.entry)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			nexusPublicationStage(t, ctx, f.publication, &f.entry, bodies)
			err = f.publication.Verify(ctx, f.entry)
			if tc.want != "" {
				if err == nil || err.Error() != tc.want {
					t.Fatalf("closure failure=%v want=%s", err, tc.want)
				}
				if err = f.publication.Publish(ctx, f.entry); err == nil {
					t.Fatal("incomplete/unknown Maven release published")
				}
				if state, err := f.publication.Readback(ctx, f.entry); err != nil || state != nexusmigration.Absent {
					t.Fatalf("rejected closure visible=%s err=%v", state, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = f.publication.Publish(ctx, f.entry); err != nil {
				t.Fatal(err)
			}
			handler := newTestGateway(t, GatewayOptions{Store: f.storeB, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatMaven: f.objectsB}})
			nexusPublicationNativeReads(t, handler, f.repo, f.entry, bodies)
		})
	}
}

func TestPostgresRustFSNexusPublicationMavenDecodedVersionNames(t *testing.T) {
	f := newNexusPGPublicationFixture(t, repository.FormatMaven)
	const version = "1.0.0+build.42"
	f.entry.Identity = "org.example:migration:" + version
	f.entry.Assets = nil
	bodies := map[string][]byte{}
	for assetPath, body := range f.bodies {
		assetPath = strings.ReplaceAll(assetPath, "1.0.0", version)
		if strings.HasSuffix(assetPath, ".pom") {
			body = bytes.ReplaceAll(body, []byte("<version>1.0.0</version>"), []byte("<version>"+version+"</version>"))
		}
		f.entry.Assets = append(f.entry.Assets, nexusPublicationAsset(assetPath, body))
		bodies[assetPath] = body
	}
	ctx, release, err := f.publication.Lock(context.Background(), f.entry)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	nexusPublicationStage(t, ctx, f.publication, &f.entry, bodies)
	if err = f.publication.Verify(ctx, f.entry); err != nil {
		t.Fatal(err)
	}
	if err = f.publication.Publish(ctx, f.entry); err != nil {
		t.Fatal(err)
	}
	if state, err := f.publication.Readback(ctx, f.entry); err != nil || state != nexusmigration.Identical {
		t.Fatalf("decoded version identity=%s err=%v", state, err)
	}
	handler := newTestGateway(t, GatewayOptions{Store: f.storeB, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatMaven: f.objectsB}})
	// Maven GETs encode the wire path; native metadata still uses decoded '+'.
	nexusPublicationNativeReads(t, handler, f.repo, f.entry, bodies)
}

package app

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/capacityplan"
	"github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func nexusPublicationAsset(assetPath string, body []byte) nexusmigration.Asset {
	h256, h1, hmd5 := sha256.Sum256(body), sha1.Sum(body), md5.Sum(body)
	return nexusmigration.Asset{SourcePath: assetPath, TargetPath: assetPath, Size: int64(len(body)),
		SHA256: hex.EncodeToString(h256[:]), SHA1: hex.EncodeToString(h1[:]), MD5: hex.EncodeToString(hmd5[:])}
}

func nexusPublicationFixture(t *testing.T, format repository.Format) (*repository.MemoryStore, *objectstore.MemoryStore, *NexusMigrationPublication, repository.HostedRepository, nexusmigration.Entry, map[string][]byte) {
	t.Helper()
	store, objects := repository.NewMemoryStore(), objectstore.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "migration-" + uuid.NewString(), Format: format, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	entry, bodies := nexusPublicationInventory(repo)
	return store, objects, NewNexusMigrationPublication(store, ObjectStores{format: objects}, "owned-target", "owned-pool"), repo, entry, bodies
}

func nexusPublicationInventory(repo repository.HostedRepository) (nexusmigration.Entry, map[string][]byte) {
	entry := nexusmigration.Entry{ID: uuid.NewString(), BatchID: uuid.NewString(), SourceRepository: "synthetic-source", TargetRepositoryID: repo.ID, Format: string(repo.Format), State: "pending", Actor: "synthetic-admin"}
	bodies := map[string][]byte{"releases/synthetic.txt": []byte("synthetic migration payload")}
	entry.Identity = "releases/synthetic.txt"
	if repo.Format == repository.FormatMaven {
		entry.Identity = "org.example:migration:1.0.0"
		bodies = map[string][]byte{
			"org/example/migration/1.0.0/migration-1.0.0.pom": []byte(`<project><modelVersion>4.0.0</modelVersion><groupId>org.example</groupId><artifactId>migration</artifactId><version>1.0.0</version></project>`),
			"org/example/migration/1.0.0/migration-1.0.0.jar": []byte("synthetic jar bytes"),
		}
	}
	for assetPath, body := range bodies {
		entry.Assets = append(entry.Assets, nexusPublicationAsset(assetPath, body))
	}
	return entry, bodies
}

func nexusPublicationStage(t *testing.T, ctx context.Context, publication *NexusMigrationPublication, entry *nexusmigration.Entry, bodies map[string][]byte) {
	t.Helper()
	var err error
	entry.SessionID, err = publication.Begin(ctx, *entry)
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range entry.Assets {
		if err := publication.Stage(ctx, *entry, asset, bytes.NewReader(bodies[asset.SourcePath])); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNexusMigrationPublicationNativeReadbackAndCapacity(t *testing.T) {
	for _, format := range []repository.Format{repository.FormatRaw, repository.FormatMaven} {
		t.Run(string(format), func(t *testing.T) {
			store, objects, publication, repo, entry, bodies := nexusPublicationFixture(t, format)
			ctx := context.Background()
			if err := publication.ValidateTargets(ctx, []nexusmigration.Mapping{{SourceRepository: entry.SourceRepository, TargetRepositoryID: repo.ID}}); err != nil {
				t.Fatal(err)
			}
			batch := nexusPublicationBatch(entry)
			report, err := publication.Capacity(ctx, batch, []nexusmigration.Entry{entry})
			if err != nil || report.Status != capacityplan.Sufficient {
				t.Fatalf("capacity=%+v err=%v", report, err)
			}
			planned, err := nexusNativeAssets(entry)
			if err != nil {
				t.Fatal(err)
			}
			var total int64
			for _, asset := range planned {
				total += asset.Size
			}
			if len(report.Repositories) != 1 || *report.Repositories[0].AdditionalBytes != total || *report.Storage.AdditionalBytes != total {
				t.Fatalf("generated sidecars missing from capacity: %+v total=%d", report, total)
			}
			ctx, release, err := publication.Lock(ctx, entry)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			nexusPublicationStage(t, ctx, publication, &entry, bodies)
			if state, err := publication.Readback(ctx, entry); err != nil || state != nexusmigration.Absent {
				t.Fatalf("uncommitted state=%s err=%v", state, err)
			}
			if err := publication.Publish(ctx, entry); err == nil {
				t.Fatal("published before complete target verification")
			}
			if err := publication.Verify(ctx, entry); err != nil {
				t.Fatal(err)
			}
			if err := publication.Publish(ctx, entry); err != nil {
				t.Fatal(err)
			}
			if state, err := publication.Readback(ctx, entry); err != nil || state != nexusmigration.Identical {
				t.Fatalf("published state=%s err=%v", state, err)
			}
			handler := newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{format: objects}})
			nexusPublicationNativeReads(t, handler, repo, entry, bodies)
			report, err = publication.Capacity(ctx, batch, []nexusmigration.Entry{entry})
			if err != nil || report.Status != capacityplan.Sufficient || *report.Repositories[0].AdditionalBytes != 0 || *report.Storage.AdditionalBytes != 0 {
				t.Fatalf("verified existing target budget=%+v err=%v", report, err)
			}
		})
	}
}

func nexusPublicationBatch(entry nexusmigration.Entry) nexusmigration.Batch {
	zero, free := int64(0), int64(1<<30)
	var peak int64
	for _, asset := range entry.Assets {
		if asset.Size > peak {
			peak = asset.Size
		}
	}
	return nexusmigration.Batch{ID: entry.BatchID, ScanComplete: true, EntryCount: 1, ManifestSHA256: strings.Repeat("a", 64), StartInput: nexusmigration.StartInput{
		Mappings: []nexusmigration.Mapping{{SourceRepository: entry.SourceRepository, TargetRepositoryID: entry.TargetRepositoryID}},
		CapacityEvidence: nexusmigration.CapacityEvidence{TargetID: "owned-target", PoolID: "owned-pool", ObservedAt: time.Now().Add(-time.Minute), ValidUntil: time.Now().Add(time.Hour),
			FreeBytes: &free, DownloadBytes: &peak, UploadBytes: &zero, BackupBytes: &zero, RestoreBytes: &zero, HeadroomBytes: &zero}}}
}

func nexusPublicationNativeReads(t *testing.T, handler http.Handler, repo repository.HostedRepository, entry nexusmigration.Entry, bodies map[string][]byte) {
	t.Helper()
	prefix := "/raw/"
	if repo.Format == repository.FormatMaven {
		prefix = "/repository/maven/"
	}
	for _, asset := range entry.Assets {
		want := map[string][]byte{asset.TargetPath: bodies[asset.SourcePath]}
		if repo.Format == repository.FormatMaven {
			for _, checksum := range nexusMavenChecksums(asset) {
				want[asset.TargetPath+checksum.extension] = []byte(checksum.body)
			}
		}
		for name, body := range want {
			requestPath := prefix + repo.Name + "/" + name
			if repo.Format == repository.FormatMaven {
				requestPath = (&url.URL{Path: requestPath}).String()
			}
			request := httptest.NewRequest(http.MethodGet, requestPath, nil)
			authorize(request, "admin-secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), body) {
				t.Fatalf("native GET %s=%d body=%q want=%q", name, response.Code, response.Body.Bytes(), body)
			}
		}
	}
}

func TestNexusMigrationPublicationRejectsVirtualRawChecksumPaths(t *testing.T) {
	store, objects, publication, repo, entry, _ := nexusPublicationFixture(t, repository.FormatRaw)
	for _, suffix := range []string{".sha256", ".sha512", ".sha1", ".md5"} {
		t.Run(suffix, func(t *testing.T) {
			candidate := entry
			candidate.Identity = entry.Identity + suffix
			candidate.Assets = []nexusmigration.Asset{nexusPublicationAsset(candidate.Identity, []byte("original Nexus sidecar formatting"))}
			if _, _, err := publication.Lock(context.Background(), candidate); err == nil {
				t.Fatal("sidecar accepted as independently readable native Raw bytes")
			}
			if _, err := store.GetRawAsset(context.Background(), repo.ID, candidate.Identity); !errors.Is(err, repository.ErrNotFound) {
				t.Fatalf("sidecar metadata=%v", err)
			}
		})
	}
	keys, err := objects.List(context.Background(), "")
	if err != nil || len(keys) != 0 {
		t.Fatalf("unsupported sidecar wrote objects=%v err=%v", keys, err)
	}
}

func TestNexusMigrationPublicationRejectsBadBytesAndReleasedCoordination(t *testing.T) {
	_, objects, publication, _, entry, bodies := nexusPublicationFixture(t, repository.FormatRaw)
	ctx, release, err := publication.Lock(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	asset := entry.Assets[0]
	if err = publication.Stage(ctx, entry, asset, bytes.NewReader(bytes.Repeat([]byte("x"), int(asset.Size)))); err == nil {
		t.Fatal("same-size incorrect source bytes accepted")
	}
	nexusPublicationStage(t, ctx, publication, &entry, bodies)
	if err = objects.Put(ctx, "native/raw/sha256/"+asset.SHA256, bytes.Repeat([]byte("x"), int(asset.Size))); err != nil {
		t.Fatal(err)
	}
	if err = publication.Verify(ctx, entry); err == nil {
		t.Fatal("same-size incorrect target bytes accepted")
	}
	if err = publication.Publish(ctx, entry); err == nil {
		t.Fatal("published after failed target verification")
	}
	if state, err := publication.Readback(ctx, entry); err != nil || state != nexusmigration.Absent {
		t.Fatalf("bad bytes became visible: %s %v", state, err)
	}
	release()
	if err = publication.Stage(ctx, entry, asset, bytes.NewReader(bodies[asset.SourcePath])); err == nil {
		t.Fatal("released object lock admitted write")
	}
	if err = publication.Verify(context.Background(), entry); err == nil {
		t.Fatal("verification admitted without GC coordination")
	}
}

func TestNexusMigrationPublicationMavenSessionRecoveryAndPOMValidation(t *testing.T) {
	_, _, publication, _, entry, bodies := nexusPublicationFixture(t, repository.FormatMaven)
	ctx, release, err := publication.Lock(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	id, err := publication.Begin(ctx, entry)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := publication.Begin(ctx, entry)
	if err != nil || replayed != id {
		t.Fatalf("native create lost-response recovery=%s first=%s err=%v", replayed, id, err)
	}
	entry.SessionID = id
	if err = publication.Stage(ctx, entry, entry.Assets[0], bytes.NewReader(bodies[entry.Assets[0].SourcePath])); err != nil {
		t.Fatal(err)
	}
	if err = publication.Verify(ctx, entry); err == nil {
		t.Fatal("partially staged GAV verified")
	}
	if state, err := publication.Readback(ctx, entry); err != nil || state != nexusmigration.Absent {
		t.Fatalf("partial GAV visible=%s err=%v", state, err)
	}
}

func TestNexusMigrationPublicationCapacityRequiresBoundFreshCompleteEvidence(t *testing.T) {
	_, _, publication, _, entry, _ := nexusPublicationFixture(t, repository.FormatMaven)
	cases := map[string]func(*nexusmigration.Batch){
		"wrong target":  func(b *nexusmigration.Batch) { b.CapacityEvidence.TargetID = "another-target" },
		"wrong pool":    func(b *nexusmigration.Batch) { b.CapacityEvidence.PoolID = "another-pool" },
		"expired":       func(b *nexusmigration.Batch) { b.CapacityEvidence.ValidUntil = time.Now().Add(-time.Second) },
		"incomplete":    func(b *nexusmigration.Batch) { b.ScanComplete = false },
		"missing entry": func(b *nexusmigration.Batch) { b.EntryCount++ },
		"wrong batch":   func(b *nexusmigration.Batch) { b.ID = uuid.NewString() },
		"changed mapping": func(b *nexusmigration.Batch) {
			b.Mappings = []nexusmigration.Mapping{{SourceRepository: entry.SourceRepository, TargetRepositoryID: uuid.NewString()}}
		},
		"unknown free":       func(b *nexusmigration.Batch) { b.CapacityEvidence.FreeBytes = nil },
		"zero download peak": func(b *nexusmigration.Batch) { zero := int64(0); b.CapacityEvidence.DownloadBytes = &zero },
		"no sidecar room": func(b *nexusmigration.Batch) {
			var total int64
			for _, a := range entry.Assets {
				total += a.Size
			}
			b.CapacityEvidence.FreeBytes = &total
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			batch := nexusPublicationBatch(entry)
			mutate(&batch)
			report, err := publication.Capacity(context.Background(), batch, []nexusmigration.Entry{entry})
			if err == nil && report.Status == capacityplan.Sufficient {
				t.Fatalf("unbound/unknown/insufficient evidence admitted: %+v", report)
			}
		})
	}
}

func TestNexusMigrationPublicationRejectsMismatchedPOMIdentity(t *testing.T) {
	_, _, publication, _, entry, bodies := nexusPublicationFixture(t, repository.FormatMaven)
	for i, asset := range entry.Assets {
		if strings.HasSuffix(asset.SourcePath, ".pom") {
			bodies[asset.SourcePath] = bytes.ReplaceAll(bodies[asset.SourcePath], []byte("<artifactId>migration</artifactId>"), []byte("<artifactId>another</artifactId>"))
			entry.Assets[i] = nexusPublicationAsset(asset.SourcePath, bodies[asset.SourcePath])
		}
	}
	ctx, release, err := publication.Lock(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	nexusPublicationStage(t, ctx, publication, &entry, bodies)
	if err = publication.Verify(ctx, entry); err == nil || err.Error() != "invalid_maven_pom" {
		t.Fatalf("POM/path identity mismatch admitted: %v", err)
	}
	if state, err := publication.Readback(ctx, entry); err != nil || state != nexusmigration.Absent {
		t.Fatalf("invalid GAV visible=%s err=%v", state, err)
	}
}

func TestNexusMigrationPublicationReadbackRequiresCompleteRelease(t *testing.T) {
	store, objects, publication, repo, entry, bodies := nexusPublicationFixture(t, repository.FormatMaven)
	for _, asset := range entry.Assets {
		if strings.HasSuffix(asset.TargetPath, ".pom") {
			entry.Assets = []nexusmigration.Asset{asset}
			break
		}
	}
	handler := newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatMaven: objects}})
	asset := entry.Assets[0]
	request := httptest.NewRequest(http.MethodPut, "/repository/maven/"+repo.Name+"/"+asset.TargetPath, bytes.NewReader(bodies[asset.SourcePath]))
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("existing HTTP publication changed=%d %s", response.Code, response.Body.String())
	}
	if state, err := publication.Readback(context.Background(), entry); err == nil || err.Error() != "maven_required_asset_missing" || state == nexusmigration.Identical {
		t.Fatalf("incomplete existing release counted as a successful skip=%s err=%v", state, err)
	}
}

type nexusPublicationScanRecorder struct {
	t           *testing.T
	publication *NexusMigrationPublication
	entry       nexusmigration.Entry
	calls       int
}

func (s *nexusPublicationScanRecorder) ScheduleRepository(ctx context.Context, repo string, format repository.Format, identity, digest, actor string) error {
	s.t.Helper()
	if repo != s.entry.TargetRepositoryID || string(format) != s.entry.Format || identity != s.entry.Identity || digest != "sha256:"+s.entry.Assets[0].SHA256 || actor != s.entry.Actor {
		s.t.Fatalf("scanner lost native identity or actor")
	}
	if state, err := s.publication.Readback(ctx, s.entry); err != nil || state != nexusmigration.Identical {
		s.t.Fatalf("scanner ran before complete native publication=%s err=%v", state, err)
	}
	s.calls++
	return nil
}

func TestNexusMigrationPublicationSchedulesScanAfterNativeVisibility(t *testing.T) {
	_, _, publication, _, entry, bodies := nexusPublicationFixture(t, repository.FormatRaw)
	scanner := &nexusPublicationScanRecorder{t: t, publication: publication, entry: entry}
	publication.WithPublicationScanner(scanner)
	ctx, release, err := publication.Lock(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	nexusPublicationStage(t, ctx, publication, &entry, bodies)
	if scanner.calls != 0 {
		t.Fatal("unpublished upload scheduled scan")
	}
	if err = publication.Verify(ctx, entry); err != nil {
		t.Fatal(err)
	}
	if err = publication.Publish(ctx, entry); err != nil {
		t.Fatal(err)
	}
	if scanner.calls != 1 {
		t.Fatalf("scanner calls=%d", scanner.calls)
	}
}

func TestNexusMigrationPublicationRawCanonicalTargetPathIsNativelyReadable(t *testing.T) {
	store, objects, publication, repo, entry, _ := nexusPublicationFixture(t, repository.FormatRaw)
	body := []byte("synthetic encoded Raw name")
	sourcePath := "releases/资料 with spaces+1.bin"
	segments := strings.Split(sourcePath, "/")
	for i := range segments {
		segments[i] = url.PathEscape(segments[i])
	}
	asset := nexusPublicationAsset(sourcePath, body)
	asset.TargetPath = strings.Join(segments, "/")
	entry.Identity, entry.Assets = asset.TargetPath, []nexusmigration.Asset{asset}
	ctx, release, err := publication.Lock(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	bodies := map[string][]byte{sourcePath: body}
	nexusPublicationStage(t, ctx, publication, &entry, bodies)
	if err = publication.Verify(ctx, entry); err != nil {
		t.Fatal(err)
	}
	if err = publication.Publish(ctx, entry); err != nil {
		t.Fatal(err)
	}
	handler := newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatRaw: objects}})
	nexusPublicationNativeReads(t, handler, repo, entry, bodies)
}

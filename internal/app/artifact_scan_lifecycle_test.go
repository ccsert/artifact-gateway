package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/scanning"
	"github.com/google/uuid"
)

type artifactScanResolverFunc func(context.Context, string, repository.ArtifactScanPayload) (scanning.Artifact, error)

func (f artifactScanResolverFunc) ResolveArtifactScan(ctx context.Context, repositoryID string, payload repository.ArtifactScanPayload) (scanning.Artifact, error) {
	return f(ctx, repositoryID, payload)
}

func TestArtifactScanWorkerMergesScannerFieldsAndPreservesTrustEvidence(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	digest := testScanDigest("artifact")
	seed := repository.ArtifactIntelligence{
		RepositoryID: "repo", Format: repository.FormatRaw, Coordinate: "release/widget.bin", Digest: digest,
		Signatures: []repository.ArtifactSignature{{KeyID: "release-key", Algorithm: "ed25519", Identity: "release", Signature: "signed", Verified: true}},
		Provenance: &repository.ArtifactProvenance{Builder: "ci", BuildType: "release", SourceRepository: "source", SourceCommit: "abc", BuildID: "build-1"},
		SBOMs:      []repository.ArtifactSBOM{{MediaType: "application/spdx+json", Digest: testScanDigest("old-sbom")}},
		UpdatedBy:  "publisher",
	}
	if _, err := store.ReplaceArtifactIntelligence(ctx, seed, ""); err != nil {
		t.Fatal(err)
	}
	job, _, err := repository.EnqueueArtifactScanJob(ctx, store, "repo", "scan-1", repository.ArtifactScanPayload{Format: repository.FormatRaw, Coordinate: seed.Coordinate, Digest: digest})
	if err != nil {
		t.Fatal(err)
	}
	artifact := scanning.Artifact{RepositoryID: "repo", Format: repository.FormatRaw, Coordinate: seed.Coordinate, Digest: digest, Assets: []scanning.Asset{{Path: "widget.bin", Digest: digest, Size: 8, Open: func(context.Context) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("artifact")), nil }}}}
	worker := ArtifactScanWorker{
		Store: store,
		Resolver: artifactScanResolverFunc(func(_ context.Context, repositoryID string, payload repository.ArtifactScanPayload) (scanning.Artifact, error) {
			if repositoryID != "repo" || payload.Digest != digest {
				t.Fatalf("resolve repository=%q payload=%+v", repositoryID, payload)
			}
			return artifact, nil
		}),
		Scanner: scanning.ScannerFunc(func(_ context.Context, received scanning.Artifact) (scanning.Report, error) {
			if received.Digest != digest {
				t.Fatalf("scan artifact=%+v", received)
			}
			return scanning.Report{
				SBOMs:         []repository.ArtifactSBOM{{MediaType: "application/vnd.cyclonedx+json", Digest: testScanDigest("new-sbom")}},
				Licenses:      []repository.ArtifactLicense{{SPDXID: "Apache-2.0", Name: "Apache License 2.0"}},
				Vulnerability: &repository.ArtifactVulnerabilitySummary{Scanner: "trivy", ScannedAt: time.Now().UTC(), Status: "affected", High: 2},
			}, nil
		}),
		WorkerFormats: []repository.Format{repository.FormatRaw},
	}
	if err := worker.RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetArtifactIntelligence(ctx, "repo", repository.FormatRaw, seed.Coordinate, digest)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Signatures) != 1 || stored.Provenance == nil || stored.Provenance.BuildID != "build-1" || len(stored.SBOMs) != 1 || len(stored.Licenses) != 1 || stored.Vulnerability == nil || stored.Vulnerability.High != 2 || stored.UpdatedBy != "scanner:trivy" {
		t.Fatalf("merged intelligence=%+v", stored)
	}
	completed, err := store.GetLifecycleJob(ctx, "repo", job.ID)
	if err != nil || completed.State != repository.LifecycleJobCompleted {
		t.Fatalf("job=%+v err=%v", completed, err)
	}
}

func TestArtifactScanWorkerRetriesScannerFailure(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	digest := testScanDigest("failed")
	job, _, err := repository.EnqueueArtifactScanJob(ctx, store, "repo", "scan-failure", repository.ArtifactScanPayload{Format: repository.FormatRaw, Coordinate: "failed.bin", Digest: digest})
	if err != nil {
		t.Fatal(err)
	}
	worker := ArtifactScanWorker{
		Store: store,
		Resolver: artifactScanResolverFunc(func(context.Context, string, repository.ArtifactScanPayload) (scanning.Artifact, error) {
			return scanning.Artifact{RepositoryID: "repo", Format: repository.FormatRaw, Coordinate: "failed.bin", Digest: digest, Assets: []scanning.Asset{{Path: "failed.bin", Digest: digest, Size: 6, Open: func(context.Context) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("failed")), nil }}}}, nil
		}),
		Scanner: scanning.ScannerFunc(func(context.Context, scanning.Artifact) (scanning.Report, error) {
			return scanning.Report{}, scanning.ErrScannerUnavailable
		}),
		WorkerFormats: []repository.Format{repository.FormatRaw},
	}
	if err := worker.RunJobs(ctx, 1); err == nil {
		t.Fatal("RunJobs() error = nil, want scanner failure")
	}
	retrying, err := store.GetLifecycleJob(ctx, "repo", job.ID)
	if err != nil || retrying.State != repository.LifecycleJobRetrying || !strings.Contains(retrying.LastError, "scanner failed") {
		t.Fatalf("job=%+v err=%v", retrying, err)
	}
}

func TestArtifactScanWorkerRenewsLeaseDuringLongScan(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	digest := testScanDigest("long scan")
	job, _, err := repository.EnqueueArtifactScanJob(ctx, store, "repo", "scan-long", repository.ArtifactScanPayload{Format: repository.FormatRaw, Coordinate: "long.bin", Digest: digest})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	worker := ArtifactScanWorker{
		Store: store,
		Resolver: artifactScanResolverFunc(func(context.Context, string, repository.ArtifactScanPayload) (scanning.Artifact, error) {
			return scanning.Artifact{RepositoryID: "repo", Format: repository.FormatRaw, Coordinate: "long.bin", Digest: digest}, nil
		}),
		Scanner: scanning.ScannerFunc(func(ctx context.Context, _ scanning.Artifact) (scanning.Report, error) {
			close(started)
			select {
			case <-ctx.Done():
				return scanning.Report{}, ctx.Err()
			case <-release:
				return scanning.Report{}, nil
			}
		}),
		WorkerFormats:        []repository.Format{repository.FormatRaw},
		LeaseRefreshInterval: 5 * time.Millisecond,
	}
	done := make(chan error, 1)
	go func() { done <- worker.RunJobs(ctx, 1) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("scanner did not start")
	}
	claimed, err := store.GetLifecycleJob(ctx, "repo", job.ID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		renewed, getErr := store.GetLifecycleJob(ctx, "repo", job.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if renewed.LeaseExpiresAt.After(claimed.LeaseExpiresAt) && renewed.ProgressMessage == "scanning artifact" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("lease was not renewed: before=%s after=%s message=%q", claimed.LeaseExpiresAt, renewed.LeaseExpiresAt, renewed.ProgressMessage)
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	completed, err := store.GetLifecycleJob(ctx, "repo", job.ID)
	if err != nil || completed.State != repository.LifecycleJobCompleted {
		t.Fatalf("job=%+v err=%v", completed, err)
	}
}

func TestArtifactScanWorkerStartProcessesPendingJobs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := repository.NewMemoryStore()
	digest := testScanDigest("scheduled scan")
	job, _, err := repository.EnqueueArtifactScanJob(ctx, store, "repo", "scan-start", repository.ArtifactScanPayload{Format: repository.FormatRaw, Coordinate: "scheduled.bin", Digest: digest})
	if err != nil {
		t.Fatal(err)
	}
	worker := ArtifactScanWorker{
		Store: store,
		Resolver: artifactScanResolverFunc(func(context.Context, string, repository.ArtifactScanPayload) (scanning.Artifact, error) {
			return scanning.Artifact{RepositoryID: "repo", Format: repository.FormatRaw, Coordinate: "scheduled.bin", Digest: digest}, nil
		}),
		Scanner:       scanning.ScannerFunc(func(context.Context, scanning.Artifact) (scanning.Report, error) { return scanning.Report{}, nil }),
		WorkerFormats: []repository.Format{repository.FormatRaw},
	}
	worker.Start(ctx, time.Hour)
	deadline := time.Now().Add(time.Second)
	for {
		completed, getErr := store.GetLifecycleJob(ctx, "repo", job.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if completed.State == repository.LifecycleJobCompleted {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job state=%q want=%q", completed.State, repository.LifecycleJobCompleted)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestNativeArtifactScanResolverSelectsExactMavenSnapshotBuild(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	if _, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "repo", Name: "maven", Format: repository.FormatMaven}); err != nil {
		t.Fatal(err)
	}
	coordinate := "org.example:widget:1.0-SNAPSHOT"
	var selected repository.MavenArtifact
	for build, body := range []string{"first-build", "second-build"} {
		digest := testScanDigest(body)
		sessionID := "session-" + strconv.Itoa(build)
		name := "widget-1.0-SNAPSHOT.jar"
		if _, err := store.CreateMavenPublishSession(ctx, repository.MavenPublishSession{ID: sessionID, RepositoryID: "repo", Coordinate: coordinate, State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: name, Digest: digest, Size: int64(len(body))}}}); err != nil {
			t.Fatal(err)
		}
		key := "snapshot-" + strconv.Itoa(build)
		if err := objects.Put(ctx, key, []byte(body)); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkMavenPublishObject(ctx, sessionID, name, key); err != nil {
			t.Fatal(err)
		}
		artifact, err := store.CommitMavenPublishSession(ctx, sessionID, []repository.MavenAsset{{RepositoryID: "repo", Path: "org/example/widget/1.0-SNAPSHOT/" + name, ObjectKey: key, Digest: digest, Size: int64(len(body))}})
		if err != nil {
			t.Fatal(err)
		}
		if build == 1 {
			selected = artifact
		}
	}
	resolver := NewNativeArtifactScanResolver(store, objects)
	artifact, err := resolver.ResolveArtifactScan(ctx, "repo", repository.ArtifactScanPayload{Format: repository.FormatMaven, Coordinate: coordinate, Digest: selected.Digest})
	if err != nil {
		t.Fatal(err)
	}
	if len(artifact.Assets) != 1 || artifact.Assets[0].Digest != selected.Digest {
		t.Fatalf("resolved assets=%+v", artifact.Assets)
	}
	reader, err := artifact.Assets[0].Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if readErr != nil || string(body) != "second-build" {
		t.Fatalf("body=%q err=%v", body, readErr)
	}
}

func TestArtifactScanAPIEnqueuesIdempotently(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "raw", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	digest := testScanDigest("asset")
	handler := NewGatewayHandler(Dependencies{
		ArtifactScanner:        scanning.ScannerFunc(func(context.Context, scanning.Artifact) (scanning.Report, error) { return scanning.Report{}, nil }),
		ArtifactScannerFormats: []repository.Format{repository.FormatRaw},
	}, store, TestAdapter{}, Authenticator{AdminToken: "admin", AdminActor: "operator", ResolverToken: "resolver", ResolverActor: "build-agent"})
	request := func(token, key, coordinate, requestedDigest string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/artifact-scans", bytes.NewBufferString(`{"coordinate":"`+coordinate+`","digest":"`+requestedDigest+`"}`))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	first := request("admin", "scan-request-1", "asset.bin", digest)
	replay := request("admin", "scan-request-1", "asset.bin", digest)
	if first.Code != http.StatusAccepted || replay.Code != http.StatusAccepted || replay.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("first=%d body=%s replay=%d headers=%v body=%s", first.Code, first.Body.String(), replay.Code, replay.Header(), replay.Body.String())
	}
	if conflict := request("admin", "scan-request-1", "other.bin", testScanDigest("other")); conflict.Code != http.StatusConflict {
		t.Fatalf("conflict=%d body=%s", conflict.Code, conflict.Body.String())
	}
	if denied := request("resolver", "scan-request-2", "asset.bin", digest); denied.Code != http.StatusForbidden {
		t.Fatalf("denied=%d body=%s", denied.Code, denied.Body.String())
	}
	jobs, err := store.ListLifecycleJobs(context.Background(), repo.ID, 10)
	if err != nil || len(jobs) != 1 || jobs[0].Kind != repository.LifecycleJobScan {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
}

func TestArtifactScanAPIRejectsUnsupportedProxyCacheLayout(t *testing.T) {
	store := repository.NewMemoryStore()
	raw, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "raw-proxy", Format: repository.FormatRaw, Type: repository.RepositoryTypeProxy})
	if err != nil {
		t.Fatal(err)
	}
	npm, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "npm-proxy", Format: repository.FormatNPM, Type: repository.RepositoryTypeProxy})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{
		ArtifactScanner: scanning.ScannerFunc(func(context.Context, scanning.Artifact) (scanning.Report, error) {
			return scanning.Report{}, nil
		}),
		ArtifactScannerFormats: []repository.Format{repository.FormatRaw, repository.FormatNPM},
	}, store, TestAdapter{}, Authenticator{AdminToken: "admin", AdminActor: "operator"})
	request := func(repo repository.HostedRepository, coordinate string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/artifact-scans", bytes.NewBufferString(`{"coordinate":"`+coordinate+`","digest":"`+testScanDigest(coordinate)+`"}`))
		r.Header.Set("Authorization", "Bearer admin")
		r.Header.Set("Idempotency-Key", "scan-"+repo.ID)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if response := request(raw, "release/widget.bin"); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("raw proxy status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(npm, "widget@1.2.3"); response.Code != http.StatusAccepted {
		t.Fatalf("npm proxy status=%d body=%s", response.Code, response.Body.String())
	}
}

func testScanDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

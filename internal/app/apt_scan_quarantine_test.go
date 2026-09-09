package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/aptpublication"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/scanning"
	"github.com/google/uuid"
)

func TestAPTHTTPScanAndQuarantineSignedReads(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "apt-scanning", Format: repository.FormatAPT, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	deb := aptManagementDebianPackage(t, "Package: widget\nVersion: 1.0-1\nArchitecture: amd64\n")
	sum := sha256.Sum256(deb)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	coordinate := "pool/main/w/widget/widget.deb"
	manager := aptpublication.NewManager(store, objects)
	session, _, err := manager.CreateSession(ctx, aptpublication.CreateSessionInput{RepositoryID: repo.ID, Suite: "stable", Component: "main", Publisher: "ci", ObjectName: "widget.deb", DeclaredDigest: digest, DeclaredSize: int64(len(deb)), IdempotencyKey: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.UploadPackage(ctx, session.ID, session.ObjectName, bytes.NewReader(deb), int64(len(deb))); err != nil {
		t.Fatal(err)
	}
	// Exercise the actual multipart adapter, without treating a fixture report as
	// vulnerability-engine acceptance. Verify that exact .deb bytes reach it.
	scans := 0
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reader, e := r.MultipartReader()
		if e != nil {
			t.Error(e)
			w.WriteHeader(400)
			return
		}
		part, e := reader.NextPart()
		if e != nil {
			t.Error(e)
			w.WriteHeader(400)
			return
		}
		var metadata struct{ Format, Coordinate, Digest, RepositoryID string }
		if e = json.NewDecoder(part).Decode(&metadata); e != nil {
			t.Error(e)
		}
		if metadata.Format != "apt" || metadata.Coordinate != coordinate || metadata.Digest != digest || metadata.RepositoryID != repo.ID {
			t.Errorf("metadata: %+v", metadata)
		}
		part, e = reader.NextPart()
		if e != nil {
			t.Error(e)
			w.WriteHeader(400)
			return
		}
		actual, e := io.ReadAll(part)
		if e != nil || !bytes.Equal(actual, deb) {
			t.Errorf("scanner bytes differ: %v", e)
		}
		scans++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"schemaVersion":"v2","sboms":[],"licenses":[],"vulnerability":{"status":"affected","high":1,"critical":0,"medium":0,"low":0,"unknown":0}}`)
	}))
	defer service.Close()
	scanner, err := scanning.NewHTTPScanner(scanning.HTTPOptions{Name: "deb-fixture", Endpoint: service.URL})
	if err != nil {
		t.Fatal(err)
	}
	policy := repository.DefaultRepositorySecurityPolicy()
	policy.AutoScanOnPublish = true
	if _, err = store.ReplaceRepositorySecurityPolicy(ctx, repo.ID, policy, policy.Version); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeAPTObjectStore: objects, APTSigner: aptManagementSigner{}, ArtifactScanner: scanner, ArtifactScannerFormats: []repository.Format{repository.FormatAPT}}, store, TestAdapter{}, testAuthenticator())
	call := func(method, path, token, body, version string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		authorize(r, token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("If-Match", version)
		r.Header.Set("Idempotency-Key", "scan-publish")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	api := "/api/v2/repositories/" + repo.ID
	published := call("POST", api+"/apt/snapshots", "admin-secret", fmt.Sprintf(`{"suite":"stable","sequence":1,"publicationSessionIds":[%q]}`, session.ID), "")
	if published.Code != 201 {
		t.Fatalf("publish: %d %s", published.Code, published.Body.String())
	}
	for i := 0; i < 2; i++ {
		w := call("POST", api+"/apt/snapshots", "admin-secret", fmt.Sprintf(`{"suite":"stable","sequence":1,"publicationSessionIds":[%q]}`, session.ID), "")
		if w.Code != 201 {
			t.Fatal(w.Body.String())
		}
	}

	// Management rejects new Hosted membership. Pre-existing internal records
	// also cannot expose Hosted snapshots through the Group read route.
	groupBody := fmt.Sprintf(`{"name":"apt-hosted-bypass","format":"apt","members":[{"repositoryId":%q,"position":0}]}`, repo.ID)
	if w := call("POST", "/api/v2/groups", "admin-secret", groupBody, ""); w.Code != 400 {
		t.Fatalf("APT group admission: %d %s", w.Code, w.Body.String())
	}
	group, _, err := store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{ID: uuid.NewString(), Name: "apt-hosted-bypass", Format: repository.FormatAPT, Members: []repository.GroupMember{{RepositoryID: repo.ID, Position: 0}}}, "alice", "bypass", "bypass")
	if err != nil {
		t.Fatal(err)
	}
	if w := call("GET", "/apt/"+group.Name+"/dists/stable/InRelease", "resolver-secret", "", ""); w.Code != 404 {
		t.Fatalf("legacy Group exposed Hosted snapshot: %d %s", w.Code, w.Body.String())
	}

	jobs, err := store.ListLifecycleJobs(ctx, repo.ID, 100)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("publication jobs: %+v %v", jobs, err)
	}
	worker := ArtifactScanWorker{Store: store, Resolver: NewNativeArtifactScanResolver(store, objects), Scanner: scanner, WorkerFormats: []repository.Format{repository.FormatAPT}}
	if err = worker.RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	report, err := store.GetArtifactIntelligence(ctx, repo.ID, repository.FormatAPT, coordinate, digest)
	if err != nil || report.Vulnerability == nil || report.Vulnerability.High != 1 || report.UpdatedBy != "scanner:deb-fixture" || scans != 1 {
		t.Fatalf("report: %+v %v scans=%d", report, err, scans)
	}
	qurl := api + "/artifact-quarantine?coordinate=" + url.QueryEscape(coordinate) + "&digest=" + url.QueryEscape(digest)
	qbody := `{"state":"quarantined","reason":"confirmed investigation"}`
	if w := call("PUT", qurl, "resolver-secret", qbody, "0"); w.Code != 403 {
		t.Fatalf("quarantine auth: %d %s", w.Code, w.Body.String())
	}
	if w := call("PUT", qurl, "admin-secret", qbody, "0"); w.Code != 200 {
		t.Fatalf("quarantine: %d %s", w.Code, w.Body.String())
	}
	before := call("GET", "/apt/"+repo.Name+"/dists/stable/InRelease", "resolver-secret", "", "")
	if before.Code != 200 {
		t.Fatal(before.Body.String())
	}
	if w := call("PUT", api+"/quarantine-read-policy", "admin-secret", `{"version":"1","enabled":true}`, "1"); w.Code != 200 {
		t.Fatalf("read policy: %d %s", w.Code, w.Body.String())
	}
	assets, err := store.ListVisibleAPTSnapshotAssets(ctx, repo.ID, "stable")
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		for _, method := range []string{"GET", "HEAD"} {
			r := httptest.NewRequest(method, "/apt/"+repo.Name+"/"+asset.Path, nil)
			authorize(r, "resolver-secret")
			r.Header.Set("If-None-Match", `"`+strings.TrimPrefix(asset.Digest, "sha256:")+`"`)
			r.Header.Set("Range", "bytes=0-1")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 403 {
				t.Fatalf("quarantine %s %s: %d %s", method, asset.Path, w.Code, w.Body.String())
			}
		}
	}
	requireQuarantineReadDeniedAudit(t, store, repository.FormatAPT)
	// Scanning a quarantined package remains possible for release investigations.
	if _, err = NewNativeArtifactScanResolver(store, objects).ResolveArtifactScan(ctx, repo.ID, repository.ArtifactScanPayload{Format: repository.FormatAPT, Coordinate: coordinate, Digest: digest}); err != nil {
		t.Fatal(err)
	}
	if w := call("PUT", qurl, "admin-secret", `{"state":"released","reason":"review completed"}`, "0"); w.Code != 412 {
		t.Fatalf("stale release: %d", w.Code)
	}
	if w := call("PUT", qurl, "admin-secret", `{"state":"released","reason":"review completed"}`, "1"); w.Code != 200 {
		t.Fatalf("release: %d %s", w.Code, w.Body.String())
	}
	after := call("GET", "/apt/"+repo.Name+"/dists/stable/InRelease", "resolver-secret", "", "")
	if after.Code != 200 || !bytes.Equal(before.Body.Bytes(), after.Body.Bytes()) {
		t.Fatal("release changed signed bytes")
	}
	audits, err := store.ListAudits(ctx, repository.AuditQuery{Repository: repo.Name, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	operations := map[string]bool{}
	for _, a := range audits {
		if a.Actor == "alice" {
			operations[a.Operation] = true
		}
	}
	for _, op := range []string{"artifact.quarantine", "artifact.release", "artifact.scan.auto_enqueue"} {
		if !operations[op] {
			t.Errorf("missing %s audit: %+v", op, operations)
		}
	}
}

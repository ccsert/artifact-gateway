package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/scanning"
	"github.com/google/uuid"
)

func TestCargoHostedAndGroupQuarantineReadPolicy(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "cargo-quarantine", Name: "cargo-quarantine", Format: repository.FormatCargo})
	if err != nil {
		t.Fatal(err)
	}
	group := createV2Group(t, store, "cargo-quarantine-group", repository.FormatCargo, repository.GroupMember{RepositoryID: repo.ID})
	handler := NewGatewayHandler(Dependencies{NativeCargoObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	request := func(method, path string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Host = "localhost:8080"
		authorize(r, "admin-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, version := range []string{"1.0.0", "2.0.0"} {
		body, _ := cargoC0PublishFixture(t, "demo", version, "demo")
		if w := request(http.MethodPut, "/cargo/"+repo.Name+"/api/v1/crates/new", body); w.Code != http.StatusOK {
			t.Fatalf("publish %s=%d %s", version, w.Code, w.Body.String())
		}
	}
	blocked, err := store.GetCargoPublication(ctx, repo.ID, "demo", "2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{repo.Name, group.Name} {
		if w := request(http.MethodGet, "/cargo/"+name+"/de/mo/demo", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"vers":"2.0.0"`) {
			t.Fatalf("baseline %s index=%d %s", name, w.Code, w.Body.String())
		}
	}
	quarantineReadIdentity(t, store, repo, "demo@2.0.0", blocked.Digest)
	if w := request(http.MethodGet, "/cargo/"+repo.Name+"/api/v1/crates/demo/2.0.0/download", nil); w.Code != http.StatusOK {
		t.Fatalf("default policy changed Cargo read=%d %s", w.Code, w.Body.String())
	}
	enableQuarantineReadPolicy(t, store, repo.ID)
	for _, name := range []string{repo.Name, group.Name} {
		base := "/cargo/" + name
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			w := request(method, base+"/de/mo/demo", nil)
			if w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"vers":"2.0.0"`) || !strings.Contains(w.Body.String(), `"vers":"1.0.0"`) && method == http.MethodGet {
				t.Fatalf("%s %s index=%d %s", name, method, w.Code, w.Body.String())
			}
			w = request(method, base+"/api/v1/crates/demo/2.0.0/download", nil)
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s %s quarantined download=%d %s", name, method, w.Code, w.Body.String())
			}
		}
		w := request(http.MethodGet, base+"/api/v1/crates?q=demo", nil)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"max_version":"1.0.0"`) || strings.Contains(w.Body.String(), `"max_version":"2.0.0"`) {
			t.Fatalf("%s search=%d %s", name, w.Code, w.Body.String())
		}
	}
	quarantine, err := store.GetArtifactQuarantine(ctx, repo.ID, repository.FormatCargo, "demo@2.0.0", blocked.Digest)
	if err != nil {
		t.Fatal(err)
	}
	quarantine.State, quarantine.Reason, quarantine.UpdatedBy = repository.ArtifactQuarantineStateReleased, "reviewed", "admin"
	if _, err = store.ReplaceArtifactQuarantine(ctx, quarantine, quarantine.Version); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{repo.Name, group.Name} {
		if w := request(http.MethodGet, "/cargo/"+name+"/api/v1/crates/demo/2.0.0/download", nil); w.Code != http.StatusOK {
			t.Fatalf("%s released download=%d %s", name, w.Code, w.Body.String())
		}
	}
}

func TestCargoPublicationScanResolvesExactCrate(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cargo-scan", Format: repository.FormatCargo})
	if err != nil {
		t.Fatal(err)
	}
	enablePublicationScan(t, store, repo.ID)
	deps := publicationScanDependencies(Dependencies{NativeCargoObjectStore: objects}, repository.FormatCargo)
	handler := NewGatewayHandler(deps, store, TestAdapter{}, testAuthenticator())
	body, archive := cargoC0PublishFixture(t, "demo", "1.0.0", "demo")
	r := httptest.NewRequest(http.MethodPut, "/cargo/"+repo.Name+"/api/v1/crates/new", bytes.NewReader(body))
	r.Host = "localhost:8080"
	authorize(r, "admin-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("publish=%d %s", w.Code, w.Body.String())
	}
	publication, err := store.GetCargoPublication(ctx, repo.ID, "demo", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	requirePublicationScan(t, store, repo.ID, repository.FormatCargo, "demo@1.0.0", publication.Digest)
	manual := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/artifact-scans",
		strings.NewReader(`{"coordinate":"demo@1.0.0","digest":"`+publication.Digest+`"}`))
	authorize(manual, "admin-secret")
	manual.Header.Set("Idempotency-Key", "cargo-manual-scan")
	manualResponse := httptest.NewRecorder()
	handler.ServeHTTP(manualResponse, manual)
	if manualResponse.Code != http.StatusAccepted {
		t.Fatalf("manual Cargo scan=%d %s", manualResponse.Code, manualResponse.Body.String())
	}
	quarantinePath := "/api/v2/repositories/" + repo.ID + "/artifact-quarantine?coordinate=" + url.QueryEscape("demo@1.0.0") + "&digest=" + url.QueryEscape(publication.Digest)
	govern := httptest.NewRequest(http.MethodPut, quarantinePath, strings.NewReader(`{"state":"quarantined","reason":"scan review"}`))
	authorize(govern, "admin-secret")
	govern.Header.Set("If-Match", "0")
	governResponse := httptest.NewRecorder()
	handler.ServeHTTP(governResponse, govern)
	if governResponse.Code != http.StatusOK {
		t.Fatalf("Cargo quarantine API=%d %s", governResponse.Code, governResponse.Body.String())
	}
	policy := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/quarantine-read-policy", strings.NewReader(`{"version":"1","enabled":true}`))
	authorize(policy, "admin-secret")
	policy.Header.Set("If-Match", "1")
	policyResponse := httptest.NewRecorder()
	handler.ServeHTTP(policyResponse, policy)
	if policyResponse.Code != http.StatusOK {
		t.Fatalf("Cargo read policy API=%d %s", policyResponse.Code, policyResponse.Body.String())
	}
	identities, err := store.ListArtifactIdentities(ctx, repo.ID, repository.FormatCargo, repository.ArtifactIdentityScan, "", 10)
	if err != nil || len(identities) != 1 || identities[0].Coordinate != "demo@1.0.0" || identities[0].Digest != publication.Digest {
		t.Fatalf("identities=%+v err=%v", identities, err)
	}
	candidates, err := store.ListArtifactScanCandidates(ctx, repo.ID, repository.FormatCargo, 10)
	if err != nil || len(candidates) != 1 || candidates[0].Digest != publication.Digest {
		t.Fatalf("scan candidates=%+v err=%v", candidates, err)
	}
	worker := ArtifactScanWorker{Store: store, Resolver: NewNativeArtifactScanResolver(store, objects), WorkerFormats: []repository.Format{repository.FormatCargo},
		Scanner: scanning.ScannerFunc(func(_ context.Context, artifact scanning.Artifact) (scanning.Report, error) {
			if artifact.Coordinate != "demo@1.0.0" || artifact.Digest != publication.Digest || len(artifact.Assets) != 1 {
				t.Fatalf("scan identity=%+v", artifact)
			}
			reader, err := artifact.Assets[0].Open(ctx)
			if err != nil {
				return scanning.Report{}, err
			}
			defer func() { _ = reader.Close() }()
			got, err := io.ReadAll(reader)
			if err != nil {
				return scanning.Report{}, err
			}
			if !bytes.Equal(got, archive) {
				t.Fatal("scanner received a different crate")
			}
			return scanning.Report{Vulnerability: &repository.ArtifactVulnerabilitySummary{Scanner: "test", Status: "clean"}}, nil
		}),
	}
	if err := worker.RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	intelligence, err := store.GetArtifactIntelligence(ctx, repo.ID, repository.FormatCargo, "demo@1.0.0", publication.Digest)
	if err != nil || intelligence.Vulnerability == nil || intelligence.Vulnerability.Scanner != "test" {
		t.Fatalf("scan result=%+v err=%v", intelligence, err)
	}
	var payload repository.ArtifactScanPayload
	jobs, _ := store.ListLifecycleJobs(ctx, repo.ID, 10)
	if err := json.Unmarshal(jobs[0].Payload, &payload); err != nil || payload.Digest != publication.Digest {
		t.Fatalf("job payload=%+v err=%v", payload, err)
	}
}

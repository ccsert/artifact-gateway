package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/aptpublication"
	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// Only the route contract uses this stub. Importer tests and native APT E2E
// exercise actual OpenPGP verification with independent public keys.
type aptArchiveRouteTrust struct{ err error }

func (v aptArchiveRouteTrust) Verify(context.Context, aptpublication.SnapshotArchiveManifest, []byte, []byte, []byte) error {
	return v.err
}

func TestAPTArchiveRestoreRouteAuthorizationTrustReceiptAndAtomicReplay(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	source, sourceObjects, repo, snapshot := aptArchiveAPIFixture(t)
	var archive bytes.Buffer
	if _, err := (aptpublication.SnapshotArchiveExporter{Store: source, Objects: sourceObjects}).Export(ctx, snapshot.ID, &archive); err != nil {
		t.Fatal(err)
	}
	receipt := fmt.Sprintf("sha256:%x", sha256.Sum256(archive.Bytes()))
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}}, {Principal: "writer", Scopes: []string{"repositories:write"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "other-restore", Format: repository.FormatAPT, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	objects := objectstore.NewMemoryStore()
	auth := testAuthenticator()
	loader := openapi3.NewLoader()
	spec, err := loader.LoadFromFile(filepath.Join("..", "..", "api", "openapi", "management-runtime-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	router, err := legacy.NewRouter(spec)
	if err != nil {
		t.Fatal(err)
	}

	request := func(trust aptpublication.SnapshotArchiveTrustVerifier, id, token, digest, media string, length int64) *httptest.ResponseRecorder {
		handler := NewGatewayHandler(Dependencies{NativeAPTObjectStore: objects, APTArchiveTrust: trust}, store, TestAdapter{}, auth)
		req := httptest.NewRequest(http.MethodPost, "https://gateway.example.com/api/v2/repositories/"+id+"/apt/snapshots/restore", bytes.NewReader(archive.Bytes()))
		if length > 0 {
			req.ContentLength = length
		}
		if token != "" {
			authorize(req, token)
		}
		req.Header.Set("Content-Type", media)
		if digest != "" {
			req.Header.Set("X-Artifact-Archive-Digest", digest)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		route, params, e := router.FindRoute(req)
		if e != nil {
			t.Fatal(e)
		}
		input := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route}
		if e = openapi3filter.ValidateResponse(ctx, (&openapi3filter.ResponseValidationInput{RequestValidationInput: input, Status: response.Code, Header: response.Header(), Options: &openapi3filter.Options{IncludeResponseStatus: true}}).SetBodyBytes(response.Body.Bytes())); e != nil {
			t.Fatalf("restore response violates OpenAPI: %v", e)
		}
		return response
	}
	for _, tc := range []struct {
		name, id, token, digest, media string
		trust                          aptpublication.SnapshotArchiveTrustVerifier
		length                         int64
		status                         int
	}{
		{name: "anonymous", status: 401},
		{name: "reader", token: auth.IssueToken("reader"), status: 403},
		{name: "writer", token: auth.IssueToken("writer"), status: 403},
		{name: "disabled", token: "admin-secret", status: 409},
		{name: "missing receipt", token: "admin-secret", trust: aptArchiveRouteTrust{}, media: aptSnapshotArchiveContentType, status: 400},
		{name: "wrong receipt", token: "admin-secret", trust: aptArchiveRouteTrust{}, digest: "sha256:" + strings.Repeat("0", 64), media: aptSnapshotArchiveContentType, status: 412},
		{name: "untrusted", token: "admin-secret", trust: aptArchiveRouteTrust{aptpublication.ErrSnapshotArchiveUntrusted}, digest: receipt, media: aptSnapshotArchiveContentType, status: 422},
		{name: "media", token: "admin-secret", trust: aptArchiveRouteTrust{}, digest: receipt, media: "application/octet-stream", status: 415},
		{name: "oversized", token: "admin-secret", trust: aptArchiveRouteTrust{}, digest: receipt, media: aptSnapshotArchiveContentType, length: aptpublication.MaxSnapshotArchiveImportBytes + 1, status: 413},
		{name: "other repository", id: other.ID, token: "admin-secret", trust: aptArchiveRouteTrust{}, digest: receipt, media: aptSnapshotArchiveContentType, status: 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := tc.id
			if id == "" {
				id = repo.ID
			}
			digest := tc.digest
			if digest == "" && tc.name != "missing receipt" {
				digest = receipt
			}
			response := request(tc.trust, id, tc.token, digest, tc.media, tc.length)
			if response.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, tc.status, response.Body)
			}
		})
	}
	if capacity, err := store.GetRepositoryCapacity(ctx, repo.ID); err != nil || capacity.UsedBytes != 0 {
		t.Fatalf("rejected request changed metadata: %#v %v", capacity, err)
	}
	for range 2 {
		response := request(aptArchiveRouteTrust{}, repo.ID, "admin-secret", receipt, aptSnapshotArchiveContentType, 0)
		if response.Code != 200 || !strings.Contains(response.Body.String(), snapshot.ID) {
			t.Fatalf("restore response=%d %s", response.Code, response.Body)
		}
	}
	var exported bytes.Buffer
	if _, err = (aptpublication.SnapshotArchiveExporter{Store: store, Objects: objects}).Export(ctx, snapshot.ID, &exported); err != nil || !bytes.Equal(exported.Bytes(), archive.Bytes()) {
		t.Fatalf("roundtrip archive mismatch: %v", err)
	}
	audits, err := store.ListAudits(ctx, repository.AuditQuery{Repository: repo.Name, Operation: "apt.repository_snapshot.restore", Limit: 10})
	if err != nil || len(audits) != 2 || audits[0].Evidence["archiveDigest"] != receipt {
		t.Fatalf("restore audit=%#v %v", audits, err)
	}
}

// The global JSON guard must leave large binary restore bodies to the restore limit.
func TestAPTArchiveRestoreBodyBypassesJSONLimit(t *testing.T) {
	t.Parallel()
	store, objects, repo, _ := aptArchiveAPIFixture(t)
	body := bytes.Repeat([]byte("x"), (1<<20)+1)
	receipt := fmt.Sprintf("sha256:%x", sha256.Sum256(body))
	handler := NewGatewayHandler(Dependencies{NativeAPTObjectStore: objects, APTArchiveTrust: aptArchiveRouteTrust{}}, store, TestAdapter{}, testAuthenticator())
	req := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/apt/snapshots/restore", bytes.NewReader(body))
	authorize(req, "admin-secret")
	req.Header.Set("Content-Type", aptSnapshotArchiveContentType)
	req.Header.Set("X-Artifact-Archive-Digest", receipt)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != 400 || !strings.Contains(response.Body.String(), "snapshot_corrupt") {
		t.Fatalf("binary request hit JSON body limit: %d %s", response.Code, response.Body)
	}
}

package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestArtifactQuarantineManagementHTTP(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "quarantine-raw", Format: repository.FormatRaw,
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	coordinate := "releases/widget.bin"
	if _, err = store.PutRawAsset(ctx, repository.RawAsset{
		RepositoryID: repo.ID, Path: coordinate, Digest: digest,
		ObjectKey: "raw/quarantine/widget", Size: 7,
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	path := "/api/v2/repositories/" + repo.ID + "/artifact-quarantine?coordinate=" + coordinate + "&digest=" + digest
	requestAs := func(method, body, ifMatch, token string) *httptest.ResponseRecorder {
		httpRequest := httptest.NewRequest(method, path, strings.NewReader(body))
		authorize(httpRequest, token)
		if ifMatch != "" {
			httpRequest.Header.Set("If-Match", ifMatch)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httpRequest)
		return response
	}
	request := func(method, body, ifMatch string) *httptest.ResponseRecorder {
		return requestAs(method, body, ifMatch, "admin-secret")
	}

	if missing := request(http.MethodGet, "", ""); missing.Code != http.StatusNotFound {
		t.Fatalf("missing=%d body=%s", missing.Code, missing.Body.String())
	}
	if noVersion := request(http.MethodPut, `{"state":"quarantined","reason":"missing version"}`, ""); noVersion.Code != http.StatusBadRequest {
		t.Fatalf("missing If-Match=%d body=%s", noVersion.Code, noVersion.Body.String())
	}
	if denied := requestAs(http.MethodPut, `{"state":"quarantined","reason":"reader cannot govern"}`, "0", "resolver-secret"); denied.Code != http.StatusForbidden {
		t.Fatalf("reader mutation=%d body=%s", denied.Code, denied.Body.String())
	}
	created := request(http.MethodPut, `{"state":"quarantined","reason":"critical vulnerability under investigation"}`, "0")
	if created.Code != http.StatusOK || created.Header().Get("ETag") != "1" || !strings.Contains(created.Body.String(), `"state":"quarantined"`) || !strings.Contains(created.Body.String(), `"updatedBy":"alice"`) {
		t.Fatalf("created=%d etag=%q body=%s", created.Code, created.Header().Get("ETag"), created.Body.String())
	}
	read := request(http.MethodGet, "", "")
	if read.Code != http.StatusOK || read.Header().Get("ETag") != "1" || !strings.Contains(read.Body.String(), "critical vulnerability under investigation") {
		t.Fatalf("read=%d etag=%q body=%s", read.Code, read.Header().Get("ETag"), read.Body.String())
	}
	if reader := requestAs(http.MethodGet, "", "", "resolver-secret"); reader.Code != http.StatusOK {
		t.Fatalf("reader get=%d body=%s", reader.Code, reader.Body.String())
	}
	if duplicate := request(http.MethodPut, `{"state":"quarantined","reason":"duplicate transition"}`, "1"); duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate=%d body=%s", duplicate.Code, duplicate.Body.String())
	}
	if stale := request(http.MethodPut, `{"state":"released","reason":"finding accepted after review"}`, "0"); stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale=%d body=%s", stale.Code, stale.Body.String())
	}
	released := request(http.MethodPut, `{"state":"released","reason":"finding accepted after review"}`, "1")
	if released.Code != http.StatusOK || released.Header().Get("ETag") != "2" || !strings.Contains(released.Body.String(), `"state":"released"`) {
		t.Fatalf("released=%d etag=%q body=%s", released.Code, released.Header().Get("ETag"), released.Body.String())
	}

	audits, err := store.ListAudits(ctx, repository.AuditQuery{Repository: repo.Name})
	if err != nil {
		t.Fatal(err)
	}
	operations := map[string]repository.AuditRecord{}
	for _, audit := range audits {
		operations[audit.Operation] = audit
	}
	quarantineAudit, quarantined := operations["artifact.quarantine"]
	releaseAudit, releasedAudit := operations["artifact.release"]
	if !quarantined || !releasedAudit {
		t.Fatalf("audit operations=%#v", operations)
	}
	if quarantineAudit.Actor != "alice" || quarantineAudit.Resource != coordinate || quarantineAudit.Representation != digest || quarantineAudit.AuthorizationReason != "critical vulnerability under investigation" {
		t.Fatalf("quarantine audit=%#v", quarantineAudit)
	}
	if releaseAudit.Actor != "alice" || releaseAudit.Resource != coordinate || releaseAudit.Representation != digest || releaseAudit.AuthorizationReason != "finding accepted after review" {
		t.Fatalf("release audit=%#v", releaseAudit)
	}
}

func TestArtifactQuarantineConanUsesRecipeRevisionAsTheOnlyAnchor(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "quarantine-conan", Format: repository.FormatConan,
	})
	if err != nil {
		t.Fatal(err)
	}
	reference, recipeRevision := "widget/1.2.3/team/stable", "rrev"
	recipeDigest := "sha256:" + strings.Repeat("a", 64)
	packageID, packageRevision := "package-id", "prev"
	packageDigest := "sha256:" + strings.Repeat("b", 64)
	if _, err = store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{
		RepositoryID: repo.ID, Reference: reference, Revision: recipeRevision, Digest: recipeDigest,
	}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutConanPackageRevision(ctx, repository.ConanPackageRevision{
		RepositoryID: repo.ID, Reference: reference, RecipeRevision: recipeRevision,
		PackageID: packageID, Revision: packageRevision, Digest: packageDigest,
	}, nil); err != nil {
		t.Fatal(err)
	}
	visiblePackage, err := store.GetConanPackageRevision(ctx, repo.ID, reference, recipeRevision, packageID, packageRevision)
	if err != nil || visiblePackage.State != "visible" || visiblePackage.Digest != packageDigest {
		t.Fatalf("visible package revision=%#v err=%v", visiblePackage, err)
	}

	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	put := func(coordinate, digest, ifMatch, state, reason string) *httptest.ResponseRecorder {
		query := url.Values{"coordinate": {coordinate}, "digest": {digest}}
		request := httptest.NewRequest(
			http.MethodPut,
			"/api/v2/repositories/"+repo.ID+"/artifact-quarantine?"+query.Encode(),
			strings.NewReader(`{"state":"`+state+`","reason":"`+reason+`"}`),
		)
		authorize(request, "admin-secret")
		request.Header.Set("If-Match", ifMatch)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	recipeCoordinate := reference + "#" + recipeRevision
	created := put(recipeCoordinate, recipeDigest, "0", "quarantined", "recipe revision under investigation")
	if created.Code != http.StatusOK || created.Header().Get("ETag") != "1" || !strings.Contains(created.Body.String(), `"state":"quarantined"`) {
		t.Fatalf("create recipe quarantine=%d etag=%q body=%s", created.Code, created.Header().Get("ETag"), created.Body.String())
	}
	changed := put(recipeCoordinate, recipeDigest, "1", "released", "recipe revision cleared after review")
	if changed.Code != http.StatusOK || changed.Header().Get("ETag") != "2" || !strings.Contains(changed.Body.String(), `"state":"released"`) {
		t.Fatalf("change recipe quarantine=%d etag=%q body=%s", changed.Code, changed.Header().Get("ETag"), changed.Body.String())
	}

	packageCoordinate := reference + "#" + recipeRevision + "/" + packageID + "#" + packageRevision
	rejected := put(packageCoordinate, packageDigest, "0", "quarantined", "package revision finding")
	if rejected.Code != http.StatusBadRequest || !strings.Contains(rejected.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("package quarantine=%d body=%s", rejected.Code, rejected.Body.String())
	}
	packageQuery := url.Values{"coordinate": {packageCoordinate}, "digest": {packageDigest}}
	getRequest := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/artifact-quarantine?"+packageQuery.Encode(), nil)
	authorize(getRequest, "admin-secret")
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusBadRequest || !strings.Contains(getResponse.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("get package quarantine=%d body=%s", getResponse.Code, getResponse.Body.String())
	}
	if value, getErr := store.GetArtifactQuarantine(ctx, repo.ID, repository.FormatConan, packageCoordinate, packageDigest); !errors.Is(getErr, repository.ErrNotFound) {
		t.Fatalf("rejected package quarantine persisted=%#v err=%v", value, getErr)
	}

	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "quarantine-conan-target", Format: repository.FormatConan,
	})
	if err != nil {
		t.Fatal(err)
	}
	requarantined := put(recipeCoordinate, recipeDigest, "2", "quarantined", "recipe revision blocked again")
	if requarantined.Code != http.StatusOK {
		t.Fatalf("requarantine recipe=%d body=%s", requarantined.Code, requarantined.Body.String())
	}
	promotionRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v2/repositories/"+repo.ID+"/promotions",
		strings.NewReader(`{"targetRepositoryId":"`+target.ID+`","coordinate":"`+packageCoordinate+`","digest":"`+packageDigest+`"}`),
	)
	authorize(promotionRequest, "admin-secret")
	promotionRequest.Header.Set("Idempotency-Key", "conan-package-quarantine-bypass")
	promotionResponse := httptest.NewRecorder()
	handler.ServeHTTP(promotionResponse, promotionRequest)
	if promotionResponse.Code != http.StatusBadRequest || !strings.Contains(promotionResponse.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("package promotion=%d body=%s", promotionResponse.Code, promotionResponse.Body.String())
	}
	jobs, err := store.ListLifecycleJobs(ctx, target.ID, 10)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("package promotion jobs=%#v err=%v", jobs, err)
	}
}

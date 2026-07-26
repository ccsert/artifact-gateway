package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	conanprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/conan"
	mavenprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/maven"
	rawprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/raw"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestRepositoryPromotionHTTPAuthorizesBothRepositoriesAndPublishesTarget(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "promotion-source", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "promotion-target", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	body := []byte("promoted jar")
	digest := sha256.Sum256(body)
	digestText := "sha256:" + fmt.Sprintf("%x", digest)
	objectKey := "native/maven/promotion-widget"
	if err = objects.Put(ctx, objectKey, body); err != nil {
		t.Fatal(err)
	}
	session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: source.ID, Coordinate: "org.example:promotion-widget:1.0.0", Publisher: "test", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "promotion-widget-1.0.0.jar", Digest: digestText, Size: int64(len(body))}}}
	if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkMavenPublishObject(ctx, session.ID, session.Objects[0].Name, objectKey); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CommitMavenPublishSession(ctx, session.ID, []repository.MavenAsset{{RepositoryID: source.ID, Path: "org/example/promotion-widget/1.0.0/promotion-widget-1.0.0.jar", ObjectKey: objectKey, Digest: digestText, Size: int64(len(body))}}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, source.ID, []repository.RepositoryGrant{{Principal: "promoter", Scopes: []string{"repositories:admin"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, target.ID, nil, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{NativeMavenObjectStore: objects}, store, TestAdapter{}, authenticator)
	promote := func(targetID, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+source.ID+"/promotions", strings.NewReader(`{"targetRepositoryId":"`+targetID+`","coordinate":"`+session.Coordinate+`","digest":"`+digestText+`"}`))
		authorize(req, authenticator.IssueToken("promoter"))
		req.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	if denied := promote(target.ID, "promote-widget"); denied.Code != http.StatusForbidden {
		t.Fatalf("target authorization=%d %s", denied.Code, denied.Body.String())
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, target.ID, []repository.RepositoryGrant{{Principal: "promoter", Scopes: []string{"repositories:admin"}}, {Principal: "maven", Scopes: []string{"repositories:read"}}}, "2"); err != nil {
		t.Fatal(err)
	}
	first := promote(target.ID, "promote-widget")
	if first.Code != http.StatusAccepted || !strings.Contains(first.Body.String(), `"kind":"promotion"`) {
		t.Fatalf("enqueue=%d %s", first.Code, first.Body.String())
	}
	if replay := promote(target.ID, "promote-widget"); replay.Code != http.StatusAccepted || replay.Body.String() != first.Body.String() {
		t.Fatalf("idempotent replay=%d %s", replay.Code, replay.Body.String())
	}
	if err = (mavenprotocol.NativePromotion{Store: store}).RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	read := httptest.NewRequest(http.MethodGet, "/repository/maven/"+target.Name+"/org/example/promotion-widget/1.0.0/promotion-widget-1.0.0.jar", nil)
	read.SetBasicAuth("maven", "resolver-secret")
	resolved := httptest.NewRecorder()
	handler.ServeHTTP(resolved, read)
	if resolved.Code != http.StatusOK || string(resolved.Body.Bytes()) != string(body) {
		t.Fatalf("promoted HTTP read=%d %q", resolved.Code, resolved.Body.String())
	}
	foundPromotionAudit := false
	for _, audit := range store.Audits {
		foundPromotionAudit = foundPromotionAudit || (audit.Operation == "promote" && audit.Repository == source.Name && audit.Status == http.StatusAccepted)
	}
	if !foundPromotionAudit {
		t.Fatalf("promotion audit missing: %#v", store.Audits)
	}
}

func TestRepositoryOCIPromotionHTTPEnqueuesIdempotentJob(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "oci-promotion-source", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "oci-promotion-target", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, source.ID, []repository.RepositoryGrant{{Principal: "promoter", Scopes: []string{"repositories:admin"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, target.ID, []repository.RepositoryGrant{{Principal: "promoter", Scopes: []string{"repositories:admin"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+source.ID+"/promotions", strings.NewReader(`{"targetRepositoryId":"`+target.ID+`","coordinate":"team/widget","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
		authorize(r, authenticator.IssueToken("promoter"))
		r.Header.Set("Idempotency-Key", "oci-promotion")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	first, replay := request(), request()
	if first.Code != http.StatusAccepted || replay.Code != http.StatusAccepted || first.Body.String() != replay.Body.String() {
		t.Fatalf("promotion first=%d %s replay=%d %s", first.Code, first.Body.String(), replay.Code, replay.Body.String())
	}
	jobs, err := store.ListLifecycleJobs(ctx, target.ID, 10)
	if err != nil || len(jobs) != 1 || jobs[0].Kind != repository.LifecycleJobPromotion {
		t.Fatalf("jobs=%#v err=%v", jobs, err)
	}
}

func TestRepositoryRawReplicationHTTPAuthorizesPlansAndAudits(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "replication-raw-source", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "replication-raw-target", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("replicate this Raw object")
	sum := sha256.Sum256(body)
	digest := "sha256:" + fmt.Sprintf("%x", sum[:])
	asset := repository.RawAsset{RepositoryID: source.ID, Path: "releases/widget.txt", Digest: digest, ObjectKey: "native/raw/sha256/" + fmt.Sprintf("%x", sum[:]), Size: int64(len(body))}
	if _, err = store.PutRawAsset(ctx, asset); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, source.ID, []repository.RepositoryGrant{{Principal: "replicator", Scopes: []string{"repositories:admin"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, target.ID, nil, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := func(key, artifactDigest string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+source.ID+"/replications", strings.NewReader(`{"targetRepositoryId":"`+target.ID+`","coordinate":"releases/widget.txt","digest":"`+artifactDigest+`"}`))
		authorize(r, authenticator.IssueToken("replicator"))
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if denied := request("replicate-widget", digest); denied.Code != http.StatusForbidden {
		t.Fatalf("target authorization=%d %s", denied.Code, denied.Body.String())
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, target.ID, []repository.RepositoryGrant{{Principal: "replicator", Scopes: []string{"repositories:admin"}}}, "2"); err != nil {
		t.Fatal(err)
	}
	first := request("replicate-widget", digest)
	if first.Code != http.StatusAccepted || !strings.Contains(first.Body.String(), `"state":"pending"`) {
		t.Fatalf("create=%d %s", first.Code, first.Body.String())
	}
	if replay := request("replicate-widget", digest); replay.Code != http.StatusAccepted || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay=%d %s", replay.Code, replay.Body.String())
	}
	if conflict := request("replicate-widget", "sha256:"+strings.Repeat("a", 64)); conflict.Code != http.StatusNotFound {
		t.Fatalf("source digest validation=%d %s", conflict.Code, conflict.Body.String())
	}
	list := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+target.ID+"/replications", nil)
	authorize(list, authenticator.IssueToken("replicator"))
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, list)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"sourceRepositoryId":"`+source.ID) {
		t.Fatalf("list=%d %s", listed.Code, listed.Body.String())
	}
	if plans, err := store.ListReplicationPlans(ctx, target.ID, 10); err != nil || len(plans) != 1 || plans[0].Format != repository.FormatRaw {
		t.Fatalf("plans=%#v err=%v", plans, err)
	}
	found := false
	for _, audit := range store.Audits {
		found = found || (audit.Operation == "replicate" && audit.Repository == source.Name && audit.Resource == asset.Path && audit.Status == http.StatusAccepted)
	}
	if !found {
		t.Fatalf("replication audit missing: %#v", store.Audits)
	}
}

func TestRepositoryRawPromotionHTTPPublishesTargetReference(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "promotion-raw-source", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "promotion-raw-target", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("promoted Raw content")
	sum := sha256.Sum256(body)
	digest := "sha256:" + fmt.Sprintf("%x", sum[:])
	objects := NewMemoryOCIObjectStore()
	asset := repository.RawAsset{RepositoryID: source.ID, Path: "releases/widget.txt", Digest: digest, ObjectKey: "native/raw/sha256/" + fmt.Sprintf("%x", sum[:]), Size: int64(len(body)), ContentType: "text/plain"}
	if err = objects.Put(ctx, asset.ObjectKey, body); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutRawAsset(ctx, asset); err != nil {
		t.Fatal(err)
	}
	grants := []repository.RepositoryGrant{{Principal: "promoter", Scopes: []string{"repositories:admin", "repositories:read"}}}
	if _, err = store.ReplaceRepositoryGrants(ctx, source.ID, grants, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, target.ID, grants, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: objects}, store, TestAdapter{}, authenticator)
	request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+source.ID+"/promotions", strings.NewReader(`{"targetRepositoryId":"`+target.ID+`","coordinate":"`+asset.Path+`","digest":"`+digest+`"}`))
	authorize(request, authenticator.IssueToken("promoter"))
	request.Header.Set("Idempotency-Key", "promote-raw-widget")
	accepted := httptest.NewRecorder()
	handler.ServeHTTP(accepted, request)
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("enqueue=%d %s", accepted.Code, accepted.Body.String())
	}
	if err = (rawprotocol.NativePromotion{Store: store}).RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	read := httptest.NewRequest(http.MethodGet, "/raw/"+target.Name+"/"+asset.Path, nil)
	read.Header.Set("Authorization", "Bearer "+authenticator.IssueToken("promoter"))
	resolved := httptest.NewRecorder()
	handler.ServeHTTP(resolved, read)
	if resolved.Code != http.StatusOK || string(resolved.Body.Bytes()) != string(body) {
		t.Fatalf("promoted Raw read=%d %q", resolved.Code, resolved.Body.String())
	}
	if targetAsset, err := store.GetRawAsset(ctx, target.ID, asset.Path); err != nil || targetAsset.Digest != digest || targetAsset.ObjectKey != asset.ObjectKey {
		t.Fatalf("target asset=%#v err=%v", targetAsset, err)
	}
}

func TestRepositoryConanPromotionHTTPPublishesTargetRevision(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "promotion-conan-source", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "promotion-conan-target", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("promoted Conan recipe")
	sum := sha256.Sum256(body)
	digest := "sha256:" + fmt.Sprintf("%x", sum[:])
	objects := NewMemoryOCIObjectStore()
	key := "native/conan/objects/" + fmt.Sprintf("%x", sum[:])
	if err = objects.Put(ctx, key, body); err != nil {
		t.Fatal(err)
	}
	if err = store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: source.ID, ObjectKey: key, Digest: digest, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	reference, revision := "pkg/1.0/user/stable", "rrev"
	if _, err = store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: source.ID, Reference: reference, Revision: revision, Digest: digest}, []repository.ConanAsset{{RepositoryID: source.ID, Reference: reference, RecipeRevision: revision, Path: "conanfile.py", ObjectKey: key, Digest: digest, Size: int64(len(body))}}); err != nil {
		t.Fatal(err)
	}
	grants := []repository.RepositoryGrant{{Principal: "promoter", Scopes: []string{"repositories:admin", "repositories:read"}}}
	if _, err = store.ReplaceRepositoryGrants(ctx, source.ID, grants, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, target.ID, grants, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{NativeConanObjectStore: objects}, store, TestAdapter{}, authenticator)
	request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+source.ID+"/promotions", strings.NewReader(`{"targetRepositoryId":"`+target.ID+`","coordinate":"`+reference+`#`+revision+`","digest":"`+digest+`"}`))
	authorize(request, authenticator.IssueToken("promoter"))
	request.Header.Set("Idempotency-Key", "promote-conan-widget")
	accepted := httptest.NewRecorder()
	handler.ServeHTTP(accepted, request)
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("enqueue=%d %s", accepted.Code, accepted.Body.String())
	}
	if err = (conanprotocol.NativePromotion{Store: store}).RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	read := httptest.NewRequest(http.MethodGet, "/conan/v2/"+target.Name+"/conans/"+reference+"/revisions/"+revision+"/files/conanfile.py", nil)
	read.Header.Set("Authorization", "Bearer "+authenticator.IssueToken("promoter"))
	resolved := httptest.NewRecorder()
	handler.ServeHTTP(resolved, read)
	if resolved.Code != http.StatusOK || string(resolved.Body.Bytes()) != string(body) {
		t.Fatalf("promoted Conan read=%d %q", resolved.Code, resolved.Body.String())
	}
	if targetRevision, err := store.GetConanRecipeRevision(ctx, target.ID, reference, revision); err != nil || targetRevision.Digest != digest || targetRevision.State != "visible" {
		t.Fatalf("target revision=%#v err=%v", targetRevision, err)
	}
}

func TestHostedRepositoryManagementLifecycle(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(`{"name":"releases","format":"maven"}`))
	authorize(request, "admin-secret")
	request.Header.Set("Idempotency-Key", "create-releases")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, request)
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", created.Code, created.Body.String())
	}
	if !strings.Contains(created.Body.String(), `"state":"active"`) {
		t.Fatalf("created=%s", created.Body.String())
	}
	id := strings.Split(strings.Split(created.Body.String(), `"id":"`)[1], `"`)[0]
	list := httptest.NewRequest(http.MethodGet, "/api/v2/repositories", nil)
	authorize(list, "admin-secret")
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, list)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), "releases") {
		t.Fatalf("list=%d body=%s", listed.Code, listed.Body.String())
	}
	disable := httptest.NewRequest(http.MethodDelete, "/api/v2/repositories/"+id, nil)
	authorize(disable, "admin-secret")
	disabled := httptest.NewRecorder()
	handler.ServeHTTP(disabled, disable)
	if disabled.Code != http.StatusAccepted || !strings.Contains(disabled.Body.String(), `"state":"pending"`) {
		t.Fatalf("disable=%d body=%s", disabled.Code, disabled.Body.String())
	}
}

func TestRepositoryCapabilitiesReportImplementedFormatOperations(t *testing.T) {
	store := repository.NewMemoryStore()
	conan, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "capabilities-conan", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	maven, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "capabilities-maven", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	if _, err = store.ReplaceRepositoryGrants(context.Background(), conan.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+conan.ID+"/capabilities", nil)
	authorize(request, authenticator.IssueToken("reader"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"format":"conan"`) || !strings.Contains(response.Body.String(), `"restore"`) || strings.Contains(response.Body.String(), `"retain"`) {
		t.Fatalf("Conan capabilities=%d %s", response.Code, response.Body.String())
	}
	adminRequest := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+maven.ID+"/capabilities", nil)
	authorize(adminRequest, "admin-secret")
	adminResponse := httptest.NewRecorder()
	handler.ServeHTTP(adminResponse, adminRequest)
	if adminResponse.Code != http.StatusOK || !strings.Contains(adminResponse.Body.String(), `"retain"`) || !strings.Contains(adminResponse.Body.String(), `"restore"`) {
		t.Fatalf("Maven capabilities=%d %s", adminResponse.Code, adminResponse.Body.String())
	}
}

func TestCrossFormatArtifactSearchUsesFormatProjectionsAndBoundPagination(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	formats := []repository.Format{repository.FormatOCI, repository.FormatMaven, repository.FormatRaw, repository.FormatConan}
	repositories := make(map[repository.Format]repository.HostedRepository, len(formats))
	for _, format := range formats {
		repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "search-" + string(format), Format: format})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "search-reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
			t.Fatal(err)
		}
		repositories[format] = repo
	}
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	oci := repositories[repository.FormatOCI]
	for _, name := range []string{"team/alpha", "team/beta"} {
		if _, err := store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: oci.ID, Name: name, Digest: digest, ObjectKey: "oci/" + name, MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 1}, "latest"); err != nil {
			t.Fatal(err)
		}
	}
	maven := repositories[repository.FormatMaven]
	for i, coordinate := range []string{"org.example:alpha:1.0", "org.example:beta:1.0"} {
		id := uuid.NewString()
		objectName := fmt.Sprintf("artifact-%d.pom", i)
		objectKey := "maven/" + id
		if _, err := store.CreateMavenPublishSession(ctx, repository.MavenPublishSession{ID: id, RepositoryID: maven.ID, Coordinate: coordinate, Publisher: "search", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: objectName, Digest: digest, Size: 1}}}); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkMavenPublishObject(ctx, id, objectName, objectKey); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CommitMavenPublishSession(ctx, id, []repository.MavenAsset{{RepositoryID: maven.ID, Path: objectName, ObjectKey: objectKey, Digest: digest, Size: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	raw := repositories[repository.FormatRaw]
	for i, path := range []string{"releases/alpha.bin", "releases/beta.bin"} {
		if _, err := store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: raw.ID, Path: path, Digest: digest, ObjectKey: fmt.Sprintf("raw/%d", i), Size: int64(1<<40) + int64(i), ContentType: "application/octet-stream"}); err != nil {
			t.Fatal(err)
		}
	}
	conan := repositories[repository.FormatConan]
	for i, reference := range []string{"pkg/1.0/user/stable", "pkg/2.0/user/stable"} {
		objectKey := fmt.Sprintf("conan/%d", i)
		if err := store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: conan.ID, ObjectKey: objectKey, Digest: digest, Size: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: conan.ID, Reference: reference, Revision: fmt.Sprintf("rrev-%d", i), Digest: digest}, []repository.ConanAsset{{RepositoryID: conan.ID, Reference: reference, RecipeRevision: fmt.Sprintf("rrev-%d", i), Path: "conanfile.py", ObjectKey: objectKey, Digest: digest, Size: 1}}); err != nil {
			t.Fatal(err)
		}
	}

	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := func(repo repository.HostedRepository, q string, token string) *httptest.ResponseRecorder {
		t.Helper()
		values := url.Values{"q": {q}, "pageSize": {"1"}}
		if token != "" {
			values.Set("pageToken", token)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/artifact-search?"+values.Encode(), nil)
		authorize(req, authenticator.IssueToken("search-reader"))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	tests := []struct {
		format repository.Format
		query  string
		want   string
	}{
		{repository.FormatOCI, "team/", "team/alpha"},
		{repository.FormatMaven, "org.example:", "org.example:alpha:1.0"},
		{repository.FormatRaw, "releases/", "releases/alpha.bin"},
		{repository.FormatConan, "pkg/", "pkg/1.0/user/stable"},
	}
	var rawPage struct {
		Items []struct {
			Coordinate  string `json:"coordinate"`
			Size        int64  `json:"size"`
			ContentType string `json:"contentType"`
		} `json:"items"`
		NextPageToken string `json:"nextPageToken"`
	}
	for _, test := range tests {
		response := request(repositories[test.format], test.query, "")
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), test.want) {
			t.Fatalf("%s search=%d body=%s", test.format, response.Code, response.Body.String())
		}
		if test.format == repository.FormatRaw {
			if err := json.NewDecoder(response.Body).Decode(&rawPage); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(rawPage.Items) != 1 || rawPage.Items[0].Size != 1<<40 || rawPage.Items[0].ContentType != "application/octet-stream" || rawPage.NextPageToken == "" {
		t.Fatalf("raw page=%#v", rawPage)
	}
	next := request(raw, "releases/", rawPage.NextPageToken)
	if next.Code != http.StatusOK || !strings.Contains(next.Body.String(), "releases/beta.bin") {
		t.Fatalf("raw next=%d body=%s", next.Code, next.Body.String())
	}
	if changedQuery := request(raw, "other/", rawPage.NextPageToken); changedQuery.Code != http.StatusBadRequest || !strings.Contains(changedQuery.Body.String(), "invalid_page_token") {
		t.Fatalf("changed query=%d body=%s", changedQuery.Code, changedQuery.Body.String())
	}
	if wrongRepository := request(maven, "org.example:", rawPage.NextPageToken); wrongRepository.Code != http.StatusBadRequest || !strings.Contains(wrongRepository.Body.String(), "invalid_page_token") {
		t.Fatalf("wrong repository=%d body=%s", wrongRepository.Code, wrongRepository.Body.String())
	}
	denied := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+raw.ID+"/artifact-search", nil)
	authorize(denied, authenticator.IssueToken("ungranted"))
	deniedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deniedResponse, denied)
	if deniedResponse.Code != http.StatusForbidden {
		t.Fatalf("denied=%d body=%s", deniedResponse.Code, deniedResponse.Body.String())
	}
}

func TestV2AuditAPIExposesOptionalGrantDecisionFields(t *testing.T) {
	store := repository.NewMemoryStore()
	store.Audits = []repository.AuditRecord{{
		GroupName: "releases", Repository: "releases", Actor: "reader", Outcome: repository.AuditAccessDenied,
		OccurredAt: time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC), Format: "maven", Operation: "get", Status: http.StatusForbidden,
		AuthorizationSource: "repository_grants", AuthorizationReason: "scope_not_granted",
	}}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	request := httptest.NewRequest(http.MethodGet, "/api/v2/audits?group=releases&repository=releases&limit=1", nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var audits []struct {
		AuthorizationSource string    `json:"authorizationSource"`
		AuthorizationReason string    `json:"authorizationReason"`
		OccurredAt          time.Time `json:"occurredAt"`
	}
	if err := json.NewDecoder(response.Body).Decode(&audits); err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || audits[0].AuthorizationSource != "repository_grants" || audits[0].AuthorizationReason != "scope_not_granted" || audits[0].OccurredAt.IsZero() {
		t.Fatalf("audits=%#v", audits)
	}

	nonAdmin := httptest.NewRequest(http.MethodGet, "/api/v2/audits", nil)
	authorize(nonAdmin, "resolver-secret")
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, nonAdmin)
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("non-admin status=%d body=%s", denied.Code, denied.Body.String())
	}
}

func TestHostedGroupManagementLifecycle(t *testing.T) {
	store := repository.NewMemoryStore()
	first, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "group-first", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "group-second", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "group-other", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	create := httptest.NewRequest(http.MethodPost, "/api/v2/groups", strings.NewReader(`{"name":"maven-group","format":"maven","members":[{"repositoryId":"`+second.ID+`","position":1},{"repositoryId":"`+first.ID+`","position":0}]}`))
	authorize(create, "admin-secret")
	create.Header.Set("Idempotency-Key", "group-create")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", created.Code, created.Body.String())
	}
	var group repository.HostedGroup
	if err := json.NewDecoder(created.Body).Decode(&group); err != nil || group.Version != "1" || len(group.Members) != 2 || group.Members[0].RepositoryID != first.ID {
		t.Fatalf("group=%#v err=%v", group, err)
	}
	get := httptest.NewRequest(http.MethodGet, "/api/v2/groups/"+group.ID, nil)
	authorize(get, "admin-secret")
	got := httptest.NewRecorder()
	handler.ServeHTTP(got, get)
	if got.Code != http.StatusOK {
		t.Fatalf("get=%d body=%s", got.Code, got.Body.String())
	}
	replace := httptest.NewRequest(http.MethodPut, "/api/v2/groups/"+group.ID+"/members", strings.NewReader(`[{"repositoryId":"`+second.ID+`","position":0}]`))
	authorize(replace, "admin-secret")
	replace.Header.Set("If-Match", "1")
	replaced := httptest.NewRecorder()
	handler.ServeHTTP(replaced, replace)
	if replaced.Code != http.StatusOK || !strings.Contains(replaced.Body.String(), `"version":"2"`) {
		t.Fatalf("replace=%d body=%s", replaced.Code, replaced.Body.String())
	}
	stale := httptest.NewRequest(http.MethodPut, "/api/v2/groups/"+group.ID+"/members", strings.NewReader(`[{"repositoryId":"`+first.ID+`","position":0}]`))
	authorize(stale, "admin-secret")
	stale.Header.Set("If-Match", "1")
	staleResult := httptest.NewRecorder()
	handler.ServeHTTP(staleResult, stale)
	if staleResult.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale=%d body=%s", staleResult.Code, staleResult.Body.String())
	}
	mismatch := httptest.NewRequest(http.MethodPost, "/api/v2/groups", strings.NewReader(`{"name":"invalid-group","format":"maven","members":[{"repositoryId":"`+other.ID+`","position":0}]}`))
	authorize(mismatch, "admin-secret")
	mismatch.Header.Set("Idempotency-Key", "invalid-group")
	mismatchResult := httptest.NewRecorder()
	handler.ServeHTTP(mismatchResult, mismatch)
	if mismatchResult.Code != http.StatusBadRequest {
		t.Fatalf("mismatch=%d body=%s", mismatchResult.Code, mismatchResult.Body.String())
	}
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/v2/groups/"+group.ID, nil)
	authorize(deleteRequest, "admin-secret")
	deleted := httptest.NewRecorder()
	handler.ServeHTTP(deleted, deleteRequest)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete=%d body=%s", deleted.Code, deleted.Body.String())
	}
}

func TestRepositoryGrantManagementUsesETagVersioning(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "grant-target", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	list := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/grants", nil)
	authorize(list, "admin-secret")
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, list)
	if listed.Code != http.StatusOK || listed.Header().Get("ETag") != "1" || listed.Body.String() != "[]\n" {
		t.Fatalf("list=%d etag=%q body=%s", listed.Code, listed.Header().Get("ETag"), listed.Body.String())
	}
	replace := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/grants", strings.NewReader(`[{"principal":"build-agent","scopes":["repositories:read","repositories:write"]}]`))
	authorize(replace, "admin-secret")
	replace.Header.Set("If-Match", "1")
	replaced := httptest.NewRecorder()
	handler.ServeHTTP(replaced, replace)
	if replaced.Code != http.StatusOK || replaced.Header().Get("ETag") != "2" || !strings.Contains(replaced.Body.String(), "build-agent") {
		t.Fatalf("replace=%d etag=%q body=%s", replaced.Code, replaced.Header().Get("ETag"), replaced.Body.String())
	}
	stale := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/grants", strings.NewReader(`[]`))
	authorize(stale, "admin-secret")
	stale.Header.Set("If-Match", "1")
	staleResult := httptest.NewRecorder()
	handler.ServeHTTP(staleResult, stale)
	if staleResult.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale=%d body=%s", staleResult.Code, staleResult.Body.String())
	}
	invalid := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/grants", strings.NewReader(`[{"principal":"build-agent","scopes":["unknown"]}]`))
	authorize(invalid, "admin-secret")
	invalid.Header.Set("If-Match", "2")
	invalidResult := httptest.NewRecorder()
	handler.ServeHTTP(invalidResult, invalid)
	if invalidResult.Code != http.StatusBadRequest {
		t.Fatalf("invalid=%d body=%s", invalidResult.Code, invalidResult.Body.String())
	}
}

func TestRepositoryManagementUsesScopedGrants(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "scoped-target", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{
		{Principal: "reader", Scopes: []string{"repositories:read"}},
		{Principal: "writer", Scopes: []string{"repositories:write"}},
		{Principal: "manager", Scopes: []string{"repositories:admin"}},
	}, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := func(method, path, actor, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		authorize(r, authenticator.IssueToken(actor))
		if method == http.MethodPut {
			r.Header.Set("If-Match", "2")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if response := request(http.MethodGet, "/api/v2/repositories/"+repo.ID, "reader", ""); response.Code != http.StatusOK {
		t.Fatalf("reader get=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/retention-policy", "reader", ""); response.Code != http.StatusOK {
		t.Fatalf("reader policy=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/grants", "reader", `[]`); response.Code != http.StatusForbidden {
		t.Fatalf("reader grants=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/grants", "manager", ""); response.Code != http.StatusOK {
		t.Fatalf("manager grants=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodDelete, "/api/v2/repositories/"+repo.ID, "reader", ""); response.Code != http.StatusForbidden {
		t.Fatalf("reader delete=%d body=%s", response.Code, response.Body.String())
	}
	if len(store.Audits) == 0 {
		t.Fatal("expected authorization audit")
	}
	audit := store.Audits[len(store.Audits)-1]
	if audit.Outcome != repository.AuditAccessDenied || audit.AuthorizationSource != "repository_grants" || audit.AuthorizationReason != "scope_not_granted" || audit.Format != "management" {
		t.Fatalf("audit=%#v", audit)
	}
	metrics := httptest.NewRecorder()
	handler.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metrics.Body.String(), `artifact_gateway_repository_authorization_denials_total{format="management",authorization_source="repository_grants",authorization_reason="scope_not_granted"} 2`) {
		t.Fatalf("management authorization metric=%s", metrics.Body.String())
	}
	if response := request(http.MethodDelete, "/api/v2/repositories/"+repo.ID, "writer", ""); response.Code != http.StatusAccepted {
		t.Fatalf("writer delete=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRepositoryRetentionPolicyManagementUsesVersioning(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "retention-target", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	get := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/retention-policy", nil)
	authorize(get, "admin-secret")
	defaultPolicy := httptest.NewRecorder()
	handler.ServeHTTP(defaultPolicy, get)
	if defaultPolicy.Code != http.StatusOK || !strings.Contains(defaultPolicy.Body.String(), `"version":"1"`) || !strings.Contains(defaultPolicy.Body.String(), `"keepDays":30`) {
		t.Fatalf("default policy=%d body=%s", defaultPolicy.Code, defaultPolicy.Body.String())
	}
	replace := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/retention-policy", strings.NewReader(`{"version":"1","keepDays":14,"minimumVersions":3}`))
	authorize(replace, "admin-secret")
	replace.Header.Set("If-Match", "1")
	replaced := httptest.NewRecorder()
	handler.ServeHTTP(replaced, replace)
	if replaced.Code != http.StatusOK || !strings.Contains(replaced.Body.String(), `"version":"2"`) || !strings.Contains(replaced.Body.String(), `"minimumVersions":3`) {
		t.Fatalf("replace=%d body=%s", replaced.Code, replaced.Body.String())
	}
	stale := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/retention-policy", strings.NewReader(`{"version":"1","keepDays":7,"minimumVersions":1}`))
	authorize(stale, "admin-secret")
	stale.Header.Set("If-Match", "1")
	staleResult := httptest.NewRecorder()
	handler.ServeHTTP(staleResult, stale)
	if staleResult.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale=%d body=%s", staleResult.Code, staleResult.Body.String())
	}
	invalid := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/retention-policy", strings.NewReader(`{"version":"2","keepDays":0,"minimumVersions":1}`))
	authorize(invalid, "admin-secret")
	invalid.Header.Set("If-Match", "2")
	invalidResult := httptest.NewRecorder()
	handler.ServeHTTP(invalidResult, invalid)
	if invalidResult.Code != http.StatusBadRequest {
		t.Fatalf("invalid=%d body=%s", invalidResult.Code, invalidResult.Body.String())
	}
}

func TestRepositoryRetentionDryRunIsAdminOnlyMavenAndDoesNotMutateArtifacts(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	maven, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-dry-run", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-dry-run-raw", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, maven.ID, []repository.RepositoryGrant{{Principal: "retention-reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: maven.ID, Coordinate: "org.example:dry-run:1.0.0", Publisher: "test", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "dry-run-1.0.0.jar", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 1}}}
	if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkMavenPublishObject(ctx, session.ID, session.Objects[0].Name, "native/maven/dry-run/"+session.ID); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.CommitMavenPublishSession(ctx, session.ID, []repository.MavenAsset{{RepositoryID: maven.ID, Path: "org/example/dry-run/1.0.0/dry-run-1.0.0.jar", ObjectKey: "native/maven/dry-run/" + session.ID, Digest: session.Objects[0].Digest, Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := func(repositoryID, token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repositoryID+"/retention:dry-run", nil)
		authorize(req, token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	if response := request(maven.ID, "admin-secret"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"policyVersion":"1"`) || !strings.Contains(response.Body.String(), `"candidates":[]`) {
		t.Fatalf("dry run=%d body=%s", response.Code, response.Body.String())
	}
	visible, err := store.GetMavenArtifact(ctx, maven.ID, artifact.ID)
	if err != nil || visible.State != "visible" {
		t.Fatalf("dry run mutated artifact=%#v err=%v", visible, err)
	}
	if response := request(raw.ID, "admin-secret"); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "unsupported_operation") {
		t.Fatalf("raw dry run=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(maven.ID, authenticator.IssueToken("retention-reader")); response.Code != http.StatusForbidden {
		t.Fatalf("reader dry run=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRepositoryRetentionExecutionEnqueuesIdempotentMavenJob(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	maven, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-execute", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-execute-raw", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := func(repo string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo+"/retention:execute", nil)
		authorize(req, "admin-secret")
		req.Header.Set("Idempotency-Key", "retention-run")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	first := request(maven.ID)
	if first.Code != http.StatusAccepted || !strings.Contains(first.Body.String(), `"kind":"retention"`) || !strings.Contains(first.Body.String(), `"state":"pending"`) {
		t.Fatalf("execute=%d body=%s", first.Code, first.Body.String())
	}
	second := request(maven.ID)
	if second.Code != http.StatusAccepted || second.Body.String() != first.Body.String() {
		t.Fatalf("replay=%d body=%s", second.Code, second.Body.String())
	}
	if err = (NativeMavenRetention{Store: store}).RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.ListLifecycleJobs(ctx, maven.ID, 10)
	if err != nil || len(jobs) != 1 || jobs[0].State != repository.LifecycleJobCompleted {
		t.Fatalf("jobs=%#v err=%v", jobs, err)
	}
	if response := request(raw.ID); response.Code != http.StatusConflict {
		t.Fatalf("raw execute=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRepositoryRestoreRestoresConanTombstoneAndRejectsCollectedObjects(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	conan, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "restore-conan", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "restore-raw", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, conan.ID, []repository.RepositoryGrant{{Principal: "restore-reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	publish := func(reference, revision, key string) repository.ConanRecipeRevision {
		t.Helper()
		digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		if err := store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: conan.ID, ObjectKey: key, Digest: digest, Size: 1}); err != nil {
			t.Fatal(err)
		}
		item, err := store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: conan.ID, Reference: reference, Revision: revision, Digest: digest}, []repository.ConanAsset{{RepositoryID: conan.ID, Reference: reference, RecipeRevision: revision, Path: "conanfile.py", ObjectKey: key, Digest: digest, Size: 1}})
		if err != nil {
			t.Fatal(err)
		}
		return item
	}
	first := publish("pkg/1.0/user/stable", "rrev", "native/conan/restore/first")
	if _, err = store.TombstoneConanRecipeRevision(ctx, conan.ID, first.Reference, first.Revision); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := func(repositoryID, token, coordinate string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repositoryID+"/restore", strings.NewReader(`{"coordinate":"`+coordinate+`"}`))
		authorize(req, token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	coordinate := first.Reference + "#" + first.Revision
	if response := request(conan.ID, "admin-secret", coordinate); response.Code != http.StatusNoContent {
		t.Fatalf("restore=%d body=%s", response.Code, response.Body.String())
	}
	restored, err := store.GetConanRecipeRevision(ctx, conan.ID, first.Reference, first.Revision)
	if err != nil || restored.State != "visible" {
		t.Fatalf("restored=%#v err=%v", restored, err)
	}
	if _, err = store.GetArtifactTombstone(ctx, conan.ID, repository.FormatConan, coordinate); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("restore kept tombstone: %v", err)
	}
	second := publish("pkg/2.0/user/stable", "rrev", "native/conan/restore/second")
	if _, err = store.TombstoneConanRecipeRevision(ctx, conan.ID, second.Reference, second.Revision); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkConanObjectCollected(ctx, "native/conan/restore/second"); err != nil {
		t.Fatal(err)
	}
	if response := request(conan.ID, "admin-secret", second.Reference+"#"+second.Revision); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "restore_unavailable") {
		t.Fatalf("restore collected=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(raw.ID, "admin-secret", coordinate); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "unsupported_operation") {
		t.Fatalf("restore raw=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(conan.ID, authenticator.IssueToken("restore-reader"), coordinate); response.Code != http.StatusForbidden {
		t.Fatalf("restore reader=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(conan.ID, "admin-secret", "not-a-coordinate"); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid restore=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRepositoryRestoreRestoresMavenTombstone(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "restore-maven", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	coordinate := "org.example:widget:1.0.0"
	session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: coordinate, Publisher: "admin", PomObject: "widget-1.0.0.jar", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "widget-1.0.0.jar", Digest: "sha256:restore", Size: 1}}}
	if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	key := "native/maven/restore/" + session.ID
	if err = store.MarkMavenPublishObject(ctx, session.ID, session.PomObject, key); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.CommitMavenPublishSession(ctx, session.ID, []repository.MavenAsset{{RepositoryID: repo.ID, Path: "org/example/widget/1.0.0/widget-1.0.0.jar", ObjectKey: key, Digest: session.Objects[0].Digest, Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.TombstoneMavenArtifact(ctx, repo.ID, artifact.ID); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/restore", strings.NewReader(`{"coordinate":"`+coordinate+`"}`))
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("restore=%d body=%s", response.Code, response.Body.String())
	}
	if _, err = store.GetMavenAsset(ctx, repo.ID, "org/example/widget/1.0.0/widget-1.0.0.jar"); err != nil {
		t.Fatalf("restored Maven asset unavailable: %v", err)
	}
}

func TestRepositoryLifecycleJobStatusManagement(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "job-status-target", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	failedID := uuid.NewString()
	if _, _, err = store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: failedID, RepositoryID: repo.ID, Kind: repository.LifecycleJobReclaim, IdempotencyKey: "reclaim-object", Payload: []byte(`{"format":"conan","objectKey":"secret-object-key"}`)}); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimLifecycleJobs(ctx, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != failedID {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	if err = store.FailLifecycleJob(ctx, failedID, "object store unavailable"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: repo.ID, Kind: repository.LifecycleJobRetention, IdempotencyKey: "retention-run", Payload: []byte(`{"format":"conan"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "job-reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/lifecycle-jobs", nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"failed"`) || !strings.Contains(response.Body.String(), `"state":"pending"`) || !strings.Contains(response.Body.String(), `"lastError":"object store unavailable"`) || strings.Contains(response.Body.String(), "secret-object-key") || strings.Contains(response.Body.String(), "idempotencyKey") {
		t.Fatalf("jobs=%d body=%s", response.Code, response.Body.String())
	}
	denied := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/lifecycle-jobs", nil)
	authorize(denied, authenticator.IssueToken("job-reader"))
	deniedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deniedResponse, denied)
	if deniedResponse.Code != http.StatusForbidden {
		t.Fatalf("reader status=%d body=%s", deniedResponse.Code, deniedResponse.Body.String())
	}
}

func TestRepositoryTombstoneInspectionUsesBoundPagination(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "tombstone-target", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"team/alpha", "team/beta"} {
		digest := fmt.Sprintf("sha256:%064x", i+1)
		if _, err = store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: repo.ID, Name: name, Digest: digest, ObjectKey: fmt.Sprintf("oci/%d", i), MediaType: "application/json", Size: 1}, "latest"); err != nil {
			t.Fatal(err)
		}
		if err = store.DeleteOCIManifest(ctx, repo.ID, name, digest); err != nil {
			t.Fatal(err)
		}
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := func(query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/tombstones?"+query, nil)
		authorize(req, "admin-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	first := request("q=team%2F&pageSize=1")
	var page struct {
		Items []struct {
			Coordinate string `json:"coordinate"`
		}
		NextPageToken string `json:"nextPageToken"`
	}
	if first.Code != http.StatusOK || json.NewDecoder(first.Body).Decode(&page) != nil || len(page.Items) != 1 || page.Items[0].Coordinate[:10] != "team/alpha" || page.NextPageToken == "" {
		t.Fatalf("first=%d body=%s page=%#v", first.Code, first.Body.String(), page)
	}
	next := request("q=team%2F&pageSize=1&pageToken=" + url.QueryEscape(page.NextPageToken))
	if next.Code != http.StatusOK || !strings.Contains(next.Body.String(), "team/beta") {
		t.Fatalf("next=%d body=%s", next.Code, next.Body.String())
	}
	changed := request("q=other%2F&pageToken=" + url.QueryEscape(page.NextPageToken))
	if changed.Code != http.StatusBadRequest || !strings.Contains(changed.Body.String(), "invalid_page_token") {
		t.Fatalf("changed=%d body=%s", changed.Code, changed.Body.String())
	}
}

func TestMavenArtifactDetailAndTombstoneManagement(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "artifact-target", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	const key = "native/maven/sha256/artifact-target"
	session := repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:widget:1.0.0", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "widget-1.0.0.jar", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 3}}}
	if _, err = store.CreateMavenPublishSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkMavenPublishObject(context.Background(), session.ID, session.Objects[0].Name, key); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.CommitMavenPublishSession(context.Background(), session.ID, []repository.MavenAsset{{RepositoryID: repo.ID, Path: "org/example/widget/1.0.0/widget-1.0.0.jar", ObjectKey: key, Digest: session.Objects[0].Digest, Size: 3}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	detail := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/artifacts/"+artifact.ID, nil)
	authorize(detail, "admin-secret")
	detailed := httptest.NewRecorder()
	handler.ServeHTTP(detailed, detail)
	if detailed.Code != http.StatusOK || !strings.Contains(detailed.Body.String(), `"state":"visible"`) {
		t.Fatalf("detail=%d body=%s", detailed.Code, detailed.Body.String())
	}
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/v2/repositories/"+repo.ID+"/artifacts/"+artifact.ID, nil)
	authorize(deleteRequest, "admin-secret")
	deleted := httptest.NewRecorder()
	handler.ServeHTTP(deleted, deleteRequest)
	if deleted.Code != http.StatusAccepted || !strings.Contains(deleted.Body.String(), `"state":"pending"`) {
		t.Fatalf("delete=%d body=%s", deleted.Code, deleted.Body.String())
	}
	repeated := httptest.NewRecorder()
	repeatRequest := httptest.NewRequest(http.MethodDelete, "/api/v2/repositories/"+repo.ID+"/artifacts/"+artifact.ID, nil)
	authorize(repeatRequest, "admin-secret")
	handler.ServeHTTP(repeated, repeatRequest)
	if repeated.Code != http.StatusAccepted {
		t.Fatalf("repeat delete=%d body=%s", repeated.Code, repeated.Body.String())
	}
	list := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/artifacts", nil)
	authorize(list, "admin-secret")
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, list)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), artifact.ID) {
		t.Fatalf("list=%d body=%s", listed.Code, listed.Body.String())
	}
	protocolRead := httptest.NewRequest(http.MethodGet, "/repository/maven/artifact-target/org/example/widget/1.0.0/widget-1.0.0.jar", nil)
	protocolRead.SetBasicAuth("maven", "resolver-secret")
	protocolResponse := httptest.NewRecorder()
	handler.ServeHTTP(protocolResponse, protocolRead)
	if protocolResponse.Code != http.StatusNotFound {
		t.Fatalf("protocol read=%d body=%s", protocolResponse.Code, protocolResponse.Body.String())
	}
	detailAfterDelete := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/artifacts/"+artifact.ID, nil)
	authorize(detailAfterDelete, "admin-secret")
	detailedAfterDelete := httptest.NewRecorder()
	handler.ServeHTTP(detailedAfterDelete, detailAfterDelete)
	if detailedAfterDelete.Code != http.StatusOK || !strings.Contains(detailedAfterDelete.Body.String(), `"state":"deleted"`) {
		t.Fatalf("detail after delete=%d body=%s", detailedAfterDelete.Code, detailedAfterDelete.Body.String())
	}
	tombstone, err := store.GetArtifactTombstone(context.Background(), repo.ID, repository.FormatMaven, artifact.Coordinate)
	if err != nil || tombstone.Digest != artifact.Digest || tombstone.TombstonedAt.IsZero() {
		t.Fatalf("tombstone=%#v err=%v", tombstone, err)
	}
}

func TestHostedRepositoryManagementRejectsAnonymousAndInvalidRequests(t *testing.T) {
	handler := NewGatewayHandler(Dependencies{}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/api/v2/repositories", nil))
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d", denied.Code)
	}
	anonymousInvalidSession := httptest.NewRecorder()
	handler.ServeHTTP(anonymousInvalidSession, httptest.NewRequest(http.MethodPost, "/api/v2/publish-sessions/not-a-uuid:commit", nil))
	if anonymousInvalidSession.Code != http.StatusUnauthorized || !strings.Contains(anonymousInvalidSession.Body.String(), `"code":"access_denied"`) {
		t.Fatalf("anonymous invalid session=%d body=%s", anonymousInvalidSession.Code, anonymousInvalidSession.Body.String())
	}
	bad := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(`{"name":"Bad Name","format":"npm"}`))
	authorize(bad, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, bad)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid status=%d body=%s", response.Code, response.Body.String())
	}
	page := httptest.NewRequest(http.MethodGet, "/api/v2/repositories?pageToken=unknown", nil)
	authorize(page, "admin-secret")
	paged := httptest.NewRecorder()
	handler.ServeHTTP(paged, page)
	if paged.Code != http.StatusBadRequest || !strings.Contains(paged.Body.String(), `"code":"invalid_page_token"`) {
		t.Fatalf("invalid page token=%d body=%s", paged.Code, paged.Body.String())
	}
	invalidID := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/not-a-uuid", nil)
	authorize(invalidID, "admin-secret")
	invalidIDResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidIDResponse, invalidID)
	if invalidIDResponse.Code != http.StatusBadRequest || !strings.Contains(invalidIDResponse.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("invalid id=%d body=%s", invalidIDResponse.Code, invalidIDResponse.Body.String())
	}
	invalidArtifactRepositoryID := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/not-a-uuid/artifacts", nil)
	authorize(invalidArtifactRepositoryID, "admin-secret")
	invalidArtifactRepositoryIDResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidArtifactRepositoryIDResponse, invalidArtifactRepositoryID)
	if invalidArtifactRepositoryIDResponse.Code != http.StatusBadRequest || !strings.Contains(invalidArtifactRepositoryIDResponse.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("invalid artifact repository id=%d body=%s", invalidArtifactRepositoryIDResponse.Code, invalidArtifactRepositoryIDResponse.Body.String())
	}
	invalidSessionID := httptest.NewRequest(http.MethodPost, "/api/v2/publish-sessions/not-a-uuid:commit", nil)
	authorize(invalidSessionID, "admin-secret")
	invalidSessionIDResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidSessionIDResponse, invalidSessionID)
	if invalidSessionIDResponse.Code != http.StatusBadRequest || !strings.Contains(invalidSessionIDResponse.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("invalid session id=%d body=%s", invalidSessionIDResponse.Code, invalidSessionIDResponse.Body.String())
	}
	nonCommitSessionPost := httptest.NewRequest(http.MethodPost, "/api/v2/publish-sessions/"+uuid.NewString(), nil)
	authorize(nonCommitSessionPost, "admin-secret")
	nonCommitSessionPostResponse := httptest.NewRecorder()
	handler.ServeHTTP(nonCommitSessionPostResponse, nonCommitSessionPost)
	if nonCommitSessionPostResponse.Code != http.StatusMethodNotAllowed {
		t.Fatalf("non-commit session post=%d body=%s", nonCommitSessionPostResponse.Code, nonCommitSessionPostResponse.Body.String())
	}
}

func TestHostedRepositoryIdempotencyAndPagination(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	create := func(name, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(`{"name":"`+name+`","format":"raw"}`))
		authorize(r, "admin-secret")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if missing := create("missing", ""); missing.Code != http.StatusBadRequest {
		t.Fatalf("missing key=%d", missing.Code)
	}
	first := create("one", "same-key")
	if first.Code != http.StatusCreated {
		t.Fatalf("first=%d %s", first.Code, first.Body.String())
	}
	replay := create("one", "same-key")
	if replay.Code != http.StatusCreated || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay=%d %s", replay.Code, replay.Body.String())
	}
	if conflict := create("two", "same-key"); conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "idempotency_conflict") {
		t.Fatalf("conflict=%d %s", conflict.Code, conflict.Body.String())
	}
	if second := create("two", "two-key"); second.Code != http.StatusCreated {
		t.Fatalf("second=%d", second.Code)
	}
	list := httptest.NewRequest(http.MethodGet, "/api/v2/repositories?pageSize=1", nil)
	authorize(list, "admin-secret")
	pageOne := httptest.NewRecorder()
	handler.ServeHTTP(pageOne, list)
	if pageOne.Code != http.StatusOK {
		t.Fatalf("page one=%d", pageOne.Code)
	}
	var decoded repositoryPage
	if err := json.NewDecoder(pageOne.Body).Decode(&decoded); err != nil || len(decoded.Items) != 1 || decoded.NextPageToken == "" {
		t.Fatalf("page=%#v err=%v", decoded, err)
	}
	pageTwo := httptest.NewRequest(http.MethodGet, "/api/v2/repositories?pageSize=1&pageToken="+decoded.NextPageToken, nil)
	authorize(pageTwo, "admin-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, pageTwo)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), decoded.Items[0].ID) {
		t.Fatalf("page two=%d %s", w.Code, w.Body.String())
	}
	payload, _ := json.Marshal(repositoryPageCursor{Endpoint: "repositories", ID: decoded.Items[0].ID, ExpiresAt: time.Now().Add(-time.Second).Unix()})
	mac := hmac.New(sha256.New, []byte("admin-secret"))
	_, _ = mac.Write(payload)
	expired := base64.RawURLEncoding.EncodeToString(append(payload, mac.Sum(nil)...))
	expiredRequest := httptest.NewRequest(http.MethodGet, "/api/v2/repositories?pageToken="+expired, nil)
	authorize(expiredRequest, "admin-secret")
	expiredResponse := httptest.NewRecorder()
	handler.ServeHTTP(expiredResponse, expiredRequest)
	if expiredResponse.Code != http.StatusBadRequest || !strings.Contains(expiredResponse.Body.String(), "invalid_page_token") {
		t.Fatalf("expired token=%d %s", expiredResponse.Code, expiredResponse.Body.String())
	}
}

func TestNativeRepositoryGuardDeniesAnonymousAndDisabledProtocols(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	for _, format := range []repository.Format{repository.FormatRaw, repository.FormatOCI, repository.FormatMaven} {
		repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: string(format) + "-native", Format: format})
		if err != nil {
			t.Fatal(err)
		}
		path := map[repository.Format]string{repository.FormatRaw: "/raw/raw-native/a", repository.FormatOCI: "/v2/oci-native/manifests/latest", repository.FormatMaven: "/maven/maven-native/a.pom"}[format]
		anonymous := httptest.NewRecorder()
		handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, path, nil))
		if anonymous.Code != http.StatusUnauthorized {
			t.Fatalf("%s anonymous=%d", format, anonymous.Code)
		}
		if _, err := store.DisableHostedRepository(context.Background(), repo.ID); err != nil {
			t.Fatal(err)
		}
		disabled := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		authorize(r, "resolver-secret")
		handler.ServeHTTP(disabled, r)
		if disabled.Code != http.StatusForbidden {
			t.Fatalf("%s disabled=%d", format, disabled.Code)
		}
	}
}

func TestMemoryHostedRepositoryHonorsPageSize200(t *testing.T) {
	store := repository.NewMemoryStore()
	for i := 0; i < 201; i++ {
		if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: fmt.Sprintf("repo-%03d", i), Format: repository.FormatRaw}); err != nil {
			t.Fatal(err)
		}
	}
	items, next, err := store.ListHostedRepositories(context.Background(), 200, "")
	if err != nil || len(items) != 200 || next == "" {
		t.Fatalf("items=%d next=%q err=%v", len(items), next, err)
	}
}

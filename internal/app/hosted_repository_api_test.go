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
	"strconv"
	"strings"
	"testing"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
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
	if resolved.Code != http.StatusOK || resolved.Body.String() != string(body) {
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
	plans, err := store.ListReplicationPlans(ctx, target.ID, 10)
	if err != nil || len(plans) != 1 || plans[0].Format != repository.FormatRaw {
		t.Fatalf("plans=%#v err=%v", plans, err)
	}
	plan, err := store.GetReplicationPlan(ctx, target.ID, plans[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints, err := store.ListReplicationCheckpoints(ctx, plan.ID)
	if err != nil || len(checkpoints) != 1 {
		t.Fatalf("checkpoints=%#v err=%v", checkpoints, err)
	}
	checkpoint := checkpoints[0]
	checkpoint.ByteOffset = checkpoint.Size
	checkpoint.State = "verified"
	checkpoint.Attempts = 2
	checkpoint.LastError = "transient object-store error"
	checkpoint.VerifiedAt = time.Now().UTC()
	checkpoint.SourceObjectKey = "internal/source/object-key"
	claimed, err := store.ClaimReplicationPlans(ctx, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != plan.ID {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	if err = store.UpdateReplicationCheckpointWithLease(ctx, checkpoint, claimed[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	detail := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+target.ID+"/replications/"+plan.ID, nil)
	authorize(detail, authenticator.IssueToken("replicator"))
	detailed := httptest.NewRecorder()
	handler.ServeHTTP(detailed, detail)
	for _, value := range []string{`"objectKey":"` + checkpoint.ObjectKey, `"digest":"` + checkpoint.Digest, `"size":` + strconv.FormatInt(checkpoint.Size, 10), `"byteOffset":` + strconv.FormatInt(checkpoint.ByteOffset, 10), `"state":"verified"`, `"attempts":2`, `"lastError":"transient object-store error"`, `"verifiedAt":`} {
		if !strings.Contains(detailed.Body.String(), value) {
			t.Fatalf("detail missing %q: %d %s", value, detailed.Code, detailed.Body.String())
		}
	}
	if detailed.Code != http.StatusOK || strings.Contains(detailed.Body.String(), "sourceObjectKey") || strings.Contains(detailed.Body.String(), checkpoint.SourceObjectKey) {
		t.Fatalf("detail=%d %s", detailed.Code, detailed.Body.String())
	}
	other, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "replication-raw-other", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, other.ID, []repository.RepositoryGrant{{Principal: "replicator", Scopes: []string{"repositories:admin"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	notFound := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+other.ID+"/replications/"+plan.ID, nil)
	authorize(notFound, authenticator.IssueToken("replicator"))
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, notFound)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unscoped detail=%d %s", missing.Code, missing.Body.String())
	}
	found := false
	for _, audit := range store.Audits {
		found = found || (audit.Operation == "replicate" && audit.Repository == source.Name && audit.Resource == asset.Path && audit.Status == http.StatusAccepted)
	}
	if !found {
		t.Fatalf("replication audit missing: %#v", store.Audits)
	}
}

func TestRepositoryConanReplicationHTTPPlansAllVisibleRevisionAssets(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "replication-conan-source", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "replication-conan-target", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	reference, revision := "widget/1.0/user/stable", "rrev"
	key := "native/conan/source/conanfile.py"
	if err = store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: source.ID, ObjectKey: key, Digest: digest, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: source.ID, Reference: reference, Revision: revision, Digest: digest}, []repository.ConanAsset{{RepositoryID: source.ID, Reference: reference, RecipeRevision: revision, Path: "conanfile.py", ObjectKey: key, Digest: digest, Size: 1}}); err != nil {
		t.Fatal(err)
	}
	for _, repo := range []repository.HostedRepository{source, target} {
		if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "replicator", Scopes: []string{"repositories:admin"}}}, "1"); err != nil {
			t.Fatal(err)
		}
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+source.ID+"/replications", strings.NewReader(`{"targetRepositoryId":"`+target.ID+`","coordinate":"`+reference+`#`+revision+`","digest":"`+digest+`"}`))
	authorize(request, authenticator.IssueToken("replicator"))
	request.Header.Set("Idempotency-Key", "conan-copy")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("plan=%d %s", response.Code, response.Body.String())
	}
	plans, err := store.ListReplicationPlans(ctx, source.ID, 10)
	if err != nil || len(plans) != 1 || plans[0].Format != repository.FormatConan {
		t.Fatalf("plans=%#v err=%v", plans, err)
	}
	checks, err := store.ListReplicationCheckpoints(ctx, plans[0].ID)
	if err != nil || len(checks) != 1 || checks[0].SourceObjectKey != key || checks[0].ObjectKey != conanReplicationTargetObjectKey(target.ID, key) {
		t.Fatalf("checkpoints=%#v err=%v", checks, err)
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
	if resolved.Code != http.StatusOK || resolved.Body.String() != string(body) {
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
	if resolved.Code != http.StatusOK || resolved.Body.String() != string(body) {
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
	oci, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "capabilities-oci", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	conan, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "capabilities-conan", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	maven, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "capabilities-maven", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "capabilities-raw", Format: repository.FormatRaw})
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
	ociRequest := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+oci.ID+"/capabilities", nil)
	authorize(ociRequest, "admin-secret")
	ociResponse := httptest.NewRecorder()
	handler.ServeHTTP(ociResponse, ociRequest)
	if ociResponse.Code != http.StatusOK || !strings.Contains(ociResponse.Body.String(), `"restore"`) {
		t.Fatalf("OCI capabilities=%d %s", ociResponse.Code, ociResponse.Body.String())
	}
	rawRequest := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+raw.ID+"/capabilities", nil)
	authorize(rawRequest, "admin-secret")
	rawResponse := httptest.NewRecorder()
	handler.ServeHTTP(rawResponse, rawRequest)
	if rawResponse.Code != http.StatusOK || !strings.Contains(rawResponse.Body.String(), `"restore"`) || strings.Contains(rawResponse.Body.String(), `"retain"`) {
		t.Fatalf("Raw capabilities=%d %s", rawResponse.Code, rawResponse.Body.String())
	}
}

func TestHostedRepositoryManagementCreatesProxyRepository(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	create := func(body, key string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(body))
		authorize(r, "admin-secret")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	created := create(`{"name":"raw-proxy","format":"raw","type":"proxy","endpoint":"https://upstream.example","allowedHosts":["upstream.example","cdn.example"]}`, "create-raw-proxy")
	if created.Code != http.StatusCreated {
		t.Fatalf("create proxy=%d body=%s", created.Code, created.Body.String())
	}
	for _, fragment := range []string{`"type":"proxy"`, `"endpoint":"https://upstream.example"`, `"allowedHosts":["upstream.example","cdn.example"]`} {
		if !strings.Contains(created.Body.String(), fragment) {
			t.Fatalf("proxy response missing %s: %s", fragment, created.Body.String())
		}
	}
	id := strings.Split(strings.Split(created.Body.String(), `"id":"`)[1], `"`)[0]

	// The proxy shape persists through the read path.
	get := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+id, nil)
	authorize(get, "admin-secret")
	loaded := httptest.NewRecorder()
	handler.ServeHTTP(loaded, get)
	if loaded.Code != http.StatusOK || !strings.Contains(loaded.Body.String(), `"type":"proxy"`) || !strings.Contains(loaded.Body.String(), `"endpoint":"https://upstream.example"`) {
		t.Fatalf("get proxy=%d body=%s", loaded.Code, loaded.Body.String())
	}

	// Proxy capabilities are read-only plus cache reclaim: no publish, delete,
	// restore, or retain.
	capabilitiesRequest := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+id+"/capabilities", nil)
	authorize(capabilitiesRequest, "admin-secret")
	capabilities := httptest.NewRecorder()
	handler.ServeHTTP(capabilities, capabilitiesRequest)
	body := capabilities.Body.String()
	if capabilities.Code != http.StatusOK || !strings.Contains(body, `"type":"proxy"`) {
		t.Fatalf("proxy capabilities=%d %s", capabilities.Code, body)
	}
	for _, operation := range []string{`"read"`, `"browse"`, `"reclaim"`} {
		if !strings.Contains(body, operation) {
			t.Fatalf("proxy capabilities missing %s: %s", operation, body)
		}
	}
	for _, operation := range []string{`"publish"`, `"delete"`, `"restore"`, `"retain"`} {
		if strings.Contains(body, operation) {
			t.Fatalf("proxy capabilities must not contain %s: %s", operation, body)
		}
	}

	// Hosted remains the default when type is omitted.
	hosted := create(`{"name":"plain-hosted","format":"raw"}`, "create-plain-hosted")
	if hosted.Code != http.StatusCreated || !strings.Contains(hosted.Body.String(), `"type":"hosted"`) {
		t.Fatalf("default hosted=%d body=%s", hosted.Code, hosted.Body.String())
	}
}

func TestHostedRepositoryManagementRejectsInvalidProxyShapes(t *testing.T) {
	handler := NewGatewayHandler(Dependencies{}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	create := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(body))
		authorize(r, "admin-secret")
		r.Header.Set("Idempotency-Key", "key-"+body)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	cases := map[string]string{
		"proxy without endpoint":           `{"name":"proxy-no-endpoint","format":"raw","type":"proxy"}`,
		"proxy with http endpoint":         `{"name":"proxy-http","format":"raw","type":"proxy","endpoint":"http://upstream.example","allowedHosts":["upstream.example"]}`,
		"proxy with malformed endpoint":    `{"name":"proxy-bad-url","format":"raw","type":"proxy","endpoint":"not a url","allowedHosts":["upstream.example"]}`,
		"raw proxy without allowedHosts":   `{"name":"proxy-no-hosts","format":"raw","type":"proxy","endpoint":"https://upstream.example"}`,
		"conan proxy without allowedHosts": `{"name":"proxy-conan-no-hosts","format":"conan","type":"proxy","endpoint":"https://upstream.example"}`,
		"hosted with endpoint":             `{"name":"hosted-endpoint","format":"raw","endpoint":"https://upstream.example"}`,
		"hosted with allowedHosts":         `{"name":"hosted-hosts","format":"raw","allowedHosts":["upstream.example"]}`,
		"unknown type":                     `{"name":"unknown-type","format":"raw","type":"virtual"}`,
	}
	for name, body := range cases {
		if response := create(body); response.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d body=%s", name, response.Code, response.Body.String())
		}
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

func TestMavenSnapshotSearchPaginationPreservesBuildPositionAcrossSurfaces(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "snapshot-search", Format: repository.FormatMaven, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "snapshot-reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	group, _, err := store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{
		ID: uuid.NewString(), Name: "snapshot-search-group", Format: repository.FormatMaven, AnonymousRead: true,
		Members: []repository.GroupMember{{RepositoryID: repo.ID, Position: 0}},
	}, "test", "snapshot-search-group", "snapshot-search-group")
	if err != nil {
		t.Fatal(err)
	}
	enableAnonymousAccess(t, store)

	coordinate := "org.example:demo:1.0-SNAPSHOT"
	for build := 1; build <= 3; build++ {
		sessionID := uuid.NewString()
		objectName := fmt.Sprintf("demo-build-%d.pom", build)
		objectKey := "maven/snapshot-search/" + sessionID
		digest := "sha256:" + fmt.Sprintf("%064x", build)
		session := repository.MavenPublishSession{ID: sessionID, RepositoryID: repo.ID, Coordinate: coordinate, Publisher: "snapshot-publisher", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: objectName, Digest: digest, Size: 1}}}
		if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
			t.Fatal(err)
		}
		if err = store.MarkMavenPublishObject(ctx, sessionID, objectName, objectKey); err != nil {
			t.Fatal(err)
		}
		if _, err = store.CommitMavenPublishSession(ctx, sessionID, []repository.MavenAsset{{RepositoryID: repo.ID, Path: objectName, ObjectKey: objectKey, Digest: digest, Size: 1}}); err != nil {
			t.Fatal(err)
		}
	}

	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	assertPages := func(name, path string, authenticated bool) {
		t.Helper()
		token := ""
		for wantBuild := 1; wantBuild <= 3; wantBuild++ {
			values := url.Values{"q": {"org.example:demo:"}, "pageSize": {"1"}}
			if strings.Contains(path, "/artifact-search") && !strings.Contains(path, "/repositories/") {
				values.Set("format", "maven")
			}
			if token != "" {
				values.Set("pageToken", token)
			}
			request := httptest.NewRequest(http.MethodGet, path+"?"+values.Encode(), nil)
			if authenticated {
				authorize(request, authenticator.IssueToken("snapshot-reader"))
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			var page struct {
				Items []struct {
					Coordinate  string `json:"coordinate"`
					BuildNumber int    `json:"buildNumber"`
				} `json:"items"`
				NextPageToken string `json:"nextPageToken"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].Coordinate != coordinate || page.Items[0].BuildNumber != wantBuild {
				t.Fatalf("%s build %d: status=%d body=%s", name, wantBuild, response.Code, response.Body.String())
			}
			token = page.NextPageToken
			if wantBuild < 3 && token == "" {
				t.Fatalf("%s build %d: missing next page token", name, wantBuild)
			}
		}
		if token != "" {
			t.Fatalf("%s final page has next token", name)
		}
	}

	assertPages("repository", "/api/v2/repositories/"+repo.ID+"/artifact-search", true)
	assertPages("anonymous group", "/api/v2/repositories/"+group.ID+"/artifact-search", false)
	assertPages("global", "/api/v2/artifact-search", true)
}

func TestOCIManifestBrowseIncludesUntaggedManifest(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "oci-untagged", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("d", 64)
	if _, err = store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: repo.ID, Name: "nginx", Digest: digest, ObjectKey: "oci/untagged", MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 1989}, digest); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/oci/manifests?name=nginx&pageSize=50", nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"digest":"`+digest+`"`) || !strings.Contains(response.Body.String(), `"tags":[]`) {
		t.Fatalf("untagged OCI manifest browse=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAnonymousHostedGroupArtifactSearchAggregatesOnlyAnonymousMembers(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	public, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "public-group-member", Format: repository.FormatOCI, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	private, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "private-group-member", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	if _, err := store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: public.ID, Name: "team/public", Digest: digest, ObjectKey: "public", MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 1}, "latest"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: private.ID, Name: "team/private", Digest: digest, ObjectKey: "private", MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 1}, "latest"); err != nil {
		t.Fatal(err)
	}
	group, _, err := store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{
		ID: uuid.NewString(), Name: "public-search-group", Format: repository.FormatOCI, AnonymousRead: true,
		Members: []repository.GroupMember{{RepositoryID: public.ID, Position: 0}, {RepositoryID: private.ID, Position: 1}},
	}, "test", "public-search-group", "public-search-group")
	if err != nil {
		t.Fatal(err)
	}
	enableAnonymousAccess(t, store)
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+group.ID+"/artifact-search?q=team%2F&pageSize=50", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"coordinate":"team/public"`) {
		t.Fatalf("anonymous group search=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "team/private") {
		t.Fatalf("private member leaked into group search: %s", response.Body.String())
	}
}

func TestConanRecipeRevisionSearchPaginatesAndBindsCursorToQuery(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "conan-versions", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "conan-reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	reference := "pkg/1.0/user/stable"
	for i, revision := range []string{"build-alpha", "build-beta", "build-gamma"} {
		digest := "sha256:" + strings.Repeat(string(rune('a'+i)), 64)
		key := "conan/versions/" + revision
		if err = store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: repo.ID, ObjectKey: key, Digest: digest, Size: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err = store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: repo.ID, Reference: reference, Revision: revision, Digest: digest}, []repository.ConanAsset{{RepositoryID: repo.ID, Reference: reference, RecipeRevision: revision, Path: "conanfile.py", ObjectKey: key, Digest: digest, Size: 1}}); err != nil {
			t.Fatal(err)
		}
	}

	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := func(query, token string, pageSize int) *httptest.ResponseRecorder {
		t.Helper()
		values := url.Values{"reference": {reference}, "pageSize": {strconv.Itoa(pageSize)}}
		if query != "" {
			values.Set("q", query)
		}
		if token != "" {
			values.Set("pageToken", token)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/conan/recipe-revisions?"+values.Encode(), nil)
		authorize(req, authenticator.IssueToken("conan-reader"))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	type revisionPage struct {
		Items []struct {
			Revision string `json:"revision"`
		} `json:"items"`
		NextPageToken string `json:"nextPageToken"`
	}
	first := request("", "", 2)
	var firstPage revisionPage
	if first.Code != http.StatusOK || json.Unmarshal(first.Body.Bytes(), &firstPage) != nil || len(firstPage.Items) != 2 || firstPage.Items[0].Revision != "build-alpha" || firstPage.NextPageToken == "" {
		t.Fatalf("first page=%d body=%s", first.Code, first.Body.String())
	}
	second := request("", firstPage.NextPageToken, 2)
	var secondPage revisionPage
	if second.Code != http.StatusOK || json.Unmarshal(second.Body.Bytes(), &secondPage) != nil || len(secondPage.Items) != 1 || secondPage.Items[0].Revision != "build-gamma" || secondPage.NextPageToken != "" {
		t.Fatalf("second page=%d body=%s", second.Code, second.Body.String())
	}
	filtered := request("beta", "", 2)
	var filteredPage revisionPage
	if filtered.Code != http.StatusOK || json.Unmarshal(filtered.Body.Bytes(), &filteredPage) != nil || len(filteredPage.Items) != 1 || filteredPage.Items[0].Revision != "build-beta" {
		t.Fatalf("filtered=%d body=%s", filtered.Code, filtered.Body.String())
	}
	if invalid := request("beta", firstPage.NextPageToken, 2); invalid.Code != http.StatusBadRequest {
		t.Fatalf("query-bound cursor=%d body=%s", invalid.Code, invalid.Body.String())
	}
}

func TestAnonymousHostedGroupConanRecipeRevisionsAggregateMembers(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	public, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "conan-public", Format: repository.FormatConan, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	private, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "conan-private", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	reference := "demo/1.0/user/stable"
	publish := func(repoID, revision string) {
		digestChar := "a"
		if revision == "private-revision" {
			digestChar = "b"
		}
		digest := "sha256:" + strings.Repeat(digestChar, 64)
		key := "conan/group/" + repoID + "/" + revision
		if err := store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: repoID, ObjectKey: key, Digest: digest, Size: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: repoID, Reference: reference, Revision: revision, Digest: digest}, []repository.ConanAsset{{RepositoryID: repoID, Reference: reference, RecipeRevision: revision, Path: "conanfile.py", ObjectKey: key, Digest: digest, Size: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	publish(public.ID, "public-revision")
	publish(private.ID, "private-revision")
	group, _, err := store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{ID: uuid.NewString(), Name: "conan-public-group", Format: repository.FormatConan, AnonymousRead: true, Members: []repository.GroupMember{{RepositoryID: public.ID, Position: 0}, {RepositoryID: private.ID, Position: 1}}}, "test", "conan-public-group", "conan-public-group")
	if err != nil {
		t.Fatal(err)
	}
	enableAnonymousAccess(t, store)
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+group.ID+"/conan/recipe-revisions?reference="+url.QueryEscape(reference), nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "public-revision") || strings.Contains(response.Body.String(), "private-revision") {
		t.Fatalf("anonymous group revisions=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCreateHostedGroupAcceptsConanFormat(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "conan-group-member", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodPost, "/api/v2/groups", strings.NewReader(fmt.Sprintf(`{"name":"conan-group","format":"conan","members":[{"repositoryId":"%s","position":0}]}`, repo.ID)))
	authorize(request, "admin-secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "create-conan-group")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"format":"conan"`) {
		t.Fatalf("create Conan group=%d body=%s", response.Code, response.Body.String())
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
	replace := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/grants", strings.NewReader(`[{"principal":"build-agent","scopes":["repositories:read"],"resourcePrefix":"releases/"},{"principal":"build-agent","scopes":["repositories:write"],"resourcePrefix":"snapshots/"}]`))
	authorize(replace, "admin-secret")
	replace.Header.Set("If-Match", "1")
	replaced := httptest.NewRecorder()
	handler.ServeHTTP(replaced, replace)
	if replaced.Code != http.StatusOK || replaced.Header().Get("ETag") != "2" || !strings.Contains(replaced.Body.String(), `"resourcePrefix":"releases/"`) {
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
	duplicate := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/grants", strings.NewReader(`[{"principal":"build-agent","scopes":["repositories:read"],"resourcePrefix":"releases/"},{"principal":"build-agent","scopes":["repositories:write"],"resourcePrefix":"releases/"}]`))
	authorize(duplicate, "admin-secret")
	duplicate.Header.Set("If-Match", "2")
	duplicateResult := httptest.NewRecorder()
	handler.ServeHTTP(duplicateResult, duplicate)
	if duplicateResult.Code != http.StatusBadRequest {
		t.Fatalf("duplicate=%d body=%s", duplicateResult.Code, duplicateResult.Body.String())
	}
	badPrefix := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/grants", strings.NewReader(`[{"principal":"build-agent","scopes":["repositories:read"],"resourcePrefix":"/absolute"}]`))
	authorize(badPrefix, "admin-secret")
	badPrefix.Header.Set("If-Match", "2")
	badPrefixResult := httptest.NewRecorder()
	handler.ServeHTTP(badPrefixResult, badPrefix)
	if badPrefixResult.Code != http.StatusBadRequest {
		t.Fatalf("bad prefix=%d body=%s", badPrefixResult.Code, badPrefixResult.Body.String())
	}
}

func TestRepositoryConsoleAggregatesRequireAdminAndReturnCrossRepositoryData(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	first, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "aggregate-first", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "aggregate-second", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, first.ID, []repository.RepositoryGrant{{Principal: "build-agent", Scopes: []string{"repositories:read"}, ResourcePrefix: "releases/"}}, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: first.ID, Path: "release.txt", Digest: "sha256:aggregate", Size: 17}); err != nil {
		t.Fatal(err)
	}
	job := repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: second.ID, Kind: repository.LifecycleJobRetention, IdempotencyKey: "aggregate-retention"}
	if _, _, err = store.EnqueueLifecycleJob(ctx, job); err != nil {
		t.Fatal(err)
	}

	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := func(path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			authorize(req, token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	if response := request("/api/v2/repository-grants", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous grants=%d body=%s", response.Code, response.Body.String())
	}

	grantsResponse := request("/api/v2/repository-grants", "admin-secret")
	var grants adminopenapi.RepositoryGrantRecordList
	if grantsResponse.Code != http.StatusOK || json.Unmarshal(grantsResponse.Body.Bytes(), &grants) != nil || len(grants) != 1 || grants[0].RepositoryId.String() != first.ID || grants[0].Principal != "build-agent" {
		t.Fatalf("grants=%d body=%s decoded=%#v", grantsResponse.Code, grantsResponse.Body.String(), grants)
	}

	capacitiesResponse := request("/api/v2/repository-capacities", "admin-secret")
	var capacities adminopenapi.RepositoryCapacityList
	if capacitiesResponse.Code != http.StatusOK || json.Unmarshal(capacitiesResponse.Body.Bytes(), &capacities) != nil || len(capacities) != 2 {
		t.Fatalf("capacities=%d body=%s decoded=%#v", capacitiesResponse.Code, capacitiesResponse.Body.String(), capacities)
	}
	capacityByID := make(map[string]adminopenapi.RepositoryCapacity, len(capacities))
	for _, capacity := range capacities {
		capacityByID[capacity.RepositoryId.String()] = capacity
	}
	if capacityByID[first.ID].UsedBytes != 17 || capacityByID[first.ID].ObjectCount != 1 {
		t.Fatalf("raw capacity=%#v", capacityByID[first.ID])
	}

	jobsResponse := request("/api/v2/lifecycle-jobs?limit=10", "admin-secret")
	var jobs adminopenapi.RepositoryLifecycleJobList
	if jobsResponse.Code != http.StatusOK || json.Unmarshal(jobsResponse.Body.Bytes(), &jobs) != nil || len(jobs) != 1 || jobs[0].RepositoryId.String() != second.ID || jobs[0].RepositoryName != second.Name || jobs[0].Job.Id != job.ID {
		t.Fatalf("jobs=%d body=%s decoded=%#v", jobsResponse.Code, jobsResponse.Body.String(), jobs)
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
	if defaultPolicy.Code != http.StatusOK || !strings.Contains(defaultPolicy.Body.String(), `"version":"1"`) || !strings.Contains(defaultPolicy.Body.String(), `"enabled":false`) || !strings.Contains(defaultPolicy.Body.String(), `"snapshotKeepDays":30`) {
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
	invalidMaximum := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/retention-policy", strings.NewReader(`{"version":"2","keepDays":14,"minimumVersions":3,"maximumVersions":2}`))
	authorize(invalidMaximum, "admin-secret")
	invalidMaximum.Header.Set("If-Match", "2")
	invalidMaximumResult := httptest.NewRecorder()
	handler.ServeHTTP(invalidMaximumResult, invalidMaximum)
	if invalidMaximumResult.Code != http.StatusBadRequest || !strings.Contains(invalidMaximumResult.Body.String(), "maximumVersions") {
		t.Fatalf("invalid maximum=%d body=%s", invalidMaximumResult.Code, invalidMaximumResult.Body.String())
	}
	invalidPattern := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/retention-policy", strings.NewReader(`{"version":"2","keepDays":14,"minimumVersions":1,"coordinatePatterns":["["]}`))
	authorize(invalidPattern, "admin-secret")
	invalidPattern.Header.Set("If-Match", "2")
	invalidPatternResult := httptest.NewRecorder()
	handler.ServeHTTP(invalidPatternResult, invalidPattern)
	if invalidPatternResult.Code != http.StatusBadRequest || !strings.Contains(invalidPatternResult.Body.String(), "coordinatePatterns") {
		t.Fatalf("invalid pattern=%d body=%s", invalidPatternResult.Code, invalidPatternResult.Body.String())
	}
}

func TestRepositoryCapacityManagementUsesScopedGrantsAndAuditsConfiguration(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "capacity-target", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: repo.ID, Path: "widget", Digest: "sha256:widget", ObjectKey: "native/raw/widget", Size: 4}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{
		{Principal: "reader", Scopes: []string{"repositories:read"}},
		{Principal: "manager", Scopes: []string{"repositories:admin"}},
	}, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := func(method, actor, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/v2/repositories/"+repo.ID+"/capacity", strings.NewReader(body))
		authorize(r, authenticator.IssueToken(actor))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if response := request(http.MethodGet, "reader", ""); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"usedBytes":4`) || !strings.Contains(response.Body.String(), `"quotaBytes":0`) {
		t.Fatalf("reader capacity=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPut, "reader", `{"quotaBytes":10}`); response.Code != http.StatusForbidden {
		t.Fatalf("reader configure=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPut, "manager", `{"quotaBytes":-1}`); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid configure=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPut, "manager", `{"quotaBytes":10}`); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"quotaBytes":10`) {
		t.Fatalf("configure=%d body=%s", response.Code, response.Body.String())
	}
	if len(store.Audits) == 0 {
		t.Fatal("expected capacity configuration audit")
	}
	audit := store.Audits[len(store.Audits)-1]
	if audit.Outcome != repository.AuditResolved || audit.Actor != "manager" || audit.Operation != "capacity.configure" || audit.Resource != "repositories/"+repo.ID+"/capacity" {
		t.Fatalf("audit=%#v", audit)
	}
}

func TestRepositoryRetentionDryRunIsAdminOnlyAcrossFormatsAndDoesNotMutateArtifacts(t *testing.T) {
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
	if response := request(raw.ID, "admin-secret"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"candidates":[]`) {
		t.Fatalf("raw dry run=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(maven.ID, authenticator.IssueToken("retention-reader")); response.Code != http.StatusForbidden {
		t.Fatalf("reader dry run=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRepositoryRetentionDryRunPaginatesAndBindsPolicyVersion(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-page", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	otherRepo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-page-other", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryRetentionPolicy(ctx, repo.ID, repository.RepositoryRetentionPolicy{Enabled: true, KeepDays: 36500, SnapshotKeepDays: 36500, MinimumVersions: 1, MaximumVersions: 1}, "1"); err != nil {
		t.Fatal(err)
	}
	for index, version := range []string{"1.0.0", "1.1.0", "1.2.0"} {
		sessionID := uuid.NewString()
		coordinate := "org.example:pageable:" + version
		digest := "sha256:" + fmt.Sprintf("%064x", index+1)
		name := "pageable-" + version + ".jar"
		objectKey := "native/maven/pageable/" + sessionID
		session := repository.MavenPublishSession{ID: sessionID, RepositoryID: repo.ID, Coordinate: coordinate, Publisher: "test", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: name, Digest: digest, Size: 1}}}
		if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
			t.Fatal(err)
		}
		if err = store.MarkMavenPublishObject(ctx, sessionID, name, objectKey); err != nil {
			t.Fatal(err)
		}
		if _, err = store.CommitMavenPublishSession(ctx, sessionID, []repository.MavenAsset{{RepositoryID: repo.ID, Path: "org/example/pageable/" + version + "/" + name, ObjectKey: objectKey, Digest: digest, Size: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := func(target string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, target, nil)
		authorize(req, "admin-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	first := request("/api/v2/repositories/" + repo.ID + "/retention:dry-run?pageSize=1")
	if first.Code != http.StatusOK {
		t.Fatalf("first page=%d body=%s", first.Code, first.Body.String())
	}
	if !strings.Contains(first.Body.String(), `"summary":{"oldestCandidateAt":`) || !strings.Contains(first.Body.String(), `"maximumVersions":2`) || !strings.Contains(first.Body.String(), `"release":2`) {
		t.Fatalf("first page summary=%s", first.Body.String())
	}
	var firstPage adminopenapi.RetentionDryRun
	if err = json.NewDecoder(first.Body).Decode(&firstPage); err != nil {
		t.Fatal(err)
	}
	if firstPage.TotalCandidates != 2 || len(firstPage.Candidates) != 1 || firstPage.NextPageToken == nil {
		t.Fatalf("first page=%#v", firstPage)
	}
	second := request("/api/v2/repositories/" + repo.ID + "/retention:dry-run?pageSize=1&pageToken=" + url.QueryEscape(*firstPage.NextPageToken))
	if second.Code != http.StatusOK {
		t.Fatalf("second page=%d body=%s", second.Code, second.Body.String())
	}
	var secondPage adminopenapi.RetentionDryRun
	if err = json.NewDecoder(second.Body).Decode(&secondPage); err != nil {
		t.Fatal(err)
	}
	if len(secondPage.Candidates) != 1 || secondPage.Candidates[0].Coordinate == firstPage.Candidates[0].Coordinate || secondPage.NextPageToken != nil {
		t.Fatalf("second page=%#v", secondPage)
	}
	exported := request("/api/v2/repositories/" + repo.ID + "/retention:dry-run?output=csv&pageSize=1")
	if exported.Code != http.StatusOK || exported.Header().Get("Content-Type") != "text/csv; charset=utf-8" {
		t.Fatalf("export=%d content-type=%q body=%s", exported.Code, exported.Header().Get("Content-Type"), exported.Body.String())
	}
	lines := strings.Split(strings.TrimSpace(exported.Body.String()), "\n")
	if len(lines) != 3 || lines[0] != "format,coordinate,digest,createdAt,ageDays,versionType,reasons" || !strings.Contains(lines[1]+lines[2], "maximum_versions") {
		t.Fatalf("export lines=%#v", lines)
	}
	foreign := request("/api/v2/repositories/" + otherRepo.ID + "/retention:dry-run?pageSize=1&pageToken=" + url.QueryEscape(*firstPage.NextPageToken))
	if foreign.Code != http.StatusBadRequest || !strings.Contains(foreign.Body.String(), "invalid_page_token") {
		t.Fatalf("foreign repository page=%d body=%s", foreign.Code, foreign.Body.String())
	}
	expiredPayload, err := json.Marshal(retentionDryRunPageCursor{Endpoint: "retention-dry-run", RepositoryID: repo.ID, PolicyVersion: firstPage.PolicyVersion, Coordinate: firstPage.Candidates[0].Coordinate, ArtifactID: "expired-artifact", ExpiresAt: time.Now().UTC().Add(-time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	expiredMAC := hmac.New(sha256.New, []byte(authenticator.AdminToken))
	_, _ = expiredMAC.Write(expiredPayload)
	expiredToken := base64.RawURLEncoding.EncodeToString(append(expiredPayload, expiredMAC.Sum(nil)...))
	expired := request("/api/v2/repositories/" + repo.ID + "/retention:dry-run?pageSize=1&pageToken=" + url.QueryEscape(expiredToken))
	if expired.Code != http.StatusBadRequest || !strings.Contains(expired.Body.String(), "invalid_page_token") {
		t.Fatalf("expired page=%d body=%s", expired.Code, expired.Body.String())
	}
	updated, err := store.ReplaceRepositoryRetentionPolicy(ctx, repo.ID, repository.RepositoryRetentionPolicy{Enabled: true, KeepDays: 36500, SnapshotKeepDays: 36500, MinimumVersions: 1, MaximumVersions: 1}, secondPage.PolicyVersion)
	if err != nil {
		t.Fatal(err)
	}
	stale := request("/api/v2/repositories/" + repo.ID + "/retention:dry-run?pageSize=1&pageToken=" + url.QueryEscape(*firstPage.NextPageToken))
	if stale.Code != http.StatusBadRequest || !strings.Contains(stale.Body.String(), "invalid_page_token") {
		t.Fatalf("stale page=%d body=%s policy=%#v", stale.Code, stale.Body.String(), updated)
	}
}

func TestRepositoryRetentionExecutionEnqueuesIdempotentCrossFormatJobs(t *testing.T) {
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
	if _, err = store.ReplaceRepositoryRetentionPolicy(ctx, maven.ID, repository.RepositoryRetentionPolicy{Enabled: true, KeepDays: 30, MinimumVersions: 1}, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryRetentionPolicy(ctx, raw.ID, repository.RepositoryRetentionPolicy{Enabled: true, KeepDays: 30, MinimumVersions: 1}, "1"); err != nil {
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
	rawResponse := request(raw.ID)
	if rawResponse.Code != http.StatusAccepted || !strings.Contains(rawResponse.Body.String(), `"kind":"retention"`) {
		t.Fatalf("raw execute=%d body=%s", rawResponse.Code, rawResponse.Body.String())
	}
	if err = (NativeRepositoryRetention{Store: store}).RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.ListLifecycleJobs(ctx, maven.ID, 10)
	if err != nil || len(jobs) != 1 || jobs[0].State != repository.LifecycleJobCompleted {
		t.Fatalf("jobs=%#v err=%v", jobs, err)
	}
	rawJobs, err := store.ListLifecycleJobs(ctx, raw.ID, 10)
	if err != nil || len(rawJobs) != 1 || rawJobs[0].State != repository.LifecycleJobCompleted {
		t.Fatalf("raw jobs=%#v err=%v", rawJobs, err)
	}
}

func TestRepositoryRetentionExecutionChecksDryRunPolicyVersion(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "retention-if-match", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := store.ReplaceRepositoryRetentionPolicy(ctx, repo.ID, repository.RepositoryRetentionPolicy{Enabled: true, KeepDays: 30, MinimumVersions: 1}, "1")
	if err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := func(key, version string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/retention:execute", nil)
		authorize(req, "admin-secret")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("If-Match", version)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	if stale := request("retention-stale", "1"); stale.Code != http.StatusPreconditionFailed || !strings.Contains(stale.Body.String(), "version_conflict") {
		t.Fatalf("stale execute=%d body=%s", stale.Code, stale.Body.String())
	}
	if current := request("retention-current", policy.Version); current.Code != http.StatusAccepted {
		t.Fatalf("current execute=%d body=%s", current.Code, current.Body.String())
	}
	if _, err = store.ReplaceRepositoryRetentionPolicy(ctx, repo.ID, repository.RepositoryRetentionPolicy{Enabled: false, KeepDays: 30, MinimumVersions: 1}, policy.Version); err != nil {
		t.Fatal(err)
	}
	if disabled := request("retention-disabled", policy.Version); disabled.Code != http.StatusPreconditionFailed {
		// The version was incremented by the disable update, so the stale If-Match
		// is expected to fail before the disabled-policy guard.
		t.Fatalf("disabled execute=%d body=%s", disabled.Code, disabled.Body.String())
	}
	currentPolicy, err := store.GetRepositoryRetentionPolicy(ctx, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	disabled := request("retention-disabled-current", currentPolicy.Version)
	if disabled.Code != http.StatusConflict || !strings.Contains(disabled.Body.String(), "retention_disabled") {
		t.Fatalf("disabled current execute=%d body=%s", disabled.Code, disabled.Body.String())
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
	if response := request(raw.ID, "admin-secret", coordinate); response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "tombstone not found") {
		t.Fatalf("restore missing raw tombstone=%d body=%s", response.Code, response.Body.String())
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

func TestRepositoryRestoreRestoresOCIManifestAndTags(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "restore-oci", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	objectKey := "native/oci/manifests/restore"
	if err = store.StageOCIObjectIntent(ctx, repository.OCIObjectIntent{RepositoryID: repo.ID, ObjectKey: objectKey, Digest: digest, Size: 42}); err != nil {
		t.Fatal(err)
	}
	manifest, err := store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: repo.ID, Name: "team/widget", Digest: digest, ObjectKey: objectKey, MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 42}, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteOCIManifest(ctx, repo.ID, manifest.Name, manifest.Digest); err != nil {
		t.Fatal(err)
	}
	coordinate := manifest.Name + "@" + manifest.Digest
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/restore", strings.NewReader(`{"coordinate":"`+coordinate+`"}`))
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("restore=%d body=%s", response.Code, response.Body.String())
	}
	if restored, getErr := store.GetOCIManifest(ctx, repo.ID, manifest.Name, manifest.Digest); getErr != nil || restored.ObjectKey != objectKey {
		t.Fatalf("restored manifest=%#v err=%v", restored, getErr)
	}
	if restored, getErr := store.GetOCIManifest(ctx, repo.ID, manifest.Name, "1.0.0"); getErr != nil || restored.Digest != digest {
		t.Fatalf("restored tag=%#v err=%v", restored, getErr)
	}
	if _, getErr := store.GetArtifactTombstone(ctx, repo.ID, repository.FormatOCI, coordinate); !errors.Is(getErr, repository.ErrNotFound) {
		t.Fatalf("restore kept tombstone: %v", getErr)
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
	if err = store.FailLifecycleJob(ctx, failedID, claimed[0].LeaseToken, "object store unavailable"); err != nil {
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
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"retrying"`) || !strings.Contains(response.Body.String(), `"state":"pending"`) || !strings.Contains(response.Body.String(), `"lastError":"object store unavailable"`) || strings.Contains(response.Body.String(), "secret-object-key") || strings.Contains(response.Body.String(), "idempotencyKey") {
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

func TestRepositoryLifecycleJobControlsAreAdminOnlyAndAudited(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "job-controls", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	enqueue := func(id string, maxAttempts int) {
		t.Helper()
		if _, _, enqueueErr := store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: id, RepositoryID: repo.ID, Kind: repository.LifecycleJobReclaim, IdempotencyKey: id, Payload: []byte(`{"format":"raw"}`), MaxAttempts: maxAttempts}); enqueueErr != nil {
			t.Fatal(enqueueErr)
		}
	}
	cancelID, runID, failedID, pendingID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	enqueue(cancelID, 3)
	enqueue(runID, 3)
	claimed, err := store.ClaimLifecycleJobs(ctx, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != cancelID {
		t.Fatalf("first claim=%#v err=%v", claimed, err)
	}
	if err = store.FailLifecycleJob(ctx, cancelID, claimed[0].LeaseToken, "temporary"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RunLifecycleJobNow(ctx, repo.ID, cancelID); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.ClaimLifecycleJobs(ctx, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != cancelID {
		t.Fatalf("second claim=%#v err=%v", claimed, err)
	}
	if err = store.CompleteLifecycleJob(ctx, cancelID, claimed[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.ClaimLifecycleJobs(ctx, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != runID {
		t.Fatalf("run claim=%#v err=%v", claimed, err)
	}
	if err = store.FailLifecycleJob(ctx, runID, claimed[0].LeaseToken, "temporary"); err != nil {
		t.Fatal(err)
	}
	enqueue(failedID, 1)
	claimed, err = store.ClaimLifecycleJobs(ctx, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != failedID {
		t.Fatalf("failed claim=%#v err=%v", claimed, err)
	}
	if err = store.FailLifecycleJob(ctx, failedID, claimed[0].LeaseToken, "permanent"); err != nil {
		t.Fatal(err)
	}
	enqueue(pendingID, 3)

	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := func(id, action, token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+repo.ID+"/lifecycle-jobs/"+id+"/"+action, nil)
		authorize(req, token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	if response := request(pendingID, "cancel", "admin-secret"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"cancelled"`) {
		t.Fatalf("cancel=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(runID, "cancel", authenticator.IssueToken("reader")); response.Code != http.StatusForbidden {
		t.Fatalf("reader cancel=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(runID, "run", "admin-secret"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"pending"`) {
		t.Fatalf("run now=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(failedID, "retry", "admin-secret"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"pending"`) || !strings.Contains(response.Body.String(), `"attempts":0`) {
		t.Fatalf("retry=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(cancelID, "retry", "admin-secret"); response.Code != http.StatusConflict {
		t.Fatalf("completed retry=%d body=%s", response.Code, response.Body.String())
	}
	operations := map[string]bool{}
	for _, audit := range store.Audits {
		operations[audit.Operation] = true
	}
	for _, operation := range []string{"lifecycle.cancel", "lifecycle.run_now", "lifecycle.retry"} {
		if !operations[operation] {
			t.Fatalf("missing audit %q in %#v", operation, store.Audits)
		}
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

func TestConanRevisionManagementListsAndTombstonesSelectedRevisions(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	enableAnonymousAccess(t, store)
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "conan-revisions", Format: repository.FormatConan, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	const reference = "widget/1.0/user/stable"
	const recipeRevision = "recipe-r1"
	const packageID = "package-a"
	const packageRevision = "package-r1"
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err = store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: repo.ID, ObjectKey: "native/conan/recipe", Digest: digest, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: repo.ID, Reference: reference, Revision: recipeRevision, Digest: digest}, []repository.ConanAsset{{RepositoryID: repo.ID, Reference: reference, RecipeRevision: recipeRevision, Path: "conanfile.py", ObjectKey: "native/conan/recipe", Digest: digest, Size: 1}}); err != nil {
		t.Fatal(err)
	}
	if err = store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: repo.ID, ObjectKey: "native/conan/package", Digest: digest, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutConanPackageRevision(ctx, repository.ConanPackageRevision{RepositoryID: repo.ID, Reference: reference, RecipeRevision: recipeRevision, PackageID: packageID, Revision: packageRevision, Digest: digest}, []repository.ConanAsset{{RepositoryID: repo.ID, Reference: reference, RecipeRevision: recipeRevision, PackageID: packageID, PackageRevision: packageRevision, Path: "package.tgz", ObjectKey: "native/conan/package", Digest: digest, Size: 1}}); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := func(method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		authorize(req, "admin-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	recipeList := request(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/conan/recipe-revisions?reference="+url.QueryEscape(reference))
	if recipeList.Code != http.StatusOK || !strings.Contains(recipeList.Body.String(), `"revision":"recipe-r1"`) {
		t.Fatalf("recipe list=%d body=%s", recipeList.Code, recipeList.Body.String())
	}
	recipeListWithTrailingSlash := request(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/conan/recipe-revisions?reference="+url.QueryEscape(reference+"/"))
	if recipeListWithTrailingSlash.Code != http.StatusOK || !strings.Contains(recipeListWithTrailingSlash.Body.String(), `"revision":"recipe-r1"`) {
		t.Fatalf("recipe list with trailing slash=%d body=%s", recipeListWithTrailingSlash.Code, recipeListWithTrailingSlash.Body.String())
	}
	anonymousRecipeList := httptest.NewRecorder()
	handler.ServeHTTP(anonymousRecipeList, httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/conan/recipe-revisions?reference="+url.QueryEscape(reference), nil))
	if anonymousRecipeList.Code != http.StatusOK {
		t.Fatalf("anonymous recipe list=%d body=%s", anonymousRecipeList.Code, anonymousRecipeList.Body.String())
	}
	packageIDs := request(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/conan/package-ids?reference="+url.QueryEscape(reference)+"&recipeRevision="+recipeRevision)
	if packageIDs.Code != http.StatusOK || !strings.Contains(packageIDs.Body.String(), packageID) {
		t.Fatalf("package ids=%d body=%s", packageIDs.Code, packageIDs.Body.String())
	}
	emptyPackageIDs := request(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/conan/package-ids?reference="+url.QueryEscape(reference)+"&recipeRevision=missing")
	if emptyPackageIDs.Code != http.StatusOK || !strings.Contains(emptyPackageIDs.Body.String(), `"items":[]`) {
		t.Fatalf("empty package ids=%d body=%s", emptyPackageIDs.Code, emptyPackageIDs.Body.String())
	}
	packageList := request(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/conan/package-revisions?reference="+url.QueryEscape(reference)+"&recipeRevision="+recipeRevision+"&packageId="+packageID)
	if packageList.Code != http.StatusOK || !strings.Contains(packageList.Body.String(), `"revision":"package-r1"`) {
		t.Fatalf("package list=%d body=%s", packageList.Code, packageList.Body.String())
	}
	packageDelete := request(http.MethodDelete, "/api/v2/repositories/"+repo.ID+"/conan/package-revisions/"+packageRevision+"?reference="+url.QueryEscape(reference)+"&recipeRevision="+recipeRevision+"&packageId="+packageID)
	if packageDelete.Code != http.StatusNoContent {
		t.Fatalf("package delete=%d body=%s", packageDelete.Code, packageDelete.Body.String())
	}
	packageItem, err := store.GetConanPackageRevision(ctx, repo.ID, reference, recipeRevision, packageID, packageRevision)
	if err != nil || packageItem.State != "deleted" {
		t.Fatalf("package=%#v err=%v", packageItem, err)
	}
	recipe, err := store.GetConanRecipeRevision(ctx, repo.ID, reference, recipeRevision)
	if err != nil || recipe.State != "visible" {
		t.Fatalf("recipe=%#v err=%v", recipe, err)
	}
	proxy, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "conan-revisions-proxy", Format: repository.FormatConan, Type: repository.RepositoryTypeProxy, Endpoint: "https://conan.example", AllowedHosts: []string{"conan.example"}})
	if err != nil {
		t.Fatal(err)
	}
	proxyDelete := request(http.MethodDelete, "/api/v2/repositories/"+proxy.ID+"/conan/package-revisions/"+packageRevision+"?reference="+url.QueryEscape(reference)+"&recipeRevision="+recipeRevision+"&packageId="+packageID)
	if proxyDelete.Code != http.StatusBadRequest {
		t.Fatalf("proxy delete=%d body=%s", proxyDelete.Code, proxyDelete.Body.String())
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

func TestNativeRepositoryGuardAllowsAnonymousReadPolicyAndDeniesDisabledProtocols(t *testing.T) {
	store := repository.NewMemoryStore()
	enableAnonymousAccess(t, store)
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	for _, format := range []repository.Format{repository.FormatRaw, repository.FormatOCI, repository.FormatMaven} {
		repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: string(format) + "-native", Format: format, AnonymousRead: true})
		if err != nil {
			t.Fatal(err)
		}
		path := map[repository.Format]string{repository.FormatRaw: "/raw/raw-native/a", repository.FormatOCI: "/v2/oci-native/app/manifests/latest", repository.FormatMaven: "/maven/maven-native/a.pom"}[format]
		anonymous := httptest.NewRecorder()
		handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, path, nil))
		if anonymous.Code == http.StatusUnauthorized {
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

func TestRepositoryManagementUpdatesProxyConfiguration(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID:           uuid.NewString(),
		Name:         "raw-proxy",
		Format:       repository.FormatRaw,
		Type:         repository.RepositoryTypeProxy,
		Endpoint:     "https://upstream.example",
		AllowedHosts: []string{"upstream.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	patch := func(version, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPatch, "/api/v2/repositories/"+repo.ID, strings.NewReader(body))
		authorize(r, "admin-secret")
		r.Header.Set("If-Match", version)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		return rec
	}

	updated := patch("1", `{"endpoint":"https://cdn.example","allowedHosts":["cdn.example"]}`)
	if updated.Code != http.StatusOK {
		t.Fatalf("update=%d body=%s", updated.Code, updated.Body.String())
	}
	if etag := updated.Header().Get("ETag"); etag != "2" {
		t.Fatalf("etag=%q want 2", etag)
	}
	if !strings.Contains(updated.Body.String(), `"endpoint":"https://cdn.example"`) {
		t.Fatalf("body=%s", updated.Body.String())
	}
	persisted, _ := store.GetHostedRepository(context.Background(), repo.ID)
	if persisted.Endpoint != "https://cdn.example" || persisted.AllowedHosts[0] != "cdn.example" {
		t.Fatalf("persisted endpoint=%q hosts=%v", persisted.Endpoint, persisted.AllowedHosts)
	}

	if stale := patch("1", `{"endpoint":"https://other.example","allowedHosts":["other.example"]}`); stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale=%d body=%s", stale.Code, stale.Body.String())
	}
	if invalid := patch("2", `{"endpoint":"not-a-url","allowedHosts":["x"]}`); invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid=%d body=%s", invalid.Code, invalid.Body.String())
	}
	if missingHosts := patch("2", `{"endpoint":"https://cdn.example","allowedHosts":[]}`); missingHosts.Code != http.StatusBadRequest {
		t.Fatalf("missing hosts=%d body=%s", missingHosts.Code, missingHosts.Body.String())
	}

	hosted, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "raw-hosted", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	hostedPatch := httptest.NewRequest(http.MethodPatch, "/api/v2/repositories/"+hosted.ID, strings.NewReader(`{"endpoint":"https://x.example"}`))
	authorize(hostedPatch, "admin-secret")
	hostedPatch.Header.Set("If-Match", "1")
	hostedRec := httptest.NewRecorder()
	handler.ServeHTTP(hostedRec, hostedPatch)
	if hostedRec.Code != http.StatusBadRequest {
		t.Fatalf("hosted update=%d body=%s", hostedRec.Code, hostedRec.Body.String())
	}
}

func TestRepositoryManagementAnonymousReadPolicyDefaultsAndUpdates(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	create := func(body, key string) repository.HostedRepository {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(body))
		authorize(req, "admin-secret")
		req.Header.Set("Idempotency-Key", key)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create=%d body=%s", rec.Code, rec.Body.String())
		}
		var repo repository.HostedRepository
		if err := json.NewDecoder(rec.Body).Decode(&repo); err != nil {
			t.Fatal(err)
		}
		return repo
	}

	private := create(`{"name":"private-hosted","format":"raw"}`, "private-hosted")
	if private.AnonymousRead {
		t.Fatalf("anonymousRead defaulted to true: %#v", private)
	}
	public := create(`{"name":"public-hosted","format":"raw","anonymousRead":true}`, "public-hosted")
	if !public.AnonymousRead {
		t.Fatalf("anonymousRead was not returned on create: %#v", public)
	}

	patch := httptest.NewRequest(http.MethodPatch, "/api/v2/repositories/"+private.ID, strings.NewReader(`{"anonymousRead":true}`))
	authorize(patch, "admin-secret")
	patch.Header.Set("If-Match", "1")
	patched := httptest.NewRecorder()
	handler.ServeHTTP(patched, patch)
	if patched.Code != http.StatusOK || !strings.Contains(patched.Body.String(), `"anonymousRead":true`) || patched.Header().Get("ETag") != "2" {
		t.Fatalf("patch=%d etag=%q body=%s", patched.Code, patched.Header().Get("ETag"), patched.Body.String())
	}
	stored, err := store.GetHostedRepository(context.Background(), private.ID)
	if err != nil || !stored.AnonymousRead {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
}

func TestRepositoryEffectiveAccessReportsPermissionsAndAnonymousPolicy(t *testing.T) {
	store := repository.NewMemoryStore()
	enableAnonymousAccess(t, store)
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "effective-raw", Format: repository.FormatRaw, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)

	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/effective-access", nil)
	authorize(request, authenticator.IssueToken("reader"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("effective access = %d %s", response.Code, response.Body.String())
	}
	var body struct {
		Actor    string `json:"actor"`
		Identity struct {
			Kind string `json:"kind"`
		} `json:"identity"`
		AnonymousRead struct {
			Allowed bool   `json:"allowed"`
			Reason  string `json:"reason"`
		} `json:"anonymousRead"`
		Permissions struct {
			Read  struct{ Allowed bool } `json:"read"`
			Write struct{ Allowed bool } `json:"write"`
			Admin struct{ Allowed bool } `json:"admin"`
		} `json:"permissions"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Actor != "reader" || body.Identity.Kind != "static_resolver" || !body.AnonymousRead.Allowed || body.AnonymousRead.Reason != "repository_anonymous_read_enabled" || !body.Permissions.Read.Allowed || body.Permissions.Write.Allowed || body.Permissions.Admin.Allowed {
		t.Fatalf("effective access body=%#v", body)
	}

	denied := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/effective-access", nil)
	authorize(denied, authenticator.IssueToken("stranger"))
	deniedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deniedResponse, denied)
	if deniedResponse.Code != http.StatusOK || !strings.Contains(deniedResponse.Body.String(), `"actor":"stranger"`) || !strings.Contains(deniedResponse.Body.String(), `"read":{"allowed":false`) {
		t.Fatalf("denied effective access = %d %s", deniedResponse.Code, deniedResponse.Body.String())
	}

	if _, err := store.DisableHostedRepository(context.Background(), repo.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinalizeHostedRepositoryDeletion(context.Background(), repo.ID); err != nil {
		t.Fatal(err)
	}
	deleted := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/effective-access", nil)
	authorize(deleted, authenticator.IssueToken("reader"))
	deletedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deletedResponse, deleted)
	if deletedResponse.Code != http.StatusOK || !strings.Contains(deletedResponse.Body.String(), `"anonymousRead":{"allowed":false,"reason":"repository_not_active"`) {
		t.Fatalf("deleted effective access = %d %s", deletedResponse.Code, deletedResponse.Body.String())
	}
}

func TestRepositoryEffectiveAccessSupportsAdministratorSimulation(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "simulation-raw", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{{
		Principal: "build-agent", Scopes: []string{"repositories:read"}, ResourcePrefix: "releases/",
	}}, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)

	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/effective-access?actor=build-agent&resource=releases%2Fapp.bin", nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"simulated":true`) || !strings.Contains(response.Body.String(), `"resource":"releases/app.bin"`) || !strings.Contains(response.Body.String(), `"read":{"allowed":true,"reason":"scope_granted","source":"repository_grants"}`) {
		t.Fatalf("simulated grant = %d %s", response.Code, response.Body.String())
	}

	wrongResource := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/effective-access?actor=build-agent&resource=snapshots%2Fapp.bin", nil)
	authorize(wrongResource, "admin-secret")
	wrongResourceResponse := httptest.NewRecorder()
	handler.ServeHTTP(wrongResourceResponse, wrongResource)
	if wrongResourceResponse.Code != http.StatusOK || !strings.Contains(wrongResourceResponse.Body.String(), `"read":{"allowed":false`) {
		t.Fatalf("wrong resource = %d %s", wrongResourceResponse.Code, wrongResourceResponse.Body.String())
	}

	globalRole := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/effective-access?actor=release-bot&role=writer", nil)
	authorize(globalRole, "admin-secret")
	globalRoleResponse := httptest.NewRecorder()
	handler.ServeHTTP(globalRoleResponse, globalRole)
	if globalRoleResponse.Code != http.StatusOK || !strings.Contains(globalRoleResponse.Body.String(), `"read":{"allowed":true,"reason":"role_writer","source":"role"}`) || !strings.Contains(globalRoleResponse.Body.String(), `"write":{"allowed":true,"reason":"role_writer","source":"role"}`) || !strings.Contains(globalRoleResponse.Body.String(), `"admin":{"allowed":false`) {
		t.Fatalf("simulated role = %d %s", globalRoleResponse.Code, globalRoleResponse.Body.String())
	}

	forbidden := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/effective-access?actor=build-agent", nil)
	authorize(forbidden, authenticator.IssueToken("reader"))
	forbiddenResponse := httptest.NewRecorder()
	handler.ServeHTTP(forbiddenResponse, forbidden)
	if forbiddenResponse.Code != http.StatusForbidden {
		t.Fatalf("non-admin simulation = %d %s", forbiddenResponse.Code, forbiddenResponse.Body.String())
	}

	invalid := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/effective-access?role=reader", nil)
	authorize(invalid, "admin-secret")
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("role without actor = %d %s", invalidResponse.Code, invalidResponse.Body.String())
	}
}

func TestCurrentIdentityReportsSafeCredentialMetadata(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	request := httptest.NewRequest(http.MethodGet, "/api/v2/identity", nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"actor":"alice"`) || !strings.Contains(response.Body.String(), `"kind":"static_admin"`) || !strings.Contains(response.Body.String(), `"role":"admin"`) || !strings.Contains(response.Body.String(), `"administrator":true`) {
		t.Fatalf("identity = %d %s", response.Code, response.Body.String())
	}

	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v2/identity", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated identity = %d %s", unauthenticated.Code, unauthenticated.Body.String())
	}
}

func TestAnonymousRepositoryBrowseAllowsReadOnlyQueries(t *testing.T) {
	store := repository.NewMemoryStore()
	enableAnonymousAccess(t, store)
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "public-oci", Format: repository.FormatOCI, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator())
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`)
	put := httptest.NewRequest(http.MethodPut, "/v2/public-oci/app/manifests/latest", strings.NewReader(string(manifest)))
	put.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	authorize(put, "resolver-secret")
	published := httptest.NewRecorder()
	handler.ServeHTTP(published, put)
	if published.Code != http.StatusCreated {
		t.Fatalf("publish = %d %s", published.Code, published.Body.String())
	}

	browse := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/oci/images", nil)
	browseResponse := httptest.NewRecorder()
	handler.ServeHTTP(browseResponse, browse)
	if browseResponse.Code != http.StatusOK || !strings.Contains(browseResponse.Body.String(), `"app"`) {
		t.Fatalf("anonymous browse = %d %s", browseResponse.Code, browseResponse.Body.String())
	}

	private, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "private-oci", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	privateBrowse := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+private.ID+"/oci/images", nil)
	privateResponse := httptest.NewRecorder()
	handler.ServeHTTP(privateResponse, privateBrowse)
	if privateResponse.Code != http.StatusUnauthorized {
		t.Fatalf("private browse = %d %s", privateResponse.Code, privateResponse.Body.String())
	}
}

func TestGroupManagementAnonymousReadPolicyDefaultsAndUpdates(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "group-policy-repo", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	create := httptest.NewRequest(http.MethodPost, "/api/v2/groups", strings.NewReader(`{"name":"group-policy","format":"raw","members":[{"repositoryId":"`+repo.ID+`","position":0}]}`))
	authorize(create, "admin-secret")
	create.Header.Set("Idempotency-Key", "group-policy")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", created.Code, created.Body.String())
	}
	var group repository.HostedGroup
	if err := json.NewDecoder(created.Body).Decode(&group); err != nil || group.AnonymousRead {
		t.Fatalf("group=%#v err=%v", group, err)
	}

	replace := httptest.NewRequest(http.MethodPut, "/api/v2/groups/"+group.ID, strings.NewReader(`{"name":"group-policy","format":"raw","anonymousRead":true,"members":[{"repositoryId":"`+repo.ID+`","position":0}]}`))
	authorize(replace, "admin-secret")
	replace.Header.Set("If-Match", "1")
	replaced := httptest.NewRecorder()
	handler.ServeHTTP(replaced, replace)
	if replaced.Code != http.StatusOK || !strings.Contains(replaced.Body.String(), `"anonymousRead":true`) {
		t.Fatalf("replace=%d body=%s", replaced.Code, replaced.Body.String())
	}

	membersOnly := httptest.NewRequest(http.MethodPut, "/api/v2/groups/"+group.ID+"/members", strings.NewReader(`[{"repositoryId":"`+repo.ID+`","position":0}]`))
	authorize(membersOnly, "admin-secret")
	membersOnly.Header.Set("If-Match", "2")
	membersOnlyResponse := httptest.NewRecorder()
	handler.ServeHTTP(membersOnlyResponse, membersOnly)
	if membersOnlyResponse.Code != http.StatusOK || !strings.Contains(membersOnlyResponse.Body.String(), `"anonymousRead":true`) {
		t.Fatalf("members replace=%d body=%s", membersOnlyResponse.Code, membersOnlyResponse.Body.String())
	}
}

func TestAPIKeyRolesEnforceScopedManagementAccess(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID:           uuid.NewString(),
		Name:         "ci-proxy",
		Format:       repository.FormatRaw,
		Type:         repository.RepositoryTypeProxy,
		Endpoint:     "https://upstream.example",
		AllowedHosts: []string{"upstream.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	authenticator.APIKeys = store
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)

	createKey := func(t *testing.T, roles string) string {
		t.Helper()
		body := `{"name":"` + roles + `","roles":["` + roles + `"]}"`
		req := httptest.NewRequest(http.MethodPost, "/api/v2/api-keys", strings.NewReader(body))
		authorize(req, "admin-secret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s key=%d body=%s", roles, rec.Code, rec.Body.String())
		}
		var created struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.Token == "" {
			t.Fatalf("parse %s key token: %s", roles, rec.Body.String())
		}
		return created.Token
	}

	readerToken := createKey(t, "reader")
	writerToken := createKey(t, "writer")

	patch := func(token string) int {
		req := httptest.NewRequest(http.MethodPatch, "/api/v2/repositories/"+repo.ID, strings.NewReader(`{"endpoint":"https://cdn.example","allowedHosts":["cdn.example"]}`))
		authorize(req, token)
		req.Header.Set("If-Match", "1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	get := func(token string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID, nil)
		authorize(req, token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	// Reader: read allowed by role, write denied.
	if code := get(readerToken); code != http.StatusOK {
		t.Fatalf("reader get=%d", code)
	}
	if code := patch(readerToken); code != http.StatusForbidden {
		t.Fatalf("reader patch=%d want 403", code)
	}

	// Writer: read and write allowed by role.
	if code := get(writerToken); code != http.StatusOK {
		t.Fatalf("writer get=%d", code)
	}
	if code := patch(writerToken); code != http.StatusOK {
		t.Fatalf("writer patch=%d want 200", code)
	}

	// Neither reader nor writer may mint new keys (administrator-only).
	for _, token := range []string{readerToken, writerToken} {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/api-keys", strings.NewReader(`{"name":"x","roles":["admin"]}`))
		authorize(req, token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s role minted key=%d want 401", token, rec.Code)
		}
	}
}

func TestRepositoryManagementCancelsReplicationPlan(t *testing.T) {
	store := repository.NewMemoryStore()
	source, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "repl-src", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "repl-tgt", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	plan, _, err := store.CreateReplicationPlan(context.Background(), repository.ReplicationPlan{
		ID: uuid.NewString(), SourceRepositoryID: source.ID, TargetRepositoryID: target.ID, Format: repository.FormatRaw, IdempotencyKey: "cancel-key",
	}, []repository.ReplicationCheckpoint{{PlanID: uuid.NewString(), SourceObjectKey: "a", ObjectKey: "a", Digest: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	cancel := func(repoID, planID string) int {
		req := httptest.NewRequest(http.MethodDelete, "/api/v2/repositories/"+repoID+"/replications/"+planID, nil)
		authorize(req, "admin-secret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := cancel(source.ID, plan.ID); code != http.StatusNoContent {
		t.Fatalf("cancel pending=%d want 204", code)
	}
	persisted, _ := store.GetReplicationPlan(context.Background(), source.ID, plan.ID)
	if persisted.State != "cancelled" {
		t.Fatalf("state=%q want cancelled", persisted.State)
	}
	// Already cancelled is not cancellable.
	if code := cancel(source.ID, plan.ID); code != http.StatusConflict {
		t.Fatalf("cancel cancelled=%d want 409", code)
	}
	// Unknown plan id is not found.
	if code := cancel(source.ID, uuid.NewString()); code != http.StatusNotFound {
		t.Fatalf("cancel missing=%d want 404", code)
	}
	// Plan scoped to a different repository is not found through this path.
	other, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "repl-other", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if code := cancel(other.ID, plan.ID); code != http.StatusNotFound {
		t.Fatalf("cancel wrong repo=%d want 404", code)
	}
}

func TestUserManagementLoginAndSessionAuth(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	createUser := func(body string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/users", strings.NewReader(body))
		authorize(req, "admin-secret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := createUser(`{"name":"alice","password":"supersecret","role":"reader"}`); code != http.StatusCreated {
		t.Fatalf("create alice=%d", code)
	}
	if code := createUser(`{"name":"alice","password":"supersecret","role":"reader"}`); code != http.StatusConflict {
		t.Fatalf("duplicate alice=%d want 409", code)
	}
	if code := createUser(`{"name":"bob","password":"short","role":"admin"}`); code != http.StatusBadRequest {
		t.Fatalf("short password=%d want 400", code)
	}
	if code := createUser(`{"name":"root","password":"supersecret","role":"admin"}`); code != http.StatusCreated {
		t.Fatalf("create root=%d", code)
	}

	list := httptest.NewRequest(http.MethodGet, "/api/v2/users", nil)
	authorize(list, "admin-secret")
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, list)
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), `"name":"root"`) {
		t.Fatalf("list users=%d body=%s", listRec.Code, listRec.Body.String())
	}

	login := func(body string) (int, string) {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var resp struct {
			Token string `json:"token"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		return rec.Code, resp.Token
	}
	if code, _ := login(`{"username":"root","password":"wrong"}`); code != http.StatusUnauthorized {
		t.Fatalf("wrong password login=%d want 401", code)
	}
	code, token := login(`{"username":"root","password":"supersecret"}`)
	if code != http.StatusOK || token == "" {
		t.Fatalf("root login=%d token=%q", code, token)
	}

	// The admin session token can call an admin-only endpoint; a reader cannot.
	asSession := func(target string) int {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		authorize(req, token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := asSession("/api/v2/users"); code != http.StatusOK {
		t.Fatalf("admin session list users=%d want 200", code)
	}
	identityRequest := httptest.NewRequest(http.MethodGet, "/api/v2/identity", nil)
	authorize(identityRequest, token)
	identityResponse := httptest.NewRecorder()
	handler.ServeHTTP(identityResponse, identityRequest)
	if identityResponse.Code != http.StatusOK || !strings.Contains(identityResponse.Body.String(), `"kind":"local_session"`) || !strings.Contains(identityResponse.Body.String(), `"role":"admin"`) {
		t.Fatalf("admin session identity=%d body=%s", identityResponse.Code, identityResponse.Body.String())
	}

	_, readerToken := login(`{"username":"alice","password":"supersecret"}`)
	readerReq := httptest.NewRequest(http.MethodGet, "/api/v2/users", nil)
	authorize(readerReq, readerToken)
	readerRec := httptest.NewRecorder()
	handler.ServeHTTP(readerRec, readerReq)
	if readerRec.Code != http.StatusUnauthorized {
		t.Fatalf("reader session list users=%d want 401", readerRec.Code)
	}

	// Disabling a user blocks both new logins and the existing session.
	root, _ := store.GetUserByName(context.Background(), "root")
	patch := httptest.NewRequest(http.MethodPatch, "/api/v2/users/"+root.ID, strings.NewReader(`{"state":"disabled"}`))
	authorize(patch, "admin-secret")
	patch.Header.Set("If-Match", root.Version)
	patchRec := httptest.NewRecorder()
	handler.ServeHTTP(patchRec, patch)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("disable root=%d body=%s", patchRec.Code, patchRec.Body.String())
	}
	if code, _ := login(`{"username":"root","password":"supersecret"}`); code != http.StatusUnauthorized {
		t.Fatalf("disabled login=%d want 401", code)
	}
	if code := asSession("/api/v2/users"); code != http.StatusUnauthorized {
		t.Fatalf("disabled session=%d want 401", code)
	}

	del := httptest.NewRequest(http.MethodDelete, "/api/v2/users/"+root.ID, nil)
	authorize(del, "admin-secret")
	delRec := httptest.NewRecorder()
	handler.ServeHTTP(delRec, del)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete root=%d want 204", delRec.Code)
	}
}

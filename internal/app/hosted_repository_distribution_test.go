package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	conanprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/conan"
	mavenprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/maven"
	npmprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/npm"
	rawprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/raw"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
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

func TestRepositoryNPMPromotionPublishesTargetVersion(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "npm-promotion-source", Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "npm-promotion-target", Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	publishNPMGroupTestVersion(t, store, source, "widget", "1.0.0")
	sourceVersion, err := store.GetNPMVersionByTarball(ctx, source.ID, "widget", "widget-1.0.0.tgz")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+source.ID+"/promotions", strings.NewReader(`{"targetRepositoryId":"`+target.ID+`","coordinate":"widget@1.0.0","digest":"`+sourceVersion.Digest+`"}`))
	authorize(request, "admin-secret")
	request.Header.Set("Idempotency-Key", "npm-promote-widget")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("npm promotion=%d body=%s", response.Code, response.Body.String())
	}
	if err = (npmprotocol.NativePromotion{Store: store}).RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	pkg, err := store.GetNPMPackage(ctx, target.ID, "widget")
	if err != nil || len(pkg.Versions) != 1 || pkg.Versions[0].Version != "1.0.0" || pkg.Versions[0].Digest != sourceVersion.Digest {
		t.Fatalf("promoted npm package=%#v err=%v", pkg, err)
	}
	if _, err = store.GetNPMPackage(ctx, source.ID, "widget"); err != nil {
		t.Fatalf("promotion changed source: %v", err)
	}
}

func TestRepositoryNPMReplicationCopiesAndPublishesTargetVersion(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "npm-replication-source", Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "npm-replication-target", Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("replicated npm artifact")
	sum := sha256.Sum256(body)
	sourceVersion, err := store.PublishNPMVersion(ctx, repository.NPMVersion{
		RepositoryID: source.ID, PackageName: "widget", Version: "2.0.0",
		Digest: "sha256:" + fmt.Sprintf("%x", sum), Integrity: "sha512-YQ==", Shasum: strings.Repeat("b", 40),
		TarballName: "widget-2.0.0.tgz", ObjectKey: "native/npm/sha256/replication-source", Size: int64(len(body)),
		Manifest: json.RawMessage(`{"name":"widget","version":"2.0.0"}`), Publisher: "replication-test",
	}, map[string]string{"latest": "2.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.Put(ctx, sourceVersion.ObjectKey, body); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeNPMObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+source.ID+"/replications", strings.NewReader(`{"targetRepositoryId":"`+target.ID+`","coordinate":"widget@2.0.0","digest":"`+sourceVersion.Digest+`"}`))
	authorize(request, "admin-secret")
	request.Header.Set("Idempotency-Key", "npm-replicate-widget")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("npm replication=%d body=%s", response.Code, response.Body.String())
	}
	if err = (NPMReplication{Store: store, Source: objects, Destination: objects}).RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	replicated, err := store.GetNPMVersion(ctx, target.ID, "widget", "2.0.0")
	if err != nil || replicated.Digest != sourceVersion.Digest || replicated.ObjectKey == sourceVersion.ObjectKey {
		t.Fatalf("replicated npm version=%#v err=%v", replicated, err)
	}
	storedBody, err := objects.Get(ctx, replicated.ObjectKey)
	if err != nil || string(storedBody) != string(body) {
		t.Fatalf("replicated npm bytes=%q err=%v", storedBody, err)
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

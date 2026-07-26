//go:build integration

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	ociprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/oci"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresMinIOOCIPromotionCopiesManifestAndMountsBlob(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	accessKey := os.Getenv("TEST_S3_ACCESS_KEY")
	secretKey := os.Getenv("TEST_S3_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	objects, err := NewS3OCIObjectStore(endpoint, accessKey, secretKey, "promotion-oci-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:20])
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "promotion-oci-source-" + uuid.NewString(), Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "promotion-oci-target-" + uuid.NewString(), Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("OCI blob promoted with manifest")
	sum := sha256.Sum256(body)
	digest := "sha256:" + fmt.Sprintf("%x", sum[:])
	blobKey := "native/oci/blobs/sha256/" + fmt.Sprintf("%x", sum[:])
	if err = objects.PutVerifiedReader(ctx, blobKey, bytes.NewReader(body), int64(len(body)), digest); err != nil {
		t.Fatal(err)
	}
	upload, err := store.CreateOCIUpload(ctx, repository.OCIUpload{ID: uuid.NewString(), RepositoryID: source.ID, Name: "team/widget", ObjectKey: blobKey, State: "open", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.StageOCIObjectIntent(ctx, repository.OCIObjectIntent{RepositoryID: source.ID, ObjectKey: blobKey, Digest: digest, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CompleteOCIUpload(ctx, upload.ID, repository.OCIBlob{Digest: digest, ObjectKey: blobKey, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	manifestBody := []byte(`{"schemaVersion":2,"config":{"digest":"` + digest + `"}}`)
	manifestSum := sha256.Sum256(manifestBody)
	manifestDigest := "sha256:" + fmt.Sprintf("%x", manifestSum[:])
	manifestKey := "native/oci/manifests/" + source.ID + "/" + fmt.Sprintf("%x", manifestSum[:])
	if err = objects.PutVerifiedReader(ctx, manifestKey, bytes.NewReader(manifestBody), int64(len(manifestBody)), manifestDigest); err != nil {
		t.Fatal(err)
	}
	if err = store.StageOCIObjectIntent(ctx, repository.OCIObjectIntent{RepositoryID: source.ID, ObjectKey: manifestKey, Digest: manifestDigest, Size: int64(len(manifestBody))}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: source.ID, Name: "team/widget", Digest: manifestDigest, ObjectKey: manifestKey, MediaType: "application/vnd.oci.image.manifest.v1+json", Size: int64(len(manifestBody))}, manifestDigest); err != nil {
		t.Fatal(err)
	}
	job, _, err := (ociprotocol.NativePromotion{Store: store, Objects: objects}).Enqueue(ctx, target.ID, "postgres-minio-oci-promotion", ociprotocol.PromotionPayload{SourceRepositoryID: source.ID, Name: "team/widget", Digest: manifestDigest})
	if err != nil {
		t.Fatal(err)
	}
	if err = (ociprotocol.NativePromotion{Store: store, Objects: objects}).RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.ListLifecycleJobs(ctx, target.ID, 10)
	if err != nil || len(jobs) != 1 || jobs[0].ID != job.ID || jobs[0].State != repository.LifecycleJobCompleted {
		t.Fatalf("jobs=%#v err=%v", jobs, err)
	}
	promoted, err := store.GetOCIManifest(ctx, target.ID, "team/widget", manifestDigest)
	if err != nil || promoted.ObjectKey == manifestKey || promoted.Digest != manifestDigest {
		t.Fatalf("manifest=%#v err=%v", promoted, err)
	}
	if _, err = store.GetOCIBlob(ctx, target.ID, digest); err != nil {
		t.Fatalf("mounted blob: %v", err)
	}
	if info, err := objects.Stat(ctx, promoted.ObjectKey); err != nil || info.Digest != manifestDigest || info.Size != int64(len(manifestBody)) {
		t.Fatalf("target manifest object=%#v err=%v", info, err)
	}
}

func TestPostgresNativeOCIStateTransitions(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "oci-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20], Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	upload, err := store.CreateOCIUpload(ctx, repository.OCIUpload{ID: uuid.NewString(), RepositoryID: repo.ID, Name: "widget", ObjectKey: "native/oci/uploads/" + uuid.NewString(), State: "open", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if upload, err = store.UpdateOCIUpload(ctx, upload.ID, 12); err != nil || upload.Offset != 12 {
		t.Fatalf("update upload=%#v err=%v", upload, err)
	}
	blob, err := store.CompleteOCIUpload(ctx, upload.ID, repository.OCIBlob{Digest: digest, ObjectKey: "native/oci/blobs/sha256/" + strings.Repeat("a", 64), Size: 12})
	if err != nil || blob.Digest != digest {
		t.Fatalf("complete blob=%#v err=%v", blob, err)
	}
	if _, err = store.MountOCIBlob(ctx, repo.ID, digest); err != nil {
		t.Fatalf("idempotent mount: %v", err)
	}
	manifestDigest := "sha256:" + strings.Repeat("b", 64)
	manifestObjectKey := "native/oci/manifests/" + uuid.NewString()
	if err := store.StageOCIObjectIntent(ctx, repository.OCIObjectIntent{RepositoryID: repo.ID, ObjectKey: manifestObjectKey, Digest: manifestDigest, Size: 42}); err != nil {
		t.Fatal(err)
	}
	manifest, err := store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: repo.ID, Name: "widget", Digest: manifestDigest, ObjectKey: manifestObjectKey, MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 42}, "latest")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := store.GetOCIManifest(ctx, repo.ID, "widget", "latest")
	if err != nil || resolved.Digest != manifest.Digest {
		t.Fatalf("tag resolution=%#v err=%v", resolved, err)
	}
	if err = store.DeleteOCIManifest(ctx, repo.ID, "widget", manifest.Digest); err != nil {
		t.Fatalf("delete manifest: %v", err)
	}
	if _, err = store.GetOCIManifest(ctx, repo.ID, "widget", "latest"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("deleted tag lookup=%v", err)
	}
	intents, err := store.ListUnclaimedOCIObjectIntents(ctx, time.Now().Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	intentFound := false
	for _, intent := range intents {
		if intent.RepositoryID == repo.ID && intent.ObjectKey == manifestObjectKey {
			intentFound = true
			break
		}
	}
	if !intentFound {
		t.Fatalf("reclaim intents=%#v err=%v", intents, err)
	}
	tombstone, err := store.GetArtifactTombstone(ctx, repo.ID, repository.FormatOCI, "widget@"+manifest.Digest)
	if err != nil || tombstone.Digest != manifest.Digest || tombstone.TombstonedAt.IsZero() {
		t.Fatalf("tombstone=%#v err=%v", tombstone, err)
	}
}

func TestPostgresLifecycleJobsAreIdempotentAndClaimedOnce(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "jobs-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20], Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	job := repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: repo.ID, Kind: repository.LifecycleJobReclaim, IdempotencyKey: "reclaim-" + uuid.NewString(), Payload: []byte(`{"object":"intent-1"}`)}
	created, replayed, err := store.EnqueueLifecycleJob(ctx, job)
	if err != nil || replayed || created.State != repository.LifecycleJobPending {
		t.Fatalf("created=%#v replayed=%v err=%v", created, replayed, err)
	}
	replay, replayed, err := store.EnqueueLifecycleJob(ctx, job)
	if err != nil || !replayed || replay.ID != job.ID {
		t.Fatalf("replay=%#v replayed=%v err=%v", replay, replayed, err)
	}
	claimed, err := store.ClaimLifecycleJobs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	jobClaimed := false
	for _, candidate := range claimed {
		if candidate.ID == job.ID && candidate.State == repository.LifecycleJobRunning {
			jobClaimed = true
			break
		}
	}
	if !jobClaimed {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	if err := store.CompleteLifecycleJob(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	ociJob := repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: repo.ID, Kind: repository.LifecycleJobReclaim, IdempotencyKey: "oci-" + uuid.NewString(), Payload: []byte(`{"format":"oci","objectKey":"oci-object"}`)}
	mavenJob := repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: repo.ID, Kind: repository.LifecycleJobReclaim, IdempotencyKey: "maven-" + uuid.NewString(), Payload: []byte(`{"format":"maven","objectKey":"maven-object"}`)}
	if _, _, err = store.EnqueueLifecycleJob(ctx, ociJob); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.EnqueueLifecycleJob(ctx, mavenJob); err != nil {
		t.Fatal(err)
	}
	ociClaimed, err := store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobReclaim, repository.FormatOCI, 10)
	if err != nil {
		t.Fatal(err)
	}
	ociJobClaimed := false
	for _, candidate := range ociClaimed {
		if candidate.ID == ociJob.ID {
			ociJobClaimed = true
			break
		}
	}
	if !ociJobClaimed {
		t.Fatalf("OCI claimed=%#v err=%v", ociClaimed, err)
	}
	mavenClaimed, err := store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobReclaim, repository.FormatMaven, 10)
	if err != nil {
		t.Fatal(err)
	}
	mavenJobClaimed := false
	for _, candidate := range mavenClaimed {
		if candidate.ID == mavenJob.ID {
			mavenJobClaimed = true
			break
		}
	}
	if !mavenJobClaimed {
		t.Fatalf("Maven claimed=%#v err=%v", mavenClaimed, err)
	}
	if err = store.CompleteLifecycleJob(ctx, ociJob.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.CompleteLifecycleJob(ctx, mavenJob.ID); err != nil {
		t.Fatal(err)
	}
}

func TestNativeOCIHostedHTTPAcrossPostgresAndMinIOGatewayInstances(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	s3Endpoint := os.Getenv("TEST_S3_ENDPOINT")
	accessKey := os.Getenv("TEST_S3_ACCESS_KEY")
	secretKey := os.Getenv("TEST_S3_SECRET_KEY")
	if databaseURL == "" || s3Endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	storeA, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.Close()
	storeB, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeB.Close()
	bucket := "native-oci-http-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	objectsA, err := NewS3OCIObjectStore(s3Endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err = objectsA.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	objectsB, err := NewS3OCIObjectStore(s3Endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:18]
	source, err := storeA.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "oci-src-" + suffix, Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	target, err := storeA.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "oci-dst-" + suffix, Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	serverA := httptest.NewServer(NewGatewayHandler(Dependencies{NativeOCIObjectStore: objectsA}, storeA, TestAdapter{}, testAuthenticator()))
	defer serverA.Close()
	serverB := httptest.NewServer(NewGatewayHandler(Dependencies{NativeOCIObjectStore: objectsB}, storeB, TestAdapter{}, testAuthenticator()))
	defer serverB.Close()
	client := serverA.Client()
	request := func(method, address string, body []byte) *http.Response {
		t.Helper()
		r, err := http.NewRequest(method, address, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer resolver-secret")
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	body := []byte("cross-instance OCI blob")
	sum := sha256.Sum256(body)
	digest := "sha256:" + fmt.Sprintf("%x", sum[:])
	start := request(http.MethodPost, serverA.URL+"/v2/"+source.Name+"/app/blobs/uploads/", nil)
	if start.StatusCode != http.StatusAccepted {
		t.Fatalf("start upload=%d", start.StatusCode)
	}
	location := start.Header.Get("Location")
	_ = start.Body.Close()
	if location == "" {
		t.Fatal("upload location is missing")
	}
	complete := request(http.MethodPut, serverA.URL+location+"?digest="+digest, body)
	if complete.StatusCode != http.StatusCreated {
		t.Fatalf("complete upload=%d", complete.StatusCode)
	}
	_ = complete.Body.Close()
	mount := request(http.MethodPost, serverB.URL+"/v2/"+target.Name+"/app/blobs/uploads/?mount="+digest+"&from="+source.Name+"/app", nil)
	if mount.StatusCode != http.StatusCreated {
		t.Fatalf("cross-instance mount=%d", mount.StatusCode)
	}
	_ = mount.Body.Close()
	manifest := []byte(`{"schemaVersion":2,"config":{"digest":"` + digest + `","size":` + fmt.Sprint(len(body)) + `}}`)
	publish := request(http.MethodPut, serverB.URL+"/v2/"+target.Name+"/app/manifests/latest", manifest)
	if publish.StatusCode != http.StatusCreated {
		t.Fatalf("cross-instance manifest publish=%d", publish.StatusCode)
	}
	_ = publish.Body.Close()
	read := request(http.MethodGet, serverA.URL+"/v2/"+target.Name+"/app/manifests/latest", nil)
	defer read.Body.Close()
	got, err := io.ReadAll(read.Body)
	if err != nil {
		t.Fatal(err)
	}
	if read.StatusCode != http.StatusOK || !bytes.Equal(got, manifest) {
		t.Fatalf("cross-instance manifest read=%d body=%q", read.StatusCode, got)
	}
}

func TestNativeOCIReferrersAndCatalogAcrossPostgresAndMinIOGatewayInstances(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	s3Endpoint := os.Getenv("TEST_S3_ENDPOINT")
	accessKey := os.Getenv("TEST_S3_ACCESS_KEY")
	secretKey := os.Getenv("TEST_S3_SECRET_KEY")
	if databaseURL == "" || s3Endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}
	storeA, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.Close()
	storeB, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeB.Close()
	bucket := "native-oci-referrers-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	objectsA, err := NewS3OCIObjectStore(s3Endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err = objectsA.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	objectsB, err := NewS3OCIObjectStore(s3Endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:18]
	first, err := storeA.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "catalog-a-" + suffix, Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	second, err := storeA.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "catalog-b-" + suffix, Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	serverA := httptest.NewServer(NewGatewayHandler(Dependencies{NativeOCIObjectStore: objectsA}, storeA, TestAdapter{}, testAuthenticator()))
	defer serverA.Close()
	serverB := httptest.NewServer(NewGatewayHandler(Dependencies{NativeOCIObjectStore: objectsB}, storeB, TestAdapter{}, testAuthenticator()))
	defer serverB.Close()
	request := func(method, address string, body []byte) *http.Response {
		t.Helper()
		r, err := http.NewRequest(method, address, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer resolver-secret")
		response, err := serverA.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	publish := func(repositoryName, image, tag string, body []byte) string {
		t.Helper()
		response := request(http.MethodPut, serverA.URL+"/v2/"+repositoryName+"/"+image+"/manifests/"+tag, body)
		defer response.Body.Close()
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("publish %s/%s=%d", repositoryName, image, response.StatusCode)
		}
		return response.Header.Get("Docker-Content-Digest")
	}
	subject := publish(first.Name, "app", "subject", []byte(`{"schemaVersion":2}`))
	referrer := publish(first.Name, "app", "signature", []byte(`{"schemaVersion":2,"artifactType":"application/vnd.example.signature","subject":{"digest":"`+subject+`"}}`))
	publish(first.Name, "app-extra", "latest", []byte(`{"schemaVersion":2}`))
	publish(second.Name, "other", "latest", []byte(`{"schemaVersion":2}`))
	for _, repo := range []repository.HostedRepository{first, second} {
		if _, err = storeA.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{
			{Principal: "build-agent", Scopes: []string{"repositories:read", "repositories:write"}},
			{Principal: "catalog-reader", Scopes: []string{"repositories:read"}},
		}, "1"); err != nil {
			t.Fatal(err)
		}
	}

	listReferrers, err := http.NewRequest(http.MethodGet, serverB.URL+"/v2/"+first.Name+"/app/referrers/"+subject, nil)
	if err != nil {
		t.Fatal(err)
	}
	listReferrers.Header.Set("Authorization", "Bearer resolver-secret")
	referrerResponse, err := serverB.Client().Do(listReferrers)
	if err != nil {
		t.Fatal(err)
	}
	defer referrerResponse.Body.Close()
	var referrerPage struct {
		Manifests []struct {
			Digest       string `json:"digest"`
			ArtifactType string `json:"artifactType"`
		} `json:"manifests"`
	}
	if err := json.NewDecoder(referrerResponse.Body).Decode(&referrerPage); err != nil {
		t.Fatal(err)
	}
	if referrerResponse.StatusCode != http.StatusOK || len(referrerPage.Manifests) != 1 || referrerPage.Manifests[0].Digest != referrer || referrerPage.Manifests[0].ArtifactType != "application/vnd.example.signature" {
		t.Fatalf("referrers status=%d page=%#v", referrerResponse.StatusCode, referrerPage)
	}

	catalogRequest, err := http.NewRequest(http.MethodGet, serverB.URL+"/v2/_catalog?n=2", nil)
	if err != nil {
		t.Fatal(err)
	}
	catalogRequest.Header.Set("Authorization", "Bearer "+testAuthenticator().IssueToken("catalog-reader"))
	catalogResponse, err := serverB.Client().Do(catalogRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer catalogResponse.Body.Close()
	var catalogPage struct {
		Repositories []string `json:"repositories"`
	}
	if err := json.NewDecoder(catalogResponse.Body).Decode(&catalogPage); err != nil {
		t.Fatal(err)
	}
	if catalogResponse.StatusCode != http.StatusOK || len(catalogPage.Repositories) != 2 || catalogPage.Repositories[0] != first.Name+"/app" || catalogPage.Repositories[1] != first.Name+"/app-extra" || catalogResponse.Header.Get("Link") == "" {
		t.Fatalf("catalog status=%d page=%#v link=%q", catalogResponse.StatusCode, catalogPage, catalogResponse.Header.Get("Link"))
	}
	nextCatalogRequest, err := http.NewRequest(http.MethodGet, serverB.URL+"/v2/_catalog?n=2&last="+url.QueryEscape(catalogPage.Repositories[1]), nil)
	if err != nil {
		t.Fatal(err)
	}
	nextCatalogRequest.Header.Set("Authorization", "Bearer "+testAuthenticator().IssueToken("catalog-reader"))
	nextCatalogResponse, err := serverB.Client().Do(nextCatalogRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer nextCatalogResponse.Body.Close()
	var nextCatalogPage struct {
		Repositories []string `json:"repositories"`
	}
	if err := json.NewDecoder(nextCatalogResponse.Body).Decode(&nextCatalogPage); err != nil {
		t.Fatal(err)
	}
	if nextCatalogResponse.StatusCode != http.StatusOK || len(nextCatalogPage.Repositories) != 1 || nextCatalogPage.Repositories[0] != second.Name+"/other" {
		t.Fatalf("next catalog status=%d page=%#v", nextCatalogResponse.StatusCode, nextCatalogPage)
	}
	browseRequest, err := http.NewRequest(http.MethodGet, serverB.URL+"/api/v2/repositories/"+first.ID+"/oci/images?q=app&pageSize=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	browseRequest.Header.Set("Authorization", "Bearer resolver-secret")
	browseResponse, err := serverB.Client().Do(browseRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer browseResponse.Body.Close()
	var browsePage struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
		NextPageToken string `json:"nextPageToken"`
	}
	if err := json.NewDecoder(browseResponse.Body).Decode(&browsePage); err != nil {
		t.Fatal(err)
	}
	if browseResponse.StatusCode != http.StatusOK || len(browsePage.Items) != 1 || browsePage.Items[0].Name != "app" || browsePage.NextPageToken == "" {
		t.Fatalf("browse status=%d page=%#v", browseResponse.StatusCode, browsePage)
	}
	nextBrowseRequest, err := http.NewRequest(http.MethodGet, serverB.URL+"/api/v2/repositories/"+first.ID+"/oci/images?q=app&pageSize=1&pageToken="+url.QueryEscape(browsePage.NextPageToken), nil)
	if err != nil {
		t.Fatal(err)
	}
	nextBrowseRequest.Header.Set("Authorization", "Bearer resolver-secret")
	nextBrowseResponse, err := serverB.Client().Do(nextBrowseRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer nextBrowseResponse.Body.Close()
	var nextBrowsePage struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.NewDecoder(nextBrowseResponse.Body).Decode(&nextBrowsePage); err != nil {
		t.Fatal(err)
	}
	if nextBrowseResponse.StatusCode != http.StatusOK || len(nextBrowsePage.Items) != 1 || nextBrowsePage.Items[0].Name != "app-extra" {
		t.Fatalf("next browse status=%d page=%#v", nextBrowseResponse.StatusCode, nextBrowsePage)
	}

	deleted := request(http.MethodDelete, serverA.URL+"/v2/"+first.Name+"/app/manifests/"+referrer, nil)
	if deleted.StatusCode != http.StatusAccepted {
		_ = deleted.Body.Close()
		t.Fatalf("delete=%d", deleted.StatusCode)
	}
	_ = deleted.Body.Close()
	remainingRequest, err := http.NewRequest(http.MethodGet, serverB.URL+"/v2/"+first.Name+"/app/referrers/"+subject, nil)
	if err != nil {
		t.Fatal(err)
	}
	remainingRequest.Header.Set("Authorization", "Bearer resolver-secret")
	remainingResponse, err := serverB.Client().Do(remainingRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer remainingResponse.Body.Close()
	var remaining struct {
		Manifests []json.RawMessage `json:"manifests"`
	}
	if err := json.NewDecoder(remainingResponse.Body).Decode(&remaining); err != nil {
		t.Fatal(err)
	}
	if remainingResponse.StatusCode != http.StatusOK || len(remaining.Manifests) != 0 {
		t.Fatalf("remaining status=%d manifests=%d", remainingResponse.StatusCode, len(remaining.Manifests))
	}
}

func TestPostgresNativeOCIUploadLockSerializesConnections(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	first, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	release, err := first.LockOCIUpload(context.Background(), "cross-instance-upload")
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan func(), 1)
	errs := make(chan error, 1)
	go func() {
		unlock, lockErr := second.LockOCIUpload(context.Background(), "cross-instance-upload")
		if lockErr != nil {
			errs <- lockErr
			return
		}
		acquired <- unlock
	}()
	select {
	case <-acquired:
		t.Fatal("second connection acquired upload lock before release")
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case unlock := <-acquired:
		unlock()
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("second connection did not acquire upload lock")
	}
}

func TestPostgresNativeOCIObjectLockSerializesConnections(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	first, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	key := "native/oci/manifests/cross-instance-" + uuid.NewString()
	release, err := first.LockOCIObject(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan func(), 1)
	errs := make(chan error, 1)
	go func() {
		unlock, lockErr := second.LockOCIObject(context.Background(), key)
		if lockErr != nil {
			errs <- lockErr
			return
		}
		acquired <- unlock
	}()
	select {
	case <-acquired:
		t.Fatal("second connection acquired object lock before release")
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case unlock := <-acquired:
		unlock()
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("second connection did not acquire object lock")
	}
}

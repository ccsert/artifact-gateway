package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestRepositoryArtifactUsageCountsRealDownloads(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "npm-usage", Format: repository.FormatNPM, AnonymousRead: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceAnonymousAccessPolicy(ctx, repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{
		{Principal: "build-agent", Scopes: []string{"repositories:write"}},
		{Principal: "usage-reader", Scopes: []string{"repositories:read"}},
	}, "1"); err != nil {
		t.Fatal(err)
	}

	handler := NewGatewayHandler(Dependencies{NativeNPMObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator())
	tarball := npmFixtureTarball(t, "@scope/widget", "1.2.3")
	publishBody := npmFixturePublishDocument(t, "@scope/widget", "1.2.3", "@scope/widget-1.2.3.tgz", tarball)

	publish := httptest.NewRequest(http.MethodPut, "/npm/npm-usage/@scope%2Fwidget", strings.NewReader(publishBody))
	publish.Header.Set("Authorization", "Bearer resolver-secret")
	published := httptest.NewRecorder()
	handler.ServeHTTP(published, publish)
	if published.Code != http.StatusCreated {
		t.Fatalf("publish=%d body=%s", published.Code, published.Body.String())
	}

	// Two real downloads count; HEAD probes and failed lookups do not.
	for i := 0; i < 2; i++ {
		download := httptest.NewRecorder()
		handler.ServeHTTP(download, httptest.NewRequest(http.MethodGet, "/npm/npm-usage/@scope/widget/-/widget-1.2.3.tgz", nil))
		if download.Code != http.StatusOK || download.Body.Len() != len(tarball) {
			t.Fatalf("download=%d bytes=%d", download.Code, download.Body.Len())
		}
	}
	head := httptest.NewRecorder()
	handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/npm/npm-usage/@scope/widget/-/widget-1.2.3.tgz", nil))
	if head.Code != http.StatusOK {
		t.Fatalf("head=%d", head.Code)
	}
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/npm/npm-usage/@scope/widget/-/widget-9.9.9.tgz", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing=%d", missing.Code)
	}

	authenticator := testAuthenticator()
	usage := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/artifact-usage", nil)
	authorize(usage, authenticator.IssueToken("usage-reader"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, usage)

	var payload struct {
		RepositoryId string `json:"repositoryId"`
		Totals       struct {
			DownloadCount int64 `json:"downloadCount"`
			TotalBytes    int64 `json:"totalBytes"`
			Resources     int64 `json:"resources"`
		} `json:"totals"`
		Items []struct {
			Format        string `json:"format"`
			Resource      string `json:"resource"`
			DownloadCount int64  `json:"downloadCount"`
			TotalBytes    int64  `json:"totalBytes"`
			LastActor     string `json:"lastActor"`
		} `json:"items"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &payload) != nil {
		t.Fatalf("usage status=%d body=%s", response.Code, response.Body.String())
	}
	if payload.RepositoryId != repo.ID || payload.Totals.DownloadCount != 2 || payload.Totals.Resources != 1 || payload.Totals.TotalBytes != int64(2*len(tarball)) {
		t.Fatalf("totals=%#v body=%s", payload.Totals, response.Body.String())
	}
	if len(payload.Items) != 1 {
		t.Fatalf("items=%#v", payload.Items)
	}
	item := payload.Items[0]
	if item.Format != "npm" || item.Resource != "@scope/widget@1.2.3" || item.DownloadCount != 2 || item.TotalBytes != int64(2*len(tarball)) {
		t.Fatalf("item=%#v", item)
	}

	unauthorized := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/artifact-usage", nil)
	unauthorizedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous usage=%d", unauthorizedResponse.Code)
	}

	invalidLimit := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/artifact-usage?limit=9999", nil)
	authorize(invalidLimit, authenticator.IssueToken("usage-reader"))
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalidLimit)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit=%d", invalidResponse.Code)
	}
}

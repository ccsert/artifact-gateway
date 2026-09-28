package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestNativeCargoManagementBrowseAndSearchDeepLink(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "cargo-management", Format: repository.FormatCargo,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeCargoObjectStore: NewMemoryOCIObjectStore()},
		store, TestAdapter{}, testAuthenticator())
	body, _ := cargoC0PublishFixture(t, "demo-crate", "1.2.3", "demo-crate")
	publish := httptest.NewRequest(http.MethodPut, "/cargo/"+repo.Name+"/api/v1/crates/new", bytes.NewReader(body))
	publish.Host = "localhost"
	publish.Header.Set("Authorization", "admin-secret")
	published := httptest.NewRecorder()
	handler.ServeHTTP(published, publish)
	if published.Code != http.StatusOK {
		t.Fatalf("publish=%d body=%s", published.Code, published.Body.String())
	}
	_, root := browseRepositoryForTest(t, handler, "admin-secret", repo.ID, "", "", 50)
	if len(root.Items) != 1 || root.Items[0].Kind != "component" || root.Items[0].Name != "demo-crate" {
		t.Fatalf("Cargo browse root=%+v", root)
	}
	_, versions := browseRepositoryForTest(t, handler, "admin-secret", repo.ID, root.Items[0].ID, "", 50)
	if len(versions.Items) != 1 || versions.Items[0].Kind != "version" || versions.Items[0].Name != "1.2.3" {
		t.Fatalf("Cargo versions=%+v", versions)
	}
	_, assets := browseRepositoryForTest(t, handler, "admin-secret", repo.ID, versions.Items[0].ID, "", 50)
	if len(assets.Items) != 1 || assets.Items[0].Kind != "asset" || assets.Items[0].Path != "api/v1/crates/demo-crate/1.2.3/download" ||
		assets.Items[0].Digest == "" {
		t.Fatalf("Cargo asset=%+v", assets)
	}
	search := func(path string) (int, []byte) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		authorize(request, "admin-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response.Code, response.Body.Bytes()
	}
	localPath := "/api/v2/repositories/" + repo.ID + "/artifact-search?q=" + url.QueryEscape("demo")
	status, local := search(localPath)
	var localPage struct {
		Items []struct {
			Coordinate string `json:"coordinate"`
			Version    string `json:"version"`
		} `json:"items"`
	}
	if status != http.StatusOK || json.Unmarshal(local, &localPage) != nil || len(localPage.Items) != 1 ||
		localPage.Items[0].Coordinate != "demo-crate" || localPage.Items[0].Version != "1.2.3" {
		t.Fatalf("local Cargo search=%d body=%s", status, local)
	}
	status, global := search("/api/v2/artifact-search?q=demo&format=cargo")
	if status != http.StatusOK || !strings.Contains(string(global), `"coordinate":"demo-crate"`) ||
		!strings.Contains(string(global), `"repositoryId":"`+repo.ID+`"`) {
		t.Fatalf("global Cargo search=%d body=%s", status, global)
	}
	yank := httptest.NewRequest(http.MethodDelete, "/cargo/"+repo.Name+"/api/v1/crates/demo-crate/1.2.3/yank", nil)
	authorize(yank, "admin-secret")
	yanked := httptest.NewRecorder()
	handler.ServeHTTP(yanked, yank)
	if yanked.Code != http.StatusOK {
		t.Fatalf("yank=%d body=%s", yanked.Code, yanked.Body.String())
	}
	status, local = search(localPath)
	if status != http.StatusOK || json.Unmarshal(local, &localPage) != nil || len(localPage.Items) != 1 ||
		localPage.Items[0].Coordinate != "demo-crate" {
		t.Fatalf("management search lost fully yanked crate=%d body=%s", status, local)
	}
	status, global = search("/api/v2/artifact-search?q=demo&format=cargo")
	if status != http.StatusOK || !strings.Contains(string(global), `"coordinate":"demo-crate"`) {
		t.Fatalf("global search lost fully yanked crate=%d body=%s", status, global)
	}
}

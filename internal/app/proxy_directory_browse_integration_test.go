//go:build integration

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type noBrowseObjectReads struct {
	OCIObjectStore
	t *testing.T
}

func (s noBrowseObjectReads) List(context.Context, string) ([]string, error) {
	s.t.Fatal("directory query scanned object storage")
	return nil, nil
}

func (s noBrowseObjectReads) Get(context.Context, string) ([]byte, error) {
	s.t.Fatal("directory query read object bytes or a legacy cache index")
	return nil, nil
}

func proxyDirectoryFixture(t *testing.T, format repository.Format) (*PostgresCacheControlStore, *repository.MemoryStore, repository.HostedRepository, http.Handler) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("PostgreSQL integration environment is required")
	}
	control, err := NewPostgresCacheControlStore(noBrowseObjectReads{NewMemoryOCIObjectStore(), t}, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "browse-" + uuid.NewString(), Format: format,
		Type: repository.RepositoryTypeProxy, Endpoint: "https://cache.example.test/repository",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = control.db.Exec(`DELETE FROM cache_control_entries WHERE key LIKE $1`, string(format)+"/index/"+repo.Name+"%")
		_ = control.Close()
	})
	oci := NewDefaultOCICache(control, nil)
	raw := NewDefaultRawCache(control, nil)
	maven := NewDefaultMavenCache(control, nil)
	maintenance := NewCacheMaintenanceWithRaw(control, oci, raw)
	handler := NewGatewayHandlerWithFormatCaches(Dependencies{}, store, TestAdapter{}, testAuthenticator(), oci, maven, raw, nil, maintenance)
	return control, store, repo, handler
}

func putBrowseIndex(t *testing.T, control *PostgresCacheControlStore, repo repository.HostedRepository, path string, changes map[string]any) string {
	t.Helper()
	key := string(repo.Format) + "/index/" + repo.Name + "/" + uuid.NewString()
	value := map[string]any{
		"repository": repo.Name, "endpoint": repo.Endpoint, "path": path,
		"object": string(repo.Format) + "/objects/verified", "digest": strings.Repeat("b", 64),
		"size": 42, "content_type": "application/octet-stream", "expires_at": time.Now().Add(time.Hour),
	}
	for k, v := range changes {
		value[k] = v
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = control.Put(context.Background(), key, encoded); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestProxyDirectoryMavenIndexedPaginationAndSnapshotBuilds(t *testing.T) {
	control, _, repo, handler := proxyDirectoryFixture(t, repository.FormatMaven)
	paths := []string{
		"com/acme/widget/1.0/widget-1.0.pom", "com/acme/widget/1.0/widget-1.0.jar",
		"com/acme/widget/2.0-SNAPSHOT/widget-2.0-20260907.010203-2.jar",
		"com/acme/widget/2.0-SNAPSHOT/widget-2.0-20260907.010203-2.jar.sha256",
		"com/acme/widget/2.0-SNAPSHOT/widget-2.0-20260907.010203-2-sources.jar",
		"com/acme/widget/2.0-SNAPSHOT/widget-2.0-20260907.020304-2.pom",
		"org/example/other/1/other-1.jar",
	}
	for _, path := range paths {
		putBrowseIndex(t, control, repo, path, nil)
	}
	for _, f := range []struct {
		path    string
		changes map[string]any
	}{
		{"hidden/other/1/other-1.jar", map[string]any{"repository": "another-repository"}},
		{"hidden/other/1/other-1.jar", map[string]any{"endpoint": "https://previous.example.test"}},
		{"hidden/other/1/other-1.jar", map[string]any{"expires_at": time.Now().Add(-time.Minute)}},
		{"hidden/other/1/other-1.jar", map[string]any{"negative": true}},
		{"hidden/other/1/other-1.jar", map[string]any{"digest": "unknown"}},
		{"hidden/other/1/other-1.jar", map[string]any{"size": "invalid"}},
		{"hidden/other/1/other-1.jar", map[string]any{"expires_at": "invalid"}},
		{"metadata/only/maven-metadata.xml", nil},
	} {
		putBrowseIndex(t, control, repo, f.path, f.changes)
	}
	token := testAuthenticator().AdminToken
	response, root := browseRepositoryForTest(t, handler, token, repo.ID, "", "", 1)
	if response.Code != 200 || len(root.Items) != 1 || root.Items[0].Name != "com.acme" || root.NextPageToken == "" {
		t.Fatalf("root status=%d body=%s", response.Code, response.Body.String())
	}
	_, next := browseRepositoryForTest(t, handler, token, repo.ID, "", root.NextPageToken, 1)
	if len(next.Items) != 1 || next.Items[0].Name != "org.example" || next.NextPageToken != "" {
		t.Fatalf("next=%+v", next)
	}
	_, components := browseRepositoryForTest(t, handler, token, repo.ID, root.Items[0].ID, "", 50)
	if len(components.Items) != 1 || components.Items[0].Name != "widget" {
		t.Fatalf("components=%+v", components)
	}
	response, versions := browseRepositoryForTest(t, handler, token, repo.ID, components.Items[0].ID, "", 50)
	if response.Code != 200 || len(versions.Items) != 3 || !strings.Contains(versions.Items[1].Name, "20260907.010203-2") || !strings.Contains(versions.Items[2].Name, "20260907.020304-2") {
		t.Fatalf("versions status=%d body=%s", response.Code, response.Body.String())
	}
	_, assets := browseRepositoryForTest(t, handler, token, repo.ID, versions.Items[1].ID, "", 50)
	if len(assets.Items) != 3 {
		t.Fatalf("snapshot assets=%+v", assets)
	}
	for _, asset := range assets.Items {
		if asset.BuildNumber != 2 || asset.SourceRepositoryID != repo.ID || asset.SourceRepositoryName != repo.Name || asset.CacheState != "cached" || asset.CachedAt == "" {
			t.Fatalf("asset source/build evidence=%+v", asset)
		}
		if !strings.Contains(asset.Path, "20260907.010203-2") || asset.Digest != "sha256:"+strings.Repeat("b", 64) || asset.Size == nil || *asset.Size != 42 {
			t.Fatalf("asset=%+v", asset)
		}
	}
	putBrowseIndex(t, control, repo, "com/acme/widget/2.0-SNAPSHOT/widget-2.0-20260907.030405-3.jar", nil)
	_, pinned := browseRepositoryForTest(t, handler, token, repo.ID, versions.Items[1].ID, "", 50)
	if len(pinned.Items) != 3 {
		t.Fatalf("new snapshot changed old node: %+v", pinned)
	}
	response, _ = browseRepositoryForTest(t, handler, token, repo.ID, components.Items[0].ID, root.NextPageToken, 1)
	if response.Code != 400 {
		t.Fatalf("cross-parent cursor status=%d", response.Code)
	}
}

func TestProxyDirectoryRawScopeEscapingAndLiveInvalidation(t *testing.T) {
	control, store, repo, handler := proxyDirectoryFixture(t, repository.FormatRaw)
	key := putBrowseIndex(t, control, repo, "docs_100%/release%20notes.txt", nil)
	putBrowseIndex(t, control, repo, "docsX100Y/private.txt", nil)
	putBrowseIndex(t, control, repo, "root.bin", nil)
	token := testAuthenticator().AdminToken
	_, root := browseRepositoryForTest(t, handler, token, repo.ID, "", "", 50)
	var parent string
	for _, node := range root.Items {
		if node.Name == "docs_100%" {
			parent = node.ID
		}
	}
	if parent == "" {
		t.Fatalf("root=%+v", root)
	}
	_, children := browseRepositoryForTest(t, handler, token, repo.ID, parent, "", 50)
	if len(children.Items) != 1 || children.Items[0].Path != "docs_100%/release%20notes.txt" || children.Items[0].Name != "release notes.txt" {
		t.Fatalf("children=%+v", children)
	}
	if err := control.Delete(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	_, children = browseRepositoryForTest(t, handler, token, repo.ID, parent, "", 50)
	if len(children.Items) != 0 {
		t.Fatalf("invalidated assets visible=%+v", children)
	}
	response, _ := browseRepositoryForTest(t, handler, "", repo.ID, "", "", 50)
	if response.Code != 401 {
		t.Fatalf("anonymous status=%d", response.Code)
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	reader := testAuthenticator().IssueToken("reader")
	response, _ = browseRepositoryForTest(t, handler, reader, repo.ID, parent, "", 50)
	if response.Code != 400 {
		t.Fatalf("cross-principal parent status=%d", response.Code)
	}
	response, _ = browseRepositoryForTest(t, handler, testAuthenticator().IssueToken("outsider"), repo.ID, "", "", 50)
	if response.Code != 403 {
		t.Fatalf("ungranted reader status=%d", response.Code)
	}
	// Config changes invalidate the signed navigation context without touching bytes.
	changed := repo
	changed.Endpoint = "https://new.example.test"
	changed.Version = "2"
	var cursor repositoryBrowseNodeCursor
	if err := decodeSignedCursor(testAuthenticator().AdminToken, parent, &cursor); err != nil {
		t.Fatal(err)
	}
	h := hostedRepositoryAPIHandler{authenticator: testAuthenticator()}
	if _, _, err := h.decodeRepositoryBrowseParent(parent, repo, cursor.Principal); err != nil {
		t.Fatalf("current upstream parent rejected: %v", err)
	}
	if _, _, err := h.decodeRepositoryBrowseParent(parent, changed, cursor.Principal); err == nil {
		t.Fatal("old upstream parent accepted")
	}
	for _, limit := range []int{0, 202} {
		if _, err := control.ListProxyBrowseNodes(context.Background(), repo, repository.ArtifactBrowseParent{}, limit, ""); err == nil {
			t.Fatal(fmt.Sprint("accepted limit ", limit))
		}
	}
}

func TestProxyDirectoryReadsProtocolCachePublicationIncludingEmptyAssets(t *testing.T) {
	for _, format := range []repository.Format{repository.FormatMaven, repository.FormatRaw} {
		t.Run(string(format), func(t *testing.T) {
			control, _, repo, handler := proxyDirectoryFixture(t, format)
			ctx := context.Background()
			var key, path string
			var err error
			if format == repository.FormatMaven {
				path = "com/acme/empty/1/empty-1.jar"
				cache := NewDefaultMavenCache(control, nil)
				key = cache.Key(repo.Name, path)
				err = cache.Store(ctx, key, path, CachedMavenContent{Body: []byte{}, Repository: repo.Name, Endpoint: repo.Endpoint})
			} else {
				path = "empty.txt"
				cache := NewDefaultRawCache(control, nil)
				key = cache.Key(repo.Name, path, repo.Name, repo.Endpoint)
				err = cache.Store(ctx, key, RawContent{Body: []byte{}, Repository: repo.Name, Endpoint: repo.Endpoint, Path: path})
			}
			if err != nil {
				t.Fatal(err)
			}
			// Real protocol cache keys are hashed, unlike the synthetic fixtures.
			t.Cleanup(func() { _, _ = control.db.Exec(`DELETE FROM cache_control_entries WHERE key=$1`, key) })
			response, page := browseRepositoryForTest(t, handler, testAuthenticator().AdminToken, repo.ID, "", "", 50)
			for depth := 0; response.Code == 200 && len(page.Items) == 1 && page.Items[0].HasChildren && depth < 4; depth++ {
				response, page = browseRepositoryForTest(t, handler, testAuthenticator().AdminToken, repo.ID, page.Items[0].ID, "", 50)
			}
			if response.Code != 200 || len(page.Items) != 1 || page.Items[0].Path != path || page.Items[0].Size == nil || *page.Items[0].Size != 0 {
				t.Fatalf("published empty asset missing: status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestProxyDirectoryIndexAcceptsLongRawPaths(t *testing.T) {
	control, _, repo, handler := proxyDirectoryFixture(t, repository.FormatRaw)
	segments := make([]string, 120)
	for i := range segments {
		segments[i] = uuid.NewString()
	}
	path := "downloads/" + strings.Join(segments, "/") + "/release.txt"
	putBrowseIndex(t, control, repo, path, nil)
	response, root := browseRepositoryForTest(t, handler, testAuthenticator().AdminToken, repo.ID, "", "", 50)
	if response.Code != 200 || len(root.Items) != 1 || root.Items[0].Name != "downloads" {
		t.Fatalf("long path root missing: status=%d body=%s", response.Code, response.Body.String())
	}
	for depth := 0; response.Code == 200 && len(root.Items) == 1 && root.Items[0].HasChildren && depth < len(segments)+2; depth++ {
		response, root = browseRepositoryForTest(t, handler, testAuthenticator().AdminToken, repo.ID, root.Items[0].ID, "", 50)
	}
	if response.Code != 200 || len(root.Items) != 1 || root.Items[0].Path != path || root.Items[0].HasChildren {
		t.Fatalf("long prefix expansion lost the asset: status=%d body=%s", response.Code, response.Body.String())
	}
}

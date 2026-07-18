package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestMavenHostedGroupServesArtifactsMetadataAndChecksums(t *testing.T) {
	files := map[string]string{
		"/com/example/library/1.0/library-1.0.pom":        "<project/>",
		"/com/example/library/1.0/library-1.0.jar":        "jar-content",
		"/com/example/library/1.0/library-1.0.jar.sha256": "checksum",
		"/com/example/library/maven-metadata.xml":         "<metadata/>",
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		username, password, ok := request.BasicAuth()
		if !ok || username != "gitea" || password != "gitea-token" {
			t.Fatal("missing Gitea credentials")
		}
		content, exists := files[request.URL.Path]
		if !exists {
			http.NotFound(w, request)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("ETag", `"fixture"`)
		if request.Method != http.MethodHead {
			_, _ = w.Write([]byte(content))
		}
	}))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	_, err := store.CreateMavenGroup(context.Background(), repository.Group{Name: "engineering", Members: []repository.Member{{Name: "gitea-hosted", Type: repository.MemberHosted, Endpoint: upstream.URL, Position: 0}}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), GiteaClient{Username: "gitea", Token: "gitea-token"})
	for path, want := range files {
		request := httptest.NewRequest(http.MethodGet, "/maven/engineering"+path, nil)
		request.SetBasicAuth("gradle", "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Body.String() != want || response.Header().Get("ETag") != `"fixture"` {
			t.Fatalf("%s = %d headers=%v body=%q", path, response.Code, response.Header(), response.Body.String())
		}
	}
	head := httptest.NewRequest(http.MethodHead, "/maven/engineering/com/example/library/1.0/library-1.0.jar", nil)
	head.SetBasicAuth("maven", "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, head)
	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatalf("HEAD = %d %q", response.Code, response.Body.String())
	}
	if len(store.Audits) != len(files)+1 || store.Audits[0].Actor != "gradle" || store.Audits[0].Outcome != repository.AuditResolved {
		t.Fatalf("audits = %#v", store.Audits)
	}
}

func TestMavenHostedMemberWinsAndProxyIsFallback(t *testing.T) {
	hosted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/com/example/library/1.0/library-1.0.pom" {
			_, _ = w.Write([]byte("internal"))
			return
		}
		http.NotFound(w, request)
	}))
	defer hosted.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if _, _, ok := request.BasicAuth(); ok {
			t.Fatal("proxy received Gitea credentials")
		}
		_, _ = w.Write([]byte("proxy"))
	}))
	defer proxy.Close()
	store := repository.NewMemoryStore()
	_, _ = store.CreateMavenGroup(context.Background(), repository.Group{Name: "engineering", Members: []repository.Member{
		{Name: "proxy-first", Type: repository.MemberProxy, Endpoint: proxy.URL, Position: 0},
		{Name: "hosted", Type: repository.MemberHosted, Endpoint: hosted.URL, Position: 1},
	}})
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), GiteaClient{Username: "gitea", Token: "gitea-token"})
	request := httptest.NewRequest(http.MethodGet, "/maven/engineering/com/example/library/1.0/library-1.0.pom", nil)
	request.SetBasicAuth("maven", "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "internal" || len(store.Audits) != 1 || store.Audits[0].MemberName != "hosted" {
		t.Fatalf("response=%d body=%q audits=%#v", response.Code, response.Body.String(), store.Audits)
	}

	request = httptest.NewRequest(http.MethodGet, "/maven/engineering/com/example/library/2.0/library-2.0.pom", nil)
	request.SetBasicAuth("maven", "resolver-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "proxy" || len(store.Audits) != 3 || store.Audits[1].Outcome != repository.AuditNotFound || store.Audits[2].MemberName != "proxy-first" {
		t.Fatalf("fallback response=%d body=%q audits=%#v", response.Code, response.Body.String(), store.Audits)
	}
}

func TestMavenGroupManagementAndPathValidation(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	create := httptest.NewRequest(http.MethodPost, "/api/v1/maven/groups", strings.NewReader(`{"name":"engineering","members":[{"name":"hosted","type":"hosted","endpoint":"http://gitea","position":0}]}`))
	authorize(create, "admin-secret")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	if _, _, ok := parseMavenPath("/maven/engineering/../secret"); ok {
		t.Fatal("path traversal was accepted")
	}
}

func TestMavenFailsWhenAuditCannotBeRecorded(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateMavenGroup(context.Background(), repository.Group{Name: "engineering", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "test://available", Position: 0}}})
	handler := MavenHandler{Store: failingAuditStore{store}, Authenticator: testAuthenticator(), Client: GiteaClient{}, Metrics: &Metrics{}}
	request := httptest.NewRequest(http.MethodGet, "/maven/engineering/com/example/library/1.0/library-1.0.pom", nil)
	request.SetBasicAuth("maven", "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestMavenForwardsConditionalRequestsAndDisabledGroupsAreAudited(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("If-None-Match") != `"cached"` {
			t.Fatalf("If-None-Match = %q", request.Header.Get("If-None-Match"))
		}
		w.Header().Set("ETag", `"cached"`)
		w.WriteHeader(http.StatusNotModified)
	}))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	_, _ = store.CreateMavenGroup(context.Background(), repository.Group{Name: "engineering", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: upstream.URL, Position: 0}}})
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator(), GiteaClient{Username: "gitea", Token: "gitea-token"})
	request := httptest.NewRequest(http.MethodGet, "/maven/engineering/com/example/library/maven-metadata.xml", nil)
	request.Header.Set("If-None-Match", `"cached"`)
	request.SetBasicAuth("maven", "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotModified || response.Header().Get("ETag") != `"cached"` || len(store.Audits) != 1 || store.Audits[0].Outcome != repository.AuditResolved {
		t.Fatalf("response=%d headers=%v audits=%#v", response.Code, response.Header(), store.Audits)
	}
	if err := store.DisableMavenGroup(context.Background(), "engineering"); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "disabled") || len(store.Audits) != 2 || store.Audits[1].Outcome != repository.AuditGroupDisabled {
		t.Fatalf("disabled response=%d body=%q audits=%#v", response.Code, response.Body.String(), store.Audits)
	}
}

package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/scanning"
	"github.com/google/uuid"
)

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
	goHosted, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "capabilities-go", Format: repository.FormatGo})
	if err != nil {
		t.Fatal(err)
	}
	npmProxy, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "capabilities-npm-proxy", Format: repository.FormatNPM,
		Type: repository.RepositoryTypeProxy, Endpoint: "https://registry.npmjs.org", AllowedHosts: []string{"registry.npmjs.org"},
	})
	if err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	if _, err = store.ReplaceRepositoryGrants(context.Background(), conan.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{
		ArtifactScanner: scanning.ScannerFunc(func(context.Context, scanning.Artifact) (scanning.Report, error) {
			return scanning.Report{}, nil
		}),
		ArtifactScannerFormats: []repository.Format{repository.FormatOCI, repository.FormatConan, repository.FormatMaven, repository.FormatRaw, repository.FormatGo, repository.FormatNPM},
	}, store, TestAdapter{}, authenticator)
	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+conan.ID+"/capabilities", nil)
	authorize(request, authenticator.IssueToken("reader"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"format":"conan"`) || !strings.Contains(response.Body.String(), `"restore"`) || !strings.Contains(response.Body.String(), `"retain"`) || !strings.Contains(response.Body.String(), `"promote"`) || !strings.Contains(response.Body.String(), `"replicate"`) {
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
	if rawResponse.Code != http.StatusOK || !strings.Contains(rawResponse.Body.String(), `"restore"`) || !strings.Contains(rawResponse.Body.String(), `"retain"`) || !strings.Contains(rawResponse.Body.String(), `"promote"`) || !strings.Contains(rawResponse.Body.String(), `"replicate"`) || !strings.Contains(rawResponse.Body.String(), `"artifactScanning":true`) || !strings.Contains(rawResponse.Body.String(), `"publicationScanning":true`) {
		t.Fatalf("Raw capabilities=%d %s", rawResponse.Code, rawResponse.Body.String())
	}
	goRequest := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+goHosted.ID+"/capabilities", nil)
	authorize(goRequest, "admin-secret")
	goResponse := httptest.NewRecorder()
	handler.ServeHTTP(goResponse, goRequest)
	if goResponse.Code != http.StatusOK || !strings.Contains(goResponse.Body.String(), `"artifactScanning":true`) || !strings.Contains(goResponse.Body.String(), `"publicationScanning":false`) {
		t.Fatalf("Go capabilities=%d %s", goResponse.Code, goResponse.Body.String())
	}
	npmProxyRequest := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+npmProxy.ID+"/capabilities", nil)
	authorize(npmProxyRequest, "admin-secret")
	npmProxyResponse := httptest.NewRecorder()
	handler.ServeHTTP(npmProxyResponse, npmProxyRequest)
	if npmProxyResponse.Code != http.StatusOK || !strings.Contains(npmProxyResponse.Body.String(), `"artifactScanning":true`) || !strings.Contains(npmProxyResponse.Body.String(), `"publicationScanning":false`) {
		t.Fatalf("npm Proxy capabilities=%d %s", npmProxyResponse.Code, npmProxyResponse.Body.String())
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
	npmProxy := create(`{"name":"npm-proxy","format":"npm","type":"proxy","endpoint":"https://registry.npmjs.org","allowedHosts":["registry.npmjs.org"]}`, "create-npm-proxy")
	if npmProxy.Code != http.StatusCreated {
		t.Fatalf("create npm proxy=%d body=%s", npmProxy.Code, npmProxy.Body.String())
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
		"proxy endpoint with query":        `{"name":"proxy-query","format":"apt","type":"proxy","endpoint":"https://upstream.example/debian?suite=stable","allowedHosts":["upstream.example"]}`,
		"proxy endpoint with fragment":     `{"name":"proxy-fragment","format":"apt","type":"proxy","endpoint":"https://upstream.example/debian#stable","allowedHosts":["upstream.example"]}`,
		"raw proxy without allowedHosts":   `{"name":"proxy-no-hosts","format":"raw","type":"proxy","endpoint":"https://upstream.example"}`,
		"conan proxy without allowedHosts": `{"name":"proxy-conan-no-hosts","format":"conan","type":"proxy","endpoint":"https://upstream.example"}`,
		"npm proxy without allowedHosts":   `{"name":"proxy-npm-no-hosts","format":"npm","type":"proxy","endpoint":"https://registry.npmjs.org"}`,
		"apt proxy without allowedHosts":   `{"name":"proxy-apt-no-hosts","format":"apt","type":"proxy","endpoint":"https://upstream.example"}`,
		"proxy with blank allowedHost":     `{"name":"proxy-blank-host","format":"apt","type":"proxy","endpoint":"https://upstream.example","allowedHosts":[""]}`,
		"proxy with path allowedHost":      `{"name":"proxy-path-host","format":"apt","type":"proxy","endpoint":"https://upstream.example","allowedHosts":["cdn.example/path"]}`,
		"proxy with port allowedHost":      `{"name":"proxy-port-host","format":"apt","type":"proxy","endpoint":"https://upstream.example","allowedHosts":["cdn.example:443"]}`,
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
	if invalidHost := patch("2", `{"endpoint":"https://cdn.example","allowedHosts":[""]}`); invalidHost.Code != http.StatusBadRequest {
		t.Fatalf("invalid host=%d body=%s", invalidHost.Code, invalidHost.Body.String())
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
	if code := createUser(`{"name":"backup-admin","password":"supersecret","role":"admin"}`); code != http.StatusCreated {
		t.Fatalf("create backup admin=%d", code)
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

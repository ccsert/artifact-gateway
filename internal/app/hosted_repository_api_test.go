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

func TestHostedRepositoryManagementRejectsAnonymousAndInvalidRequests(t *testing.T) {
	handler := NewGatewayHandler(Dependencies{}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/api/v2/repositories", nil))
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d", denied.Code)
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

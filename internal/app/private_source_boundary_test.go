package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/repository"
)

func privateRejectSourceRebinding(t *testing.T, handler http.Handler, store GatewayStore, repo repository.HostedRepository, requests func() int32) {
	t.Helper()
	before := requests()
	for _, keep := range []bool{false, true} {
		body := map[string]any{"endpoint": "https://" + redirectCDN + "/new-source"}
		if keep {
			body["upstreamAuth"] = map[string]string{"scheme": string(repo.UpstreamAuth.Scheme), "username": repo.UpstreamAuth.Username}
		}
		encoded, _ := json.Marshal(body)
		r := httptest.NewRequest(http.MethodPatch, "/api/v2/repositories/"+repo.ID, bytes.NewReader(encoded))
		authorize(r, "admin-secret")
		r.Header.Set("If-Match", repo.Version)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"code":"upstream_credential_rebind_required"`) {
			t.Fatalf("credential rebind status=%d body=%s", w.Code, w.Body.String())
		}
		current, err := store.GetHostedRepository(t.Context(), repo.ID)
		if err != nil || current.Version != repo.Version || current.Endpoint != repo.Endpoint || current.UpstreamAuth == nil || *current.UpstreamAuth != *repo.UpstreamAuth {
			t.Fatal("rejected credential rebind changed configuration or encrypted secret")
		}
	}
	if requests() != before {
		t.Fatal("rejected management credential rebind contacted a source")
	}
}

func privateAssertRedactedManagementAndAudit(t *testing.T, handler http.Handler, store GatewayStore, repo repository.HostedRepository, sentinels ...string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID, nil)
	authorize(r, "admin-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("repository read-back status=%d", w.Code)
	}
	audits, err := store.ListAudits(t.Context(), repository.AuditQuery{Repository: repo.Name, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(audits)
	if err != nil {
		t.Fatal(err)
	}
	for _, sentinel := range sentinels {
		if strings.Contains(w.Body.String(), sentinel) || bytes.Contains(encoded, []byte(sentinel)) {
			t.Fatal("source secret leaked into management output or persisted audit")
		}
	}
}

// Corruption cannot be submitted through the management API (which encrypts
// plaintext itself). Inject it through the public store to represent persisted
// ciphertext damage and prove the HTTP cache cannot hide it.
func privateCorruptSourceCredential(t *testing.T, store GatewayStore, repo repository.HostedRepository, denied func()) repository.HostedRepository {
	t.Helper()
	good := *repo.UpstreamAuth
	bad := good
	bad.Secret = "damaged-source-ciphertext"
	repo.UpstreamAuth = &bad
	var err error
	repo, err = store.UpdateHostedRepository(t.Context(), repo, repo.Version)
	if err != nil {
		t.Fatal(err)
	}
	denied()
	repo.UpstreamAuth = &good
	repo, err = store.UpdateHostedRepository(t.Context(), repo, repo.Version)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

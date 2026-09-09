package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/aptpublication"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestAPTLifecycleManagementAuthorizationAndEmptySnapshot(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "apt-lifecycle", Format: repository.FormatAPT, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	manager := aptpublication.NewManager(store, objects)
	deb := aptManagementDebianPackage(t, "Package: widget\nVersion: 1.0-1\nArchitecture: amd64\n")
	sum := sha256.Sum256(deb)
	session, _, err := manager.CreateSession(ctx, aptpublication.CreateSessionInput{RepositoryID: repo.ID, Suite: "stable", Component: "main", Publisher: "ci", ObjectName: "widget.deb", DeclaredDigest: "sha256:" + hex.EncodeToString(sum[:]), DeclaredSize: int64(len(deb)), IdempotencyKey: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.UploadPackage(ctx, session.ID, session.ObjectName, bytes.NewReader(deb), int64(len(deb))); err != nil {
		t.Fatal(err)
	}
	snapshot, err := aptpublication.NewPublisher(store, objects, aptManagementSigner{}).Publish(ctx, aptpublication.PublishSnapshotInput{ID: uuid.NewString(), RepositoryID: repo.ID, Suite: "stable", Sequence: 1, SessionIDs: []string{session.ID}, Actor: "ci", CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeAPTObjectStore: objects, APTSigner: aptManagementSigner{}}, store, TestAdapter{}, testAuthenticator())
	request := aptpublication.LifecycleRequest{Suite: "stable", ExpectedSnapshotID: snapshot.ID, Action: "delete", PublicationSessionIDs: []string{session.ID}}
	body, _ := json.Marshal(request)
	call := func(method, path, token string, body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/v2/repositories/"+repo.ID+"/apt/"+path, bytes.NewReader(body))
		if token != "" {
			authorize(r, token)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "delete-one")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		token string
		code  int
	}{{"", 401}, {"resolver-secret", 403}} {
		w := call(http.MethodPost, "lifecycle", tc.token, body)
		if w.Code != tc.code {
			t.Fatalf("authorization: %d %s", w.Code, w.Body.String())
		}
	}
	visible := call(http.MethodGet, "lifecycle?suite=stable", "admin-secret", nil)
	var visibleState struct {
		Packages []struct {
			PoolPath string `json:"poolPath"`
			Revision struct {
				CanonicalIdentity string `json:"canonicalIdentity"`
			} `json:"revision"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(visible.Body.Bytes(), &visibleState); visible.Code != 200 || err != nil || len(visibleState.Packages) != 1 {
		t.Fatalf("visible state: %d %s %v", visible.Code, visible.Body.String(), err)
	}
	if visibleState.Packages[0].PoolPath != "pool/main/w/widget/widget.deb" || visibleState.Packages[0].Revision.CanonicalIdentity != "widget@1.0-1#amd64" {
		t.Fatalf("lifecycle response lost native package identity: %+v", visibleState.Packages[0])
	}
	for _, path := range []string{"lifecycle/preview", "lifecycle", "lifecycle"} {
		w := call(http.MethodPost, path, "admin-secret", body)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	w := call(http.MethodGet, "lifecycle?suite=stable", "admin-secret", nil)
	if w.Code != 200 {
		t.Fatalf("state: %d %s", w.Code, w.Body.String())
	}
	var state struct {
		Deletions []json.RawMessage
		Packages  []json.RawMessage
	}
	if err = json.Unmarshal(w.Body.Bytes(), &state); err != nil || len(state.Deletions) != 1 || len(state.Packages) != 0 {
		t.Fatalf("state: %s %v", w.Body.String(), err)
	}
	w = call(http.MethodPost, "snapshots/prune", "admin-secret", []byte(`{"snapshotIds":["`+snapshot.ID+`"]}`))
	if w.Code != 409 {
		t.Fatalf("prune grace: %d %s", w.Code, w.Body.String())
	}
	w = call(http.MethodPost, "snapshots/prune", "admin-secret", []byte(`{}`))
	if w.Code != 400 {
		t.Fatalf("missing prune selection: %d", w.Code)
	}
}

func TestAPTLifecyclePackagePoolPathIsDistinctFromPackageIdentity(t *testing.T) {
	revision := repository.APTPackageRevision{ID: uuid.NewString(), RepositoryID: uuid.NewString(), Package: "libwidget", Version: "1.0-1", Architecture: "amd64", CanonicalIdentity: "libwidget@1.0-1#amd64", ObjectName: "libwidget_1.0-1_amd64.deb", Digest: "sha256:" + strings.Repeat("a", 64), CreatedAt: time.Now().UTC()}
	response, err := aptLifecyclePackageResponse(uuid.NewString(), "main", revision)
	if err != nil {
		t.Fatal(err)
	}
	if response.PoolPath != "pool/main/libw/libwidget/libwidget_1.0-1_amd64.deb" || response.Revision.CanonicalIdentity != revision.CanonicalIdentity {
		t.Fatalf("pool path and package identity must remain distinct: %+v", response)
	}
}

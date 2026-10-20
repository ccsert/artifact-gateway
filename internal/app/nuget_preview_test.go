package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func nugetGatewayRequest(t *testing.T, h http.Handler, method, path, body string, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if authenticated {
		r.Header.Set("Authorization", "Bearer admin-secret")
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", uuid.NewString())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestNuGetPreviewExplicitCompositionAndFormalDiscoveryBoundary(t *testing.T) {
	o := explicitGatewayOptions()
	store := o.Store.(*repository.MemoryStore)
	ctx := context.Background()
	disabled, err := NewGatewayHandler(o)
	if err != nil {
		t.Fatal(err)
	}
	if w := nugetGatewayRequest(t, disabled, "GET", "/nuget/preview/v3/index.json", "", true); w.Code != 404 {
		t.Fatalf("disabled protocol=%d", w.Code)
	}
	if w := nugetGatewayRequest(t, disabled, "POST", "/api/v2/previews/nuget/repositories", `{"name":"preview"}`, true); w.Code != 503 {
		t.Fatalf("disabled management=%d %s", w.Code, w.Body.String())
	}
	o.NuGetPreviewEnabled, o.NuGetStore, o.NuGetObjects = true, store, objectstore.NewMemoryStore()
	h, err := NewGatewayHandler(o)
	if err != nil {
		t.Fatal(err)
	}
	w := nugetGatewayRequest(t, h, "POST", "/api/v2/previews/nuget/repositories", `{"name":"preview","anonymousRead":true}`, true)
	var preview struct {
		ID, SourceURL string
		Preview       bool
	}
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &preview) != nil || preview.ID == "" || !preview.Preview {
		t.Fatalf("preview create=%d %s", w.Code, w.Body.String())
	}
	apt, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "apt-preview", Format: repository.FormatAPT, Type: repository.RepositoryTypeHosted, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceAnonymousAccessPolicy(ctx, repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"/api/v2/repositories", "/api/v2/repositories?pageSize=1", "/api/v2/public/repositories", "/api/v2/formats"} {
		w = nugetGatewayRequest(t, h, "GET", endpoint, "", true)
		if w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte(`"nuget"`)) || bytes.Contains(w.Body.Bytes(), []byte(preview.ID)) {
			t.Fatalf("formal discovery %s=%d %s", endpoint, w.Code, w.Body.String())
		}
	}
	for _, endpoint := range []string{"/api/v2/repositories/" + preview.ID + "/capacity", "/api/v2/repositories/" + preview.ID + "/capabilities"} {
		if w = nugetGatewayRequest(t, h, "GET", endpoint, "", true); w.Code != 404 {
			t.Fatalf("formal projection %s=%d %s", endpoint, w.Code, w.Body.String())
		}
	}
	w = nugetGatewayRequest(t, h, "PUT", "/api/v2/previews/nuget/repositories/"+preview.ID+"/capacity", `{"quotaBytes":12345}`, true)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"quotaBytes":12345`)) {
		t.Fatalf("preview quota=%d %s", w.Code, w.Body.String())
	}
	if w = nugetGatewayRequest(t, h, "GET", "/api/v2/previews/nuget/repositories/"+preview.ID+"/capacity", "", true); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"quotaBytes":12345`)) {
		t.Fatalf("preview capacity=%d %s", w.Code, w.Body.String())
	}
	for _, id := range []string{preview.ID, apt.ID} {
		w = nugetGatewayRequest(t, h, "GET", "/api/v2/repositories/"+id, "", true)
		if id == preview.ID && w.Code != 404 || id == apt.ID && w.Code != 200 {
			t.Fatalf("formal detail id=%s status=%d %s", id, w.Code, w.Body.String())
		}
	}
	for _, endpoint := range []string{"/api/v2/previews/nuget/repositories/" + preview.ID, "/nuget/preview/v3/index.json"} {
		if w = nugetGatewayRequest(t, h, "GET", endpoint, "", true); w.Code != 200 {
			t.Fatalf("preview detail %s=%d %s", endpoint, w.Code, w.Body.String())
		}
	}
	if w = nugetGatewayRequest(t, h, "POST", "/api/v2/repositories", `{"name":"formal-nuget","format":"nuget","type":"hosted"}`, true); w.Code != 400 {
		t.Fatalf("formal create admitted=%d %s", w.Code, w.Body.String())
	}
	for _, format := range []string{"nuget", "maven"} {
		payload := `{"name":"preview-group-` + format + `","format":"` + format + `","members":[{"repositoryId":"` + preview.ID + `","position":0}]}`
		if w = nugetGatewayRequest(t, h, "POST", "/api/v2/groups", payload, true); w.Code != 400 {
			t.Fatalf("group admitted %s=%d %s", format, w.Code, w.Body.String())
		}
	}
}

func TestNuGetPreviewRejectsIncompleteExplicitDependencies(t *testing.T) {
	for _, objects := range []ObjectStore{nil, (*objectstore.MemoryStore)(nil)} {
		o := explicitGatewayOptions()
		o.NuGetPreviewEnabled, o.NuGetStore, o.NuGetObjects = true, o.Store.(repository.NativeNuGetStore), objects
		if h, err := NewGatewayHandler(o); err == nil || h != nil {
			t.Fatalf("nil objects accepted: %v %v", h, err)
		}
	}
	o := explicitGatewayOptions()
	o.NuGetPreviewEnabled, o.NuGetStore, o.NuGetObjects = true, (*repository.PostgresStore)(nil), objectstore.NewMemoryStore()
	if h, err := NewGatewayHandler(o); err == nil || h != nil {
		t.Fatalf("typed-nil store accepted: %v %v", h, err)
	}
}

func TestNuGetPreviewWorkerDiagnosticsRemainOutsideFormalFormatEnum(t *testing.T) {
	o := explicitGatewayOptions()
	store := o.Store.(*repository.MemoryStore)
	o.NuGetPreviewEnabled, o.NuGetStore, o.NuGetObjects = true, store, objectstore.NewMemoryStore()
	o.Dependencies.Runtime = DiagnosticRuntime{InstanceID: "preview-worker", WorkerFormats: []repository.Format{repository.FormatMaven, repository.FormatNuGet}, WorkerKinds: []string{"retention"}}
	now := time.Now().UTC()
	if err := store.UpsertRuntimeNodeHeartbeat(context.Background(), repository.RuntimeNode{
		InstanceID: "preview-worker", SessionID: "preview-session", Roles: []string{"worker"},
		WorkerFormats: []string{"maven", "nuget"}, WorkerKinds: []string{"retention"}, StartedAt: now, LastSeenAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	h, err := NewGatewayHandler(o)
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"/api/v2/diagnostics", "/api/v2/runtime/nodes"} {
		w := nugetGatewayRequest(t, h, http.MethodGet, endpoint, "", true)
		if w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte(`"workerFormats":["maven","nuget"]`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"workerFormats":["maven"]`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"previewWorkerFormats":["nuget"]`)) {
			t.Errorf("worker preview projection %s=%d %s", endpoint, w.Code, w.Body.String())
		}
	}
}

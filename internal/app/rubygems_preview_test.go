package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/rubygemspublication"
	"github.com/ccsert/artifact-gateway/internal/testsupport/rubygemsfixture"
	"github.com/google/uuid"
)

func rubyGemsGatewayRequest(handler http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer admin-secret")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", uuid.NewString())
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestRubyGemsPreviewGatewayPublishesWithoutEnteringFormalCatalog(t *testing.T) {
	o := explicitGatewayOptions()
	repos := o.Store.(*repository.MemoryStore)
	disabled, err := NewGatewayHandler(o)
	if err != nil {
		t.Fatal(err)
	}
	createPath := "/api/v2/previews/rubygems/repositories"
	if w := rubyGemsGatewayRequest(disabled, "POST", createPath, []byte(`{"name":"owned-gems"}`)); w.Code != 503 {
		t.Fatalf("disabled management=%d %s", w.Code, w.Body.String())
	}
	if w := rubyGemsGatewayRequest(disabled, "GET", "/rubygems/owned-gems/versions", nil); w.Code != 404 {
		t.Fatalf("disabled protocol=%d", w.Code)
	}
	o.RubyGemsPreviewEnabled = true
	o.RubyGemsStore = rubygemspublication.NewMemory(repos, repos)
	o.RubyGemsObjects = objectstore.NewMemoryStore()
	h, err := NewGatewayHandler(o)
	if err != nil {
		t.Fatal(err)
	}
	w := rubyGemsGatewayRequest(h, "POST", createPath, []byte(`{"name":"owned-gems","anonymousRead":true}`))
	var created struct {
		ID      string `json:"id"`
		Preview bool   `json:"preview"`
	}
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &created) != nil || created.ID == "" || !created.Preview {
		t.Fatalf("create=%d %s", w.Code, w.Body.String())
	}
	gem := rubygemsfixture.Build(t, rubygemsfixture.YAML("owned", "1.0.0", "ruby", "fixture", rubygemsfixture.Dependency("owned_leaf", "runtime")), "lib/owned.rb")
	w = rubyGemsGatewayRequest(h, "POST", "/rubygems/owned-gems/api/v1/gems", gem)
	if w.Code != 201 {
		t.Fatalf("native publish=%d %s", w.Code, w.Body.String())
	}
	if w = rubyGemsGatewayRequest(h, "GET", "/rubygems/owned-gems/gems/owned-1.0.0.gem", nil); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), gem) {
		t.Fatalf("original bytes=%d %s", w.Code, w.Body.String())
	}
	if w = rubyGemsGatewayRequest(h, "GET", "/rubygems/owned-gems/info/owned", nil); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("owned_leaf")) {
		t.Fatalf("dependency index=%d %s", w.Code, w.Body.String())
	}
	if _, err := repos.ReplaceAnonymousAccessPolicy(t.Context(), repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v2/formats", "/api/v2/repositories", "/api/v2/repositories?pageSize=1", "/api/v2/public/repositories"} {
		w = rubyGemsGatewayRequest(h, "GET", path, nil)
		if w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte(`"rubygems"`)) || bytes.Contains(w.Body.Bytes(), []byte(created.ID)) {
			t.Fatalf("formal catalog %s=%d %s", path, w.Code, w.Body.String())
		}
	}
	for _, suffix := range []string{"", "/capacity", "/capabilities"} {
		if w = rubyGemsGatewayRequest(h, "GET", "/api/v2/repositories/"+created.ID+suffix, nil); w.Code != 404 {
			t.Fatalf("formal projection %s=%d %s", suffix, w.Code, w.Body.String())
		}
	}
	if w = rubyGemsGatewayRequest(h, "POST", "/api/v2/repositories", []byte(`{"name":"formal-gems","format":"rubygems","type":"hosted"}`)); w.Code != 400 {
		t.Fatalf("formal create=%d %s", w.Code, w.Body.String())
	}
	group := []byte(`{"name":"formal-gems","format":"rubygems","members":[{"repositoryId":"` + created.ID + `","position":0}]}`)
	if w = rubyGemsGatewayRequest(h, "POST", "/api/v2/groups", group); w.Code != 400 {
		t.Fatalf("formal group=%d %s", w.Code, w.Body.String())
	}
	if w = rubyGemsGatewayRequest(h, "PUT", createPath+"/"+created.ID+"/capacity", []byte(`{"quotaBytes":123456}`)); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"quotaBytes":123456`)) {
		t.Fatalf("preview quota=%d %s", w.Code, w.Body.String())
	}
}

func TestRubyGemsPreviewGatewayRequiresIndependentNonNilPorts(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*GatewayOptions)
	}{
		{"missing store", func(o *GatewayOptions) { o.RubyGemsStore = nil }},
		{"typed nil store", func(o *GatewayOptions) { o.RubyGemsStore = (*rubygemspublication.Memory)(nil) }},
		{"missing objects", func(o *GatewayOptions) { o.RubyGemsObjects = nil }},
		{"typed nil objects", func(o *GatewayOptions) { o.RubyGemsObjects = (*objectstore.MemoryStore)(nil) }},
		{"disabled with ports", func(o *GatewayOptions) { o.RubyGemsPreviewEnabled = false }},
	} {
		t.Run(test.name, func(t *testing.T) {
			o := explicitGatewayOptions()
			repos := o.Store.(*repository.MemoryStore)
			o.RubyGemsPreviewEnabled = true
			o.RubyGemsStore = rubygemspublication.NewMemory(repos, repos)
			o.RubyGemsObjects = objectstore.NewMemoryStore()
			test.mutate(&o)
			if h, err := NewGatewayHandler(o); err == nil || h != nil {
				t.Fatalf("incomplete preview accepted: handler=%v error=%v", h, err)
			}
		})
	}
}

func TestRubyGemsPreviewWorkerDiagnosticsUseIndependentPreviewProjection(t *testing.T) {
	o := explicitGatewayOptions()
	repos := o.Store.(*repository.MemoryStore)
	o.Dependencies.Runtime = DiagnosticRuntime{InstanceID: "ruby-preview-worker", WorkerFormats: []repository.Format{repository.FormatMaven, repository.FormatRubyGems}, WorkerKinds: []string{"retention"}}
	now := time.Now().UTC()
	if err := repos.UpsertRuntimeNodeHeartbeat(t.Context(), repository.RuntimeNode{InstanceID: "ruby-preview-worker", SessionID: "owned-session", Roles: []string{"worker"}, WorkerFormats: []string{"maven", "rubygems"}, WorkerKinds: []string{"retention"}, StartedAt: now, LastSeenAt: now}); err != nil {
		t.Fatal(err)
	}
	h, err := NewGatewayHandler(o)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v2/diagnostics", "/api/v2/runtime/nodes"} {
		w := rubyGemsGatewayRequest(h, "GET", path, nil)
		if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"workerFormats":["maven"]`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"previewWorkerFormats":["rubygems"]`)) {
			t.Fatalf("worker projection %s=%d %s", path, w.Code, w.Body.String())
		}
	}
}

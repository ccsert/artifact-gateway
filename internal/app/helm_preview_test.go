package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

func TestHelmPreviewGatewayCompositionAndFormalCapabilitiesStayConsistent(t *testing.T) {
	o := explicitGatewayOptions()
	store := o.Store.(*repository.MemoryStore)
	disabled, err := NewGatewayHandler(o)
	if err != nil {
		t.Fatal(err)
	}
	if w := nugetGatewayRequest(t, disabled, "GET", "/helm/owned/index.yaml", "", true); w.Code != 404 {
		t.Fatalf("disabled protocol=%d", w.Code)
	}
	if w := nugetGatewayRequest(t, disabled, "POST", "/api/v2/previews/helm/repositories", `{"name":"owned"}`, true); w.Code != 503 {
		t.Fatalf("disabled preview=%d %s", w.Code, w.Body.String())
	}
	o.HelmPreviewEnabled = true
	o.HelmStore = store
	o.HelmObjects = objectstore.NewMemoryStore()
	h, err := NewGatewayHandler(o)
	if err != nil {
		t.Fatal(err)
	}
	w := nugetGatewayRequest(t, h, "POST", "/api/v2/previews/helm/repositories", `{"name":"owned"}`, true)
	var preview struct {
		ID, SourceURL string
		Preview       bool
	}
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &preview) != nil || preview.ID == "" || !preview.Preview || preview.SourceURL != "/helm/owned/index.yaml" {
		t.Fatalf("preview create=%d %s", w.Code, w.Body.String())
	}
	if _, err := store.ReplaceRepositoryGrants(t.Context(), preview.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}, ResourcePrefix: "owned"}}, "1"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v2/overview-statistics", "/api/v2/repository-grants", "/api/v2/artifact-search?q=sha256:" + strings.Repeat("a", 64), "/api/v2/repositories", "/api/v2/repositories?pageSize=1", "/api/v2/public/repositories", "/api/v2/formats", "/api/v2/repository-capacities"} {
		w = nugetGatewayRequest(t, h, "GET", path, "", true)
		if w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte(`"helm"`)) || bytes.Contains(w.Body.Bytes(), []byte(preview.ID)) {
			t.Fatalf("formal discovery %s=%d %s", path, w.Code, w.Body.String())
		}
	}
	for _, suffix := range []string{"", "/capacity", "/capabilities", "/artifact-search?q=owned"} {
		if w = nugetGatewayRequest(t, h, "GET", "/api/v2/repositories/"+preview.ID+suffix, "", true); w.Code != 404 {
			t.Fatalf("formal detail %s=%d %s", suffix, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/v2/previews/helm/repositories/" + preview.ID, "/api/v2/previews/helm/repositories/" + preview.ID + "/charts", "/helm/owned/index.yaml"} {
		if w = nugetGatewayRequest(t, h, "GET", path, "", true); w.Code != 200 {
			t.Fatalf("explicit preview %s=%d %s", path, w.Code, w.Body.String())
		}
	}
	if w = nugetGatewayRequest(t, h, "GET", "/helm/owned/index.yaml", "", false); w.Code != 401 {
		t.Fatalf("private index discovery=%d", w.Code)
	}
	if w = nugetGatewayRequest(t, h, "PUT", "/api/v2/previews/helm/repositories/"+preview.ID+"/capacity", `{"quotaBytes":12345}`, true); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"quotaBytes":12345`)) {
		t.Fatalf("quota=%d %s", w.Code, w.Body.String())
	}
	if w = nugetGatewayRequest(t, h, "POST", "/api/v2/repositories", `{"name":"formal-helm","format":"helm","type":"hosted"}`, true); w.Code != 400 {
		t.Fatalf("formal create=%d %s", w.Code, w.Body.String())
	}
	if w = nugetGatewayRequest(t, h, "POST", "/api/v2/previews/helm/repositories", `{"name":"proxy","type":"proxy"}`, true); w.Code != 400 {
		t.Fatalf("unimplemented proxy=%d", w.Code)
	}
}

func TestHelmPreviewGatewayRejectsImplicitOrIncompletePorts(t *testing.T) {
	for _, mutate := range []func(*GatewayOptions){func(o *GatewayOptions) { o.HelmPreviewEnabled = true }, func(o *GatewayOptions) {
		o.HelmPreviewEnabled = true
		o.HelmStore = (*repository.PostgresStore)(nil)
		o.HelmObjects = objectstore.NewMemoryStore()
	}, func(o *GatewayOptions) {
		o.HelmPreviewEnabled = true
		o.HelmStore = repository.NewMemoryStore()
		o.HelmObjects = (*objectstore.MemoryStore)(nil)
	}, func(o *GatewayOptions) {
		o.HelmStore = repository.NewMemoryStore()
		o.HelmObjects = objectstore.NewMemoryStore()
	}} {
		o := explicitGatewayOptions()
		mutate(&o)
		if h, err := NewGatewayHandler(o); err == nil || h != nil {
			t.Fatalf("incomplete ports=%v %v", h, err)
		}
	}
}

package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	o.HelmGovernance = store
	o.HelmSources = store
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
	if w = nugetGatewayRequest(t, h, "POST", "/api/v2/repositories/"+preview.ID+"/grants", `{"principal":"reader","scopes":["repositories:read"],"resourcePrefix":"owned"}`, true); w.Code != 200 {
		t.Fatalf("preview grant=%d %s", w.Code, w.Body.String())
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
		t.Fatalf("proxy missing explicit source=%d", w.Code)
	}
}

func TestHelmPreviewGatewayRejectsImplicitOrIncompletePorts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*GatewayOptions)
	}{
		{"missing publication store", func(o *GatewayOptions) { o.HelmStore = nil }},
		{"typed nil publication store", func(o *GatewayOptions) { o.HelmStore = (*repository.PostgresStore)(nil) }},
		{"missing governance store", func(o *GatewayOptions) { o.HelmGovernance = nil }},
		{"typed nil governance store", func(o *GatewayOptions) { o.HelmGovernance = (*repository.PostgresStore)(nil) }},
		{"typed nil replication store", func(o *GatewayOptions) { o.HelmDistribution = (*repository.PostgresStore)(nil) }},
		{"missing source store", func(o *GatewayOptions) { o.HelmSources = nil }},
		{"typed nil source store", func(o *GatewayOptions) { o.HelmSources = (*repository.PostgresStore)(nil) }},
		{"missing object store", func(o *GatewayOptions) { o.HelmObjects = nil }},
		{"typed nil object store", func(o *GatewayOptions) { o.HelmObjects = (*objectstore.MemoryStore)(nil) }},
		{"typed nil upstream", func(o *GatewayOptions) { o.HelmUpstream = (*UpstreamClient)(nil) }},
		{"disabled with injected ports", func(o *GatewayOptions) { o.HelmPreviewEnabled = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := explicitGatewayOptions()
			store := o.Store.(*repository.MemoryStore)
			o.HelmPreviewEnabled, o.HelmStore, o.HelmGovernance, o.HelmSources, o.HelmObjects = true, store, store, store, objectstore.NewMemoryStore()
			tc.mutate(&o)
			if h, err := NewGatewayHandler(o); err == nil || h != nil {
				t.Fatalf("incomplete ports=%v %v", h, err)
			}
		})
	}
}

func TestHelmReplicationPortCannotEnablePreviewImplicitly(t *testing.T) {
	o := explicitGatewayOptions()
	o.HelmDistribution = o.Store.(*repository.MemoryStore)
	if h, err := NewGatewayHandler(o); err == nil || h != nil {
		t.Fatalf("replication enabled a default-off preview: handler=%v error=%v", h, err)
	}
}

func TestHelmGrantManagementAcceptsChartPrefixesWithoutFormalSearch(t *testing.T) {
	for _, method := range []string{"replace", "upsert", "template"} {
		for _, prefix := range []string{"", "owned", "team-", strings.Repeat("a", 63)} {
			t.Run(method+"/"+prefix, func(t *testing.T) {
				o := explicitGatewayOptions()
				store := o.Store.(*repository.MemoryStore)
				o.HelmPreviewEnabled, o.HelmStore, o.HelmGovernance, o.HelmSources, o.HelmObjects = true, store, store, store, objectstore.NewMemoryStore()
				h, err := NewGatewayHandler(o)
				if err != nil {
					t.Fatal(err)
				}
				call := func(verb, path, body, version string) *httptest.ResponseRecorder {
					r := httptest.NewRequest(verb, path, strings.NewReader(body))
					r.Header.Set("Authorization", "Bearer admin-secret")
					r.Header.Set("Content-Type", "application/json")
					if version != "" {
						r.Header.Set("If-Match", version)
					}
					w := httptest.NewRecorder()
					h.ServeHTTP(w, r)
					return w
				}
				created := call("POST", "/api/v2/previews/helm/repositories", `{"name":"grant-preview"}`, "")
				var repo struct{ ID string }
				if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &repo) != nil || repo.ID == "" {
					t.Fatalf("preview=%d %s", created.Code, created.Body.String())
				}
				grant, _ := json.Marshal(repository.RepositoryGrant{Principal: "build-agent", Scopes: []string{"repositories:read", "repositories:write"}, ResourcePrefix: prefix})
				grantsPath := "/api/v2/repositories/" + repo.ID + "/grants"
				var w *httptest.ResponseRecorder
				switch method {
				case "replace":
					w = call("PUT", grantsPath, "["+string(grant)+"]", "1")
				case "upsert":
					w = call("POST", grantsPath, string(grant), "")
				case "template":
					template := call("POST", "/api/v2/authorization-templates", `{"name":"helm-grant","grants":[`+string(grant)+"]}", "")
					var item struct{ ID string }
					if template.Code != 201 || json.Unmarshal(template.Body.Bytes(), &item) != nil {
						t.Fatalf("template=%d %s", template.Code, template.Body.String())
					}
					w = call("POST", "/api/v2/authorization-templates/"+item.ID+"/apply", `{"repositoryId":"`+repo.ID+`"}`, "1")
				}
				if w.Code != http.StatusOK || w.Header().Get("ETag") != "2" {
					t.Fatalf("%s Helm prefix %q=%d %s", method, prefix, w.Code, w.Body.String())
				}
				for _, invalid := range []string{"Owned", "owned/version", "../owned", "owned%2f", "-owned", "owned_", "owned@1", "owned ", strings.Repeat("a", 64)} {
					body, _ := json.Marshal(repository.RepositoryGrant{Principal: "other", Scopes: []string{"repositories:read"}, ResourcePrefix: invalid})
					if denied := call("POST", grantsPath, string(body), ""); denied.Code != 400 {
						t.Fatalf("invalid chart prefix %q=%d %s", invalid, denied.Code, denied.Body.String())
					}
				}
				if w = call("GET", "/api/v2/repositories/"+repo.ID+"/artifact-search?q=owned", "", ""); w.Code != 404 {
					t.Fatalf("preview entered formal search=%d %s", w.Code, w.Body.String())
				}
			})
		}
	}
}

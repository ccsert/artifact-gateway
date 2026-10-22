package app

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

func TestHelmGroupPreviewStaysOutsideFormalDiscoveryAndDeepLinks(t *testing.T) {
	o := explicitGatewayOptions()
	store := o.Store.(*repository.MemoryStore)
	o.HelmPreviewEnabled, o.HelmStore, o.HelmSources, o.HelmObjects = true, store, store, objectstore.NewMemoryStore()
	if _, err := store.ReplaceAnonymousAccessPolicy(t.Context(), repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	h, err := NewGatewayHandler(o)
	if err != nil {
		t.Fatal(err)
	}
	w := nugetGatewayRequest(t, h, "POST", "/api/v2/previews/helm/repositories", `{"name":"helm-group-member","anonymousRead":true}`, true)
	var member struct{ ID string }
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &member) != nil || member.ID == "" {
		t.Fatalf("Hosted preview: %d %s", w.Code, w.Body.String())
	}
	w = nugetGatewayRequest(t, h, "POST", "/api/v2/previews/helm/groups", `{"name":"helm-group","anonymousRead":true,"members":["`+member.ID+`"]}`, true)
	var group struct{ ID string }
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &group) != nil || group.ID == "" {
		t.Fatalf("Group preview: %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/v2/groups", "/api/v2/groups?pageSize=1", "/api/v2/public/repositories", "/api/v2/formats"} {
		w = nugetGatewayRequest(t, h, "GET", path, "", true)
		if w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte(group.ID)) || bytes.Contains(w.Body.Bytes(), []byte(`"helm"`)) {
			t.Fatalf("formal discovery %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, suffix := range []string{"", "/members", "/resolution", "/capacity"} {
		w = nugetGatewayRequest(t, h, "GET", "/api/v2/groups/"+group.ID+suffix, "", true)
		if w.Code != 404 {
			t.Fatalf("formal Group deep link %s: %d %s", suffix, w.Code, w.Body.String())
		}
	}
	if w = nugetGatewayRequest(t, h, "GET", "/api/v2/previews/helm/groups/"+group.ID, "", true); w.Code != 200 {
		t.Fatalf("explicit Group management: %d %s", w.Code, w.Body.String())
	}
	if w = nugetGatewayRequest(t, h, "GET", "/helm/helm-group/index.yaml", "", false); w.Code != 200 {
		t.Fatalf("explicit anonymous Group read: %d %s", w.Code, w.Body.String())
	}
}

func TestFormatPreviewNewManagementRoutesRequireExplicitEnablement(t *testing.T) {
	h, err := NewGatewayHandler(explicitGatewayOptions())
	if err != nil {
		t.Fatal(err)
	}
	id := "11111111-1111-4111-8111-111111111111"
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/v2/previews/helm/groups"},
		{"GET", "/api/v2/previews/helm/groups/" + id},
		{"PUT", "/api/v2/previews/helm/groups/" + id},
		{"GET", "/api/v2/previews/helm/groups/" + id + "/sources/owned/1.0.0"},
		{"PUT", "/api/v2/previews/helm/repositories/" + id},
		{"GET", "/api/v2/previews/nuget/repositories/" + id + "/packages/owned/1.0.0/governance"},
		{"POST", "/api/v2/previews/nuget/repositories/" + id + "/packages/owned/1.0.0/governance"},
		{"GET", "/api/v2/previews/nuget/repositories/" + id + "/quarantine-read-policy"},
		{"PUT", "/api/v2/previews/nuget/repositories/" + id + "/quarantine-read-policy"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := nugetGatewayRequest(t, h, tc.method, tc.path, `{}`, true)
			if w.Code != 503 {
				t.Fatalf("default-off route: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

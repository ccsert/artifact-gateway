package app

import (
	"bytes"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestNuGetGroupPreviewDoesNotEnterFormalCatalogOrDeepLinks(t *testing.T) {
	o := explicitGatewayOptions()
	store := o.Store.(*repository.MemoryStore)
	o.NuGetPreviewEnabled, o.NuGetStore, o.NuGetObjects, o.NuGetSources = true, store, objectstore.NewMemoryStore(), store
	repo, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "nuget-group-member", Format: repository.FormatNuGet, Type: repository.RepositoryTypeHosted, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	group, _, err := store.CreateHostedGroupIdempotently(t.Context(), repository.HostedGroup{ID: uuid.NewString(), Name: "nuget-group-preview", Format: repository.FormatNuGet, AnonymousRead: true, Members: []repository.GroupMember{{RepositoryID: repo.ID, Position: 0}}}, "operator", uuid.NewString(), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceAnonymousAccessPolicy(t.Context(), repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	h, err := NewGatewayHandler(o)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v2/groups", "/api/v2/groups?pageSize=1", "/api/v2/public/repositories", "/api/v2/formats"} {
		w := nugetGatewayRequest(t, h, "GET", path, "", true)
		if w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte(group.ID)) || bytes.Contains(w.Body.Bytes(), []byte(`"nuget"`)) {
			t.Fatalf("formal Group discovery %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, suffix := range []string{"", "/members", "/resolution", "/capacity"} {
		w := nugetGatewayRequest(t, h, "GET", "/api/v2/groups/"+group.ID+suffix, "", true)
		if w.Code != 404 {
			t.Fatalf("formal Group deep link %s: %d %s", suffix, w.Code, w.Body.String())
		}
	}
	if w := nugetGatewayRequest(t, h, "GET", "/nuget/"+group.Name+"/v3/index.json", "", false); w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte("PackagePublish")) {
		t.Fatalf("explicit read-only Group: %d %s", w.Code, w.Body.String())
	}
}

package repository

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCargoProxyBrowseProjectsVerifiedCachedIndexes(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{
		ID: "cargo-proxy-browse", Name: "cargo-proxy-browse", Format: FormatCargo,
		Type: RepositoryTypeProxy, Endpoint: "https://index.crates.io",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	put := func(name, version string, status int) {
		t.Helper()
		var body []byte
		if status == 200 {
			body = []byte(`{"name":"` + name + `","vers":"` + version + `","cksum":"` + strings.Repeat("a", 64) + `","yanked":false}` + "\n")
		}
		if err := store.PutCargoProxyIndex(ctx, CargoProxyIndex{RepositoryID: repo.ID, Name: name, Status: status, Body: body, FetchedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	put("alpha-crate", "1.0.0", 200)
	put("beta-crate", "2.1.0", 200)
	put("missing-crate", "", 404)
	first, err := store.SearchCargoProxyCrates(ctx, repo.ID, "crate", 1, "")
	if err != nil || len(first) != 1 || first[0].Name != "alpha-crate" || first[0].MaxVersion != "1.0.0" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := store.SearchCargoProxyCrates(ctx, repo.ID, "crate", 2, first[0].Name)
	if err != nil || len(second) != 1 || second[0].Name != "beta-crate" || second[0].Versions != 1 {
		t.Fatalf("second=%+v err=%v", second, err)
	}
}

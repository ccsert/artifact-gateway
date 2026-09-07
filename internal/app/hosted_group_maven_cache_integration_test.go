//go:build integration

package app

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"
)

func TestPostgresMavenGroupResolutionScopeSurvivesReload(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	for _, status := range []int{200, 404} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			control, err := NewPostgresCacheControlStore(NewMemoryOCIObjectStore(), url)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = control.Close() })
			f := newGroupMavenFixture(t, control, status, 200)
			f.replaceMembers(0)
			f.expectGet(status, "")
			key := f.cache.Key(f.group.Name, groupMavenPath)
			t.Cleanup(func() { _ = control.Delete(context.Background(), key) })
			// A fresh cache instance must recover the scope from the PostgreSQL
			// JSON index, for both positive and negative entries.
			reloaded := NewDefaultMavenCache(control, nil)
			content, err := reloaded.Load(context.Background(), key)
			if (status == 200 && err != nil) || (status == 404 && !errors.Is(err, errMavenCacheNegative)) || content.ResolutionScope == "" {
				t.Fatalf("scope=%q err=%v", content.ResolutionScope, err)
			}
			f.replaceMembers(1, 0)
			f.expectGet(200, f.repos[1].Name)
			updated, err := reloaded.Load(context.Background(), key)
			if err != nil || updated.Member != f.repos[1].Name || updated.ResolutionScope == content.ResolutionScope {
				t.Fatalf("updated member=%q scope=%q err=%v", updated.Member, updated.ResolutionScope, err)
			}
		})
	}
}

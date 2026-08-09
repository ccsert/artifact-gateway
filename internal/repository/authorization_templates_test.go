package repository

import (
	"context"
	"testing"
)

func TestMemoryAuthorizationTemplatesApplyWithVersionCheck(t *testing.T) {
	store := NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), HostedRepository{ID: "repo-1", Name: "releases", Format: FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	template, err := store.CreateAuthorizationTemplate(context.Background(), AuthorizationTemplate{Name: "release-readers", Grants: []RepositoryGrant{{Principal: "user:alice", Scopes: []string{"repositories:read"}, ResourcePrefix: "com.example"}}})
	if err != nil {
		t.Fatal(err)
	}
	set, err := store.ApplyAuthorizationTemplate(context.Background(), template.ID, repo.ID, "1")
	if err != nil || set.Version != "2" || len(set.Grants) != 1 {
		t.Fatalf("apply = %#v err=%v", set, err)
	}
	if _, err = store.ApplyAuthorizationTemplate(context.Background(), template.ID, repo.ID, "1"); err != ErrVersionConflict {
		t.Fatalf("stale apply err=%v", err)
	}
}

func TestMemoryAuthorizationTemplateNameIsUnique(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.CreateAuthorizationTemplate(context.Background(), AuthorizationTemplate{Name: "Readers"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAuthorizationTemplate(context.Background(), AuthorizationTemplate{Name: " readers "}); err != ErrTemplateNameExists {
		t.Fatalf("duplicate name err=%v", err)
	}
}

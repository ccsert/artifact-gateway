package app

import (
	"context"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestAnonymousPolicyRequiresGroupAndMember(t *testing.T) {
	store := repository.NewMemoryStore()
	groups := []repository.Group{
		{Name: "disabled", Anonymous: true, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Anonymous: true}}},
		{Name: "private", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Anonymous: true}}},
		{Name: "partial", Anonymous: true, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted}}},
		{Name: "public", Anonymous: true, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Anonymous: true}}},
	}
	for _, group := range groups {
		if _, err := store.CreateGroup(context.Background(), group); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DisableGroup(context.Background(), "disabled"); err != nil {
		t.Fatal(err)
	}
	h := OCIHandler{Resolver: Resolver{Store: store}}
	for _, tc := range []struct {
		group string
		want  bool
	}{{"disabled", false}, {"private", false}, {"partial", false}, {"public", true}} {
		if got := h.anonymousOCIAllowed(context.Background(), tc.group); got != tc.want {
			t.Errorf("group %s: got %v want %v", tc.group, got, tc.want)
		}
	}
}

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type nuGetSourceContract interface {
	nuGetContractStore
	NuGetSourceStore
	CreateHostedGroupIdempotently(context.Context, HostedGroup, string, string, string) (HostedGroup, bool, error)
	ReplaceHostedGroupMembers(context.Context, string, []GroupMember, string) (HostedGroup, error)
}

func TestNuGetMemorySourceStoreContract(t *testing.T) {
	testNuGetSourceStoreContract(t, NewMemoryStore())
}
func testNuGetSourceStoreContract(t *testing.T, s nuGetSourceContract) {
	t.Helper()
	ctx := t.Context()
	a, err := s.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "nuget-proxy-" + uuid.NewString(), Format: FormatNuGet, Type: RepositoryTypeProxy, Endpoint: "https://private.example.test/dynamic/index.json"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "nuget-hosted-" + uuid.NewString(), Format: FormatNuGet, Type: RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"version":"3.0.0","resources":[]}`)
	sum := sha256.Sum256(body)
	doc := NuGetSourceDocument{RepositoryID: a.ID, SourceVersion: a.Version, URL: a.Endpoint, Status: 200, Body: body, Digest: "sha256:" + hex.EncodeToString(sum[:]), ETag: `"one"`, FetchedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Minute)}
	if err = s.PutNuGetSourceDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	doc.Body[0] = 'x'
	got, err := s.GetNuGetSourceDocument(ctx, a.ID, a.Version, a.Endpoint)
	if err != nil || string(got.Body) != `{"version":"3.0.0","resources":[]}` {
		t.Fatalf("document clone: %+v %v", got, err)
	}
	group, _, err := s.CreateHostedGroupIdempotently(ctx, HostedGroup{ID: uuid.NewString(), Name: "nuget-group-" + uuid.NewString(), Format: FormatNuGet, Members: []GroupMember{{RepositoryID: a.ID, Position: 0}, {RepositoryID: b.ID, Position: 1}}}, "operator", uuid.NewString(), "body")
	if err != nil {
		t.Fatal(err)
	}
	owner := NuGetGroupAuthority{GroupID: group.ID, PackageID: "private.a", Version: "1.0.0", RepositoryID: a.ID, RepositoryVersion: a.Version}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.BindNuGetGroupAuthority(ctx, group.Version, owner); e != nil {
				t.Errorf("bind: %v", e)
			}
		}()
	}
	wg.Wait()
	group, err = s.ReplaceHostedGroupMembers(ctx, group.ID, []GroupMember{{RepositoryID: b.ID, Position: 0}, {RepositoryID: a.ID, Position: 1}}, group.Version)
	if err != nil {
		t.Fatal(err)
	}
	other := owner
	other.RepositoryID = b.ID
	other.RepositoryVersion = b.Version
	bound, err := s.BindNuGetGroupAuthority(ctx, group.Version, other)
	if err != nil || bound.RepositoryID != a.ID {
		t.Fatalf("reorder switched owner: %+v %v", bound, err)
	}
	owner.Digest = "sha256:" + fmt.Sprintf("%064x", 1)
	owner.Size = 4
	bound, err = s.BindNuGetGroupAuthority(ctx, group.Version, owner)
	if err != nil || bound.Digest != owner.Digest {
		t.Fatalf("confirm: %+v %v", bound, err)
	}
	owner.Digest = "sha256:" + fmt.Sprintf("%064x", 2)
	if _, err = s.BindNuGetGroupAuthority(ctx, group.Version, owner); !errors.Is(err, ErrNuGetPublicationConflict) {
		t.Fatalf("mutable owner digest: %v", err)
	}
	session, _, err := s.ReserveNuGetPublication(ctx, NuGetSession{ID: uuid.NewString(), RepositoryID: a.ID, PackageID: "private.a", Version: "1.0.0", Digest: "sha256:" + fmt.Sprintf("%064x", 1), Size: 4, Actor: "proxy-cache", SourceVersion: a.Version, ExpiresAt: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	session.MetadataJSON = []byte(`{"id":"private.a","version":"1.0.0"}`)
	session.Nuspec = []byte("original")
	session.SourceURL = "https://private.example.test/packages/private.a/1.0.0/private.a.1.0.0.nupkg"
	session.RegistrationURL = "https://private.example.test/reg/private.a/index.json"
	if _, err = s.StageNuGetPublication(ctx, session); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetNuGetPublication(ctx, a.ID, "private.a", "1.0.0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("uncommitted proxy visible: %v", err)
	}
	if _, _, err = s.CommitNuGetPublication(ctx, session.ID, session.Actor); err != nil {
		t.Fatal(err)
	}
	updated, err := s.UpdateHostedRepository(ctx, a, a.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetNuGetPublication(ctx, a.ID, "private.a", "1.0.0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old binding visible after update: %v", err)
	}
	if err = s.PutNuGetSourceDocument(ctx, got); !errors.Is(err, ErrNuGetSourceChanged) {
		t.Fatalf("late document write: %v", err)
	}
	if _, _, err = s.CommitNuGetPublication(ctx, session.ID, session.Actor); !errors.Is(err, ErrNuGetSourceChanged) {
		t.Fatalf("late commit replay: %v", err)
	}
	if _, err = s.GetNuGetSourceDocument(ctx, a.ID, updated.Version, a.Endpoint); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cache crossed binding: %v", err)
	}
	if _, err = s.BindNuGetGroupAuthority(ctx, group.Version, other); !errors.Is(err, ErrNuGetSourceChanged) {
		t.Fatalf("owner changed binding fell through: %v", err)
	}
	owner.RepositoryVersion = updated.Version
	owner.Digest = "sha256:" + fmt.Sprintf("%064x", 1)
	if _, err = s.RevalidateNuGetGroupAuthority(ctx, group.Version, a.Version, owner); err != nil {
		t.Fatalf("same-owner verified revalidation: %v", err)
	}
	if _, err = s.RevalidateNuGetGroupAuthority(ctx, group.Version, a.Version, owner); err != nil {
		t.Fatalf("lost revalidation response: %v", err)
	}
	owner.Digest = "sha256:" + fmt.Sprintf("%064x", 2)
	if _, err = s.RevalidateNuGetGroupAuthority(ctx, group.Version, updated.Version, owner); !errors.Is(err, ErrNuGetPublicationConflict) {
		t.Fatalf("revalidation changed immutable identity: %v", err)
	}
	capacity, err := s.GetRepositoryCapacity(ctx, a.ID)
	if err != nil || capacity.UsedBytes != 4 || capacity.ObjectCount != 1 {
		t.Fatalf("immutable cache reference lost capacity: %+v %v", capacity, err)
	}
}

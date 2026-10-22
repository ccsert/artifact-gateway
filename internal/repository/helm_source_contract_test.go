package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type helmSourceContract interface {
	helmContractStore
	HelmSourceStore
	CreateHostedGroupIdempotently(context.Context, HostedGroup, string, string, string) (HostedGroup, bool, error)
	ReplaceHostedGroupMembers(context.Context, string, []GroupMember, string) (HostedGroup, error)
}

func TestHelmMemorySourceStoreContract(t *testing.T) {
	testHelmSourceStoreContract(t, NewMemoryStore())
}
func testHelmSourceStoreContract(t *testing.T, s helmSourceContract) {
	ctx := t.Context()
	a, e := s.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "helm-proxy-" + uuid.NewString(), Format: FormatHelm, Type: RepositoryTypeProxy, Endpoint: "https://private.example.invalid/index.yaml"})
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "helm-hosted-" + uuid.NewString(), Format: FormatHelm, Type: RepositoryTypeHosted})
	if e != nil {
		t.Fatal(e)
	}
	doc := HelmSourceDocument{RepositoryID: a.ID, RepositoryVersion: a.Version, Body: []byte("apiVersion: v1\nentries: {}\n"), ETag: `"owned"`, FetchedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Minute)}
	doc.Digest = helmDigest(doc.Body)
	if e = s.PutHelmSourceDocument(ctx, doc); e != nil {
		t.Fatal(e)
	}
	doc.Body[0] = 'X'
	got, e := s.GetHelmSourceDocument(ctx, a.ID, a.Version)
	if e != nil || got.Body[0] != 'a' {
		t.Fatalf("document clone=%+v %v", got, e)
	}
	bad := got
	bad.Digest = "sha256:" + fmt.Sprintf("%064x", 1)
	if e = s.PutHelmSourceDocument(ctx, bad); !errors.Is(e, ErrInvalidHelmPublication) {
		t.Fatalf("bad document digest=%v", e)
	}
	group, _, e := s.CreateHostedGroupIdempotently(ctx, HostedGroup{ID: uuid.NewString(), Name: "helm-group-" + uuid.NewString(), Format: FormatHelm, Members: []GroupMember{{RepositoryID: a.ID, Position: 0}, {RepositoryID: b.ID, Position: 1}}}, "operator", uuid.NewString(), "body")
	if e != nil {
		t.Fatal(e)
	}
	entry := HelmSourceEntry{Name: "owned", Version: "1.2.3", Digest: "sha256:" + fmt.Sprintf("%064x", 1), RepositoryID: a.ID, RepositoryVersion: a.Version, URL: "https://private.example.invalid/assets/owned-1.2.3.tgz", MetadataJSON: []byte(`{"name":"owned","version":"1.2.3"}`)}
	owner := HelmGroupAuthority{GroupID: group.ID, Entry: entry}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.BindHelmGroupAuthority(ctx, group.Version, owner); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	group, e = s.ReplaceHostedGroupMembers(ctx, group.ID, []GroupMember{{RepositoryID: b.ID, Position: 0}, {RepositoryID: a.ID, Position: 1}}, group.Version)
	if e != nil {
		t.Fatal(e)
	}
	other := owner
	other.Entry.RepositoryID = b.ID
	other.Entry.RepositoryVersion = b.Version
	other.Entry.Digest = "sha256:" + fmt.Sprintf("%064x", 2)
	bound, e := s.BindHelmGroupAuthority(ctx, group.Version, other)
	if e != nil || bound.Entry.RepositoryID != a.ID || bound.Entry.Digest != entry.Digest {
		t.Fatalf("reorder switched=%+v %v", bound, e)
	}
	entries := []HelmSourceEntry{entry}
	encoded, _ := json.Marshal(entries)
	snapshot := HelmSourceSnapshot{ID: uuid.NewString(), ViewID: group.ID, ViewVersion: group.Version, Entries: entries, Digest: helmDigest(encoded)}
	snap, e := s.PutHelmSourceSnapshot(ctx, snapshot)
	if e != nil || snap.CreatedAt.IsZero() {
		t.Fatalf("snapshot=%+v %v", snap, e)
	}
	entries[0].MetadataJSON[0] = 'X'
	saved, e := s.GetHelmSourceSnapshot(ctx, group.ID, snapshot.ID)
	if e != nil || saved.Entries[0].MetadataJSON[0] != '{' {
		t.Fatalf("snapshot clone=%+v %v", saved, e)
	}
	if _, e = s.GetHelmSourceSnapshot(ctx, a.ID, snapshot.ID); !errors.Is(e, ErrNotFound) {
		t.Fatalf("other view snapshot=%v", e)
	}
	session := HelmSession{ID: uuid.NewString(), RepositoryID: a.ID, SourceVersion: a.Version, Name: "owned", Version: "1.2.3", Digest: entry.Digest, Size: 4, MetadataJSON: entry.MetadataJSON, ChartYAML: []byte("Chart.yaml original"), Actor: "helm-proxy", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	// Restore the owned input after intentionally mutating caller buffers.
	session.MetadataJSON = []byte(`{"name":"owned","version":"1.2.3"}`)
	in, _, e := s.ReserveHelmPublication(ctx, session)
	if e != nil {
		t.Fatalf("proxy reserve=%v", e)
	}
	a.Endpoint = "https://rotated.example.invalid/index.yaml"
	a, e = s.UpdateHostedRepository(ctx, a, a.Version)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.CommitHelmPublication(ctx, in.ID, in.Actor); !errors.Is(e, ErrHelmSourceChanged) {
		t.Fatalf("commit changed source=%v", e)
	}
	other.Entry.MetadataJSON = []byte(`{"name":"owned","version":"1.2.3"}`)
	if _, e = s.BindHelmGroupAuthority(ctx, group.Version, other); !errors.Is(e, ErrHelmSourceChanged) {
		t.Fatalf("source replacement switched authority=%v", e)
	}
	if _, e = s.PutHelmSourceSnapshot(ctx, snapshot); e == nil {
		t.Fatal("invalid mutated snapshot accepted")
	}
}

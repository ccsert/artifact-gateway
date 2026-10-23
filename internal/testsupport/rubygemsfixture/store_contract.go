package rubygemsfixture

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	p "github.com/ccsert/artifact-gateway/internal/protocol/rubygems"
	"github.com/ccsert/artifact-gateway/internal/repository"
	d "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
	"github.com/google/uuid"
)

func Session(t testing.TB, repo string) d.Session {
	t.Helper()
	body := Build(t, YAML("contract", "1.0.0", "ruby", "fixture", Dependency("leaf", "runtime")), "lib/owned.rb")
	in, e := p.Inspect(bytes.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(body)
	return d.Session{ID: uuid.NewString(), RepositoryID: repo, Name: in.Metadata.Name, Version: in.Metadata.Version, Platform: in.Metadata.Platform, Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(body)), Actor: "operator", MetadataJSON: in.MetadataJSON, MetadataYAML: in.MetadataYAML, ExpiresAt: time.Now().UTC().Add(time.Hour)}
}
func StoreContract(t *testing.T, store d.Store, repo repository.HostedRepository) {
	t.Helper()
	ctx := t.Context()
	in := Session(t, repo.ID)
	session, replay, e := store.Reserve(ctx, in)
	if e != nil || replay || session.State != "pending" || session.ObjectKey != d.ObjectKey(in) {
		t.Fatalf("reserve %+v %t %v", session, replay, e)
	}
	rows, e := store.List(ctx, repo.ID, 10)
	if e != nil || len(rows) != 0 {
		t.Fatalf("pending visible %v %v", rows, e)
	}
	in.ID = uuid.NewString()
	same, replay, e := store.Reserve(ctx, in)
	if e != nil || !replay || same.ID != session.ID {
		t.Fatalf("pending replay %+v %t %v", same, replay, e)
	}
	wrong := in
	wrong.Digest = "sha256:" + strings.Repeat("f", 64)
	if _, _, e = store.Reserve(ctx, wrong); !errors.Is(e, d.ErrConflict) {
		t.Fatalf("different bytes %v", e)
	}
	pub, replay, e := store.Commit(ctx, session.ID, "operator")
	if e != nil || replay || pub.Digest != in.Digest || pub.Size != in.Size {
		t.Fatalf("commit %+v %t %v", pub, replay, e)
	}
	pub2, replay, e := store.Commit(ctx, session.ID, "operator")
	if e != nil || !replay || pub2.SessionID != pub.SessionID {
		t.Fatalf("commit replay %+v %t %v", pub2, replay, e)
	}
	got, e := store.Get(ctx, repo.ID, pub.FullName())
	if e != nil || got.Digest != pub.Digest || !bytes.Equal(got.MetadataYAML, in.MetadataYAML) {
		t.Fatalf("get %+v %v", got, e)
	}
	c, e := store.Capacity(ctx, repo.ID)
	if e != nil || c.UsedBytes != in.Size || c.PendingBytes != 0 || c.ObjectCount != 1 {
		t.Fatalf("capacity %+v %v", c, e)
	}
	if _, e = store.SetQuota(ctx, repo.ID, "operator", in.Size-1); !errors.Is(e, repository.ErrQuotaExceeded) {
		t.Fatalf("underflow quota %v", e)
	}
	if _, e = store.SetQuota(ctx, repo.ID, "operator", in.Size); e != nil {
		t.Fatal(e)
	}
	other := Session(t, repo.ID)
	other.Name = "second"
	other.MetadataYAML = bytes.Replace(other.MetadataYAML, []byte("name: contract"), []byte("name: second"), 1)
	meta, _ := p.ParseMetadata(other.MetadataYAML)
	other.MetadataJSON, _ = json.Marshal(meta)
	if _, _, e = store.Reserve(ctx, other); !errors.Is(e, repository.ErrQuotaExceeded) {
		t.Fatalf("quota refusal %v", e)
	}
	expired, e := store.Expire(ctx, time.Now().Add(2*time.Hour), 10)
	if e != nil || len(expired) != 0 {
		t.Fatalf("visible publication expired %+v %v", expired, e)
	}
	if _, e = store.Get(ctx, repo.ID, pub.FullName()); e != nil {
		t.Fatal(e)
	}
}

package rubygems

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	protocol "github.com/ccsert/artifact-gateway/internal/protocol/rubygems"
	"github.com/ccsert/artifact-gateway/internal/repository"
	domain "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
	"github.com/ccsert/artifact-gateway/internal/uploadspool"
	"github.com/google/uuid"
)

var errIntegrity = errors.New("RubyGems object integrity verification failed")

func (m *Module) push(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, p authorization.Principal) {
	r.Body = http.MaxBytesReader(w, r.Body, m.maxBytes)
	spool, e := uploadspool.Read(r.Body, m.maxBytes)
	if e != nil {
		var maxErr *http.MaxBytesError
		if errors.Is(e, uploadspool.ErrTooLarge) || errors.As(e, &maxErr) {
			m.problem(w, 413, "gem_too_large")
		} else {
			m.problem(w, 400, "invalid_gem_upload")
		}
		return
	}
	defer func() { _ = spool.Close() }()
	inspection, e := protocol.Inspect(spool.Reader())
	if e != nil {
		m.problem(w, 400, "invalid_gem_archive")
		return
	}
	meta := inspection.Metadata
	if _, ok := m.authorize(w, r, repo, authorization.RepositoryWrite, meta.Name, false); !ok {
		return
	}
	ctx, release, e := m.store.Lock(r.Context(), repo.ID, domain.Identity(meta.Name, meta.Version, meta.Platform))
	if e != nil {
		m.failure(w, e)
		return
	}
	defer release()
	in := domain.Session{ID: uuid.NewString(), RepositoryID: repo.ID, Name: meta.Name, Version: meta.Version, Platform: meta.Platform, Digest: spool.Digest(), Size: spool.Size(), Actor: p.Actor, MetadataJSON: inspection.MetadataJSON, MetadataYAML: inspection.MetadataYAML, ExpiresAt: time.Now().UTC().Add(time.Hour)}
	session, _, e := m.store.Reserve(ctx, in)
	if e != nil {
		m.failure(w, e)
		return
	}
	if session.State != "committed" {
		if e = spool.Rewind(); e == nil {
			e = m.objects.PutVerifiedReader(ctx, session.ObjectKey, spool.Reader(), session.Size, session.Digest)
		}
		if e != nil {
			m.failure(w, e)
			return
		}
	}
	verified, e := m.openVerified(ctx, session.ObjectKey, session.Digest, session.Size)
	if e != nil {
		m.failure(w, e)
		return
	}
	defer func() { _ = verified.Close() }()
	actual, e := protocol.Inspect(verified.Reader())
	if e != nil || !bytes.Equal(actual.MetadataJSON, session.MetadataJSON) || !bytes.Equal(actual.MetadataYAML, session.MetadataYAML) {
		m.failure(w, errIntegrity)
		return
	}
	current, allowed := m.authorize(w, r, repo, authorization.RepositoryWrite, meta.Name, false)
	if !allowed {
		return
	}
	if current.Actor != p.Actor {
		m.problem(w, 403, "access_denied")
		return
	}
	pub, _, e := m.store.Commit(ctx, session.ID, p.Actor)
	if e != nil {
		m.failure(w, e)
		return
	}
	w.Header().Set("Location", "/rubygems/"+repo.Name+"/gems/"+pub.FullName()+".gem")
	w.Header().Set("ETag", `"`+pub.Digest+`"`)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(201)
	_, _ = io.WriteString(w, "Successfully registered gem: "+pub.Name+" ("+pub.Version+")\n")
}
func (m *Module) openVerified(ctx context.Context, key, digest string, size int64) (*uploadspool.Spool, error) {
	r, n, e := m.objects.Open(ctx, key)
	if e != nil {
		return nil, e
	}
	if n != size {
		_ = r.Close()
		return nil, errIntegrity
	}
	spool, e := uploadspool.Read(r, size)
	ce := r.Close()
	if e != nil {
		return nil, e
	}
	if ce != nil || spool.Size() != size || spool.Digest() != digest {
		_ = spool.Close()
		return nil, errIntegrity
	}
	return spool, nil
}

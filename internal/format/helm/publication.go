package helm

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	helmprotocol "github.com/ccsert/artifact-gateway/internal/protocol/helm"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/uploadspool"
	"github.com/google/uuid"
)

var errIntegrity = errors.New("helm object integrity verification failed")

func (m *Module) upload(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, name, version, actor string) {
	expected := helmprotocol.Filename(name, version)
	r.Body = http.MaxBytesReader(w, r.Body, m.maxBytes+helmprotocol.MaxProvenanceBytes+(64<<10))
	reader, err := r.MultipartReader()
	if err != nil {
		m.problem(w, 400, "multipart_chart_required")
		return
	}
	var chart, prov *uploadspool.Spool
	defer func() {
		if chart != nil {
			_ = chart.Close()
		}
		if prov != nil {
			_ = prov.Close()
		}
	}()
	seen := map[string]bool{}
	for count := 0; ; count++ {
		part, e := reader.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			m.uploadFailure(w, e)
			return
		}
		field := part.FormName()
		_, params, e := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		filename := expected
		limit := m.maxBytes
		if field == "provenance" {
			filename += ".prov"
			limit = helmprotocol.MaxProvenanceBytes
		}
		if e != nil || count >= 2 || (field != "chart" && field != "provenance") || seen[field] || params["filename"] != filename {
			_ = part.Close()
			m.problem(w, 400, "invalid_chart_part")
			return
		}
		seen[field] = true
		spool, e := uploadspool.Read(part, limit)
		closeErr := part.Close()
		if e != nil {
			m.uploadFailure(w, e)
			return
		}
		if closeErr != nil {
			_ = spool.Close()
			m.uploadFailure(w, closeErr)
			return
		}
		if field == "chart" {
			chart = spool
		} else {
			prov = spool
		}
	}
	if chart == nil {
		m.problem(w, 400, "chart_required")
		return
	}
	inspection, err := helmprotocol.Inspect(chart.Reader())
	if err != nil || inspection.Metadata.Name != name || inspection.Metadata.Version != version {
		m.problem(w, 400, "chart_identity_mismatch")
		return
	}
	if prov != nil {
		data, e := io.ReadAll(prov.Reader())
		if e != nil || helmprotocol.InspectProvenance(data, inspection, chart.Digest()) != nil {
			m.problem(w, 400, "invalid_provenance")
			return
		}
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 200 || strings.ContainsAny(key, "\x00\r\n") {
		m.problem(w, 400, "invalid_idempotency_key")
		return
	}
	sessionID := uuid.NewString()
	if key != "" {
		sessionID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(repo.ID+"\x00helm\x00"+actor+"\x00"+key)).String()
	}
	in := repository.HelmSession{ID: sessionID, RepositoryID: repo.ID, Name: name, Version: version, Digest: chart.Digest(), Size: chart.Size(), Actor: actor, MetadataJSON: inspection.MetadataJSON, ChartYAML: inspection.ChartYAML, ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if prov != nil {
		in.ProvenanceDigest = prov.Digest()
		in.ProvenanceSize = prov.Size()
	}
	ctx, release, err := m.store.LockHelmPublication(r.Context(), repo.ID, name, version)
	if err != nil {
		m.failure(w, err)
		return
	}
	defer release()
	session, _, err := m.store.ReserveHelmPublication(ctx, in)
	if err != nil {
		m.failure(w, err)
		return
	}
	if session.State != "committed" {
		if err = chart.Rewind(); err == nil {
			err = m.objects.PutVerifiedReader(ctx, session.ObjectKey, chart.Reader(), session.Size, session.Digest)
		}
		if err == nil && prov != nil {
			if err = prov.Rewind(); err == nil {
				err = m.objects.PutVerifiedReader(ctx, session.ProvenanceKey, prov.Reader(), session.ProvenanceSize, session.ProvenanceDigest)
			}
		}
		if err != nil {
			m.failure(w, err)
			return
		}
	}
	publication, err := m.commit(ctx, session, actor)
	if err != nil {
		m.failure(w, err)
		return
	}
	w.Header().Set("Location", assetPath(repo, publication, false))
	w.Header().Set("ETag", `"`+publication.Digest+`"`)
	writeJSON(w, r, 201, chartDTO(repo, publication))
}
func (m *Module) uploadFailure(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	if errors.Is(err, uploadspool.ErrTooLarge) || errors.As(err, &maxErr) {
		m.problem(w, 413, "chart_too_large")
	} else {
		m.problem(w, 400, "invalid_chart_upload")
	}
}
func (m *Module) openVerified(ctx context.Context, key, digest string, size int64) (*uploadspool.Spool, error) {
	reader, actual, err := m.objects.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	if actual != size {
		_ = reader.Close()
		return nil, errIntegrity
	}
	spool, err := uploadspool.Read(reader, size)
	closeErr := reader.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		_ = spool.Close()
		return nil, closeErr
	}
	if spool.Size() != size || spool.Digest() != digest {
		_ = spool.Close()
		return nil, errIntegrity
	}
	return spool, nil
}
func (m *Module) verify(ctx context.Context, in repository.HelmSession) (*uploadspool.Spool, *uploadspool.Spool, error) {
	chart, err := m.openVerified(ctx, in.ObjectKey, in.Digest, in.Size)
	if err != nil {
		return nil, nil, err
	}
	inspection, err := helmprotocol.Inspect(chart.Reader())
	if err != nil || inspection.Metadata.Name != in.Name || inspection.Metadata.Version != in.Version || !bytes.Equal(inspection.MetadataJSON, in.MetadataJSON) || !bytes.Equal(inspection.ChartYAML, in.ChartYAML) {
		_ = chart.Close()
		return nil, nil, errIntegrity
	}
	if err = chart.Rewind(); err != nil {
		_ = chart.Close()
		return nil, nil, err
	}
	var prov *uploadspool.Spool
	if in.ProvenanceSize > 0 {
		prov, err = m.openVerified(ctx, in.ProvenanceKey, in.ProvenanceDigest, in.ProvenanceSize)
		if err != nil {
			_ = chart.Close()
			return nil, nil, err
		}
		data, e := io.ReadAll(prov.Reader())
		if e != nil || helmprotocol.InspectProvenance(data, inspection, in.Digest) != nil {
			_ = chart.Close()
			_ = prov.Close()
			return nil, nil, errIntegrity
		}
		if err = prov.Rewind(); err != nil {
			_ = chart.Close()
			_ = prov.Close()
			return nil, nil, err
		}
	}
	return chart, prov, nil
}
func (m *Module) commit(ctx context.Context, in repository.HelmSession, actor string) (repository.HelmPublication, error) {
	chart, prov, err := m.verify(ctx, in)
	if err != nil {
		return repository.HelmPublication{}, err
	}
	defer func() {
		_ = chart.Close()
		if prov != nil {
			_ = prov.Close()
		}
	}()
	if in.State == "committed" {
		p, e := m.store.GetHelmPublication(ctx, in.RepositoryID, in.Name, in.Version)
		if e != nil {
			return p, e
		}
		if p.SessionID != in.ID || p.ObjectKey != in.ObjectKey || p.Digest != in.Digest || p.Size != in.Size || p.ProvenanceKey != in.ProvenanceKey || p.ProvenanceDigest != in.ProvenanceDigest || p.ProvenanceSize != in.ProvenanceSize || !bytes.Equal(p.MetadataJSON, in.MetadataJSON) || !bytes.Equal(p.ChartYAML, in.ChartYAML) {
			return repository.HelmPublication{}, errIntegrity
		}
		return p, nil
	}
	p, _, err := m.store.CommitHelmPublication(ctx, in.ID, actor)
	return p, err
}

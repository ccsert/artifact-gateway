package rubygems

import (
	"bytes"
	"context"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	protocol "github.com/ccsert/artifact-gateway/internal/protocol/rubygems"
	"github.com/ccsert/artifact-gateway/internal/repository"
	domain "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
)

type governanceReceipt struct {
	RepositoryID      string     `json:"repositoryId"`
	Name              string     `json:"name"`
	Version           string     `json:"version"`
	Platform          string     `json:"platform"`
	Digest            string     `json:"digest"`
	GovernanceVersion string     `json:"governanceVersion"`
	Yanked            bool       `json:"yanked"`
	UpdatedAt         *time.Time `json:"updatedAt,omitempty"`
	UpdatedBy         string     `json:"updatedBy,omitempty"`
	Reason            string     `json:"reason,omitempty"`
}

func governanceDTO(p domain.Publication) governanceReceipt {
	g := p.Governance
	out := governanceReceipt{RepositoryID: p.RepositoryID, Name: p.Name, Version: p.Version, Platform: p.Platform, Digest: p.Digest, GovernanceVersion: g.Version, Yanked: g.Yanked, UpdatedBy: g.UpdatedBy, Reason: g.Reason}
	if !g.UpdatedAt.IsZero() {
		out.UpdatedAt = &g.UpdatedAt
	}
	return out
}

func (m *Module) verifyRetained(ctx context.Context, p domain.Publication) error {
	spool, err := m.openVerified(ctx, p.ObjectKey, p.Digest, p.Size)
	if err != nil {
		return errIntegrity
	}
	defer func() { _ = spool.Close() }()
	in, err := protocol.Inspect(spool.Reader())
	if err != nil || in.Metadata.Name != p.Name || in.Metadata.Version != p.Version || in.Metadata.Platform != p.Platform || !bytes.Equal(in.MetadataJSON, p.MetadataJSON) || !bytes.Equal(in.MetadataYAML, p.MetadataYAML) {
		return errIntegrity
	}
	return nil
}

func (m *Module) nativeYank(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository) {
	if r.Method != http.MethodDelete {
		w.Header().Set("Allow", "DELETE")
		w.WriteHeader(405)
		return
	}
	// Parse only the body, never query parameters; duplicate values and unknown
	// selectors must not silently broaden the chosen version/platform identity.
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/x-www-form-urlencoded" {
		m.problem(w, 415, "unsupported_media_type")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil {
		m.problem(w, 413, "yank_request_too_large")
		return
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		m.problem(w, 400, "invalid_yank_request")
		return
	}
	for key, values := range form {
		if (key != "gem_name" && key != "version" && key != "platform") || len(values) != 1 {
			m.problem(w, 400, "invalid_yank_request")
			return
		}
	}
	name, version, platform := form.Get("gem_name"), form.Get("version"), form.Get("platform")
	if platform == "" {
		platform = "ruby"
	}
	if !protocol.ValidName(name) || !protocol.ValidVersion(version) || !protocol.ValidPlatform(platform) {
		m.problem(w, 400, "invalid_yank_request")
		return
	}
	p, ok := m.authorize(w, r, repo, authorization.RepositoryWrite, name, false)
	if !ok {
		return
	}
	ctx, release, err := m.store.Lock(r.Context(), repo.ID, domain.Identity(name, version, platform))
	if err != nil {
		m.failure(w, err)
		return
	}
	defer release()
	before, err := m.store.GetRetained(ctx, repo.ID, name, version, platform)
	if err != nil {
		m.failure(w, err)
		return
	}
	current, ok := m.authorize(w, r, repo, authorization.RepositoryWrite, name, false)
	if !ok {
		return
	}
	if current.Actor != p.Actor {
		m.problem(w, 403, "access_denied")
		return
	}
	_, err = m.store.TransitionGovernance(ctx, domain.GovernanceChange{RepositoryID: repo.ID, Name: name, Version: version, Platform: platform, ExpectedDigest: before.Digest, ExpectedVersion: before.Governance.Version, Action: "yank", Actor: current.Actor, Reason: "Native gem yank"})
	if err != nil {
		m.failure(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(200)
	_, _ = io.WriteString(w, "Successfully deleted gem: "+before.Name+" ("+before.Version+")\n")
}

func (m *Module) previewGovernance(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, name, version, platform string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		w.WriteHeader(405)
		return
	}
	if !protocol.ValidName(name) || !protocol.ValidVersion(version) || !protocol.ValidPlatform(platform) {
		m.problem(w, 404, "not_found")
		return
	}
	p, ok := m.authorize(w, r, repo, authorization.RepositoryAdmin, name, false)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		pub, err := m.store.GetRetained(r.Context(), repo.ID, name, version, platform)
		if err != nil {
			m.failure(w, err)
			return
		}
		jsonResponse(w, 200, governanceDTO(pub))
		return
	}
	var input struct {
		ExpectedDigest            string `json:"expectedDigest"`
		ExpectedGovernanceVersion string `json:"expectedGovernanceVersion"`
		Action                    string `json:"action"`
		Reason                    string `json:"reason"`
	}
	if !decode(w, r, &input) {
		m.problem(w, 400, "invalid_governance_request")
		return
	}
	c := domain.GovernanceChange{RepositoryID: repo.ID, Name: name, Version: version, Platform: platform, ExpectedDigest: input.ExpectedDigest, ExpectedVersion: input.ExpectedGovernanceVersion, Action: input.Action, Actor: p.Actor, Reason: input.Reason}
	if !domain.ValidGovernanceChange(c) {
		m.problem(w, 400, "invalid_governance_request")
		return
	}
	ctx, release, err := m.store.Lock(r.Context(), repo.ID, domain.Identity(name, version, platform))
	if err != nil {
		m.failure(w, err)
		return
	}
	defer release()
	before, err := m.store.GetRetained(ctx, repo.ID, name, version, platform)
	if err == nil && (before.Digest != c.ExpectedDigest || before.Governance.Version != c.ExpectedVersion) {
		err = repository.ErrVersionConflict
	}
	if err == nil && c.Action == "restore" && before.Governance.Yanked {
		err = m.verifyRetained(ctx, before)
	}
	if err != nil {
		m.failure(w, err)
		return
	}
	current, ok := m.authorize(w, r, repo, authorization.RepositoryAdmin, name, false)
	if !ok {
		return
	}
	if current.Actor != p.Actor {
		m.problem(w, 403, "access_denied")
		return
	}
	pub, err := m.store.TransitionGovernance(ctx, c)
	if err != nil {
		m.failure(w, err)
		return
	}
	jsonResponse(w, 200, governanceDTO(pub))
}

// Keep this check local to the preview boundary: native paths have their own
// canonical-path check and neither surface accepts a query selector.
func validGovernancePath(parts []string) bool {
	return len(parts) == 6 && parts[1] == "packages" && parts[5] == "governance" && !strings.ContainsAny(strings.Join(parts, ""), "\\\x00\r\n")
}

// Package nexussource implements the migration-source management HTTP boundary.
package nexussource

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	api "github.com/ccsert/artifact-gateway/internal/admin/openapi"
	"github.com/ccsert/artifact-gateway/internal/authorization"
	source "github.com/ccsert/artifact-gateway/internal/nexussource"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type Authenticator interface {
	AuthenticateContext(context.Context, string) (authorization.Principal, bool)
}
type Auditor interface {
	RecordAudit(context.Context, repository.AuditRecord) error
}

// Adapter is embedded in the generated API's composition adapter. It owns no routes.
type Adapter struct {
	service *source.Service
	auth    Authenticator
	audit   Auditor
}

func New(service *source.Service, auth Authenticator, audit Auditor) *Adapter {
	return &Adapter{service, auth, audit}
}

type operation struct {
	adapter   *Adapter
	request   *http.Request
	principal authorization.Principal
	name, id  string
	status    int
}

func (h *Adapter) begin(w http.ResponseWriter, r *http.Request, name, id string) (*operation, bool) {
	w.Header().Set("Cache-Control", "no-store")
	op := &operation{adapter: h, request: r, name: name, id: id, status: 500}
	if h == nil || h.auth == nil {
		problem(w, 503, "source_service_unavailable")
		return op, false
	}
	p, ok := h.auth.AuthenticateContext(r.Context(), r.Header.Get("Authorization"))
	op.principal = p
	if !ok {
		op.status = 401
		problem(w, 401, "access_denied")
		op.finish()
		return op, false
	}
	if p.MustChangePassword || !p.Admin {
		op.status = 403
		problem(w, 403, "access_denied")
		op.finish()
		return op, false
	}
	if !h.service.Available() {
		op.status = 503
		problem(w, 503, "source_service_unavailable")
		op.finish()
		return op, false
	}
	return op, true
}
func (op *operation) finish() {
	if op.adapter == nil || op.adapter.audit == nil {
		return
	}
	outcome := repository.AuditResolved
	if op.status >= 400 {
		outcome = repository.AuditAccessDenied
	}
	// The source ID and bounded operation code are the only source-related audit values.
	_ = op.adapter.audit.RecordAudit(op.request.Context(), repository.AuditRecord{Actor: op.principal.Actor, Operation: op.name, Resource: op.id, Format: "management", Outcome: outcome, Status: op.status, OccurredAt: time.Now().UTC(), CacheDisposition: "bypass"})
}
func problem(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": code, "status": status, "code": code, "detail": "migration source operation failed"})
}
func (op *operation) failed(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	status, code := 500, "internal_error"
	var failure *source.Failure
	switch {
	case errors.Is(err, source.ErrNotFound):
		status, code = 404, "not_found"
	case errors.Is(err, source.ErrVersionConflict):
		status, code = 412, "version_conflict"
	case errors.As(err, &failure):
		code = failure.Code
		switch code {
		case "invalid_source", "invalid_request", "credential_replacement_required":
			status = 400
		case "encryption_key_unavailable", "target_lookup_unavailable":
			status = 503
		case "source_limit_exceeded", "source_snapshot_unsupported", "source_repository_not_visible":
			status = 422
		case "source_timeout":
			status = 504
		default:
			status = 502
		}
	}
	op.status = status
	problem(w, status, code)
	return true
}
func (op *operation) body(w http.ResponseWriter, out any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, op.request.Body, 64<<10))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(&struct{}{}) != io.EOF {
		op.status = 400
		problem(w, 400, "invalid_request")
		return false
	}
	return true
}
func (op *operation) respond(w http.ResponseWriter, status int, value any) {
	op.status = status
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func input(v api.MigrationSourceInput) (source.Input, error) {
	out := source.Input{Name: v.Name, BaseURL: v.BaseUrl}
	if v.ClearCredentials != nil {
		out.ClearCredentials = *v.ClearCredentials
	}
	if v.Credentials != nil {
		if v.Credentials.Username == nil || v.Credentials.Password == nil {
			return out, &source.Failure{Code: "invalid_source"}
		}
		out.Credentials = &source.Credentials{Username: *v.Credentials.Username, Password: *v.Credentials.Password}
	}
	return out, nil
}
func sourceResponse(v source.Source) api.MigrationSource {
	return api.MigrationSource{Id: uuid.MustParse(v.ID), Name: v.Name, BaseUrl: v.BaseURL, Kind: "nexus3", CredentialsConfigured: v.CredentialCiphertext != "", Version: v.Version, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}
func (h *Adapter) CreateMigrationSource(w http.ResponseWriter, r *http.Request) {
	op, ok := h.begin(w, r, "migration.source.create", "")
	if !ok {
		return
	}
	defer op.finish()
	var body api.MigrationSourceInput
	if !op.body(w, &body) {
		return
	}
	in, err := input(body)
	if op.failed(w, err) {
		return
	}
	out, err := h.service.Create(r.Context(), in)
	if op.failed(w, err) {
		return
	}
	op.id = out.ID
	w.Header().Set("ETag", out.Version)
	op.respond(w, 201, sourceResponse(out))
}
func (h *Adapter) GetMigrationSource(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	op, ok := h.begin(w, r, "migration.source.get", id.String())
	if !ok {
		return
	}
	defer op.finish()
	out, err := h.service.Get(r.Context(), id.String())
	if op.failed(w, err) {
		return
	}
	w.Header().Set("ETag", out.Version)
	op.respond(w, 200, sourceResponse(out))
}
func (h *Adapter) ListMigrationSources(w http.ResponseWriter, r *http.Request, params api.ListMigrationSourcesParams) {
	op, ok := h.begin(w, r, "migration.source.list", "")
	if !ok {
		return
	}
	defer op.finish()
	limit, cursor := 20, ""
	if params.Limit != nil {
		limit = *params.Limit
	}
	if params.Cursor != nil {
		cursor = *params.Cursor
	}
	values, next, err := h.service.List(r.Context(), limit, cursor)
	if op.failed(w, err) {
		return
	}
	out := api.MigrationSourcePage{Items: []api.MigrationSource{}, NextCursor: next}
	for _, v := range values {
		out.Items = append(out.Items, sourceResponse(v))
	}
	op.respond(w, 200, out)
}
func (h *Adapter) UpdateMigrationSource(w http.ResponseWriter, r *http.Request, id uuid.UUID, params api.UpdateMigrationSourceParams) {
	op, ok := h.begin(w, r, "migration.source.update", id.String())
	if !ok {
		return
	}
	defer op.finish()
	var body api.MigrationSourceInput
	if !op.body(w, &body) {
		return
	}
	in, err := input(body)
	if op.failed(w, err) {
		return
	}
	out, err := h.service.Update(r.Context(), id.String(), in, params.IfMatch)
	if op.failed(w, err) {
		return
	}
	w.Header().Set("ETag", out.Version)
	op.respond(w, 200, sourceResponse(out))
}
func (h *Adapter) DeleteMigrationSource(w http.ResponseWriter, r *http.Request, id uuid.UUID, params api.DeleteMigrationSourceParams) {
	op, ok := h.begin(w, r, "migration.source.delete", id.String())
	if !ok {
		return
	}
	defer op.finish()
	if op.failed(w, h.service.Delete(r.Context(), id.String(), params.IfMatch)) {
		return
	}
	op.status = 204
	w.WriteHeader(204)
}
func stats(v source.Stats) api.MigrationSourceStats {
	return api.MigrationSourceStats{Requests: v.Requests, Pages: v.Pages, ResponseBytes: v.ResponseBytes}
}
func fields(values []source.Field) []api.MigrationSourceField {
	out := make([]api.MigrationSourceField, 0, len(values))
	for _, v := range values {
		out = append(out, api.MigrationSourceField{Field: v.Field, Disposition: api.MigrationSourceFieldDisposition(v.Disposition), Reason: v.Reason})
	}
	return out
}
func repositories(values []source.Repository) []api.MigrationSourceRepository {
	out := make([]api.MigrationSourceRepository, 0, len(values))
	for _, v := range values {
		out = append(out, api.MigrationSourceRepository{Name: v.Name, Format: v.Format, Type: v.Type, Status: api.MigrationSourceRepositoryStatus(v.Status), Reason: v.Reason, Members: v.Members, RemoteUrl: v.RemoteURL, VersionPolicy: v.VersionPolicy, Fields: fields(v.Fields)})
	}
	return out
}
func (h *Adapter) PreflightMigrationSource(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	op, ok := h.begin(w, r, "migration.source.preflight", id.String())
	if !ok {
		return
	}
	defer op.finish()
	out, err := h.service.Preflight(r.Context(), id.String())
	if op.failed(w, err) {
		return
	}
	op.respond(w, 200, api.MigrationSourcePreflight{SourceId: id, SourceVersion: out.SourceVersion, State: "complete", Visibility: "credential_visible", Repositories: repositories(out.Repositories), Stats: stats(out.Stats)})
}
func (h *Adapter) DiscoverMigrationSource(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	op, ok := h.begin(w, r, "migration.source.discover", id.String())
	if !ok {
		return
	}
	defer op.finish()
	var body api.MigrationSourceDiscoveryInput
	if !op.body(w, &body) {
		return
	}
	if body.MaxPages != nil && *body.MaxPages < 1 || body.MaxAssets != nil && *body.MaxAssets < 1 {
		op.status = 400
		problem(w, 400, "invalid_request")
		return
	}
	in := source.DiscoveryInput{Repositories: body.Repositories}
	if body.MaxPages != nil {
		in.MaxPages = *body.MaxPages
	}
	if body.MaxAssets != nil {
		in.MaxAssets = *body.MaxAssets
	}
	out, err := h.service.Discover(r.Context(), id.String(), in)
	if op.failed(w, err) {
		return
	}
	assets := make([]api.MigrationSourceAsset, 0, len(out.Assets))
	for _, v := range out.Assets {
		assets = append(assets, api.MigrationSourceAsset{Repository: v.Repository, Path: v.Path, DownloadUrl: v.DownloadURL, Size: v.Size, Checksum: v.Checksum})
	}
	unsupported := make([]api.MigrationSourceUnsupportedAsset, 0, len(out.UnsupportedAssets))
	for _, v := range out.UnsupportedAssets {
		unsupported = append(unsupported, api.MigrationSourceUnsupportedAsset{Repository: v.Repository, Path: v.Path, Reason: v.Reason})
	}
	op.respond(w, 200, api.MigrationSourceDiscovery{UnsupportedAssets: unsupported, SourceId: id, SourceVersion: out.SourceVersion, State: "complete", Visibility: "credential_visible", Repositories: repositories(out.Repositories), Assets: assets, Stats: stats(out.Stats)})
}
func (h *Adapter) PreviewMigrationSourceMapping(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	op, ok := h.begin(w, r, "migration.source.mapping-preview", id.String())
	if !ok {
		return
	}
	defer op.finish()
	var body api.MigrationSourceMappingInput
	if !op.body(w, &body) {
		return
	}
	mappings := make([]source.Mapping, 0, len(body.Mappings))
	for _, v := range body.Mappings {
		m := source.Mapping{SourceRepository: v.SourceRepository, TargetName: v.TargetName, Format: string(v.Format), Type: string(v.Type)}
		if v.TargetMembers != nil {
			m.TargetMembers = *v.TargetMembers
		}
		mappings = append(mappings, m)
	}
	out, err := h.service.PreviewMapping(r.Context(), id.String(), mappings)
	if op.failed(w, err) {
		return
	}
	values := make([]api.MigrationSourceMappingResult, 0, len(out.Mappings))
	for _, v := range out.Mappings {
		values = append(values, api.MigrationSourceMappingResult{SourceRepository: v.SourceRepository, TargetName: v.TargetName, Status: api.MigrationSourceMappingResultStatus(v.Status), Reasons: v.Reasons, TargetMembers: v.TargetMembers, Fields: fields(v.Fields)})
	}
	op.respond(w, 200, api.MigrationSourceMappingPreview{SourceId: id, SourceVersion: out.SourceVersion, State: "complete", Valid: out.Valid, Mappings: values, Stats: stats(out.Stats)})
}

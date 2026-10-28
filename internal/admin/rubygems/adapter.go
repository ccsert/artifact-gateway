// Package rubygems binds generated management operations to an explicitly
// injected preview handler. Storage and native routes remain format-owned.
package rubygems

import (
	"net/http"
	"reflect"

	api "github.com/ccsert/artifact-gateway/internal/admin/openapi"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

type RubyGemsBoundary struct{ handler http.Handler }

func New(handler http.Handler) *RubyGemsBoundary {
	if handler != nil {
		v := reflect.ValueOf(handler)
		if v.Kind() == reflect.Pointer && v.IsNil() {
			handler = nil
		}
	}
	return &RubyGemsBoundary{handler}
}
func (a *RubyGemsBoundary) serve(w http.ResponseWriter, r *http.Request) {
	if a == nil || a.handler == nil {
		w.Header().Set("Content-Type", "application/problem+json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"type":"about:blank","title":"rubygems_preview_unavailable","status":503,"code":"rubygems_preview_unavailable"}`))
		return
	}
	a.handler.ServeHTTP(w, r)
}
func (a *RubyGemsBoundary) CreateRubyGemsPreviewRepository(w http.ResponseWriter, r *http.Request, _ api.CreateRubyGemsPreviewRepositoryParams) {
	a.serve(w, r)
}
func (a *RubyGemsBoundary) GetRubyGemsPreviewRepository(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	a.serve(w, r)
}
func (a *RubyGemsBoundary) GetRubyGemsPreviewCapacity(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	a.serve(w, r)
}
func (a *RubyGemsBoundary) SetRubyGemsPreviewQuota(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	a.serve(w, r)
}

func (a *RubyGemsBoundary) GetRubyGemsPreviewGovernance(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID, _, _, _ string) {
	a.serve(w, r)
}
func (a *RubyGemsBoundary) ChangeRubyGemsPreviewGovernance(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID, _, _, _ string) {
	a.serve(w, r)
}

func (a *RubyGemsBoundary) GetRubyGemsPreviewQuarantineReadPolicy(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	a.serve(w, r)
}
func (a *RubyGemsBoundary) ReplaceRubyGemsPreviewQuarantineReadPolicy(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	a.serve(w, r)
}

func (a *RubyGemsBoundary) GetRubyGemsPreviewPackageIdentity(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID, _, _, _ string) {
	a.serve(w, r)
}
func (a *RubyGemsBoundary) CreateRubyGemsPreviewReplicationJob(w http.ResponseWriter, r *http.Request, _ api.CreateRubyGemsPreviewReplicationJobParams) {
	a.serve(w, r)
}
func (a *RubyGemsBoundary) GetRubyGemsPreviewReplicationJob(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	a.serve(w, r)
}
func (a *RubyGemsBoundary) RetryRubyGemsPreviewReplicationJob(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID, _ api.RetryRubyGemsPreviewReplicationJobParams) {
	a.serve(w, r)
}

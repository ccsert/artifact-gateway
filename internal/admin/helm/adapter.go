// Package helm binds generated management routes to the explicitly injected
// Helm preview boundary. It constructs neither storage nor protocol routes.
package helm

import (
	"net/http"
	"reflect"

	api "github.com/ccsert/artifact-gateway/internal/admin/openapi"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Boundary has a distinct embedded name from other preview adapters.
type Boundary struct{ handler http.Handler }

func New(handler http.Handler) *Boundary {
	if handler != nil {
		v := reflect.ValueOf(handler)
		if v.Kind() == reflect.Pointer && v.IsNil() {
			handler = nil
		}
	}
	return &Boundary{handler: handler}
}
func (a *Boundary) serve(w http.ResponseWriter, r *http.Request) {
	if a == nil || a.handler == nil {
		w.Header().Set("Content-Type", "application/problem+json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"type":"about:blank","title":"helm_preview_unavailable","status":503,"code":"helm_preview_unavailable"}`))
		return
	}
	a.handler.ServeHTTP(w, r)
}
func (a *Boundary) CreateHelmPreviewRepository(w http.ResponseWriter, r *http.Request, _ api.CreateHelmPreviewRepositoryParams) {
	a.serve(w, r)
}
func (a *Boundary) GetHelmPreviewRepository(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	a.serve(w, r)
}
func (a *Boundary) ReplaceHelmPreviewRepository(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	a.serve(w, r)
}
func (a *Boundary) CreateHelmPreviewGroup(w http.ResponseWriter, r *http.Request) {
	a.serve(w, r)
}
func (a *Boundary) GetHelmPreviewGroup(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	a.serve(w, r)
}
func (a *Boundary) ReplaceHelmPreviewGroup(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	a.serve(w, r)
}
func (a *Boundary) GetHelmPreviewSource(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID, _, _ string) {
	a.serve(w, r)
}
func (a *Boundary) ListHelmPreviewCharts(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	a.serve(w, r)
}
func (a *Boundary) GetHelmPreviewChart(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID, _, _ string) {
	a.serve(w, r)
}
func (a *Boundary) PublishHelmPreviewChart(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID, _, _ string, _ api.PublishHelmPreviewChartParams) {
	a.serve(w, r)
}
func (a *Boundary) GetHelmPreviewCapacity(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	a.serve(w, r)
}
func (a *Boundary) ReplaceHelmPreviewQuota(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	a.serve(w, r)
}

func (a *Boundary) GetHelmPreviewGovernance(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID, _, _ string) {
	a.serve(w, r)
}
func (a *Boundary) ApplyHelmPreviewGovernance(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID, _, _ string) {
	a.serve(w, r)
}
func (a *Boundary) GetHelmPreviewQuarantineReadPolicy(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	a.serve(w, r)
}
func (a *Boundary) ReplaceHelmPreviewQuarantineReadPolicy(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	a.serve(w, r)
}

func (a *Boundary) CreateHelmPreviewReplicationJob(w http.ResponseWriter, r *http.Request, _ api.CreateHelmPreviewReplicationJobParams) {
	a.serve(w, r)
}
func (a *Boundary) GetHelmPreviewReplicationJob(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	a.serve(w, r)
}
func (a *Boundary) RetryHelmPreviewReplicationJob(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID, _ api.RetryHelmPreviewReplicationJobParams) {
	a.serve(w, r)
}

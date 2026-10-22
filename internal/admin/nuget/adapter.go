// Package nuget binds generated preview operations to the explicitly injected
// NuGet boundary. It does not register protocol routes or construct storage.
package nuget

import (
	"net/http"
	"reflect"

	api "github.com/ccsert/artifact-gateway/internal/admin/openapi"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

type Adapter struct{ handler http.Handler }

func New(handler http.Handler) *Adapter {
	if handler != nil {
		v := reflect.ValueOf(handler)
		if v.Kind() == reflect.Pointer && v.IsNil() {
			handler = nil
		}
	}
	return &Adapter{handler: handler}
}
func (a *Adapter) serve(w http.ResponseWriter, r *http.Request) {
	if a == nil || a.handler == nil {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"type":"about:blank","title":"nuget_preview_unavailable","status":503,"code":"nuget_preview_unavailable"}`))
		return
	}
	a.handler.ServeHTTP(w, r)
}
func (a *Adapter) CreateNuGetPreviewRepository(w http.ResponseWriter, r *http.Request) { a.serve(w, r) }
func (a *Adapter) ReplaceNuGetPreviewRepository(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	a.serve(w, r)
}
func (a *Adapter) CreateNuGetPreviewGroup(w http.ResponseWriter, r *http.Request) { a.serve(w, r) }
func (a *Adapter) GetNuGetPreviewGroup(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	a.serve(w, r)
}
func (a *Adapter) ReplaceNuGetPreviewGroup(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	a.serve(w, r)
}
func (a *Adapter) GetNuGetPreviewGroupSource(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID, _, _ string) {
	a.serve(w, r)
}
func (a *Adapter) RevalidateNuGetPreviewGroupSource(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID, _, _ string) {
	a.serve(w, r)
}
func (a *Adapter) GetNuGetPreviewRepository(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	a.serve(w, r)
}
func (a *Adapter) GetNuGetPreviewCapacity(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	a.serve(w, r)
}
func (a *Adapter) ReplaceNuGetPreviewQuota(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	a.serve(w, r)
}
func (a *Adapter) ListNuGetPreviewPackages(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID, _ api.ListNuGetPreviewPackagesParams) {
	a.serve(w, r)
}
func (a *Adapter) GetNuGetPreviewPackage(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID, _, _ string) {
	a.serve(w, r)
}
func (a *Adapter) GetNuGetPreviewGovernance(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID, _, _ string) {
	a.serve(w, r)
}
func (a *Adapter) ApplyNuGetPreviewGovernance(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID, _, _ string) {
	a.serve(w, r)
}
func (a *Adapter) GetNuGetPreviewQuarantineReadPolicy(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	a.serve(w, r)
}
func (a *Adapter) ReplaceNuGetPreviewQuarantineReadPolicy(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	a.serve(w, r)
}
func (a *Adapter) CreateNuGetPublicationSession(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID, _ api.CreateNuGetPublicationSessionParams) {
	a.serve(w, r)
}
func (a *Adapter) GetNuGetPublicationSession(w http.ResponseWriter, r *http.Request, _, _ openapi_types.UUID) {
	a.serve(w, r)
}
func (a *Adapter) StageNuGetPublicationSession(w http.ResponseWriter, r *http.Request, _, _ openapi_types.UUID) {
	a.serve(w, r)
}
func (a *Adapter) CommitNuGetPublicationSession(w http.ResponseWriter, r *http.Request, _, _ openapi_types.UUID) {
	a.serve(w, r)
}

package app

import (
	"context"
	adminopenapi "github.com/ccsert/artifact-gateway/internal/admin/openapi"
	formatmodule "github.com/ccsert/artifact-gateway/internal/format"
	rawformat "github.com/ccsert/artifact-gateway/internal/format/raw"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

// This adapter supplies shared authorization/member ordering. Raw response,
// publication, proxy fallthrough and quarantine execution live in the module.
type rawModuleGroupRuntime struct {
	resolver v2GroupResolver
	policy   interface {
		GetAnonymousAccessPolicy(context.Context) (repository.AnonymousAccessPolicy, error)
	}
}

func (r rawModuleGroupRuntime) ResolveMembers(ctx context.Context, group repository.HostedGroup) ([]repository.Member, error) {
	return r.resolver.resolveMembers(ctx, group)
}
func (r rawModuleGroupRuntime) AuthorizeMembers(ctx context.Context, p Principal, f repository.Format, path string, members []repository.Member) ([]repository.Member, bool) {
	return r.resolver.authorizeMembers(ctx, p, f, path, members)
}
func (r rawModuleGroupRuntime) AuditResolution(ctx context.Context, group repository.HostedGroup, f repository.Format, path, operation, actor string, outcome repository.AuditOutcome, status int) {
	r.resolver.auditResolution(ctx, group, f, path, operation, actor, outcome, status)
}
func (r rawModuleGroupRuntime) AnonymousAllowed(ctx context.Context) bool {
	return anonymousAccessAllowed(ctx, r.policy)
}

type rawModuleMetrics struct{ metrics *Metrics }

func (m rawModuleMetrics) RecordRawSpoolRejected() {
	if m.metrics != nil {
		m.metrics.recordRawSpoolRejected()
	}
}
func (m rawModuleMetrics) RecordRepositoryAuthorizationDenied(format, source, reason string) {
	if m.metrics != nil {
		m.metrics.recordRepositoryAuthorizationDenied(format, source, reason)
	}
}

// rawFormatRegistry binds one built-in module to a use case's narrow ports.
// Its identity is fixed in code, so a registration error is a programming bug.
// No storage, authentication or transport is manufactured here.
func rawFormatRegistry(options rawformat.Options) *formatmodule.Registry {
	registry, err := formatmodule.New(rawformat.New(options))
	if err != nil {
		panic(err)
	}
	return registry
}

func validMigratedCoordinate(format repository.Format, value string) bool {
	if capability, ok := formatmodule.Capability[formatmodule.Governance](rawFormatRegistry(rawformat.Options{}), format); ok {
		return capability.ValidCoordinate(value)
	}
	return true
}
func formatArtifactSummaryResponse(summary formatmodule.ArtifactSummary) adminopenapi.ArtifactSummary {
	return adminopenapi.ArtifactSummary{Coordinate: summary.Coordinate, Digest: &summary.Digest, ContentType: &summary.ContentType, Size: &summary.Size, CreatedAt: &summary.CreatedAt}
}

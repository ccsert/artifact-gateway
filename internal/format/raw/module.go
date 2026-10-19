// Package raw owns the Raw format's application behavior. Wire grammar and
// durable execution remain in the protocol and maintenance packages.
package raw

import (
	"context"
	"net/http"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	formatmodule "github.com/ccsert/artifact-gateway/internal/format"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	rawprotocol "github.com/ccsert/artifact-gateway/internal/protocol/raw"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

type RepositoryLookup interface {
	GetHostedRepository(context.Context, string) (repository.HostedRepository, error)
	GetHostedRepositoryByName(context.Context, string) (repository.HostedRepository, error)
}
type GroupLookup interface {
	GetHostedGroupByName(context.Context, string) (repository.HostedGroup, error)
}
type AuditRecorder interface {
	RecordAudit(context.Context, repository.AuditRecord) error
}
type AnonymousPolicy interface {
	GetAnonymousAccessPolicy(context.Context) (repository.AnonymousAccessPolicy, error)
}
type Metrics interface {
	RecordRawSpoolRejected()
	RecordRepositoryAuthorizationDenied(string, string, string)
}
type PublicationScanner interface {
	Schedule(context.Context, repository.HostedRepository, string, string, string) error
}

// GroupRuntime supplies shared member ordering/authorization and durable audit;
// Raw owns the format-specific resolution and response decisions.
type GroupRuntime interface {
	ResolveMembers(context.Context, repository.HostedGroup) ([]repository.Member, error)
	AuthorizeMembers(context.Context, authorization.Principal, repository.Format, string, []repository.Member) ([]repository.Member, bool)
	AuditResolution(context.Context, repository.HostedGroup, repository.Format, string, string, string, repository.AuditOutcome, int)
	AnonymousAllowed(context.Context) bool
}

// JobStore and ReplicationStore retain the concrete durable store's dynamic
// transaction/lease/notification ports; never compose wrappers that erase them.
type JobStore interface {
	repository.NativeRawStore
	repository.LifecycleJobStore
}
type ReplicationStore interface {
	repository.NativeRawStore
	repository.ReplicationStore
}

type PublicationStore interface {
	LockRawObject(context.Context, string) (func(), error)
	StageRawObject(context.Context, repository.RawObject) error
	PutRawAssetIf(context.Context, repository.RawAsset, repository.RawWriteCondition) (repository.RawAsset, error)
	GetRawAsset(context.Context, string, string) (repository.RawAsset, error)
}

type Options struct {
	Publication       PublicationStore
	Jobs              JobStore
	Replications      ReplicationStore
	Intelligence      repository.ArtifactIntelligenceStore
	BackgroundMetrics repository.BackgroundOperationMetrics
	Store             repository.NativeRawStore
	Repositories      RepositoryLookup
	Groups            GroupLookup
	Objects           objectstore.Store
	Authenticator     authorization.Authenticator
	Authorizer        authorization.RepositoryAuthorizer
	Audit             AuditRecorder
	Anonymous         AnonymousPolicy
	ReadPolicies      repository.RepositoryQuarantineReadPolicyStore
	Quarantine        repository.ArtifactQuarantineStore
	Client            rawprotocol.Client
	Cache             *rawprotocol.Cache
	Metrics           Metrics
	Scanner           PublicationScanner
	GroupRuntime      GroupRuntime
	LegacyStore       repository.RawStore
	LegacyRuntime     rawprotocol.Runtime
	RecordFailure     func(context.Context, string)
}

type Module struct {
	publication        PublicationStore
	jobs               JobStore
	replications       ReplicationStore
	intelligence       repository.ArtifactIntelligenceStore
	backgroundMetrics  repository.BackgroundOperationMetrics
	store              repository.NativeRawStore
	repos              RepositoryLookup
	groups             GroupLookup
	objects            objectstore.Store
	auth               authorization.Authenticator
	authorizer         authorization.RepositoryAuthorizer
	audit              AuditRecorder
	anonymous          AnonymousPolicy
	readPolicies       repository.RepositoryQuarantineReadPolicyStore
	quarantine         repository.ArtifactQuarantineStore
	proxyClient        rawprotocol.Client
	proxyCache         *rawprotocol.Cache
	metrics            Metrics
	publicationScanner PublicationScanner
	groupRuntime       GroupRuntime
	legacy             rawprotocol.Handler
	recordFailure      func(context.Context, string)
}

func New(options Options) *Module {
	return &Module{publication: options.Publication, jobs: options.Jobs, replications: options.Replications, intelligence: options.Intelligence, backgroundMetrics: options.BackgroundMetrics, store: options.Store, repos: options.Repositories, groups: options.Groups, objects: options.Objects,
		auth: options.Authenticator, authorizer: options.Authorizer, audit: options.Audit, anonymous: options.Anonymous,
		readPolicies: options.ReadPolicies, quarantine: options.Quarantine, proxyClient: options.Client, proxyCache: options.Cache,
		metrics: options.Metrics, publicationScanner: options.Scanner, groupRuntime: options.GroupRuntime,
		legacy:        rawprotocol.Handler{Store: options.LegacyStore, Runtime: options.LegacyRuntime, Client: options.Client, Cache: options.Cache},
		recordFailure: options.RecordFailure}
}
func (*Module) Format() repository.Format { return repository.FormatRaw }
func (m *Module) Routes() []formatmodule.Route {
	return []formatmodule.Route{{Pattern: "/raw/", Handler: m}}
}
func (m *Module) failure(ctx context.Context, code string) {
	if m.recordFailure != nil {
		m.recordFailure(ctx, code)
	}
}

func (m *Module) anonymousAllowed(ctx context.Context) bool {
	if m.anonymous == nil {
		return false
	}
	policy, err := m.anonymous.GetAnonymousAccessPolicy(ctx)
	return err == nil && policy.Enabled
}
func (m *Module) anonymousRepositoryRead(ctx context.Context, repo repository.HostedRepository, method string) bool {
	return (method == http.MethodGet || method == http.MethodHead) && repo.State == repository.RepositoryActive && repo.AnonymousRead && m.anonymousAllowed(ctx)
}

const anonymousActor = "anonymous"
const anonymousAuthorizationSource = "anonymous_policy"
const anonymousAuthorizationReason = "global_and_resource_policy_enabled"

func anonymousPrincipal() authorization.Principal {
	return authorization.Principal{Actor: anonymousActor}
}
func isAnonymous(p authorization.Principal) bool { return p.Actor == anonymousActor }

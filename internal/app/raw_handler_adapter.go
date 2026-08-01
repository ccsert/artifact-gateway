package app

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	rawprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/raw"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// RawHandler is the app composition adapter for the protocol-owned handler.
type RawHandler struct {
	Store         repository.RawStore
	Repositories  repository.HostedRepositoryStore
	Authorizer    RepositoryAuthorizer
	Authenticator Authenticator
	Client        RawClient
	Metrics       *Metrics
	Cache         *RawCache
}

func (h RawHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r = r.WithContext(withRawAuditCorrelation(r.Context(), r.Header.Get("X-Request-ID")))
	rawprotocol.Handler{Store: h.Store, Client: h.Client, Cache: h.Cache, Runtime: rawRuntime{handler: h}}.ServeHTTP(w, r)
}

type rawRuntime struct{ handler RawHandler }

func (r rawRuntime) Authenticate(header string) (rawprotocol.Principal, bool) {
	p, ok := r.handler.Authenticator.Authenticate(header)
	return rawprotocol.Principal{Actor: p.Actor, Admin: p.Admin}, ok
}

func (r rawRuntime) AnonymousAllowed(ctx context.Context, groupName string) bool {
	if r.handler.Store == nil || !anonymousAccessAllowed(ctx, r.handler.Store) {
		return false
	}
	group, err := r.handler.Store.GetRawGroup(ctx, groupName)
	if err != nil || !group.Enabled || !group.Anonymous {
		return false
	}
	for _, member := range group.Members {
		if member.Anonymous {
			return true
		}
	}
	return false
}

func (r rawRuntime) CanRead(principal rawprotocol.Principal, name string) bool {
	p := r.handler.Authenticator.PrincipalForActor(principal.Actor)
	p.Admin = principal.Admin
	return r.handler.Authenticator.CanReadRepository(p, name)
}

func (r rawRuntime) ManagedDecision(ctx context.Context, principal rawprotocol.Principal, member repository.Member, resource string) (rawprotocol.Decision, bool) {
	decision, managed := ManagedGroupMemberDecision(ctx, r.handler.Repositories, r.handler.Authorizer, Principal{Actor: principal.Actor, Admin: principal.Admin}, member, repository.FormatRaw, resource)
	return rawprotocol.Decision{Allowed: decision.Allowed, Source: decision.Source, Reason: decision.Reason}, managed
}

func (r rawRuntime) Prioritize(members []repository.Member) []repository.Member {
	return prioritizeHosted(members)
}
func (r rawRuntime) RecordRequest(group, method string) {
	if r.handler.Metrics == nil {
		return
	}
	r.handler.Metrics.recordRawRequest(method)
}
func (r rawRuntime) RecordRepositoryRequest(group string) {
	if r.handler.Metrics != nil {
		r.handler.Metrics.recordRequest(group)
	}
}
func (r rawRuntime) RecordAnonymousRead() {
	if r.handler.Metrics != nil {
		r.handler.Metrics.recordAnonymousRead()
	}
}
func (r rawRuntime) RecordCacheHit() {
	if r.handler.Metrics != nil {
		r.handler.Metrics.recordRawCacheHit()
	}
}
func (r rawRuntime) RecordCacheMiss() {
	if r.handler.Metrics != nil {
		r.handler.Metrics.recordRawCacheMiss()
	}
}
func (r rawRuntime) RecordNegativeCacheHit() {
	if r.handler.Metrics != nil {
		r.handler.Metrics.recordRawNegativeCacheHit()
	}
}
func (r rawRuntime) RecordQuotaDenied() {
	if r.handler.Metrics != nil {
		r.handler.Metrics.cacheQuotaDenied.Add(1)
	}
}

func (r rawRuntime) Audit(ctx context.Context, group, path string, event rawprotocol.AuditEvent) {
	if event.Actor == "" {
		event.Actor = "anonymous"
	}
	upstreamHost := ""
	if endpoint, err := url.Parse(event.Member.Endpoint); err == nil {
		upstreamHost = endpoint.Hostname()
	}
	_ = r.handler.Store.RecordAudit(ctx, repository.AuditRecord{
		GroupName: group, Repository: group, MemberName: event.Member.Name, Actor: event.Actor, Outcome: event.Outcome, OccurredAt: time.Now().UTC(),
		Format: "raw", Resource: path, Representation: "body", MemberType: string(event.Member.Type), UpstreamHost: upstreamHost,
		Operation: strings.ToLower(event.Method), Status: event.Status, CacheDisposition: event.CacheDisposition, Bytes: event.Bytes,
		AuthorizationSource: event.AuthorizationSource, AuthorizationReason: event.AuthorizationReason,
		RequestID: rawAuditRequestID(ctx), TraceID: rawAuditTraceID(ctx),
	})
	if r.handler.Metrics != nil {
		r.handler.Metrics.recordRawAudit(event.Outcome, event.Bytes, event.ChecksumFailure)
		r.handler.Metrics.recordRepositoryAuthorizationDenied("raw", event.AuthorizationSource, event.AuthorizationReason)
	}
}

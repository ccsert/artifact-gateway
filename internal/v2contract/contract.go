// Package v2contract contains the shared V2 rules that format adapters use.
package v2contract

import (
	"net"
	"net/url"
	"strings"
	"time"
)

type MemberType string

const (
	Hosted MemberType = "hosted"
	Proxy  MemberType = "proxy"
)

type Member struct {
	Name                    string
	Type                    MemberType
	Anonymous, ProxyAllowed bool
	Endpoint                string
}
type CanonicalResource struct{ value string }

func NewCanonicalResource(path string) (CanonicalResource, bool) {
	path, ok := CanonicalRawPath(path)
	if !ok {
		return CanonicalResource{}, false
	}
	return CanonicalResource{value: path}, true
}

func (r CanonicalResource) String() string { return r.value }

type Request struct {
	ID, Actor                                string
	Authenticated                            bool
	Format, Group, Representation, Operation string
	Resource                                 CanonicalResource
}
type CacheKey struct {
	Format, Group, Representation, Operation, Member, Endpoint string
	Resource                                                   CanonicalResource
}
type FetchResult struct {
	Status    int
	Outcome   string
	Cacheable bool
	Bytes     int64
}
type Audit struct {
	OccurredAt                                                                                  time.Time
	RequestID                                                                                   string
	Actor, Format, Group, Resource, Representation, Member, MemberType, UpstreamHost, Operation string
	Status                                                                                      int
	Outcome, CacheDisposition                                                                   string
	Bytes                                                                                       int64
}
type Source interface {
	Fetch(Member, Request) FetchResult
}
type Cache interface {
	Load(CacheKey) (FetchResult, bool)
	Store(CacheKey, FetchResult)
}
type Authorizer interface {
	Allowed(Member, Request) bool
}
type Auditor interface{ Record(Audit) }
type Resolver struct {
	GroupAnonymous bool
	Members        []Member
	Source         Source
	Cache          Cache
	Authorizer     Authorizer
	Auditor        Auditor
}

func (r Resolver) Resolve(request Request) (int, string) {
	actor := "anonymous"
	if request.Authenticated {
		actor = request.Actor
		if actor == "" {
			actor = "user"
		}
	}
	audit := func(member Member, status int, outcome, disposition string, bytes int64) {
		r.Auditor.Record(Audit{
			OccurredAt: time.Now().UTC(), RequestID: request.ID, Actor: actor, Format: request.Format, Group: request.Group, Resource: request.Resource.String(), Representation: request.Representation,
			Member: member.Name, MemberType: string(member.Type), UpstreamHost: endpointHost(member.Endpoint),
			Operation: request.Operation, Status: status, Outcome: outcome, CacheDisposition: disposition, Bytes: bytes,
		})
	}
	if !request.Authenticated && !r.GroupAnonymous {
		audit(Member{}, 401, "access_denied", "bypass", 0)
		return 401, "access_denied"
	}
	if !request.Authenticated && !anonymousReadOperation(request.Operation) {
		audit(Member{}, 403, "access_denied", "bypass", 0)
		return 403, "access_denied"
	}
	if request.Resource.value == "" {
		audit(Member{}, 400, "upstream_error", "bypass", 0)
		return 400, "upstream_error"
	}
	for _, kind := range []MemberType{Hosted, Proxy} {
		for _, member := range r.Members {
			if member.Type != kind {
				continue
			}
			if request.Authenticated && (r.Authorizer == nil || !r.Authorizer.Allowed(member, request)) {
				audit(member, 403, "access_denied", "bypass", 0)
				return 403, "access_denied"
			}
			if !request.Authenticated && !member.Anonymous {
				audit(member, 401, "access_denied", "bypass", 0)
				return 401, "access_denied"
			}
			if kind == Proxy && !member.ProxyAllowed {
				audit(member, 403, "proxy_denied", "bypass", 0)
				continue
			}
			key := CacheKey{Format: request.Format, Group: request.Group, Resource: request.Resource, Representation: request.Representation, Operation: request.Operation, Member: member.Name, Endpoint: member.Endpoint}
			if result, ok := r.Cache.Load(key); ok {
				audit(member, result.Status, cacheOutcome(result), "hit", result.Bytes)
				if result.Status >= 200 && result.Status < 300 {
					return result.Status, "resolved"
				}
				continue
			}
			result := r.Source.Fetch(member, request)
			if result.Status >= 200 && result.Status < 300 || result.Status == 404 {
				if result.Cacheable {
					r.Cache.Store(key, result)
				}
			}
			if result.Status >= 200 && result.Status < 300 {
				audit(member, result.Status, "resolved", "miss", result.Bytes)
				return result.Status, "resolved"
			}
			if result.Status == 404 {
				audit(member, 404, "not_found", "miss", 0)
				continue
			}
			audit(member, result.Status, "upstream_error", "bypass", 0)
			return result.Status, "upstream_error"
		}
	}
	audit(Member{}, 404, "not_found", "miss", 0)
	return 404, "not_found"
}

func anonymousReadOperation(operation string) bool {
	switch strings.ToLower(operation) {
	case "get", "head":
		return true
	default:
		return false
	}
}

func cacheOutcome(result FetchResult) string {
	if result.Status == 404 {
		return "not_found"
	}
	return "resolved"
}

func endpointHost(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	host, _, err := net.SplitHostPort(u.Host)
	if err == nil {
		return host
	}
	return u.Host
}

func CanonicalRawPath(path string) (string, bool) {
	if path == "" || strings.HasSuffix(path, "/") {
		return "", false
	}
	for _, encoded := range strings.Split(path, "/") {
		decoded, err := url.PathUnescape(encoded)
		if err != nil || decoded == "" || decoded == "." || decoded == ".." || strings.ContainsAny(decoded, "\\\x00") || strings.Contains(strings.ToLower(encoded), "%2f") || url.PathEscape(decoded) != encoded {
			return "", false
		}
	}
	return path, true
}

func ConanReadEndpoint(method, path string) bool {
	if method != "GET" && method != "HEAD" {
		return false
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 5 || parts[0] != "conans" {
		return false
	}
	for _, segment := range parts[1:] {
		if !validConanSegment(segment) {
			return false
		}
	}
	rest := parts[5:]
	return matches(rest, "revisions") || matches(rest, "revisions", "*", "files") || matches(rest, "revisions", "*", "files", "*") || matches(rest, "revisions", "*", "packages", "*", "revisions") || matches(rest, "revisions", "*", "packages", "*", "revisions", "*", "files") || matches(rest, "revisions", "*", "packages", "*", "revisions", "*", "files", "*")
}
func matches(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if want[i] != "*" && got[i] != want[i] {
			return false
		}
	}
	return true
}
func validConanSegment(s string) bool {
	lower := strings.ToLower(s)
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, "/\\\x00#") && !strings.Contains(lower, "%2f") && !strings.Contains(lower, "%23")
}

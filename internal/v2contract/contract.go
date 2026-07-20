// Package v2contract contains the shared V2 rules that format adapters use.
package v2contract

import (
	"net/url"
	"strings"
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
type Audit struct{ Actor, Outcome, Member string }
type Source interface{ Fetch(Member, string) int }
type Cache interface {
	Load(Member, string) (int, bool)
	Store(Member, string, int)
}
type Auditor interface{ Record(Audit) }
type Resolver struct {
	GroupAnonymous bool
	Members        []Member
	Source         Source
	Cache          Cache
	Auditor        Auditor
}

func (r Resolver) Resolve(authenticated bool, path string) (int, string) {
	actor := "anonymous"
	if authenticated {
		actor = "user"
	}
	audit := func(outcome, member string) { r.Auditor.Record(Audit{Actor: actor, Outcome: outcome, Member: member}) }
	if !authenticated && !r.GroupAnonymous {
		audit("access_denied", "")
		return 401, "access_denied"
	}
	for _, kind := range []MemberType{Hosted, Proxy} {
		for _, member := range r.Members {
			if member.Type != kind {
				continue
			}
			if !authenticated && !member.Anonymous {
				audit("access_denied", member.Name)
				return 401, "access_denied"
			}
			if kind == Proxy && !member.ProxyAllowed {
				audit("proxy_denied", member.Name)
				continue
			}
			if status, ok := r.Cache.Load(member, path); ok {
				audit(cacheOutcome(status), member.Name)
				if status == 200 {
					return 200, "resolved"
				}
				continue
			}
			status := r.Source.Fetch(member, path)
			if status == 200 || status == 404 {
				r.Cache.Store(member, path, status)
			}
			if status == 200 {
				audit("resolved", member.Name)
				return 200, "resolved"
			}
			if status == 404 {
				audit("not_found", member.Name)
			}
		}
	}
	audit("not_found", "")
	return 404, "not_found"
}

func cacheOutcome(status int) string {
	if status == 404 {
		return "not_found"
	}
	return "resolved"
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

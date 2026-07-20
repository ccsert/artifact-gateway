package contracts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type v2MemberType string

const (
	v2Hosted v2MemberType = "hosted"
	v2Proxy  v2MemberType = "proxy"
)

type v2Member struct {
	name           string
	type_          v2MemberType
	anonymous      bool
	endpoint       string
	proxyAllowed   bool
	upstreamStatus int
}

type v2CacheEntry struct {
	status int
	member string
}

type v2Audit struct {
	actor, outcome, member string
}

// v2ResolverHarness is deliberately small: format adapters will own protocol
// parsing, while this fixes the shared policy, precedence, cache, and audit
// contract before those adapters exist.
type v2ResolverHarness struct {
	groupAnonymous bool
	members        []v2Member
	cache          map[string]v2CacheEntry
	audits         []v2Audit
	fetches        []string
}

func (h *v2ResolverHarness) resolve(authenticated bool, path string) (int, string) {
	if !authenticated && !h.groupAnonymous {
		h.audit("anonymous", "access_denied", "")
		return 401, "access_denied"
	}
	for _, memberType := range []v2MemberType{v2Hosted, v2Proxy} {
		for _, member := range h.members {
			if member.type_ != memberType {
				continue
			}
			if !authenticated && !member.anonymous {
				h.audit("anonymous", "access_denied", member.name)
				return 401, "access_denied"
			}
			if member.type_ == v2Proxy && !member.proxyAllowed {
				h.audit(actor(authenticated), "proxy_denied", member.name)
				continue
			}
			key := member.name + ":" + member.endpoint + ":" + path
			if cached, ok := h.cache[key]; ok {
				h.audit(actor(authenticated), outcome(cached.status), cached.member)
				if cached.status == 200 {
					return 200, "resolved"
				}
				continue
			}
			h.fetches = append(h.fetches, member.name)
			if member.upstreamStatus == 200 {
				h.cache[key] = v2CacheEntry{status: 200, member: member.name}
				h.audit(actor(authenticated), "resolved", member.name)
				return 200, "resolved"
			}
			if member.upstreamStatus == 404 {
				h.cache[key] = v2CacheEntry{status: 404, member: member.name}
				h.audit(actor(authenticated), "not_found", member.name)
			}
		}
	}
	h.audit(actor(authenticated), "not_found", "")
	return 404, "not_found"
}

func (h *v2ResolverHarness) audit(actor, result, member string) {
	h.audits = append(h.audits, v2Audit{actor: actor, outcome: result, member: member})
}

func actor(authenticated bool) string {
	if authenticated {
		return "user"
	}
	return "anonymous"
}

func outcome(status int) string {
	if status == 404 {
		return "not_found"
	}
	return "resolved"
}

func newV2Resolver(groupAnonymous bool, members ...v2Member) *v2ResolverHarness {
	return &v2ResolverHarness{groupAnonymous: groupAnonymous, members: members, cache: make(map[string]v2CacheEntry)}
}

func TestV2ContractResolverScenarios(t *testing.T) {
	for _, test := range []struct {
		name          string
		resolver      *v2ResolverHarness
		authenticated bool
		wantStatus    int
		wantOutcome   string
		wantFetches   []string
		wantAudit     string
		mustAudit     string
	}{
		{"anonymous defaults to deny", newV2Resolver(false, v2Member{name: "hosted", type_: v2Hosted, anonymous: true, upstreamStatus: 200}), false, 401, "access_denied", nil, "access_denied", "access_denied"},
		{"repository narrows anonymous group", newV2Resolver(true, v2Member{name: "hosted", type_: v2Hosted, upstreamStatus: 200}), false, 401, "access_denied", nil, "access_denied", "access_denied"},
		{"anonymous requires group and repository permission", newV2Resolver(true, v2Member{name: "hosted", type_: v2Hosted, anonymous: true, upstreamStatus: 200}), false, 200, "resolved", []string{"hosted"}, "resolved", "resolved"},
		{"hosted positive result precedes proxy", newV2Resolver(true, v2Member{name: "proxy", type_: v2Proxy, anonymous: true, proxyAllowed: true, upstreamStatus: 200}, v2Member{name: "hosted", type_: v2Hosted, anonymous: true, upstreamStatus: 200}), false, 200, "resolved", []string{"hosted"}, "resolved", "resolved"},
		{"hosted negative cache cannot hide hosted success", func() *v2ResolverHarness {
			h := newV2Resolver(true, v2Member{name: "hosted-a", type_: v2Hosted, anonymous: true, endpoint: "a", upstreamStatus: 404}, v2Member{name: "hosted-b", type_: v2Hosted, anonymous: true, endpoint: "b", upstreamStatus: 200}, v2Member{name: "proxy", type_: v2Proxy, anonymous: true, endpoint: "p", proxyAllowed: true, upstreamStatus: 200})
			h.cache["hosted-a:a:pkg"] = v2CacheEntry{status: 404, member: "hosted-a"}
			h.cache["proxy:p:pkg"] = v2CacheEntry{status: 404, member: "proxy"}
			return h
		}(), false, 200, "resolved", []string{"hosted-b"}, "resolved", "resolved"},
		{"proxy denial is audited without fetch", newV2Resolver(true, v2Member{name: "proxy", type_: v2Proxy, anonymous: true, proxyAllowed: false, upstreamStatus: 200}), false, 404, "not_found", nil, "not_found", "proxy_denied"},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, gotOutcome := test.resolver.resolve(test.authenticated, "pkg")
			if status != test.wantStatus || gotOutcome != test.wantOutcome {
				t.Fatalf("resolve = (%d, %q), want (%d, %q)", status, gotOutcome, test.wantStatus, test.wantOutcome)
			}
			if got := strings.Join(test.resolver.fetches, ","); got != strings.Join(test.wantFetches, ",") {
				t.Fatalf("fetches = %q, want %q", got, strings.Join(test.wantFetches, ","))
			}
			if got := test.resolver.audits[len(test.resolver.audits)-1]; got.actor != actor(test.authenticated) || got.outcome != test.wantAudit {
				t.Fatalf("final audit = %#v, want actor %q and outcome %q", got, actor(test.authenticated), test.wantAudit)
			}
			for _, audit := range test.resolver.audits {
				if audit.outcome == test.mustAudit {
					return
				}
			}
			t.Fatalf("audits = %#v, missing outcome %q", test.resolver.audits, test.mustAudit)
		})
	}
}

func TestV2ContractRawAndConanShapes(t *testing.T) {
	for _, test := range []struct {
		value string
		valid bool
	}{
		{"artifacts/app.tar.gz", true}, {"artifacts/%2fsecret", false}, {"artifacts/../secret", false}, {"artifacts/", false},
	} {
		t.Run(test.value, func(t *testing.T) {
			got := canonicalV2Path(test.value)
			if (got != "") != test.valid {
				t.Fatalf("canonicalV2Path(%q) = %q, valid = %v", test.value, got, test.valid)
			}
		})
	}
	for _, segment := range []struct {
		value string
		valid bool
	}{{"pkg", true}, {"pkg%23rev", false}, {"pkg#rev", false}, {"%2f", false}, {"..", false}} {
		if got := validConanSegment(segment.value); got != segment.valid {
			t.Errorf("validConanSegment(%q) = %v, want %v", segment.value, got, segment.valid)
		}
	}
}

func canonicalV2Path(path string) string {
	if strings.HasSuffix(path, "/") || path == "" {
		return ""
	}
	for _, segment := range strings.Split(path, "/") {
		if !validConanSegment(segment) || strings.Contains(strings.ToLower(segment), "%2f") {
			return ""
		}
	}
	return path
}

func validConanSegment(segment string) bool {
	lower := strings.ToLower(segment)
	return segment != "" && segment != "." && segment != ".." && !strings.ContainsAny(segment, "/\\\x00#") && !strings.Contains(lower, "%2f") && !strings.Contains(lower, "%23")
}

func TestV2ContractMigrationProjection(t *testing.T) {
	type oldGroup struct{ name, format string }
	groups := []oldGroup{{"oci", "oci"}, {"maven", "maven"}, {"raw", "raw"}}
	for _, format := range []string{"oci", "maven"} {
		var got []string
		for _, group := range groups {
			if group.format == format {
				got = append(got, group.name)
			}
		}
		if len(got) != 1 || got[0] != format {
			t.Fatalf("%s compatibility view = %v", format, got)
		}
	}
}

func TestV2ContractHasRequiredDecisions(t *testing.T) {
	document, err := os.ReadFile(filepath.Join("..", "docs", "v2-contract.md"))
	if err != nil {
		t.Fatalf("read V2 contract: %v", err)
	}
	for _, clause := range []string{"CONTRACT: anonymous-default-deny", "CONTRACT: audit-fields", "CONTRACT: raw-path-normalization", "CONTRACT: raw-checksum", "CONTRACT: raw-proxy-allowlist", "CONTRACT: raw-cache", "CONTRACT: conan2-only", "CONTRACT: conan-coordinate", "CONTRACT: conan2-read-endpoints", "CONTRACT: conan-cache", "CONTRACT: conan-proxy-allowlist", "CONTRACT: fixtures-and-upgrade"} {
		if !strings.Contains(string(document), clause) {
			t.Errorf("V2 contract is missing %q", clause)
		}
	}
}

func Example_v2ResolverHarness() {
	h := newV2Resolver(true, v2Member{name: "hosted", type_: v2Hosted, anonymous: true, upstreamStatus: 200})
	status, result := h.resolve(false, "com/example/app")
	fmt.Println(status, result)
	// Output: 200 resolved
}

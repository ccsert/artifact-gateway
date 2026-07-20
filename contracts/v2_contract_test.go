package contracts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/v2contract"
)

type fakeSource map[string]v2contract.FetchResult

func (f fakeSource) Fetch(member v2contract.Member, _ v2contract.Request) v2contract.FetchResult {
	return f[member.Name]
}

type fakeCache map[string]v2contract.FetchResult

func (f fakeCache) key(key v2contract.CacheKey) string {
	return strings.Join([]string{key.Format, key.Group, key.Resource.String(), key.Representation, key.Operation, key.Member, key.Endpoint}, ":")
}
func (f fakeCache) Load(key v2contract.CacheKey) (v2contract.FetchResult, bool) {
	v, ok := f[f.key(key)]
	return v, ok
}
func (f fakeCache) Store(key v2contract.CacheKey, result v2contract.FetchResult) {
	f[f.key(key)] = result
}

type fakeAudit []v2contract.Audit

func (f *fakeAudit) Record(a v2contract.Audit) { *f = append(*f, a) }

func canonicalResource(t *testing.T, path string) v2contract.CanonicalResource {
	t.Helper()
	resource, ok := v2contract.NewCanonicalResource(path)
	if !ok {
		t.Fatalf("invalid test resource %q", path)
	}
	return resource
}

func TestV2ResolverContract(t *testing.T) {
	for _, tc := range []struct {
		name          string
		group, member bool
		members       []v2contract.Member
		source        fakeSource
		cached        fakeCache
		status        int
		fetch         string
		audit         string
	}{
		{"default deny", false, true, []v2contract.Member{{Name: "hosted", Type: v2contract.Hosted, Anonymous: true}}, fakeSource{"hosted": {Status: 200, Cacheable: true}}, fakeCache{}, 401, "", "access_denied"},
		{"repository narrows group", true, false, []v2contract.Member{{Name: "hosted", Type: v2contract.Hosted}}, fakeSource{"hosted": {Status: 200, Cacheable: true}}, fakeCache{}, 401, "", "access_denied"},
		{"hosted before proxy and proxy negative", true, false, []v2contract.Member{{Name: "proxy", Type: v2contract.Proxy, Anonymous: true, ProxyAllowed: true, Endpoint: "https://p"}, {Name: "hosted", Type: v2contract.Hosted, Anonymous: true, Endpoint: "https://h"}}, fakeSource{"hosted": {Status: 200, Cacheable: true}}, fakeCache{"raw:g:pkg:file:get:proxy:https://p": {Status: 404, Cacheable: true}}, 200, "hosted", "resolved"},
		{"hosted negative then proxy", true, false, []v2contract.Member{{Name: "hosted", Type: v2contract.Hosted, Anonymous: true}, {Name: "proxy", Type: v2contract.Proxy, Anonymous: true, ProxyAllowed: true}}, fakeSource{"hosted": {Status: 404, Cacheable: true}, "proxy": {Status: 200, Cacheable: true}}, fakeCache{}, 200, "hosted,proxy", "resolved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			audit := fakeAudit{}
			fetched := []string{}
			source := tc.source
			r := v2contract.Resolver{GroupAnonymous: tc.group, Members: tc.members, Cache: tc.cached, Auditor: &audit, Source: fakeSourceFunc(func(m v2contract.Member, p v2contract.Request) v2contract.FetchResult {
				fetched = append(fetched, m.Name)
				return source.Fetch(m, p)
			})}
			status, _ := r.Resolve(v2contract.Request{ID: "test-request", Format: "raw", Group: "g", Resource: canonicalResource(t, "pkg"), Representation: "file", Operation: "get"})
			if status != tc.status || strings.Join(fetched, ",") != tc.fetch || audit[len(audit)-1].Outcome != tc.audit {
				t.Fatalf("status=%d fetch=%q audit=%#v", status, fetched, audit)
			}
		})
	}
}

type fakeSourceFunc func(v2contract.Member, v2contract.Request) v2contract.FetchResult

func (f fakeSourceFunc) Fetch(m v2contract.Member, p v2contract.Request) v2contract.FetchResult {
	return f(m, p)
}

func TestV2ResolverCacheAndUpstreamErrorContract(t *testing.T) {
	members := []v2contract.Member{{Name: "member", Type: v2contract.Hosted, Anonymous: true, Endpoint: "https://host.example:8443"}}
	cache := fakeCache{}
	audit := fakeAudit{}
	r := v2contract.Resolver{GroupAnonymous: true, Members: members, Cache: cache, Auditor: &audit, Source: fakeSource{"member": {Status: 200, Cacheable: true, Bytes: 7}}}
	request := v2contract.Request{ID: "request-42", Format: "raw", Group: "alpha", Resource: canonicalResource(t, "artifact"), Representation: "body", Operation: "get"}
	if status, _ := r.Resolve(request); status != 200 {
		t.Fatalf("first status=%d", status)
	}
	request.Group = "beta"
	if status, _ := r.Resolve(request); status != 200 || len(cache) != 2 {
		t.Fatalf("cross-group cache leaked: status=%d entries=%d", status, len(cache))
	}
	request.Group, request.Representation = "alpha", "metadata"
	if status, _ := r.Resolve(request); status != 200 || len(cache) != 3 {
		t.Fatalf("cross-representation cache leaked: status=%d entries=%d", status, len(cache))
	}
	last := audit[len(audit)-1]
	if last.OccurredAt.IsZero() || last.RequestID != "request-42" || last.Actor != "anonymous" || last.Format != "raw" || last.Group != "alpha" || last.Resource != "artifact" || last.Representation != "metadata" || last.MemberType != "hosted" || last.UpstreamHost != "host.example" || last.Operation != "get" || last.Status != 200 || last.CacheDisposition != "miss" || last.Bytes != 7 {
		t.Fatalf("incomplete audit: %#v", last)
	}

	cache, audit = fakeCache{}, fakeAudit{}
	r = v2contract.Resolver{GroupAnonymous: true, Members: members, Cache: cache, Auditor: &audit, Source: fakeSource{"member": {Status: 502, Outcome: "upstream_error"}}}
	if status, outcome := r.Resolve(request); status != 502 || outcome != "upstream_error" || len(cache) != 0 || audit[len(audit)-1].Outcome != "upstream_error" {
		t.Fatalf("checksum error not propagated: status=%d outcome=%s cache=%d audit=%#v", status, outcome, len(cache), audit)
	}
	for _, status := range []int{400, 500, 503} {
		cache, audit = fakeCache{}, fakeAudit{}
		r.Cache, r.Auditor, r.Source = cache, &audit, fakeSource{"member": {Status: status, Outcome: "upstream_error"}}
		if got, _ := r.Resolve(request); got != status || len(cache) != 0 || audit[len(audit)-1].Outcome != "upstream_error" {
			t.Fatalf("status=%d got=%d cache=%d audit=%#v", status, got, len(cache), audit)
		}
	}
	cache, audit = fakeCache{}, fakeAudit{}
	r.Cache, r.Auditor, r.Source = cache, &audit, fakeSource{"member": {Status: 500, Outcome: "resolved"}}
	if got, outcome := r.Resolve(request); got != 500 || outcome != "upstream_error" || len(cache) != 0 || audit[len(audit)-1].Outcome != "upstream_error" {
		t.Fatalf("source error outcome trusted: status=%d outcome=%s cache=%d audit=%#v", got, outcome, len(cache), audit)
	}
	r.Cache, r.Auditor, r.Source = fakeCache{}, &fakeAudit{}, fakeSource{"member": {Status: 206, Cacheable: true}}
	if got, _ := r.Resolve(request); got != 206 {
		t.Fatalf("range status lost: got=%d", got)
	}
	if got, _ := r.Resolve(request); got != 206 {
		t.Fatalf("cached range status lost: got=%d", got)
	}
	if _, ok := v2contract.NewCanonicalResource("artifact/%61"); ok {
		t.Fatal("non-canonical resource accepted")
	}
	cache, audit = fakeCache{}, fakeAudit{}
	r.Cache, r.Auditor = cache, &audit
	request.Resource = v2contract.CanonicalResource{}
	if got, _ := r.Resolve(request); got != 400 || len(cache) != 0 || audit[len(audit)-1].Outcome != "upstream_error" {
		t.Fatalf("missing canonical resource reached resolver: status=%d cache=%d audit=%#v", got, len(cache), audit)
	}
}

func TestV2RawAndConanContract(t *testing.T) {
	for _, tc := range []struct {
		path string
		ok   bool
	}{{"artifacts/app.tar.gz", true}, {"artifacts/%61", false}, {"artifacts/%2fsecret", false}, {"artifacts/../secret", false}, {"artifacts/", false}} {
		if _, ok := v2contract.CanonicalRawPath(tc.path); ok != tc.ok {
			t.Errorf("raw %q ok=%v", tc.path, ok)
		}
	}
	base := "conans/pkg/1.0/u/c/"
	for _, suffix := range []string{"revisions", "revisions/r/files", "revisions/r/files/conanfile.py", "revisions/r/packages/id/revisions", "revisions/r/packages/id/revisions/p/files", "revisions/r/packages/id/revisions/p/files/pkg"} {
		if !v2contract.ConanReadEndpoint("GET", base+suffix) {
			t.Errorf("endpoint rejected: %s", suffix)
		}
	}
	for _, path := range []string{base + "revisions/r/files/x/y", base + "revisions/%23/files", base + "upload"} {
		if v2contract.ConanReadEndpoint("GET", path) || v2contract.ConanReadEndpoint("POST", path) {
			t.Errorf("invalid endpoint accepted: %s", path)
		}
	}
}

func TestV2ContractHasRequiredDecisions(t *testing.T) {
	document, err := os.ReadFile(filepath.Join("..", "docs", "v2-contract.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, clause := range []string{"CONTRACT: anonymous-default-deny", "CONTRACT: audit-fields", "CONTRACT: raw-path-normalization", "CONTRACT: raw-checksum", "CONTRACT: raw-proxy-allowlist", "CONTRACT: raw-cache", "CONTRACT: conan2-only", "CONTRACT: conan-coordinate", "CONTRACT: conan2-read-endpoints", "CONTRACT: conan-cache", "CONTRACT: conan-proxy-allowlist", "CONTRACT: fixtures-and-upgrade"} {
		if !strings.Contains(string(document), clause) {
			t.Errorf("missing %q", clause)
		}
	}
}

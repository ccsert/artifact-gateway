package contracts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/v2contract"
)

type fakeSource map[string]int

func (f fakeSource) Fetch(member v2contract.Member, _ string) int { return f[member.Name] }

type fakeCache map[string]int

func (f fakeCache) key(member v2contract.Member, path string) string {
	return member.Name + ":" + member.Endpoint + ":" + path
}
func (f fakeCache) Load(member v2contract.Member, path string) (int, bool) {
	v, ok := f[f.key(member, path)]
	return v, ok
}
func (f fakeCache) Store(member v2contract.Member, path string, status int) {
	f[f.key(member, path)] = status
}

type fakeAudit []v2contract.Audit

func (f *fakeAudit) Record(a v2contract.Audit) { *f = append(*f, a) }

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
		{"default deny", false, true, []v2contract.Member{{Name: "hosted", Type: v2contract.Hosted, Anonymous: true}}, fakeSource{"hosted": 200}, fakeCache{}, 401, "", "access_denied"},
		{"repository narrows group", true, false, []v2contract.Member{{Name: "hosted", Type: v2contract.Hosted}}, fakeSource{"hosted": 200}, fakeCache{}, 401, "", "access_denied"},
		{"hosted before proxy and proxy negative", true, false, []v2contract.Member{{Name: "proxy", Type: v2contract.Proxy, Anonymous: true, ProxyAllowed: true, Endpoint: "p"}, {Name: "hosted", Type: v2contract.Hosted, Anonymous: true, Endpoint: "h"}}, fakeSource{"hosted": 200}, fakeCache{"proxy:p:pkg": 404}, 200, "hosted", "resolved"},
		{"hosted negative then proxy", true, false, []v2contract.Member{{Name: "hosted", Type: v2contract.Hosted, Anonymous: true}, {Name: "proxy", Type: v2contract.Proxy, Anonymous: true, ProxyAllowed: true}}, fakeSource{"hosted": 404, "proxy": 200}, fakeCache{}, 200, "hosted,proxy", "resolved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			audit := fakeAudit{}
			fetched := []string{}
			source := tc.source
			r := v2contract.Resolver{GroupAnonymous: tc.group, Members: tc.members, Cache: tc.cached, Auditor: &audit, Source: fakeSourceFunc(func(m v2contract.Member, p string) int { fetched = append(fetched, m.Name); return source.Fetch(m, p) })}
			status, _ := r.Resolve(false, "pkg")
			if status != tc.status || strings.Join(fetched, ",") != tc.fetch || audit[len(audit)-1].Outcome != tc.audit {
				t.Fatalf("status=%d fetch=%q audit=%#v", status, fetched, audit)
			}
		})
	}
}

type fakeSourceFunc func(v2contract.Member, string) int

func (f fakeSourceFunc) Fetch(m v2contract.Member, p string) int { return f(m, p) }

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

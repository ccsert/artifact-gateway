package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

const groupMavenPath = "org/example/widget/1.0/widget-1.0.jar"

type groupMavenUpstream struct {
	status atomic.Int32
	calls  atomic.Int32
}

type groupMavenFixture struct {
	t         *testing.T
	store     *repository.MemoryStore
	cache     *MavenCache
	repos     []repository.HostedRepository
	upstreams []*groupMavenUpstream
	group     repository.HostedGroup
	gateway   *httptest.Server
}

func newGroupMavenFixture(t *testing.T, objects OCIObjectStore, statuses ...int) *groupMavenFixture {
	t.Helper()
	if objects == nil {
		objects = NewMemoryOCIObjectStore()
	}
	f := &groupMavenFixture{t: t, store: repository.NewMemoryStore()}
	var hosts []string
	var members []repository.GroupMember
	for i, status := range statuses {
		upstream := &groupMavenUpstream{}
		upstream.status.Store(int32(status))
		name := "proxy-" + uuid.NewString()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upstream.calls.Add(1)
			w.Header().Set("ETag", `"`+name+`"`)
			w.Header().Set("Content-Type", "application/java-archive")
			w.WriteHeader(int(upstream.status.Load()))
			if r.Method != http.MethodHead {
				_, _ = w.Write([]byte(name))
			}
		}))
		t.Cleanup(server.Close)
		host := strings.TrimPrefix(server.URL, "http://")
		hosts = append(hosts, host)
		repo, err := f.store.CreateHostedRepository(context.Background(), repository.HostedRepository{
			ID: uuid.NewString(), Name: name, Format: repository.FormatMaven, Type: repository.RepositoryTypeProxy,
			Endpoint: server.URL, AllowedHosts: []string{host},
		})
		if err != nil {
			t.Fatal(err)
		}
		f.repos = append(f.repos, repo)
		f.upstreams = append(f.upstreams, upstream)
		members = append(members, repository.GroupMember{RepositoryID: repo.ID, Position: i})
	}
	name := "maven-chain-" + uuid.NewString()
	createV2Group(t, f.store, name, repository.FormatMaven, members...)
	var err error
	f.group, err = f.store.GetHostedGroupByName(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	f.cache = NewMavenCache(objects, time.Hour, time.Hour, time.Minute, time.Minute, hosts)
	f.gateway = httptest.NewServer(NewGatewayHandlerWithCaches(Dependencies{NativeMavenObjectStore: NewMemoryOCIObjectStore()}, f.store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), f.cache, UpstreamClient{}))
	t.Cleanup(f.gateway.Close)
	return f
}

func (f *groupMavenFixture) request(method, actor string, headers http.Header) (int, string) {
	f.t.Helper()
	r, err := http.NewRequest(method, f.gateway.URL+"/maven/"+f.group.Name+"/"+groupMavenPath, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	r.Header = headers.Clone()
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	if actor != "" {
		r.SetBasicAuth(actor, "resolver-secret")
	}
	response, err := f.gateway.Client().Do(r)
	if err != nil {
		f.t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		f.t.Fatal(err)
	}
	return response.StatusCode, string(body)
}

func (f *groupMavenFixture) replaceMembers(indices ...int) {
	f.t.Helper()
	var members []repository.GroupMember
	for position, index := range indices {
		members = append(members, repository.GroupMember{RepositoryID: f.repos[index].ID, Position: position})
	}
	updated, err := f.store.ReplaceHostedGroupMembers(context.Background(), f.group.ID, members, f.group.Version)
	if err != nil {
		f.t.Fatal(err)
	}
	f.group = updated
}

func (f *groupMavenFixture) expectGet(status int, body string) {
	f.t.Helper()
	got, content := f.request(http.MethodGet, "maven", nil)
	if got != status || (body != "" && content != body) {
		f.t.Fatalf("status=%d body=%q want=%d %q", got, content, status, body)
	}
}

func TestV2GroupMavenMultiProxyHTTPFallback(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			f := newGroupMavenFixture(t, nil, 404, 404, 200)
			for range 2 {
				status, body := f.request(method, "maven", nil)
				if status != 200 || (method == http.MethodGet && body != f.repos[2].Name) || (method == http.MethodHead && body != "") {
					t.Fatalf("status=%d body=%q", status, body)
				}
			}
			want := int32(1)
			if method == http.MethodHead {
				want = 2 // HEAD must not create a complete body cache entry.
			}
			for i, upstream := range f.upstreams {
				if upstream.calls.Load() != want {
					t.Fatalf("upstream %d calls=%d want=%d", i, upstream.calls.Load(), want)
				}
			}
			if method == http.MethodGet {
				if status, body := f.request(method, "maven", http.Header{"Range": {"bytes=0-4"}}); status != 206 || body != "proxy" {
					t.Fatalf("cached range=%d %q", status, body)
				}
				if status, body := f.request(method, "maven", http.Header{"If-None-Match": {`"` + f.repos[2].Name + `"`}}); status != 304 || body != "" {
					t.Fatalf("cached conditional=%d %q", status, body)
				}
			}
		})
	}
}

func TestV2GroupMavenRejectsUnscopedMultiProxyCache(t *testing.T) {
	for _, negative := range []bool{true, false} {
		t.Run(map[bool]string{true: "negative", false: "positive"}[negative], func(t *testing.T) {
			f := newGroupMavenFixture(t, nil, 404, 200)
			key := f.cache.Key(f.group.Name, groupMavenPath)
			member := repository.Member{Name: f.repos[0].Name, Endpoint: f.repos[0].Endpoint}
			var err error
			if negative {
				err = f.cache.StoreNegativeForRepositoryPath(context.Background(), key, f.group.Name, groupMavenPath, member)
			} else {
				err = f.cache.Store(context.Background(), key, groupMavenPath, CachedMavenContent{Body: []byte("stale-first"), Member: member.Name, Endpoint: member.Endpoint, Repository: f.group.Name})
			}
			if err != nil {
				t.Fatal(err)
			}
			f.expectGet(200, f.repos[1].Name)
		})
	}
}

func TestV2GroupMavenCacheRevalidatesMemberChanges(t *testing.T) {
	t.Run("negative then add member", func(t *testing.T) {
		f := newGroupMavenFixture(t, nil, 404, 200)
		f.replaceMembers(0)
		f.expectGet(404, "")
		f.expectGet(404, "")
		if f.upstreams[0].calls.Load() != 1 {
			t.Fatal("negative cache was not reused")
		}
		f.replaceMembers(0, 1)
		f.expectGet(200, f.repos[1].Name)
	})
	t.Run("positive then reorder", func(t *testing.T) {
		f := newGroupMavenFixture(t, nil, 200, 200)
		f.expectGet(200, f.repos[0].Name)
		f.replaceMembers(1, 0)
		f.expectGet(200, f.repos[1].Name)
	})
	for _, status := range []int{200, 404} {
		t.Run(http.StatusText(status)+" then change earlier upstream", func(t *testing.T) {
			f := newGroupMavenFixture(t, nil, 404, status, 200)
			f.replaceMembers(0, 1)
			f.expectGet(status, "")
			repo := f.repos[0]
			repo.Endpoint, repo.AllowedHosts = f.repos[2].Endpoint, f.repos[2].AllowedHosts
			if _, err := f.store.UpdateHostedRepository(context.Background(), repo, repo.Version); err != nil {
				t.Fatal(err)
			}
			f.expectGet(200, f.repos[2].Name)
		})
	}
}

func TestV2GroupMavenCacheUsesAuthorizedCandidates(t *testing.T) {
	f := newGroupMavenFixture(t, nil, 200, 200)
	for i, actor := range []string{"alice", "bob"} {
		if _, err := f.store.ReplaceRepositoryGrants(context.Background(), f.repos[i].ID, []repository.RepositoryGrant{{Principal: actor, Scopes: []string{"repositories:read"}}}, "1"); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		for i, actor := range []string{"alice", "bob"} {
			if status, body := f.request(http.MethodGet, actor, nil); status != 200 || body != f.repos[i].Name {
				t.Fatalf("%s status=%d body=%q", actor, status, body)
			}
		}
	}
	if _, err := f.store.ReplaceRepositoryGrants(context.Background(), f.repos[0].ID, []repository.RepositoryGrant{{Principal: "bob", Scopes: []string{"repositories:read"}}}, "2"); err != nil {
		t.Fatal(err)
	}
	if status, body := f.request(http.MethodGet, "bob", nil); status != 200 || body != f.repos[0].Name {
		t.Fatalf("expanded grants status=%d body=%q", status, body)
	}
}

func TestV2GroupMavenCacheTracksAllowlistAndEgress(t *testing.T) {
	f := newGroupMavenFixture(t, nil, 200, 200)
	f.cache.SetAllowedProxyHosts([]string{strings.TrimPrefix(f.repos[1].Endpoint, "http://")})
	f.expectGet(200, f.repos[1].Name)
	f.cache.SetAllowedProxyHosts([]string{strings.TrimPrefix(f.repos[0].Endpoint, "http://"), strings.TrimPrefix(f.repos[1].Endpoint, "http://")})
	f.expectGet(200, f.repos[0].Name)
	repo := f.repos[0]
	repo.EgressProxy = &repository.EgressProxy{Mode: repository.EgressProxyModeEnvironment}
	if _, err := f.store.UpdateHostedRepository(context.Background(), repo, repo.Version); err != nil {
		t.Fatal(err)
	}
	f.expectGet(200, f.repos[0].Name)
	if f.upstreams[0].calls.Load() != 2 {
		t.Fatalf("egress change did not revalidate cache: calls=%d", f.upstreams[0].calls.Load())
	}
}

func TestV2GroupMavenCacheSeparatesAnonymousCandidates(t *testing.T) {
	f := newGroupMavenFixture(t, nil, 200, 200)
	enableAnonymousAccess(t, f.store)
	repo := f.repos[1]
	repo.AnonymousRead = true
	if _, err := f.store.UpdateHostedRepository(context.Background(), repo, repo.Version); err != nil {
		t.Fatal(err)
	}
	group := f.group
	group.AnonymousRead = true
	if _, err := f.store.ReplaceHostedGroup(context.Background(), group, group.Version); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []string{"", "maven", ""} {
		want := f.repos[1].Name
		if actor != "" {
			want = f.repos[0].Name
		}
		if status, body := f.request(http.MethodGet, actor, nil); status != 200 || body != want {
			t.Fatalf("actor=%q status=%d body=%q want=%q", actor, status, body, want)
		}
	}
}

func TestMavenSingleProxyReadsLegacyUnscopedCache(t *testing.T) {
	for _, negative := range []bool{false, true} {
		f := newGroupMavenFixture(t, nil, 500)
		// Use the direct Repository route and its existing namespace.
		f.group.Name = f.repos[0].Name
		key := f.cache.Key(f.group.Name, groupMavenPath)
		member := repository.Member{Name: f.repos[0].Name, Endpoint: f.repos[0].Endpoint}
		var err error
		if negative {
			err = f.cache.StoreNegativeForRepositoryPath(context.Background(), key, f.group.Name, groupMavenPath, member)
		} else {
			err = f.cache.Store(context.Background(), key, groupMavenPath, CachedMavenContent{Body: []byte("legacy"), Member: member.Name, Endpoint: member.Endpoint, Repository: f.group.Name})
		}
		if err != nil {
			t.Fatal(err)
		}
		if negative {
			f.expectGet(404, "")
		} else {
			f.expectGet(200, "legacy")
		}
		if f.upstreams[0].calls.Load() != 0 {
			t.Fatal("compatible single-Proxy cache was discarded")
		}
	}
}

func TestV2GroupMavenFailureDoesNotPoisonResolutionCache(t *testing.T) {
	for _, secondStatus := range []int{200, 404} {
		t.Run(http.StatusText(secondStatus), func(t *testing.T) {
			f := newGroupMavenFixture(t, nil, 500, secondStatus)
			want := 502
			if secondStatus == 200 {
				want = 200
			}
			f.expectGet(want, "")
			if f.upstreams[0].calls.Load() != 2 || f.upstreams[1].calls.Load() != 1 {
				t.Fatalf("retry/fallback calls=%d,%d", f.upstreams[0].calls.Load(), f.upstreams[1].calls.Load())
			}
			if _, err := f.cache.Load(context.Background(), f.cache.Key(f.group.Name, groupMavenPath)); err == nil || errors.Is(err, errMavenCacheNegative) {
				t.Fatal("failed prefix was cached as a complete resolution")
			}
			f.upstreams[0].status.Store(200)
			f.cache.RecordUpstreamSuccess(context.Background(), f.repos[0].Endpoint)
			f.expectGet(200, f.repos[0].Name)
		})
	}
}

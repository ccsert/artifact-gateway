package nuget

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	nugetprotocol "github.com/ccsert/artifact-gateway/internal/protocol/nuget"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/secrets"
	"github.com/ccsert/artifact-gateway/internal/upstreamauth"
	"github.com/google/uuid"
)

type testSourceClient struct{ client *http.Client }

func (c testSourceClient) FetchNuGet(ctx context.Context, repo repository.HostedRepository, target string, headers http.Header) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, "GET", target, nil)
	if err != nil {
		return nil, err
	}
	request.Header = headers.Clone()
	if err = upstreamauth.ApplyOrigin(request, repo.Endpoint, repo.UpstreamAuth); err != nil {
		return nil, err
	}
	client := *c.client
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client.Do(request)
}

type testFeed struct {
	server        *httptest.Server
	mu            sync.Mutex
	packages      map[string][]byte
	wirePackages  map[string][]byte
	documents     map[string]map[string]any
	requests      map[string]int
	conditionals  int
	mutate        func(string, map[string]any)
	status        int
	authorization string
}

func newTestFeed(t *testing.T, packages ...[]byte) *testFeed {
	t.Helper()
	f := &testFeed{packages: map[string][]byte{}, requests: map[string]int{}}
	for _, body := range packages {
		inspection, err := nugetprotocol.InspectPackage(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			t.Fatal(err)
		}
		f.packages[strings.ToLower(inspection.Identity.ID)+"@"+strings.ToLower(inspection.Identity.NormalizedVersion)] = body
	}
	f.server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}
func (f *testFeed) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests[r.URL.Path]++
	if f.authorization != "" && r.Header.Get("Authorization") != f.authorization {
		w.WriteHeader(401)
		return
	}
	if f.status != 0 {
		w.WriteHeader(f.status)
		return
	}
	base := f.server.URL
	path := strings.Trim(r.URL.Path, "/")
	etag := `"fixture-v3"`
	if r.Header.Get("If-None-Match") == etag {
		f.conditionals++
		w.WriteHeader(304)
		return
	}
	w.Header().Set("ETag", etag)
	var value map[string]any
	switch {
	case f.documents[path] != nil:
		value = f.documents[path]
	case path == "not-nuget-org/discover.json":
		value = map[string]any{"version": "3.0.0", "resources": []map[string]string{{"@id": base + "/immutable-files/", "@type": "PackageBaseAddress/3.0.0"}, {"@id": base + "/page-hive/", "@type": "RegistrationsBaseUrl/3.6.0"}, {"@id": base + "/lookup-packages", "@type": "SearchQueryService/3.5.0"}, {"@id": base + "/complete-packages", "@type": "SearchAutocompleteService/3.5.0"}}}
	case strings.HasPrefix(path, "immutable-files/"):
		parts := strings.Split(strings.TrimPrefix(path, "immutable-files/"), "/")
		if len(parts) == 2 && parts[1] == "index.json" {
			versions := f.versions(parts[0])
			if len(versions) == 0 {
				w.WriteHeader(404)
				return
			}
			value = map[string]any{"versions": versions}
			break
		}
		if len(parts) != 3 {
			w.WriteHeader(404)
			return
		}
		body := f.packages[parts[0]+"@"+parts[1]]
		if body == nil {
			w.WriteHeader(404)
			return
		}
		if parts[2] != parts[0]+"."+parts[1]+".nupkg" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		if wire, ok := f.wirePackages[parts[0]+"@"+parts[1]]; ok {
			body = wire
		}
		_, _ = w.Write(body)
		return
	case strings.HasPrefix(path, "page-hive/"):
		parts := strings.Split(strings.TrimPrefix(path, "page-hive/"), "/")
		if len(parts) != 2 || parts[1] != "index.json" {
			w.WriteHeader(404)
			return
		}
		id := parts[0]
		versions := f.versions(id)
		if len(versions) == 0 {
			w.WriteHeader(404)
			return
		}
		leaves := []map[string]any{}
		for _, version := range versions {
			body := f.packages[id+"@"+version]
			inspection, _ := nugetprotocol.InspectPackage(bytes.NewReader(body), int64(len(body)))
			hash := sha512.Sum512(body)
			content := base + "/immutable-files/" + id + "/" + version + "/" + id + "." + version + ".nupkg"
			leaf := base + "/page-hive/" + id + "/" + version + ".json"
			leaves = append(leaves, map[string]any{"@id": leaf, "packageContent": content, "catalogEntry": map[string]any{"id": inspection.Identity.ID, "version": version, "description": inspection.Description, "authors": inspection.Authors, "dependencyGroups": inspection.DependencyGroups, "packageContent": content, "packageHash": base64.StdEncoding.EncodeToString(hash[:]), "packageHashAlgorithm": "SHA512"}})
		}
		value = map[string]any{"count": 1, "items": []map[string]any{{"@id": base + "/page-hive/" + id + "/page.json", "lower": versions[0], "upper": versions[len(versions)-1], "count": len(leaves), "items": leaves}}}
	case path == "lookup-packages" || path == "complete-packages":
		ids := []string{}
		seen := map[string]bool{}
		for key := range f.packages {
			id, _, _ := strings.Cut(key, "@")
			if !seen[id] && strings.Contains(id, strings.ToLower(r.URL.Query().Get("q"))) {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		skip, take := 0, 100
		_, _ = fmt.Sscan(r.URL.Query().Get("skip"), &skip)
		_, _ = fmt.Sscan(r.URL.Query().Get("take"), &take)
		end := min(skip+take, len(ids))
		skip = min(skip, len(ids))
		if path == "complete-packages" {
			value = map[string]any{"totalHits": len(ids), "data": ids[skip:end]}
		} else {
			data := []map[string]string{}
			for _, id := range ids[skip:end] {
				data = append(data, map[string]string{"id": id})
			}
			value = map[string]any{"totalHits": len(ids), "data": data}
		}
	default:
		w.WriteHeader(404)
		return
	}
	if f.mutate != nil {
		f.mutate(path, value)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
func (f *testFeed) versions(id string) []string {
	out := []string{}
	for key := range f.packages {
		if strings.HasPrefix(key, id+"@") {
			out = append(out, strings.TrimPrefix(key, id+"@"))
		}
	}
	sort.Slice(out, func(i, j int) bool { return nugetprotocol.CompareVersions(out[i], out[j]) < 0 })
	return out
}
func proxyFixture(t *testing.T, feed *testFeed) (fixture, repository.HostedRepository) {
	t.Helper()
	f := newFixture(t)
	repo, err := f.store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "nuget-source", Format: repository.FormatNuGet, Type: repository.RepositoryTypeProxy, Endpoint: feed.server.URL + "/not-nuget-org/discover.json"})
	if err != nil {
		t.Fatal(err)
	}
	module, err := New(Options{Store: f.store, Repositories: f.store, Objects: f.objects, Authenticator: f.module.auth, Authorizer: f.module.access, Audit: f.store, Anonymous: f.store, Capacities: f.store, Sources: f.store, Configuration: f.store, Groups: f.store, Upstream: testSourceClient{feed.server.Client()}})
	if err != nil {
		t.Fatal(err)
	}
	f.module = module
	return f, repo
}
func nativeGet(t *testing.T, m *Module, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", "https://gateway.test/nuget/"+path, nil)
	r.Header.Set("Authorization", "Bearer nuget-test-admin")
	w := httptest.NewRecorder()
	m.ServeHTTP(w, r)
	return w
}
func expireDocument(t *testing.T, f fixture, repo repository.HostedRepository, target string) {
	t.Helper()
	doc, err := f.store.GetNuGetSourceDocument(t.Context(), repo.ID, repo.Version, target)
	if err != nil {
		t.Fatal(err)
	}
	doc.FetchedAt = time.Now().UTC().Add(-time.Minute)
	doc.ExpiresAt = doc.FetchedAt.Add(time.Second)
	if err = f.store.PutNuGetSourceDocument(t.Context(), doc); err != nil {
		t.Fatal(err)
	}
}

func TestNuGetProxyDynamicDiscoveryVerifiedCacheAndOffline(t *testing.T) {
	body := packageFixture(t, "Acme.Leaf", "1.0.0", "")
	feed := newTestFeed(t, body)
	f, repo := proxyFixture(t, feed)
	for _, path := range []string{"v3/index.json", "v3/flat/acme.leaf/index.json", "v3/registration/acme.leaf/index.json", "v3/query?q=acme", "v3/autocomplete?q=acme"} {
		w := nativeGet(t, f.module, repo.Name+"/"+path)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if capacity, err := f.store.GetRepositoryCapacity(t.Context(), repo.ID); err != nil || capacity.UsedBytes != 0 {
		t.Fatalf("unverified metadata became artifact: %+v %v", capacity, err)
	}
	path := repo.Name + "/v3/flat/acme.leaf/1.0.0/acme.leaf.1.0.0.nupkg"
	w := nativeGet(t, f.module, path)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) {
		t.Fatalf("cold package: %d %s", w.Code, w.Body.String())
	}
	p, err := f.store.GetNuGetPublication(t.Context(), repo.ID, "acme.leaf", "1.0.0")
	sum := sha256.Sum256(body)
	if err != nil || p.Digest != "sha256:"+hex.EncodeToString(sum[:]) || p.Size != int64(len(body)) || p.SourceVersion != repo.Version || p.SourceURL != feed.server.URL+"/immutable-files/acme.leaf/1.0.0/acme.leaf.1.0.0.nupkg" {
		t.Fatalf("verified identity: %+v %v", p, err)
	}
	expireDocument(t, f, repo, repo.Endpoint)
	w = nativeGet(t, f.module, path)
	if w.Code != 200 {
		t.Fatalf("conditional: %d %s", w.Code, w.Body.String())
	}
	feed.mu.Lock()
	conditionals := feed.conditionals
	downloads := feed.requests["/immutable-files/acme.leaf/1.0.0/acme.leaf.1.0.0.nupkg"]
	feed.mu.Unlock()
	if conditionals != 1 || downloads != 1 {
		t.Fatalf("conditional=%d package fetches=%d", conditionals, downloads)
	}
	expireDocument(t, f, repo, repo.Endpoint)
	feed.server.Close()
	w = nativeGet(t, f.module, path)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) {
		t.Fatalf("offline verified bytes: %d %s", w.Code, w.Body.String())
	}
	if err = f.objects.Delete(t.Context(), p.ObjectKey); err != nil {
		t.Fatal(err)
	}
	w = nativeGet(t, f.module, path)
	if w.Code == 200 {
		t.Fatal("incomplete cache reported success")
	}
}
func TestNuGetProxyRejectsAmbiguousIdentityAndCrossSourceContent(t *testing.T) {
	for _, kind := range []string{"case-version-alias", "wrong-registration-id", "other-package-origin", "different-dependencies", "bad-bytes", "bad-sha512", "unknown-resource", "over-page-count", "unknown-auth"} {
		t.Run(kind, func(t *testing.T) {
			body := packageFixture(t, "Acme.Leaf", "1.0.0", "")
			feed := newTestFeed(t, body)
			f, repo := proxyFixture(t, feed)
			feed.mutate = func(path string, value map[string]any) {
				if kind == "case-version-alias" && strings.HasSuffix(path, "acme.leaf/index.json") && strings.HasPrefix(path, "immutable-files/") {
					value["versions"] = []string{"1.0.0", "1.0"}
				}
				if strings.HasPrefix(path, "page-hive/") {
					page := value["items"].([]map[string]any)[0]
					leaf := page["items"].([]map[string]any)[0]
					catalog := leaf["catalogEntry"].(map[string]any)
					switch kind {
					case "wrong-registration-id":
						catalog["id"] = "Other"
					case "other-package-origin":
						leaf["packageContent"] = "https://public.example.test/acme.leaf.1.0.0.nupkg"
					case "different-dependencies":
						catalog["dependencyGroups"] = []map[string]any{{"dependencies": []map[string]string{{"id": "Injected", "range": "[1.0.0]"}}}}
					case "bad-sha512":
						catalog["packageHash"] = base64.StdEncoding.EncodeToString(make([]byte, 64))
					case "over-page-count":
						page["count"] = 5000
					}
				}
				if kind == "unknown-resource" && path == "not-nuget-org/discover.json" {
					value["resources"] = []map[string]string{{"@id": feed.server.URL + "/ignored/", "@type": "PackageBaseAddress/9.0.0"}}
				}
			}
			if kind == "bad-bytes" {
				feed.wirePackages = map[string][]byte{"acme.leaf@1.0.0": []byte("not an archive")}
			}
			if kind == "unknown-auth" {
				repo.UpstreamAuth = &repository.UpstreamAuth{Scheme: "opaque", Secret: "do-not-log"}
				var err error
				repo, err = f.store.UpdateHostedRepository(t.Context(), repo, repo.Version)
				if err != nil {
					t.Fatal(err)
				}
			}
			w := nativeGet(t, f.module, repo.Name+"/v3/flat/acme.leaf/1.0.0/acme.leaf.1.0.0.nupkg")
			if w.Code == 200 {
				t.Fatalf("%s was accepted", kind)
			}
			if kind == "bad-bytes" && feed.requests["/immutable-files/acme.leaf/1.0.0/acme.leaf.1.0.0.nupkg"] != 1 {
				t.Fatal("bad archive rejection did not reach the actual package bytes")
			}
			items, err := f.store.ListNuGetPublications(t.Context(), repo.ID, "", 10, "")
			if err != nil || len(items) != 0 {
				t.Fatalf("bad input visible: %+v %v", items, err)
			}
		})
	}
}
func TestNuGetProxyNegativeCacheAndSourceDenialDoNotFallBack(t *testing.T) {
	feed := newTestFeed(t, packageFixture(t, "Acme.Leaf", "1.0.0", ""))
	f, repo := proxyFixture(t, feed)
	for range 2 {
		w := nativeGet(t, f.module, repo.Name+"/v3/flat/absent/index.json")
		if w.Code != 404 {
			t.Fatalf("negative: %d", w.Code)
		}
	}
	feed.mu.Lock()
	negative := feed.requests["/immutable-files/absent/index.json"]
	feed.mu.Unlock()
	if negative != 1 {
		t.Fatalf("negative fetched %d times", negative)
	}
	w := nativeGet(t, f.module, repo.Name+"/v3/flat/acme.leaf/1.0.0/acme.leaf.1.0.0.nupkg")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	expireDocument(t, f, repo, repo.Endpoint)
	feed.mu.Lock()
	feed.status = 401
	feed.mu.Unlock()
	for range 2 {
		w = nativeGet(t, f.module, repo.Name+"/v3/flat/acme.leaf/1.0.0/acme.leaf.1.0.0.nupkg")
		if w.Code != 502 {
			t.Fatalf("denied source fell back to warm bytes: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestNuGetProxyRegistrationPageBoundaries(t *testing.T) {
	for _, kind := range []string{"complete", "broad-lower", "broad-upper", "fetched-boundary", "overlap", "count", "foreign-page"} {
		t.Run(kind, func(t *testing.T) {
			first := packageFixture(t, "Acme.Paged", "1.0.0", "")
			second := packageFixture(t, "Acme.Paged", "2.0.0", "")
			feed := newTestFeed(t, first, second)
			base := feed.server.URL
			leaf := func(version string, body []byte) map[string]any {
				sum := sha512.Sum512(body)
				content := base + "/immutable-files/acme.paged/" + version + "/acme.paged." + version + ".nupkg"
				return map[string]any{"@id": base + "/page-hive/acme.paged/" + version + ".json", "packageContent": content, "catalogEntry": map[string]any{"id": "Acme.Paged", "version": version, "packageContent": content, "packageHash": base64.StdEncoding.EncodeToString(sum[:]), "packageHashAlgorithm": "SHA512"}}
			}
			pageURL := base + "/page-hive/acme.paged/page-1.json"
			page := map[string]any{"@id": pageURL, "lower": "1.0.0", "upper": "1.0.0", "count": 1, "items": []map[string]any{leaf("1.0.0", first)}}
			firstPage := map[string]any{"@id": pageURL, "lower": "1.0.0", "upper": "1.0.0", "count": 1}
			lastPage := map[string]any{"@id": base + "/page-hive/acme.paged/page-2.json", "lower": "2.0.0", "upper": "2.0.0", "count": 1, "items": []map[string]any{leaf("2.0.0", second)}}
			switch kind {
			case "broad-lower":
				firstPage["lower"], page["lower"] = "0.9.0", "0.9.0"
			case "broad-upper":
				firstPage["upper"], page["upper"] = "1.5.0", "1.5.0"
			case "fetched-boundary":
				page["lower"] = "0.9.0"
			case "overlap":
				lastPage["lower"] = "1.0.0"
			case "count":
				page["count"] = 2
			case "foreign-page":
				firstPage["@id"] = "https://unapproved-source.example.test/page.json"
			}
			feed.documents = map[string]map[string]any{"page-hive/acme.paged/index.json": {"count": 2, "items": []map[string]any{firstPage, lastPage}}, "page-hive/acme.paged/page-1.json": page}
			f, repo := proxyFixture(t, feed)
			w := nativeGet(t, f.module, repo.Name+"/v3/flat/acme.paged/2.0.0/acme.paged.2.0.0.nupkg")
			if kind == "complete" {
				if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), second) || feed.requests["/page-hive/acme.paged/page-1.json"] != 1 {
					t.Fatalf("complete external and inline pages: %d %s", w.Code, w.Body.String())
				}
			} else if w.Code == 200 {
				t.Fatalf("ambiguous page accepted: %s", kind)
			}
		})
	}
}

func TestNuGetProxyListedAndUnlistedVersionsKeepIndependentIdentity(t *testing.T) {
	first := packageFixture(t, "Acme.Listed", "1.0.0", "")
	second := packageFixture(t, "Acme.Listed", "2.0.0", "")
	feed := newTestFeed(t, first, second)
	feed.mutate = func(path string, value map[string]any) {
		if strings.HasPrefix(path, "page-hive/") {
			for _, page := range value["items"].([]map[string]any) {
				for _, leaf := range page["items"].([]map[string]any) {
					catalog := leaf["catalogEntry"].(map[string]any)
					catalog["listed"] = catalog["version"] == "1.0.0"
				}
			}
		}
	}
	f, proxy := proxyFixture(t, feed)
	hostedPackage(t, f, f.repo, packageFixture(t, "Acme.Listed", "2.0.0", `<dependencies><dependency id="Wrong.Source" version="1.0.0"/></dependencies>`))
	group := testNuGetGroup(t, f, proxy, f.repo)
	check := func() {
		t.Helper()
		for _, item := range []struct {
			version string
			body    []byte
		}{{"1.0.0", first}, {"2.0.0", second}} {
			w := nativeGet(t, f.module, proxy.Name+"/v3/flat/acme.listed/"+item.version+"/acme.listed."+item.version+".nupkg")
			if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), item.body) {
				t.Fatalf("independent exact package %s: %d %s", item.version, w.Code, w.Body.String())
			}
		}
		for _, name := range []string{proxy.Name, group.Name} {
			w := nativeGet(t, f.module, name+"/v3/flat/acme.listed/index.json")
			if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"versions":["1.0.0","2.0.0"]`)) {
				t.Fatalf("unlisted version disappeared from the exact-version inventory: %d %s", w.Code, w.Body.String())
			}
			w = nativeGet(t, f.module, name+"/v3/registration/acme.listed/2.0.0.json")
			if w.Code != 200 {
				t.Fatalf("unlisted registration: %d %s", w.Code, w.Body.String())
			}
			reader, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(reader)
			_ = reader.Close()
			if err != nil {
				t.Fatal(err)
			}
			var leaf struct {
				Listed  *bool `json:"listed"`
				Catalog struct {
					Listed *bool `json:"listed"`
				} `json:"catalogEntry"`
			}
			if json.Unmarshal(body, &leaf) != nil || leaf.Listed == nil || *leaf.Listed || leaf.Catalog.Listed == nil || *leaf.Catalog.Listed || !bytes.Contains(body, []byte(`"version":"2.0.0"`)) {
				t.Fatalf("unlisted state was forged: %s", body)
			}
			w = nativeGet(t, f.module, name+"/v3/query?q=acme.listed")
			var query struct {
				TotalHits int
				Data      []struct {
					Version  string
					Versions []struct{ Version string }
				}
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &query) != nil || query.TotalHits != 1 || len(query.Data) != 1 || query.Data[0].Version != "1.0.0" || len(query.Data[0].Versions) != 1 {
				t.Fatalf("unlisted version polluted listed search: %d %s", w.Code, w.Body.String())
			}
			w = nativeGet(t, f.module, name+"/v3/autocomplete?id=acme.listed")
			if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"data":["1.0.0"]`)) {
				t.Fatalf("unlisted autocomplete: %d %s", w.Code, w.Body.String())
			}
		}
		w := nativeGet(t, f.module, group.Name+"/v3/flat/acme.listed/2.0.0/acme.listed.2.0.0.nupkg")
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), second) || w.Header().Get("X-NuGet-Source-Repository") != proxy.ID {
			t.Fatalf("unlisted owner changed: %d %s", w.Code, w.Body.String())
		}
		owner, err := f.store.GetNuGetGroupAuthority(t.Context(), group.ID, "acme.listed", "2.0.0")
		if err != nil || owner.RepositoryID != proxy.ID || owner.RepositoryVersion != proxy.Version || owner.Digest == "" {
			t.Fatalf("unlisted durable identity: %+v %v", owner, err)
		}
	}
	check()
	var err error
	group, err = f.store.ReplaceHostedGroupMembers(t.Context(), group.ID, []repository.GroupMember{{RepositoryID: f.repo.ID, Position: 0}, {RepositoryID: proxy.ID, Position: 1}}, group.Version)
	if err != nil {
		t.Fatal(err)
	}
	expireDocument(t, f, proxy, proxy.Endpoint)
	feed.server.Close()
	check()
}

type failNuGetFirstStage struct {
	objectstore.Store
	once sync.Once
}

func (s *failNuGetFirstStage) PutVerifiedReader(ctx context.Context, key string, reader io.Reader, size int64, digest string) error {
	fail := false
	s.once.Do(func() { fail = true })
	if fail {
		return errors.New("owned partial cache fill")
	}
	return s.Store.PutVerifiedReader(ctx, key, reader, size, digest)
}

func TestNuGetProxyExpiredPartialFillCanStartNewReservation(t *testing.T) {
	body := packageFixture(t, "Acme.Retry", "1.0.0", "")
	feed := newTestFeed(t, body)
	f, proxy := proxyFixture(t, feed)
	f.module.objects = &failNuGetFirstStage{Store: f.objects}
	path := proxy.Name + "/v3/flat/acme.retry/1.0.0/acme.retry.1.0.0.nupkg"
	if w := nativeGet(t, f.module, path); w.Code == 200 {
		t.Fatal("partial cache fill was visible")
	}
	expired, err := f.store.ExpireNuGetSessions(t.Context(), time.Now().Add(2*time.Hour), 100)
	if err != nil || len(expired) != 1 || expired[0].State != "expired" {
		t.Fatalf("expire the actual incomplete reservation: %+v %v", expired, err)
	}
	if w := nativeGet(t, f.module, path); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) {
		t.Fatalf("expired fill blocked a fresh verified download: %d %s", w.Code, w.Body.String())
	}
	publication, err := f.store.GetNuGetPublication(t.Context(), proxy.ID, "acme.retry", "1.0.0")
	if err != nil || publication.SessionID == expired[0].ID || publication.ObjectKey == expired[0].ObjectKey || publication.SourceVersion != proxy.Version {
		t.Fatalf("expired and visible bytes share an unsafe identity: %+v %v", publication, err)
	}
}

func TestNuGetProxySearchPaginationDoesNotTruncateOrRepeat(t *testing.T) {
	packages := make([][]byte, 101)
	for i := range packages {
		packages[i] = packageFixture(t, fmt.Sprintf("Page.Pkg%03d", i), "1.0.0", "")
	}
	for _, kind := range []string{"query", "autocomplete", "duplicate-id"} {
		t.Run(kind, func(t *testing.T) {
			feed := newTestFeed(t, packages...)
			if kind == "duplicate-id" {
				feed.mutate = func(path string, value map[string]any) {
					if path == "lookup-packages" && feed.requests["/lookup-packages"] == 2 {
						value["data"] = []map[string]string{{"id": "PAGE.PKG000"}}
					}
				}
			}
			f, repo := proxyFixture(t, feed)
			endpoint := "query"
			if kind == "autocomplete" {
				endpoint = kind
			}
			w := nativeGet(t, f.module, repo.Name+"/v3/"+endpoint+"?q=page&skip=99&take=2")
			if kind == "duplicate-id" {
				if w.Code == 200 {
					t.Fatal("duplicate canonical ID across search pages was accepted")
				}
				return
			}
			var result struct {
				TotalHits int               `json:"totalHits"`
				Data      []json.RawMessage `json:"data"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.TotalHits != 101 || len(result.Data) != 2 {
				t.Fatalf("bounded search pagination: %d %s", w.Code, w.Body.String())
			}
			for i, raw := range result.Data {
				var id string
				if kind == "autocomplete" {
					_ = json.Unmarshal(raw, &id)
				} else {
					var entry struct{ ID string }
					_ = json.Unmarshal(raw, &entry)
					id = entry.ID
				}
				if !strings.EqualFold(id, fmt.Sprintf("page.pkg%03d", 99+i)) {
					t.Fatalf("wrong page identity: %s", id)
				}
			}
			path := "/lookup-packages"
			if kind == "autocomplete" {
				path = "/complete-packages"
			}
			if feed.requests[path] != 2 {
				t.Fatalf("source pages=%d", feed.requests[path])
			}
		})
	}
}

func TestNuGetProxyTLSFailureCannotUseStaleCache(t *testing.T) {
	body := packageFixture(t, "Acme.TLS", "1.0.0", "")
	feed := newTestFeed(t, body)
	f, repo := proxyFixture(t, feed)
	path := repo.Name + "/v3/flat/acme.tls/1.0.0/acme.tls.1.0.0.nupkg"
	if w := nativeGet(t, f.module, path); w.Code != 200 {
		t.Fatalf("warm cache: %d %s", w.Code, w.Body.String())
	}
	expireDocument(t, f, repo, repo.Endpoint)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	f.module.upstream = testSourceClient{&http.Client{Transport: transport, Timeout: time.Second}}
	defer transport.CloseIdleConnections()
	for range 2 {
		w := nativeGet(t, f.module, path)
		if w.Code == 200 {
			t.Fatal("untrusted TLS source fell back to stale authenticated cache")
		}
	}
}

func TestNuGetProxyUnavailableEgressUsesVerifiedCache(t *testing.T) {
	body := packageFixture(t, "Acme.Offline", "1.0.0", "")
	feed := newTestFeed(t, body)
	f, repo := proxyFixture(t, feed)
	path := repo.Name + "/v3/flat/acme.offline/1.0.0/acme.offline.1.0.0.nupkg"
	if w := nativeGet(t, f.module, path); w.Code != 200 {
		t.Fatalf("warm cache: %d %s", w.Code, w.Body.String())
	}
	expireDocument(t, f, repo, repo.Endpoint)
	proxy := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("closed proxy received a request")
	}))
	proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := feed.server.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	defer transport.CloseIdleConnections()
	f.module.upstream = testSourceClient{&http.Client{Transport: transport, Timeout: time.Second}}
	for range 2 {
		w := nativeGet(t, f.module, path)
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) {
			t.Fatalf("unavailable egress lost verified cache: %d %s", w.Code, w.Body.String())
		}
	}
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			t.Error("expected fixture CONNECT request")
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer denied.Close()
	deniedURL, err := url.Parse(denied.URL)
	if err != nil {
		t.Fatal(err)
	}
	deniedTransport := feed.server.Client().Transport.(*http.Transport).Clone()
	deniedTransport.Proxy = http.ProxyURL(deniedURL)
	defer deniedTransport.CloseIdleConnections()
	f.module.upstream = testSourceClient{&http.Client{Transport: deniedTransport, Timeout: time.Second}}
	for range 2 {
		if w := nativeGet(t, f.module, path); w.Code == 200 {
			t.Fatal("denied CONNECT fell back to cached authenticated bytes")
		}
	}
}
func TestNuGetPreviewSourceCASSecretSeparationAndReadOnlyProxy(t *testing.T) {
	t.Setenv(secrets.KeyEnv, strings.Repeat("31", 32))
	feed := newTestFeed(t, packageFixture(t, "Acme.Leaf", "1.0.0", ""))
	f, _ := proxyFixture(t, feed)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://gateway.test/api/v2/previews/nuget/"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer nuget-test-admin")
		r.Header.Set("Idempotency-Key", "source-request")
		w := httptest.NewRecorder()
		f.module.Preview().ServeHTTP(w, r)
		return w
	}
	input := fmt.Sprintf(`{"name":"configured","type":"proxy","endpoint":%q,"upstreamAuth":{"scheme":"bearer","secret":"source-secret-only"}}`, feed.server.URL+"/not-nuget-org/discover.json")
	for range 2 {
		w := request("POST", "repositories", input)
		if w.Code != 201 || strings.Contains(w.Body.String(), "source-secret-only") {
			t.Fatalf("create/replay: %d %s", w.Code, w.Body.String())
		}
	}
	repo, err := f.store.GetHostedRepositoryByName(t.Context(), "configured")
	if err != nil || repo.UpstreamAuth == nil || repo.UpstreamAuth.Secret == "source-secret-only" {
		t.Fatalf("not sealed: %+v %v", repo, err)
	}
	for _, body := range []string{fmt.Sprintf(`{"name":"configured","type":"proxy","version":"1","endpoint":%q}`, feed.server.URL+"/changed/index.json"), fmt.Sprintf(`{"name":"configured","type":"proxy","version":"0","endpoint":%q}`, repo.Endpoint)} {
		w := request("PUT", "repositories/"+repo.ID, body)
		if w.Code != 400 && w.Code != 409 {
			t.Fatalf("unsafe rebind/CAS: %d %s", w.Code, w.Body.String())
		}
	}
	w := request("PUT", "repositories/"+repo.ID, fmt.Sprintf(`{"name":"configured","type":"proxy","version":"1","endpoint":%q,"upstreamAuth":{"scheme":"none"}}`, feed.server.URL+"/changed/index.json"))
	if w.Code != 200 {
		t.Fatalf("explicit clear rebind: %d %s", w.Code, w.Body.String())
	}
	w = nativeGet(t, f.module, "configured/v3/index.json")
	if w.Code != 404 || strings.Contains(w.Body.String(), "PackagePublish") {
		t.Fatalf("Changed source must be discovered rather than advertised as usable: %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"https://gateway.test/nuget/configured/api/v2/package/", "https://gateway.test/api/v2/previews/nuget/repositories/" + repo.ID + "/publication-sessions"} {
		r := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer nuget-test-admin")
		w := httptest.NewRecorder()
		if strings.Contains(path, "/api/v2/previews/") {
			f.module.Preview().ServeHTTP(w, r)
		} else {
			f.module.ServeHTTP(w, r)
		}
		if w.Code != 405 {
			t.Fatalf("Proxy push boundary: %d %s", w.Code, w.Body.String())
		}
	}
}

var _ Upstream = testSourceClient{}

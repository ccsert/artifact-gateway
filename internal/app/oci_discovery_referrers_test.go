package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	discovery "github.com/ccsert/artifact-gateway/internal/ocidiscovery"
	"github.com/ccsert/artifact-gateway/internal/ocigraph"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type discoveryFixtureObject struct {
	body  []byte
	media string
}

type discoveryArtifactFixture struct {
	subject discovery.Descriptor
	refs    []discovery.Descriptor
	objects map[string]discoveryFixtureObject
}

func newDiscoveryArtifactFixture(t *testing.T, payload string) discoveryArtifactFixture {
	t.Helper()
	return newDiscoveryArtifactFixtureWithImageLayer(t, payload, nil)
}

func newDiscoveryArtifactFixtureWithImageLayer(t *testing.T, payload string, imageLayer []byte) discoveryArtifactFixture {
	t.Helper()
	f := discoveryArtifactFixture{objects: map[string]discoveryFixtureObject{}}
	add := func(media string, body []byte) discovery.Descriptor {
		d := discovery.Descriptor{MediaType: media, Digest: ocigraph.Digest(body), Size: int64(len(body))}
		f.objects[d.Digest] = discoveryFixtureObject{body: body, media: media}
		return d
	}
	encode := func(value any) []byte {
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	layers := []discovery.Descriptor{}
	diffIDs := []string{}
	if len(imageLayer) != 0 {
		layer := add("application/vnd.oci.image.layer.v1.tar", imageLayer)
		layers = append(layers, layer)
		diffIDs = append(diffIDs, layer.Digest)
	}
	config := add("application/vnd.oci.image.config.v1+json", encode(map[string]any{"architecture": runtime.GOARCH, "os": "linux", "rootfs": map[string]any{"type": "layers", "diff_ids": diffIDs}}))
	f.subject = add(ocigraph.OCIManifest, encode(map[string]any{"schemaVersion": 2, "mediaType": ocigraph.OCIManifest, "config": config, "layers": layers}))
	empty := add("application/vnd.oci.empty.v1+json", []byte("{}"))
	layer := add("application/spdx+json", []byte(payload))
	layer.Annotations = map[string]string{"org.opencontainers.image.title": "sbom.spdx.json"}
	annotations := map[string]string{"org.opencontainers.image.description": "verified bytes, no signer trust assertion"}
	ref := add(ocigraph.OCIManifest, encode(map[string]any{"schemaVersion": 2, "mediaType": ocigraph.OCIManifest, "artifactType": "application/spdx+json", "subject": f.subject, "config": empty, "layers": []discovery.Descriptor{layer}, "annotations": annotations}))
	ref.ArtifactType = "application/spdx+json"
	ref.Annotations = annotations
	f.refs = []discovery.Descriptor{ref}
	return f
}

func (f discoveryArtifactFixture) serve(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.URL.Path, "/referrers/") {
		w.Header().Set("Content-Type", ocigraph.OCIIndex)
		refs := []discovery.Descriptor{}
		if strings.HasSuffix(r.URL.Path, "/"+f.subject.Digest) {
			refs = f.refs
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"schemaVersion": 2, "mediaType": ocigraph.OCIIndex, "manifests": refs})
		return
	}
	parts := strings.Split(r.URL.Path, "/")
	object, found := f.objects[parts[len(parts)-1]]
	if !found {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", object.media)
	w.Header().Set("Docker-Content-Digest", parts[len(parts)-1])
	w.Header().Set("Content-Length", fmt.Sprint(len(object.body)))
	_, _ = w.Write(object.body)
}

func TestOCIReferrersVerifiedSourceBindingAndColdReads(t *testing.T) {
	a, b := newDiscoveryArtifactFixture(t, `{"SPDXID":"A"}`), newDiscoveryArtifactFixture(t, `{"SPDXID":"B"}`)
	var aCalls, bCalls atomic.Int32
	fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/a/") {
			aCalls.Add(1)
			a.serve(w, r)
			return
		}
		bCalls.Add(1)
		b.serve(w, r)
	})
	store := repository.NewMemoryStore()
	ra, rb := catalogProxy(t, store, "source-a"), catalogProxy(t, store, "source-b")
	var err error
	ra.Endpoint += "/a"
	ra, err = store.UpdateHostedRepository(context.Background(), ra, ra.Version)
	if err != nil {
		t.Fatal(err)
	}
	rb.Endpoint += "/b"
	rb, err = store.UpdateHostedRepository(context.Background(), rb, rb.Version)
	if err != nil {
		t.Fatal(err)
	}
	group := createV2Group(t, store, "discovery-group", repository.FormatOCI, repository.GroupMember{RepositoryID: ra.ID, Position: 0}, repository.GroupMember{RepositoryID: rb.ID, Position: 1})
	handler := catalogGateway(t, store, fixture.client, true)
	path := "/v2/" + group.Name + "/team/app/referrers/" + a.subject.Digest
	first := discoveryRequest(t, handler, path, "admin-secret")
	if first.Code != 200 || !strings.Contains(first.Body.String(), a.refs[0].Digest) || strings.Contains(first.Body.String(), b.refs[0].Digest) || bCalls.Load() != 0 {
		t.Fatalf("referrers=%d %s sourceB=%d", first.Code, first.Body.String(), bCalls.Load())
	}
	head := httptest.NewRequest(http.MethodHead, path, nil)
	authorize(head, "admin-secret")
	headed := httptest.NewRecorder()
	handler.ServeHTTP(headed, head)
	if headed.Code != 200 || headed.Body.Len() != 0 || headed.Header().Get("OCI-Gateway-Discovery-Coverage") != "complete" {
		t.Fatalf("Referrers HEAD=%d %s", headed.Code, headed.Body.String())
	}
	for digest, object := range a.objects {
		resource := "blobs"
		if object.media == ocigraph.OCIManifest {
			resource = "manifests"
		}
		response := discoveryRequest(t, handler, "/v2/"+group.Name+"/team/app/"+resource+"/"+digest, "admin-secret")
		if response.Code != 200 || response.Body.String() != string(object.body) || response.Header().Get("Docker-Content-Digest") != digest {
			t.Fatalf("bound object %s=%d %s", digest, response.Code, response.Body.String())
		}
	}
	before := aCalls.Load()
	group.Members[0].Position, group.Members[1].Position = 1, 0
	if _, err := store.ReplaceHostedGroup(context.Background(), group, group.Version); err != nil {
		t.Fatal(err)
	}
	again := discoveryRequest(t, handler, path, "admin-secret")
	if again.Code != 200 || !strings.Contains(again.Body.String(), a.refs[0].Digest) || bCalls.Load() != 0 || aCalls.Load() != before {
		t.Fatalf("group reordering drifted source=%d %s A=%d B=%d", again.Code, again.Body.String(), aCalls.Load(), bCalls.Load())
	}
	ra.Endpoint += "/changed"
	if _, err := store.UpdateHostedRepository(context.Background(), ra, ra.Version); err != nil {
		t.Fatal(err)
	}
	changed := discoveryRequest(t, handler, path, "admin-secret")
	if changed.Code != 409 || bCalls.Load() != 0 {
		t.Fatalf("source change=%d %s", changed.Code, changed.Body.String())
	}
}

func TestOCIReferrersRejectInFlightSourceChange(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := objectstore.NewMemoryStore()
	testOCIReferrersRejectInFlightSourceChange(t, store, store, objects)
}

func testOCIReferrersRejectInFlightSourceChange(t *testing.T, store GatewayStore, persistence discovery.Store, objects ObjectStore) {
	t.Helper()
	f := newDiscoveryArtifactFixture(t, "source version race payload")
	entered, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/blobs/"+ocigraph.Digest([]byte("source version race payload"))) {
			enterOnce.Do(func() { close(entered) })
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		f.serve(w, r)
	})
	fixture.client.HTTPClient.Timeout = 20 * time.Second
	repo := catalogProxy(t, store, "inflight-"+uuid.NewString()[:8])
	handler := newTestGateway(t, GatewayOptions{Store: store, OCIDiscoveryStore: persistence, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatOCI: objects}, Caches: GatewayCaches{OCI: NewDefaultOCICache(objects, []string{redirectOrigin, redirectCDN})}, Upstream: fixture.client})
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	response := make(chan *httptest.ResponseRecorder, 1)
	finished := make(chan struct{})
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("owned discovery request did not settle")
		}
	})
	go func() {
		defer close(finished)
		r := httptest.NewRequest(http.MethodGet, "/v2/"+repo.Name+"/team/app/referrers/"+f.subject.Digest, nil).WithContext(ctx)
		authorize(r, "admin-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		response <- w
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("source payload gate was not reached")
	}
	if _, err := store.UpdateHostedRepository(ctx, repo, repo.Version); err != nil {
		t.Fatal(err)
	}
	select {
	case premature := <-response:
		t.Fatalf("source request settled before its original body was released: %d %s", premature.Code, premature.Body.String())
	default:
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case got := <-response:
		if got.Code != 409 || !strings.Contains(got.Body.String(), "DISCOVERY_CHANGED") {
			t.Fatalf("stale source publication=%d %s", got.Code, got.Body.String())
		}
	case <-ctx.Done():
		t.Fatal("source change response did not settle")
	}
	if _, err := persistence.GetOCISubjectBinding(t.Context(), "repository:"+repo.ID, discovery.OwnerForActor("alice"), "team/app", f.subject.Digest); err != discovery.ErrNotFound {
		t.Fatalf("stale in-flight graph became a published binding: %v", err)
	}
}

func TestOCIReferrersRejectIncompleteAndUnsupportedAuthority(t *testing.T) {
	for _, mode := range []string{"wrong subject", "missing closure", "unsupported", "malformed index", "foreign link", "filter mismatch", "source unauthorized", "source forbidden", "missing subject", "pagination loop", "missing descriptor size", "wrong media type", "referrer budget", "graph depth"} {
		t.Run(mode, func(t *testing.T) {
			f := newDiscoveryArtifactFixture(t, "SBOM")
			if mode == "wrong subject" {
				object := f.objects[f.refs[0].Digest]
				var document map[string]any
				if err := json.Unmarshal(object.body, &document); err != nil {
					t.Fatal(err)
				}
				document["subject"] = discovery.Descriptor{Digest: "sha256:" + strings.Repeat("f", 64), Size: f.subject.Size, MediaType: f.subject.MediaType}
				body, err := json.Marshal(document)
				if err != nil {
					t.Fatal(err)
				}
				delete(f.objects, f.refs[0].Digest)
				f.refs[0].Digest = ocigraph.Digest(body)
				f.refs[0].Size = int64(len(body))
				f.objects[f.refs[0].Digest] = discoveryFixtureObject{body: body, media: ocigraph.OCIManifest}
			}
			if mode == "missing closure" {
				delete(f.objects, ocigraph.Digest([]byte("SBOM")))
			}
			if mode == "missing subject" {
				delete(f.objects, f.subject.Digest)
			}
			if mode == "wrong media type" {
				object := f.objects[f.refs[0].Digest]
				object.media = "application/json"
				f.objects[f.refs[0].Digest] = object
			}
			if mode == "referrer budget" {
				f.refs = make([]discovery.Descriptor, discovery.MaxReferrers+1)
				for i := range f.refs {
					f.refs[i] = f.subject
				}
			}
			if mode == "graph depth" {
				for range ocigraph.MaxDepth + 1 {
					body, err := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": ocigraph.OCIIndex, "manifests": []discovery.Descriptor{f.subject}})
					if err != nil {
						t.Fatal(err)
					}
					f.subject = discovery.Descriptor{Digest: ocigraph.Digest(body), Size: int64(len(body)), MediaType: ocigraph.OCIIndex}
					f.objects[f.subject.Digest] = discoveryFixtureObject{body: body, media: ocigraph.OCIIndex}
				}
			}
			if mode == "filter mismatch" {
				f.refs[0].ArtifactType = "application/forged"
			}
			fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/referrers/") {
					if mode == "source unauthorized" || mode == "source forbidden" {
						status := http.StatusUnauthorized
						if mode == "source forbidden" {
							status = http.StatusForbidden
						}
						w.WriteHeader(status)
						return
					}
					if mode == "unsupported" {
						http.NotFound(w, r)
						return
					}
					if mode == "malformed index" {
						w.Header().Set("Content-Type", ocigraph.OCIIndex)
						_, _ = w.Write([]byte(`{"schemaVersion":2}`))
						return
					}
					if mode == "foreign link" {
						w.Header().Set("Link", `<https://`+redirectCDN+`/v2/team/app/referrers/`+f.subject.Digest+`?last=x>; rel="next"`)
					}
					if mode == "pagination loop" {
						w.Header().Set("Link", `</v2/team/app/referrers/`+f.subject.Digest+`?last=loop>; rel="next"`)
					}
					if mode == "missing descriptor size" {
						w.Header().Set("Content-Type", ocigraph.OCIIndex)
						_ = json.NewEncoder(w).Encode(map[string]any{"schemaVersion": 2, "manifests": []map[string]any{{"digest": f.refs[0].Digest, "mediaType": ocigraph.OCIManifest}}})
						return
					}
				}
				f.serve(w, r)
			})
			store := repository.NewMemoryStore()
			repo := catalogProxy(t, store, "discovery-proxy")
			response := discoveryRequest(t, catalogGateway(t, store, fixture.client, true), "/v2/"+repo.Name+"/team/app/referrers/"+f.subject.Digest, "admin-secret")
			want := 502
			if mode == "unsupported" {
				want = 501
			}
			if mode == "missing subject" {
				want = 404
			}
			if mode == "referrer budget" || mode == "graph depth" {
				want = 429
			}
			if response.Code != want {
				t.Fatalf("invalid authority=%d %s", response.Code, response.Body.String())
			}
			if _, err := store.GetOCISubjectBinding(context.Background(), "repository:"+repo.ID, discovery.OwnerForActor("alice"), "team/app", f.subject.Digest); err != discovery.ErrNotFound {
				t.Fatalf("incomplete publication=%v", err)
			}
		})
	}
}

func TestOCIReferrersRequireExplicitAuthorityBeforeSourceAccess(t *testing.T) {
	f := newDiscoveryArtifactFixture(t, "explicit authorization required")
	var calls atomic.Int32
	fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/v2/_catalog" {
			_, _ = w.Write([]byte(`{"repositories":["team/app","private/secret"]}`))
			return
		}
		f.serve(w, r)
	})
	store := repository.NewMemoryStore()
	repo := catalogProxy(t, store, "discovery-explicit-auth")
	group := createV2Group(t, store, "discovery-explicit-group", repository.FormatOCI, repository.GroupMember{RepositoryID: repo.ID})
	handler := catalogGateway(t, store, fixture.client, true)
	token := testAuthenticator().IssueToken("unbound-reader")
	for _, name := range []string{repo.Name, group.Name} {
		got := discoveryRequest(t, handler, "/v2/"+name+"/team/app/referrers/"+f.subject.Digest, token)
		if got.Code != 403 || calls.Load() != 0 {
			t.Fatalf("unbound principal reached %s authority: %d %s source calls=%d", name, got.Code, got.Body.String(), calls.Load())
		}
	}
	listed := discoveryRequest(t, handler, "/v2/_catalog", token)
	if listed.Code != 200 || strings.Contains(listed.Body.String(), "team/app") || calls.Load() != 0 {
		t.Fatalf("unbound principal enumerated private names: %d %s", listed.Code, listed.Body.String())
	}
	catalogReader(t, store, repo, "unbound-reader", "team/")
	for _, name := range []string{repo.Name, group.Name} {
		got := discoveryRequest(t, handler, "/v2/"+name+"/team/app/referrers/"+f.subject.Digest, token)
		if got.Code != 200 || !strings.Contains(got.Body.String(), f.refs[0].Digest) {
			t.Fatalf("minimal grant failed %s positive control: %d %s", name, got.Code, got.Body.String())
		}
	}
	listed = discoveryRequest(t, handler, "/v2/_catalog", token)
	if listed.Code != 200 || !strings.Contains(listed.Body.String(), repo.Name+"/team/app") || !strings.Contains(listed.Body.String(), group.Name+"/team/app") || strings.Contains(listed.Body.String(), "private/secret") || calls.Load() == 0 {
		t.Fatalf("minimal grant Catalog positive control=%d %s", listed.Code, listed.Body.String())
	}
}

func TestOCIReferrersStablePaginationAndLocalFilter(t *testing.T) {
	f := newDiscoveryArtifactFixture(t, "first artifact")
	other := newDiscoveryArtifactFixture(t, "second artifact")
	for digest, object := range other.objects {
		f.objects[digest] = object
	}
	refs := []discovery.Descriptor{f.refs[0], other.refs[0]}
	var calls atomic.Int32
	fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if strings.Contains(r.URL.Path, "/referrers/") {
			if r.URL.Query().Has("artifactType") {
				t.Error("local filter was sent upstream and could conceal an incomplete graph")
			}
			items := []discovery.Descriptor{refs[0], refs[0]}
			if r.URL.Query().Get("last") == "" {
				w.Header().Set("Link", "</v2/team/app/referrers/"+f.subject.Digest+"?last="+refs[0].Digest+">; rel=\"next\"")
			} else {
				items = []discovery.Descriptor{refs[1]}
			}
			w.Header().Set("Content-Type", ocigraph.OCIIndex)
			_ = json.NewEncoder(w).Encode(map[string]any{"schemaVersion": 2, "mediaType": ocigraph.OCIIndex, "manifests": items})
			return
		}
		f.serve(w, r)
	})
	store := repository.NewMemoryStore()
	repo := catalogProxy(t, store, "discovery-filter")
	catalogReader(t, store, repo, "catalog-reader", "team/")
	handler := catalogGateway(t, store, fixture.client, true)
	path := "/v2/" + repo.Name + "/team/app/referrers/" + f.subject.Digest
	token := testAuthenticator().IssueToken("catalog-reader")
	first := discoveryRequest(t, handler, path+"?n=1&artifactType=application%2Fspdx%2Bjson", token)
	link, err := ociDiscoveryNext(first.Header().Values("Link"))
	if err != nil || first.Code != 200 || link == "" || first.Header().Get("OCI-Filters-Applied") != "artifactType" {
		t.Fatalf("first page=%d %s link=%q err=%v", first.Code, first.Body.String(), link, err)
	}
	before := calls.Load()
	next := discoveryRequest(t, handler, link, token)
	if next.Code != 200 || next.Body.String() == first.Body.String() || calls.Load() != before {
		t.Fatalf("immutable next page=%d %s calls=%d/%d", next.Code, next.Body.String(), calls.Load(), before)
	}
	wrong := discoveryRequest(t, handler, strings.Replace(link, "application%2Fspdx%2Bjson", "application%2Fother", 1), token)
	if wrong.Code != 400 {
		t.Fatalf("cursor filter substitution=%d %s", wrong.Code, wrong.Body.String())
	}
	empty := discoveryRequest(t, handler, path+"?artifactType=application%2Fother", token)
	if empty.Code != 200 || !strings.Contains(empty.Body.String(), `"manifests":[]`) || calls.Load() != before {
		t.Fatalf("known supported empty filter=%d %s", empty.Code, empty.Body.String())
	}
	catalogReader(t, store, repo, "other-reader", "")
	stale := discoveryRequest(t, handler, link, token)
	if stale.Code != 401 && stale.Code != 409 {
		t.Fatalf("revoked snapshot=%d %s", stale.Code, stale.Body.String())
	}
	if calls.Load() != before {
		t.Fatal("revoked source was contacted")
	}
}

func TestOCIReferrersRevocationNeverSelectsAnotherSource(t *testing.T) {
	a, b := newDiscoveryArtifactFixture(t, "source A"), newDiscoveryArtifactFixture(t, "source B")
	var lower atomic.Int32
	fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/a/") {
			a.serve(w, r)
		} else {
			lower.Add(1)
			b.serve(w, r)
		}
	})
	store := repository.NewMemoryStore()
	ra, rb := catalogProxy(t, store, "authority-a"), catalogProxy(t, store, "authority-b")
	var err error
	ra.Endpoint += "/a"
	ra, err = store.UpdateHostedRepository(t.Context(), ra, ra.Version)
	if err != nil {
		t.Fatal(err)
	}
	rb.Endpoint += "/b"
	rb, err = store.UpdateHostedRepository(t.Context(), rb, rb.Version)
	if err != nil {
		t.Fatal(err)
	}
	catalogReader(t, store, ra, "catalog-reader", "team/")
	catalogReader(t, store, rb, "catalog-reader", "team/")
	group := createV2Group(t, store, "fixed-authority", repository.FormatOCI, repository.GroupMember{RepositoryID: ra.ID}, repository.GroupMember{RepositoryID: rb.ID, Position: 1})
	handler := catalogGateway(t, store, fixture.client, true)
	token := testAuthenticator().IssueToken("catalog-reader")
	base := "/v2/" + group.Name + "/team/app/"
	if response := discoveryRequest(t, handler, base+"referrers/"+a.subject.Digest, token); response.Code != 200 {
		t.Fatalf("first authority=%d %s", response.Code, response.Body.String())
	}
	catalogReader(t, store, ra, "other-reader", "")
	for _, path := range []string{"referrers/" + a.subject.Digest, "manifests/" + a.refs[0].Digest, "blobs/" + ocigraph.Digest([]byte("source A"))} {
		response := discoveryRequest(t, handler, base+path, token)
		if response.Code != 403 || lower.Load() != 0 {
			t.Fatalf("revocation selected lower source=%d %s lower=%d", response.Code, response.Body.String(), lower.Load())
		}
	}
}

func TestOCIReferrersPrincipalAuthorityPartition(t *testing.T) {
	for _, adminFirst := range []bool{false, true} {
		t.Run(fmt.Sprint("adminFirst=", adminFirst), func(t *testing.T) {
			store := repository.NewMemoryStore()
			objects := objectstore.NewMemoryStore()
			testOCIPrincipalAuthority(t, store, store, objects, objects, adminFirst)
		})
	}
}

func testOCIPrincipalAuthority(t *testing.T, store GatewayStore, bindings discovery.Store, objects, control ObjectStore, adminFirst bool) {
	t.Helper()
	a, b := newDiscoveryArtifactFixture(t, "authority A"), newDiscoveryArtifactFixture(t, "restricted B")
	fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/a/") {
			a.serve(w, r)
		} else {
			b.serve(w, r)
		}
	})
	ra, rb := catalogProxy(t, store, "authority-a-"+uuid.NewString()[:8]), catalogProxy(t, store, "authority-b-"+uuid.NewString()[:8])
	var err error
	ra.Endpoint += "/a"
	ra, err = store.UpdateHostedRepository(t.Context(), ra, ra.Version)
	if err != nil {
		t.Fatal(err)
	}
	rb.Endpoint += "/b"
	rb, err = store.UpdateHostedRepository(t.Context(), rb, rb.Version)
	if err != nil {
		t.Fatal(err)
	}
	catalogReader(t, store, ra, "other-reader", "")
	catalogReader(t, store, rb, "catalog-reader", "")
	group, _, err := store.CreateHostedGroupIdempotently(t.Context(), repository.HostedGroup{ID: uuid.NewString(), Name: "authority-group-" + uuid.NewString()[:8], Format: repository.FormatOCI, Members: []repository.GroupMember{{RepositoryID: ra.ID}, {RepositoryID: rb.ID, Position: 1}}}, "fixture-admin", uuid.NewString(), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	gateway := func(auth Authenticator) http.Handler {
		return newTestGateway(t, GatewayOptions{Store: store, OCIDiscoveryStore: bindings, Adapter: TestAdapter{}, Authenticator: auth, Storage: ObjectStores{repository.FormatOCI: objects}, Caches: GatewayCaches{OCI: NewDefaultOCICache(control, []string{redirectOrigin})}, Upstream: fixture.client})
	}
	handler := gateway(testAuthenticator())
	path := "/v2/" + group.Name + "/team/app/referrers/" + a.subject.Digest
	reader := testAuthenticator().IssueToken("catalog-reader")
	check := func(h http.Handler, token, want, absent string) {
		t.Helper()
		response := discoveryRequest(t, h, path, token)
		if response.Code != 200 || !strings.Contains(response.Body.String(), want) || strings.Contains(response.Body.String(), absent) {
			t.Fatalf("cross-principal authority=%d %s", response.Code, response.Body.String())
		}
	}
	if adminFirst {
		check(handler, "admin-secret", a.refs[0].Digest, b.refs[0].Digest)
		check(handler, reader, b.refs[0].Digest, a.refs[0].Digest)
	} else {
		check(handler, reader, b.refs[0].Digest, a.refs[0].Digest)
		check(handler, "admin-secret", a.refs[0].Digest, b.refs[0].Digest)
	}
	group.Members[0].Position, group.Members[1].Position = 1, 0
	if _, err := store.ReplaceHostedGroup(t.Context(), group, group.Version); err != nil {
		t.Fatal(err)
	}
	check(handler, "admin-secret", a.refs[0].Digest, b.refs[0].Digest)
	check(handler, reader, b.refs[0].Digest, a.refs[0].Digest)
	elevated := testAuthenticator()
	elevated.AdminActor = "catalog-reader"
	elevated.AdminToken = "elevated-fixture-credential"
	check(gateway(elevated), elevated.AdminToken, b.refs[0].Digest, a.refs[0].Digest)
	elevated.AdminToken = "rotated-fixture-credential"
	check(gateway(elevated), elevated.AdminToken, b.refs[0].Digest, a.refs[0].Digest)
	catalogReader(t, store, rb, "other-reader", "")
	if denied := discoveryRequest(t, handler, path, reader); denied.Code != 403 {
		t.Fatalf("revocation replaced authority=%d %s", denied.Code, denied.Body.String())
	}
	rb.Endpoint += "/changed"
	if _, err := store.UpdateHostedRepository(t.Context(), rb, rb.Version); err != nil {
		t.Fatal(err)
	}
	if changed := discoveryRequest(t, gateway(elevated), path, elevated.AdminToken); changed.Code != 409 {
		t.Fatalf("version change replaced authority=%d %s", changed.Code, changed.Body.String())
	}
}

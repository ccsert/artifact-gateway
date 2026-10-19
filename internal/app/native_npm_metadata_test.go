package app

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type npmGroupSummaryOnlyStore struct{ GatewayStore }

func npmMetadataDocument(t *testing.T, handler http.Handler, path string) map[string]any {
	t.Helper()
	response := npmDistTagRequest(t, handler, http.MethodGet, path+"?write=true", "resolver-secret", "")
	if response.Code != http.StatusOK {
		t.Fatalf("packument %s=%d %s", path, response.Code, response.Body.String())
	}
	var document map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func TestNativeNPMMetadataRejectsPartialAndImmutableChanges(t *testing.T) {
	store := repository.NewMemoryStore()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "npm-invalid-metadata", Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatNPM: objectstore.NewMemoryStore()}})
	publishNPMDistTagHTTPFixture(t, handler, repo.Name, "widget", "1.0.0", map[string]string{"latest": "1.0.0"})
	publishNPMDistTagHTTPFixture(t, handler, repo.Name, "widget", "1.1.0", map[string]string{"next": "1.1.0"})
	path := "/npm/" + repo.Name + "/widget"
	for _, tc := range []struct {
		name   string
		status int
		change func(map[string]any)
	}{
		{"missing revision", 400, func(d map[string]any) { delete(d, "_rev") }},
		{"immutable bytes identity", 400, func(d map[string]any) {
			d["versions"].(map[string]any)["1.0.0"].(map[string]any)["dist"].(map[string]any)["integrity"] = "sha512-forged"
		}},
		{"deprecation changes tags", 400, func(d map[string]any) { d["dist-tags"].(map[string]any)["latest"] = "1.1.0" }},
		{"deprecation removes version", 400, func(d map[string]any) { delete(d["versions"].(map[string]any), "1.1.0") }},
		{"partial unknown selection", 404, func(d map[string]any) {
			v := d["versions"].(map[string]any)
			v["1.0.0"].(map[string]any)["deprecated"] = "must not commit"
			v["9.9.9"] = map[string]string{"name": "widget", "version": "9.9.9", "deprecated": "unknown"}
		}},
		{"oversized message", 400, func(d map[string]any) {
			d["versions"].(map[string]any)["1.0.0"].(map[string]any)["deprecated"] = strings.Repeat("界", 1366)
		}},
		{"terminal escape", 400, func(d map[string]any) {
			d["versions"].(map[string]any)["1.0.0"].(map[string]any)["deprecated"] = "unsafe\x1b[2J"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document := npmMetadataDocument(t, handler, path)
			tc.change(document)
			response := npmDistTagRequest(t, handler, http.MethodPut, path, "resolver-secret", npmMetadataJSON(t, document))
			if response.Code != tc.status {
				t.Fatalf("invalid mutation=%d want %d: %s", response.Code, tc.status, response.Body.String())
			}
			version, e := store.GetNPMVersion(ctx, repo.ID, "widget", "1.0.0")
			if e != nil || version.Deprecated != "" {
				t.Fatalf("partial metadata committed: %#v %v", version, e)
			}
		})
	}
	document := npmMetadataDocument(t, handler, path)
	document["versions"].(map[string]any)["1.0.0"].(map[string]any)["deprecated"] = "cancelled"
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	request := httptest.NewRequest(http.MethodPut, path, strings.NewReader(npmMetadataJSON(t, document))).WithContext(cancelled)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 503 {
		t.Fatalf("cancelled metadata=%d", response.Code)
	}
	version, err := store.GetNPMVersion(ctx, repo.ID, "widget", "1.0.0")
	if err != nil || version.Deprecated != "" {
		t.Fatal("cancelled request committed metadata")
	}
}

func npmMetadataJSON(t *testing.T, document map[string]any) string {
	t.Helper()
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestNativeNPMProxyRevalidationUpdatesDeprecation(t *testing.T) {
	const name, version = "upstream-deprecation", "1.0.0"
	tarball := npmFixtureTarball(t, name, version)
	strong, weak := sha512.Sum512(tarball), sha1.Sum(tarball)
	var generation, conditional, leakedCredentials atomic.Int64
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leakedCredentials.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		etag, warning := `"deprecated"`, "upstream warning"
		if generation.Load() == 1 {
			etag, warning = `"cleared"`, ""
		}
		if r.Header.Get("If-None-Match") != "" {
			conditional.Add(1)
		}
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": name, "dist-tags": map[string]string{"latest": version},
			"versions": map[string]any{version: map[string]any{
				"name": name, "version": version, "deprecated": warning,
				"dist": map[string]string{"tarball": upstream.URL + "/upstream-deprecation-1.0.0.tgz", "integrity": "sha512-" + base64.StdEncoding.EncodeToString(strong[:]), "shasum": hex.EncodeToString(weak[:])},
			}},
		})
	}))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "npm-deprecation-proxy", Format: repository.FormatNPM,
		Type: repository.RepositoryTypeProxy, Endpoint: upstream.URL, AllowedHosts: []string{"127.0.0.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{NPMMetadataTTL: time.Hour}, Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatNPM: objectstore.NewMemoryStore()}, Upstream: UpstreamClient{HTTPClient: upstream.Client()}})
	path := "/npm/" + repo.Name + "/" + name
	for index, want := range []string{"upstream warning", "", ""} {
		if index != 0 {
			generation.Store(1)
			cached, readErr := store.GetNPMPackage(context.Background(), repo.ID, name)
			if readErr != nil {
				t.Fatal(readErr)
			}
			// Explicitly expire the fixture snapshot; no scheduler timing is
			// needed to prove the conditional revalidation and 304 paths.
			cached.MetadataExpiresAt = time.Now().UTC().Add(-time.Hour)
			if _, err = store.SyncNPMProxyPackage(context.Background(), cached); err != nil {
				t.Fatal(err)
			}
		}
		document := npmMetadataDocument(t, handler, path)
		if got := document["versions"].(map[string]any)[version].(map[string]any)["deprecated"]; got != want {
			t.Fatalf("proxy warning=%#v want %q", got, want)
		}
		exact := npmDistTagRequest(t, handler, http.MethodGet, path+"/"+version, "resolver-secret", "")
		var manifest map[string]any
		if err = json.Unmarshal(exact.Body.Bytes(), &manifest); err != nil || exact.Code != 200 || manifest["deprecated"] != want {
			t.Fatalf("exact proxy warning=%s status=%d err=%v", exact.Body.String(), exact.Code, err)
		}
		if _, exists := document["_rev"]; exists {
			t.Fatal("Proxy advertised Hosted mutation revision")
		}
	}
	if conditional.Load() != 2 || leakedCredentials.Load() != 0 {
		t.Fatalf("conditional=%d leaked local credentials=%d", conditional.Load(), leakedCredentials.Load())
	}
}

func TestNativeNPMMetadataAndUnpublishLifecycle(t *testing.T) {
	for _, name := range []string{"governed", "@scope/governed"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store := repository.NewMemoryStore()
			objects := objectstore.NewMemoryStore()
			repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "npm-governed", Format: repository.FormatNPM})
			if err != nil {
				t.Fatal(err)
			}
			handler := newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatNPM: objects}})
			first := publishNPMDistTagHTTPFixture(t, handler, repo.Name, name, "1.0.0", map[string]string{"latest": "1.0.0"})
			publishNPMDistTagHTTPFixture(t, handler, repo.Name, name, "1.1.0", map[string]string{"next": "1.1.0"})
			path := "/npm/" + repo.Name + "/" + strings.Replace(name, "/", "%2f", 1)
			document := npmMetadataDocument(t, handler, path)
			versions := document["versions"].(map[string]any)
			versions["1.0.0"].(map[string]any)["deprecated"] = "Use 1.1.0 <script>text</script>"
			body := npmMetadataJSON(t, document)
			response := npmDistTagRequest(t, handler, http.MethodPut, path, "resolver-secret", body)
			if response.Code != http.StatusOK {
				t.Fatalf("deprecate=%d %s", response.Code, response.Body.String())
			}
			if replay := npmDistTagRequest(t, handler, http.MethodPut, path, "resolver-secret", body); replay.Code != http.StatusConflict {
				t.Fatalf("lost-response replay=%d %s", replay.Code, replay.Body.String())
			}
			exact := npmDistTagRequest(t, handler, http.MethodGet, path+"/1.0.0", "resolver-secret", "")
			if exact.Code != 200 || !strings.Contains(exact.Body.String(), `"deprecated":"Use 1.1.0`) {
				t.Fatalf("exact=%d %s", exact.Code, exact.Body.String())
			}
			document = npmMetadataDocument(t, handler, path)
			versions = document["versions"].(map[string]any)
			versions["1.0.0"].(map[string]any)["deprecated"] = ""
			if result := npmDistTagRequest(t, handler, http.MethodPut, path, "resolver-secret", npmMetadataJSON(t, document)); result.Code != 200 {
				t.Fatalf("undeprecate=%d %s", result.Code, result.Body.String())
			}
			document = npmMetadataDocument(t, handler, path)
			versions = document["versions"].(map[string]any)
			if _, deprecated := versions["1.0.0"].(map[string]any)["deprecated"]; deprecated {
				t.Fatal("cleared deprecation remained")
			}
			stored, _ := store.GetNPMVersion(ctx, repo.ID, name, "1.0.0")
			delete(versions, "1.0.0")
			document["dist-tags"] = map[string]string{"latest": "1.1.0", "next": "1.1.0"}
			unpublishPath := path + "/-rev/" + document["_rev"].(string)
			if result := npmDistTagRequest(t, handler, http.MethodPut, unpublishPath, "admin-secret", npmMetadataJSON(t, document)); result.Code != 200 {
				t.Fatalf("unpublish=%d %s", result.Code, result.Body.String())
			}
			for _, suffix := range []string{"/1.0.0", "/-/" + stored.TarballName} {
				if result := npmDistTagRequest(t, handler, http.MethodGet, path+suffix, "resolver-secret", ""); result.Code != 404 {
					t.Fatalf("unpublished %s=%d", suffix, result.Code)
				}
			}
			document = npmMetadataDocument(t, handler, path)
			cleanup := path + "/-/" + stored.TarballName + "/-rev/" + document["_rev"].(string)
			if result := npmDistTagRequest(t, handler, http.MethodDelete, cleanup, "admin-secret", ""); result.Code != 200 {
				t.Fatalf("cleanup=%d %s", result.Code, result.Body.String())
			}
			if data, getErr := objects.Get(ctx, stored.ObjectKey); getErr != nil || !bytes.Equal(data, first) {
				t.Fatal("unpublish removed immutable bytes")
			}
			if result := npmDistTagRequest(t, handler, http.MethodPost, "/api/v2/repositories/"+repo.ID+"/restore", "admin-secret", `{"coordinate":"`+name+`@1.0.0"}`); result.Code != 204 {
				t.Fatalf("restore=%d %s", result.Code, result.Body.String())
			}
			if restored := npmDistTagRequest(t, handler, http.MethodGet, path+"/-/"+stored.TarballName, "resolver-secret", ""); restored.Code != 200 || !bytes.Equal(restored.Body.Bytes(), first) {
				t.Fatalf("restored bytes=%d", restored.Code)
			}
			document = npmMetadataDocument(t, handler, path)
			if result := npmDistTagRequest(t, handler, http.MethodDelete, path+"/-rev/"+document["_rev"].(string), "admin-secret", ""); result.Code != 200 {
				t.Fatalf("whole unpublish=%d %s", result.Code, result.Body.String())
			}
			if result := npmDistTagRequest(t, handler, http.MethodGet, path, "resolver-secret", ""); result.Code != 404 {
				t.Fatalf("whole package=%d", result.Code)
			}
			var deprecations, withdrawals int
			for _, a := range store.Audits {
				if a.Outcome == repository.AuditResolved {
					if a.Operation == "npm.deprecate" {
						deprecations++
					}
					if a.Operation == "npm.unpublish" {
						withdrawals++
					}
				}
			}
			if deprecations != 2 || withdrawals != 2 {
				t.Fatalf("atomic audits deprecations=%d withdrawals=%d", deprecations, withdrawals)
			}
		})
	}
}

func TestNativeNPMGroupTombstoneAuthority(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := objectstore.NewMemoryStore()
	primary, _ := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "npm-primary", Format: repository.FormatNPM, AnonymousRead: true})
	secondary, _ := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "npm-secondary", Format: repository.FormatNPM, AnonymousRead: true})
	handler := newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatNPM: objects}})
	publishNPMDistTagHTTPFixture(t, handler, primary.Name, "widget", "1.0.0", map[string]string{"latest": "1.0.0"})
	visibleTarball := publishNPMDistTagHTTPFixture(t, handler, secondary.Name, "widget", "2.0.0", map[string]string{"next": "2.0.0"})
	// The lower member's most recent publication and latest tag both identify
	// the withdrawn version; its older v2 must still be discoverable.
	publishNPMDistTagHTTPFixture(t, handler, secondary.Name, "widget", "1.0.0", map[string]string{"latest": "1.0.0"})
	groupRecord, _, createErr := store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{ID: uuid.NewString(), Name: "npm-group", Format: repository.FormatNPM, Members: []repository.GroupMember{{RepositoryID: primary.ID, Position: 0}, {RepositoryID: secondary.ID, Position: 1}}}, "fixture", "npm-group", "fixture")
	if createErr != nil {
		t.Fatal(createErr)
	}
	groupRecord.AnonymousRead = true
	if _, err := store.ReplaceHostedGroup(ctx, groupRecord, groupRecord.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceAnonymousAccessPolicy(ctx, repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	document := npmMetadataDocument(t, handler, "/npm/npm-primary/widget")
	versions := document["versions"].(map[string]any)
	versions["1.0.0"].(map[string]any)["deprecated"] = "primary warning"
	if result := npmDistTagRequest(t, handler, http.MethodPut, "/npm/npm-primary/widget", "resolver-secret", npmMetadataJSON(t, document)); result.Code != 200 {
		t.Fatal(result.Body.String())
	}
	group := npmMetadataDocument(t, handler, "/npm/npm-group/widget")
	if group["versions"].(map[string]any)["1.0.0"].(map[string]any)["deprecated"] != "primary warning" {
		t.Fatal("Group lost authoritative deprecation")
	}
	document = npmMetadataDocument(t, handler, "/npm/npm-primary/widget")
	if result := npmDistTagRequest(t, handler, http.MethodDelete, "/npm/npm-primary/widget/-rev/"+document["_rev"].(string), "admin-secret", ""); result.Code != 200 {
		t.Fatal(result.Body.String())
	}
	group = npmMetadataDocument(t, handler, "/npm/npm-group/widget")
	if _, exists := group["versions"].(map[string]any)["1.0.0"]; exists {
		t.Fatal("Group resurrected tombstone from secondary")
	}
	if _, exists := group["versions"].(map[string]any)["2.0.0"]; !exists {
		t.Fatal("Group hid the remaining authoritative version")
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, target := range []struct{ suffix, resource string }{{"/1.0.0", "widget@1.0.0"}, {"/-/widget-1.0.0.tgz", "widget/-/widget-1.0.0.tgz"}} {
			suffix := target.suffix
			query := repository.AuditQuery{GroupName: groupRecord.Name, Format: "npm", Actor: "build-agent", Operation: strings.ToLower(method), Outcome: string(repository.AuditNotFound), Limit: 100}
			before, err := store.ListAudits(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			if result := npmDistTagRequest(t, handler, method, "/npm/npm-group/widget"+suffix, "resolver-secret", ""); result.Code != http.StatusNotFound {
				t.Fatalf("Group tombstone %s %s=%d %s", method, suffix, result.Code, result.Body.String())
			}
			after, err := store.ListAudits(ctx, query)
			if err != nil || len(after) != len(before)+1 {
				t.Fatalf("Group tombstone %s %s lost or duplicated its read audit: before=%d after=%d err=%v", method, suffix, len(before), len(after), err)
			}
			audit := after[0]
			if audit.GroupName != groupRecord.Name || audit.Repository != groupRecord.Name || audit.Resource != target.resource || audit.Actor != "build-agent" || audit.Status != http.StatusNotFound || audit.Outcome != repository.AuditNotFound {
				t.Fatalf("Group tombstone read audit identifies the wrong target: %+v", audit)
			}
		}
	}
	expectedDigest := sha256.Sum256(visibleTarball)
	// Both the projection and narrow reader fallback must answer the same
	// public search contract; this wrapper removes only optional capabilities.
	summaryOnly := newTestGateway(t, GatewayOptions{Store: npmGroupSummaryOnlyStore{store}, NPMMetadata: store, NPMRestore: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatNPM: objects}})
	for _, gateway := range []http.Handler{handler, summaryOnly} {
		for _, match := range []string{"prefix", "contains"} {
			search := npmDistTagRequest(t, gateway, http.MethodGet, "/api/v2/repositories/"+groupRecord.ID+"/artifact-search?q=widget&match="+match+"&pageSize=1", "", "")
			var page struct {
				Items []struct {
					Coordinate   string `json:"coordinate"`
					Version      string `json:"version"`
					VersionCount int    `json:"versionCount"`
					Digest       string `json:"digest"`
					Size         int64  `json:"size"`
				} `json:"items"`
				Next string `json:"nextPageToken"`
			}
			if search.Code != http.StatusOK || json.Unmarshal(search.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].Coordinate != "widget" || page.Items[0].Version != "2.0.0" || page.Items[0].VersionCount != 1 || page.Items[0].Digest != "sha256:"+hex.EncodeToString(expectedDigest[:]) || page.Items[0].Size != int64(len(visibleTarball)) || page.Next != "" {
				t.Fatalf("Group search lost the remaining authoritative version: %d %s", search.Code, search.Body.String())
			}
		}
	}
	document = npmMetadataDocument(t, handler, "/npm/"+secondary.Name+"/widget")
	if result := npmDistTagRequest(t, handler, http.MethodDelete, "/npm/"+secondary.Name+"/widget/-rev/"+document["_rev"].(string), "admin-secret", ""); result.Code != http.StatusOK {
		t.Fatalf("withdraw remaining package=%d %s", result.Code, result.Body.String())
	}
	for _, suffix := range []string{"", "/2.0.0", "/-/widget-2.0.0.tgz"} {
		if result := npmDistTagRequest(t, handler, http.MethodGet, "/npm/npm-group/widget"+suffix, "resolver-secret", ""); result.Code != http.StatusNotFound {
			t.Fatalf("Group revived a fully withdrawn package via %s: %d %s", suffix, result.Code, result.Body.String())
		}
	}
	for _, gateway := range []http.Handler{handler, summaryOnly} {
		search := npmDistTagRequest(t, gateway, http.MethodGet, "/api/v2/repositories/"+groupRecord.ID+"/artifact-search?q=widget", "", "")
		var page struct {
			Items []json.RawMessage `json:"items"`
		}
		if search.Code != http.StatusOK || json.Unmarshal(search.Body.Bytes(), &page) != nil || len(page.Items) != 0 {
			t.Fatalf("Group search revived a fully withdrawn package: %d %s", search.Code, search.Body.String())
		}
	}
}

func TestNativeNPMGroupTombstoneDoesNotHideDifferentVersionTarball(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := objectstore.NewMemoryStore()
	primary, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "npm-filename-primary", Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "npm-filename-secondary", Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatNPM: objects}})
	publish := func(repoName, version, filename string) []byte {
		t.Helper()
		tarball := npmFixtureTarball(t, "widget", version)
		body := npmFixturePublishDocumentWithTags(t, "widget", version, filename, tarball, map[string]string{"latest": version})
		response := npmDistTagRequest(t, handler, http.MethodPut, "/npm/"+repoName+"/widget", "resolver-secret", body)
		if response.Code != http.StatusCreated {
			t.Fatalf("publish %s@%s=%d %s", repoName, version, response.Code, response.Body.String())
		}
		return tarball
	}
	const sharedFilename = "widget-1.0.0.tgz"
	publish(primary.Name, "1.0.0", sharedFilename)
	publish(secondary.Name, "1.0.0", "alternate.tgz")
	visibleTarball := publish(secondary.Name, "2.0.0", sharedFilename)
	createV2Group(t, store, "npm-filename-group", repository.FormatNPM, repository.GroupMember{RepositoryID: primary.ID}, repository.GroupMember{RepositoryID: secondary.ID, Position: 1})
	document := npmMetadataDocument(t, handler, "/npm/"+primary.Name+"/widget")
	if response := npmDistTagRequest(t, handler, http.MethodDelete, "/npm/"+primary.Name+"/widget/-rev/"+document["_rev"].(string), "admin-secret", ""); response.Code != http.StatusOK {
		t.Fatalf("withdraw primary=%d %s", response.Code, response.Body.String())
	}
	groupPath := "/npm/npm-filename-group/widget"
	document = npmMetadataDocument(t, handler, groupPath)
	versions := document["versions"].(map[string]any)
	if _, exists := versions["1.0.0"]; exists || versions["2.0.0"] == nil {
		t.Fatalf("Group version authority=%v", versions)
	}
	if tarballURL := versions["2.0.0"].(map[string]any)["dist"].(map[string]any)["tarball"].(string); !strings.HasSuffix(tarballURL, groupPath+"/-/"+sharedFilename) {
		t.Fatalf("Group changed the declared tarball name: %s", tarballURL)
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		response := npmDistTagRequest(t, handler, method, groupPath+"/-/"+sharedFilename, "resolver-secret", "")
		if response.Code != http.StatusOK || response.Header().Get("Content-Length") == "" || method == http.MethodGet && !bytes.Equal(response.Body.Bytes(), visibleTarball) || method == http.MethodHead && response.Body.Len() != 0 {
			t.Fatalf("Group %s hid v2 because withdrawn v1 shared its filename: %d %s", method, response.Code, response.Body.String())
		}
		for _, suffix := range []string{"/1.0.0", "/-/alternate.tgz"} {
			if response := npmDistTagRequest(t, handler, method, groupPath+suffix, "resolver-secret", ""); response.Code != http.StatusNotFound {
				t.Fatalf("Group %s resurrected withdrawn v1 via %s: %d %s", method, suffix, response.Code, response.Body.String())
			}
		}
	}
}

func TestNativeNPMMetadataAuthorizationAndRevocation(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := objectstore.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "npm-authorization", Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	reset, err := store.CreateUser(ctx, repository.User{ID: uuid.NewString(), Name: "npm-reset", Role: string(RoleAdmin), SecretHash: "synthetic-only", MustChangePassword: true})
	if err != nil {
		t.Fatal(err)
	}
	auth := testAuthenticatorWithUsers(store)
	handler := newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: auth, Storage: ObjectStores{repository.FormatNPM: objects}})
	publishNPMDistTagHTTPFixture(t, handler, repo.Name, "widget", "1.0.0", map[string]string{"latest": "1.0.0"})
	grants := []repository.RepositoryGrant{
		{Principal: "reader", Scopes: []string{"repositories:read"}},
		{Principal: "writer", Scopes: []string{"repositories:write"}, ResourcePrefix: "widget"},
		{Principal: "other-writer", Scopes: []string{"repositories:write"}, ResourcePrefix: "other"},
	}
	// A caller without a recognized grant never acquires write authority.
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, grants[:3], "1"); err != nil {
		t.Fatal(err)
	}
	path := "/npm/" + repo.Name + "/widget"
	document := npmMetadataDocumentAs(t, handler, path, "admin-secret")
	document["versions"].(map[string]any)["1.0.0"].(map[string]any)["deprecated"] = "upgrade"
	body := npmMetadataJSON(t, document)
	for _, tc := range []struct {
		token  string
		status int
	}{
		{"", 401}, {auth.IssueToken("reader"), 403}, {auth.IssueToken("other-writer"), 403}, {auth.IssueToken("unknown-scope"), 403},
		{auth.IssuePrincipalToken(Principal{Actor: "pending", Role: RoleNone}), 403}, {auth.IssueUserSession(reset.ID), 403},
	} {
		for _, request := range []struct{ method, path string }{{http.MethodPut, path}, {http.MethodPut, path + "/-rev/" + document["_rev"].(string)}, {http.MethodDelete, path + "/-rev/" + document["_rev"].(string)}, {http.MethodDelete, path + "/-/widget-1.0.0.tgz/-rev/" + document["_rev"].(string)}} {
			response := npmDistTagRequest(t, handler, request.method, request.path, tc.token, body)
			if response.Code != tc.status {
				t.Fatalf("denied %s %s=%d want %d: %s", request.method, request.path, response.Code, tc.status, response.Body.String())
			}
		}
	}
	writer := auth.IssueToken("writer")
	if response := npmDistTagRequest(t, handler, http.MethodPut, path, writer, body); response.Code != 200 {
		t.Fatalf("writer deprecate=%d %s", response.Code, response.Body.String())
	}
	document = npmMetadataDocumentAs(t, handler, path, writer)
	if response := npmDistTagRequest(t, handler, http.MethodDelete, path+"/-rev/"+document["_rev"].(string), writer, ""); response.Code != 403 {
		t.Fatalf("writer deleted=%d", response.Code)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, grants[:1], "2"); err != nil {
		t.Fatal(err)
	}
	if response := npmDistTagRequest(t, handler, http.MethodPut, path, writer, body); response.Code != 403 {
		t.Fatalf("revoked writer=%d", response.Code)
	}
	var denied bool
	for _, audit := range store.Audits {
		if audit.Actor == "writer" && audit.Outcome == repository.AuditAccessDenied && audit.AuthorizationReason == "scope_not_granted" {
			denied = true
		}
	}
	if !denied {
		t.Fatal("revocation denial was not audited")
	}
}

func npmMetadataDocumentAs(t *testing.T, handler http.Handler, path, token string) map[string]any {
	t.Helper()
	response := npmDistTagRequest(t, handler, http.MethodGet, path+"?write=true", token, "")
	if response.Code != 200 {
		t.Fatalf("read=%d %s", response.Code, response.Body.String())
	}
	var document map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	return document
}

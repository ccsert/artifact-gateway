package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func npmDistTagRequest(t *testing.T, handler http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, path, reader)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		authorize(request, token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeNPMDistTags(t *testing.T, response *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("dist-tags=%d %s", response.Code, response.Body.String())
	}
	var tags map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &tags); err != nil {
		t.Fatal(err)
	}
	return tags
}

func publishNPMDistTagHTTPFixture(t *testing.T, handler http.Handler, repoName, packageName, version string, tags map[string]string) []byte {
	t.Helper()
	tarball := npmFixtureTarball(t, packageName, version)
	body := npmFixturePublishDocumentWithTags(t, packageName, version, packageName+"-"+version+".tgz", tarball, tags)
	response := npmDistTagRequest(t, handler, http.MethodPut, "/npm/"+repoName+"/"+strings.Replace(packageName, "/", "%2F", 1), "resolver-secret", body)
	if response.Code != http.StatusCreated {
		t.Fatalf("publish %s@%s=%d %s", packageName, version, response.Code, response.Body.String())
	}
	return tarball
}

func TestNativeNPMDistTagsAddListMoveAndRemove(t *testing.T) {
	for _, packageName := range []string{"widget", "@scope/widget"} {
		t.Run(packageName, func(t *testing.T) {
			store := repository.NewMemoryStore()
			repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
				ID: uuid.NewString(), Name: "npm-tags", Format: repository.FormatNPM,
			})
			if err != nil {
				t.Fatal(err)
			}
			handler := NewGatewayHandler(Dependencies{NativeNPMObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator())
			first := publishNPMDistTagHTTPFixture(t, handler, repo.Name, packageName, "1.0.0", map[string]string{"latest": "1.0.0"})
			publishNPMDistTagHTTPFixture(t, handler, repo.Name, packageName, "1.1.0", map[string]string{"next": "1.1.0"})
			before, err := store.GetNPMVersion(context.Background(), repo.ID, packageName, "1.0.0")
			if err != nil {
				t.Fatal(err)
			}
			// npm escapes the scope separator with lower-case %2f.
			tagsPath := "/npm/" + repo.Name + "/-/package/" + strings.Replace(packageName, "/", "%2f", 1) + "/dist-tags"

			tags := decodeNPMDistTags(t, npmDistTagRequest(t, handler, http.MethodGet, tagsPath, "resolver-secret", ""))
			if len(tags) != 2 || tags["latest"] != "1.0.0" || tags["next"] != "1.1.0" {
				t.Fatalf("initial tags=%v", tags)
			}
			added := npmDistTagRequest(t, handler, http.MethodPut, tagsPath+"/beta", "resolver-secret", `"1.1.0"`)
			if added.Code != http.StatusOK || !strings.Contains(added.Body.String(), `"beta":"1.1.0"`) {
				t.Fatalf("add=%d %s", added.Code, added.Body.String())
			}
			if replay := npmDistTagRequest(t, handler, http.MethodPut, tagsPath+"/beta", "resolver-secret", `"1.1.0"`); replay.Code != http.StatusOK {
				t.Fatalf("replayed add=%d %s", replay.Code, replay.Body.String())
			}
			if moved := npmDistTagRequest(t, handler, http.MethodPut, tagsPath+"/latest", "resolver-secret", `"1.1.0"`); moved.Code != http.StatusOK {
				t.Fatalf("move latest=%d %s", moved.Code, moved.Body.String())
			}

			packument := npmDistTagRequest(t, handler, http.MethodGet, "/npm/"+repo.Name+"/"+strings.Replace(packageName, "/", "%2F", 1), "resolver-secret", "")
			var document struct {
				DistTags map[string]string `json:"dist-tags"`
			}
			if err = json.Unmarshal(packument.Body.Bytes(), &document); err != nil || document.DistTags["latest"] != "1.1.0" || document.DistTags["beta"] != "1.1.0" {
				t.Fatalf("packument tags=%v err=%v", document.DistTags, err)
			}

			removed := npmDistTagRequest(t, handler, http.MethodDelete, tagsPath+"/beta", "resolver-secret", "")
			if removed.Code != http.StatusOK || strings.Contains(removed.Body.String(), `"beta"`) {
				t.Fatalf("remove=%d %s", removed.Code, removed.Body.String())
			}
			tags = decodeNPMDistTags(t, npmDistTagRequest(t, handler, http.MethodGet, tagsPath, "resolver-secret", ""))
			if len(tags) != 2 || tags["latest"] != "1.1.0" || tags["next"] != "1.1.0" {
				t.Fatalf("final tags=%v", tags)
			}

			// Tag changes never touch the version's bytes or identity.
			after, err := store.GetNPMVersion(context.Background(), repo.ID, packageName, "1.0.0")
			if err != nil || after.Digest != before.Digest || after.Integrity != before.Integrity || after.ObjectKey != before.ObjectKey || after.Size != before.Size {
				t.Fatalf("version identity changed: before=%#v after=%#v err=%v", before, after, err)
			}
			download := npmDistTagRequest(t, handler, http.MethodGet, "/npm/"+repo.Name+"/"+packageName+"/-/"+before.TarballName, "resolver-secret", "")
			if download.Code != http.StatusOK || !bytes.Equal(download.Body.Bytes(), first) {
				t.Fatalf("download=%d bytes=%d", download.Code, download.Body.Len())
			}

			var sets, deletes int
			for _, audit := range store.Audits {
				if audit.Resource != packageName || audit.Representation != "dist-tag" {
					continue
				}
				if audit.Actor != "build-agent" || audit.Repository != repo.Name || audit.Outcome != repository.AuditResolved || audit.Status != http.StatusOK {
					t.Fatalf("dist-tag audit=%#v", audit)
				}
				switch audit.Operation {
				case npmDistTagSetOperation:
					sets++
				case npmDistTagDeleteOperation:
					deletes++
					if audit.Evidence["distTag"] != "beta" || audit.Evidence["previous"] != "1.1.0" {
						t.Fatalf("delete audit evidence=%#v", audit.Evidence)
					}
				}
			}
			if sets != 3 || deletes != 1 {
				t.Fatalf("dist-tag audits sets=%d deletes=%d all=%#v", sets, deletes, store.Audits)
			}
		})
	}
}

func TestNativeNPMDistTagsRefuseInvalidTargetsAndCallers(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "npm-tag-guards", Format: repository.FormatNPM, AnonymousRead: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceAnonymousAccessPolicy(ctx, repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeNPMObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator())
	publishNPMDistTagHTTPFixture(t, handler, repo.Name, "widget", "1.0.0", map[string]string{"latest": "1.0.0"})
	publishNPMDistTagHTTPFixture(t, handler, repo.Name, "widget", "2.0.0", map[string]string{"next": "2.0.0"})
	quarantined := publishNPMDistTagHTTPFixture(t, handler, repo.Name, "widget", "3.0.0", map[string]string{"rc": "3.0.0"})
	if _, err = store.TombstoneNPMVersion(ctx, repo.ID, "widget", "2.0.0"); err != nil {
		t.Fatal(err)
	}
	enableQuarantineReadPolicy(t, store, repo.ID)
	quarantineReadIdentity(t, store, repo, "widget@3.0.0", testScanDigest(string(quarantined)))
	tagsPath := "/npm/" + repo.Name + "/-/package/widget/dist-tags"
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{
		{Principal: "build-agent", Scopes: []string{"repositories:read", "repositories:write"}},
		{Principal: "reader", Scopes: []string{"repositories:read"}},
	}, "1"); err != nil {
		t.Fatal(err)
	}
	readerToken := testAuthenticator().IssueToken("reader")
	if tags := decodeNPMDistTags(t, npmDistTagRequest(t, handler, http.MethodGet, tagsPath, readerToken, "")); tags["latest"] != "1.0.0" {
		t.Fatalf("reader tags=%v", tags)
	}

	for _, tc := range []struct {
		name, method, tag, token, body string
		status                         int
	}{
		{"unknown version", http.MethodPut, "beta", "resolver-secret", `"9.9.9"`, http.StatusNotFound},
		{"tombstoned version", http.MethodPut, "beta", "resolver-secret", `"2.0.0"`, http.StatusNotFound},
		{"quarantined version", http.MethodPut, "beta", "resolver-secret", `"3.0.0"`, http.StatusConflict},
		{"range target", http.MethodPut, "beta", "resolver-secret", `"^1.0.0"`, http.StatusBadRequest},
		{"non-string body", http.MethodPut, "beta", "resolver-secret", `{"version":"1.0.0"}`, http.StatusBadRequest},
		{"missing tag", http.MethodDelete, "beta", "resolver-secret", "", http.StatusNotFound},
		{"latest removal", http.MethodDelete, "latest", "resolver-secret", "", http.StatusBadRequest},
		{"anonymous add", http.MethodPut, "beta", "", `"1.0.0"`, http.StatusUnauthorized},
		{"anonymous remove", http.MethodDelete, "latest", "", "", http.StatusUnauthorized},
		{"read-only add", http.MethodPut, "beta", readerToken, `"1.0.0"`, http.StatusForbidden},
		{"read-only remove", http.MethodDelete, "rc", readerToken, "", http.StatusForbidden},
		{"list one tag", http.MethodGet, "latest", "resolver-secret", "", http.StatusMethodNotAllowed},
	} {
		response := npmDistTagRequest(t, handler, tc.method, tagsPath+"/"+tc.tag, tc.token, tc.body)
		if response.Code != tc.status {
			t.Fatalf("%s=%d %s", tc.name, response.Code, response.Body.String())
		}
	}
	if response := npmDistTagRequest(t, handler, http.MethodPut, tagsPath+"/1.0.0", "resolver-secret", `"1.0.0"`); response.Code == http.StatusOK {
		t.Fatalf("version-like tag was accepted: %s", response.Body.String())
	}
	if response := npmDistTagRequest(t, handler, http.MethodPut, "/npm/"+repo.Name+"/-/package/missing/dist-tags/beta", "resolver-secret", `"1.0.0"`); response.Code != http.StatusNotFound {
		t.Fatalf("missing package=%d %s", response.Code, response.Body.String())
	}

	// Anonymous reads follow the repository's anonymous policy, and a tag on a
	// quarantined version is hidden like the version itself.
	tags := decodeNPMDistTags(t, npmDistTagRequest(t, handler, http.MethodGet, tagsPath, "", ""))
	if len(tags) != 1 || tags["latest"] != "1.0.0" {
		t.Fatalf("anonymous tags=%v", tags)
	}
	pkg, err := store.GetNPMPackage(ctx, repo.ID, "widget")
	if err != nil || pkg.DistTags["beta"] != "" || pkg.DistTags["latest"] != "1.0.0" {
		t.Fatalf("refused changes reached the store: %#v err=%v", pkg.DistTags, err)
	}
	var refused int
	for _, audit := range store.Audits {
		if audit.Representation == "dist-tag" && audit.Outcome != repository.AuditResolved {
			refused++
		}
	}
	// unknown, tombstoned, quarantined, missing tag, and missing package.
	if refused != 5 {
		t.Fatalf("refused dist-tag audits=%d all=%#v", refused, store.Audits)
	}
}

func TestNativeNPMDistTagsPreserveOrdinaryVersionLikeNames(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "npm-ordinary-tags", Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeNPMObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator())
	publishNPMDistTagHTTPFixture(t, handler, repo.Name, "widget", "1.0.0", map[string]string{"latest": "1.0.0"})
	path := "/npm/" + repo.Name + "/-/package/widget/dist-tags"
	for _, tag := range []string{"2026-release", "2026-10-16", "1-canary", "v1-release", "1.2-beta", "V2.0.0"} {
		if response := npmDistTagRequest(t, handler, http.MethodPut, path+"/"+tag, "resolver-secret", `"1.0.0"`); response.Code != http.StatusOK {
			t.Fatalf("add ordinary tag %q=%d %s", tag, response.Code, response.Body.String())
		}
		if tags := decodeNPMDistTags(t, npmDistTagRequest(t, handler, http.MethodGet, path, "resolver-secret", "")); tags[tag] != "1.0.0" {
			t.Fatalf("ordinary tag %q missing: %v", tag, tags)
		}
		if response := npmDistTagRequest(t, handler, http.MethodDelete, path+"/"+tag, "resolver-secret", ""); response.Code != http.StatusOK {
			t.Fatalf("remove ordinary tag %q=%d %s", tag, response.Code, response.Body.String())
		}
	}
}

func TestNativeNPMGroupDistTagWritesRequirePackageWritePermission(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "npm-tag-permissions", Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeNPMObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator())
	publishNPMDistTagHTTPFixture(t, handler, repo.Name, "widget", "1.0.0", map[string]string{"latest": "1.0.0", "beta": "1.0.0"})
	createV2Group(t, store, "npm-tag-permissions-group", repository.FormatNPM, repository.GroupMember{RepositoryID: repo.ID, Position: 0})
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{
		{Principal: "tag-reader", Scopes: []string{"repositories:read"}},
		{Principal: "tag-writer", Scopes: []string{"repositories:read", "repositories:write"}, ResourcePrefix: "widget"},
		{Principal: "other-writer", Scopes: []string{"repositories:read", "repositories:write"}, ResourcePrefix: "other"},
	}, "1"); err != nil {
		t.Fatal(err)
	}
	path := "/npm/npm-tag-permissions-group/-/package/widget/dist-tags"
	reader := testAuthenticator().IssueToken("tag-reader")
	if tags := decodeNPMDistTags(t, npmDistTagRequest(t, handler, http.MethodGet, path, reader, "")); tags["beta"] != "1.0.0" {
		t.Fatalf("reader tags=%v", tags)
	}
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		for _, tc := range []struct {
			actor  string
			status int
		}{{"tag-reader", http.StatusForbidden}, {"other-writer", http.StatusForbidden}, {"tag-writer", http.StatusMethodNotAllowed}} {
			response := npmDistTagRequest(t, handler, method, path+"/beta", testAuthenticator().IssueToken(tc.actor), `"1.0.0"`)
			if response.Code != tc.status {
				t.Errorf("%s as %s=%d, want %d; body=%s", method, tc.actor, response.Code, tc.status, response.Body.String())
			}
		}
	}
	if tags := decodeNPMDistTags(t, npmDistTagRequest(t, handler, http.MethodGet, path, reader, "")); tags["beta"] != "1.0.0" {
		t.Fatalf("refused writes changed tags=%v", tags)
	}
}

func TestNativeNPMDistTagsProxyAndGroupAreReadOnly(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	primary, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "npm-primary", Name: "npm-primary", Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	// The Proxy fixture fetches this repository without credentials.
	secondary, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "npm-secondary", Name: "npm-secondary", Format: repository.FormatNPM, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeNPMObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	publishNPMDistTagHTTPFixture(t, handler, primary.Name, "widget", "1.0.0", map[string]string{"latest": "1.0.0", "beta": "1.0.0"})
	publishNPMDistTagHTTPFixture(t, handler, secondary.Name, "widget", "2.0.0", map[string]string{"latest": "2.0.0", "beta": "2.0.0", "rc": "2.0.0"})

	upstream := httptest.NewServer(handler)
	defer upstream.Close()
	proxy, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: "npm-upstream", Name: "npm-upstream", Format: repository.FormatNPM, Type: repository.RepositoryTypeProxy,
		Endpoint: upstream.URL + "/npm/npm-secondary", AllowedHosts: []string{"127.0.0.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	createV2Group(t, store, "npm-tag-group", repository.FormatNPM,
		repository.GroupMember{RepositoryID: primary.ID, Position: 0},
		repository.GroupMember{RepositoryID: secondary.ID, Position: 1},
	)
	proxyHandler := NewGatewayHandler(Dependencies{NativeNPMObjectStore: objects}, store, TestAdapter{}, testAuthenticator(), UpstreamClient{HTTPClient: upstream.Client()})

	if _, err = store.ReplaceAnonymousAccessPolicy(ctx, repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}

	proxyTags := decodeNPMDistTags(t, npmDistTagRequest(t, proxyHandler, http.MethodGet, "/npm/"+proxy.Name+"/-/package/widget/dist-tags", "resolver-secret", ""))
	if len(proxyTags) != 3 || proxyTags["beta"] != "2.0.0" || proxyTags["rc"] != "2.0.0" {
		t.Fatalf("proxy tags=%v", proxyTags)
	}
	groupTags := decodeNPMDistTags(t, npmDistTagRequest(t, proxyHandler, http.MethodGet, "/npm/npm-tag-group/-/package/widget/dist-tags", "resolver-secret", ""))
	// The first member that defines a tag is authoritative for it.
	if len(groupTags) != 3 || groupTags["latest"] != "1.0.0" || groupTags["beta"] != "1.0.0" || groupTags["rc"] != "2.0.0" {
		t.Fatalf("group tags=%v", groupTags)
	}
	// Unsupported Group writes are tested with an actual member writer;
	// readers receive 403 before the route's read-only 405 response.
	for _, member := range []repository.HostedRepository{primary, secondary} {
		if _, err = store.ReplaceRepositoryGrants(ctx, member.ID, []repository.RepositoryGrant{{Principal: "build-agent", Scopes: []string{"repositories:read", "repositories:write"}}}, "1"); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/npm/" + proxy.Name + "/-/package/widget/dist-tags/beta", "/npm/npm-tag-group/-/package/widget/dist-tags/beta"} {
		for _, method := range []string{http.MethodPut, http.MethodDelete} {
			body := ""
			if method == http.MethodPut {
				body = `"2.0.0"`
			}
			response := npmDistTagRequest(t, proxyHandler, method, path, "resolver-secret", body)
			if response.Code != http.StatusMethodNotAllowed {
				t.Fatalf("%s %s=%d %s", method, path, response.Code, response.Body.String())
			}
		}
	}

	// Moving the authoritative member's tag changes the Group answer at once;
	// Hosted packuments are read through without a metadata cache.
	if moved := npmDistTagRequest(t, handler, http.MethodPut, "/npm/npm-primary/-/package/widget/dist-tags/beta", "resolver-secret", `"1.0.0"`); moved.Code != http.StatusOK {
		t.Fatalf("primary replay=%d", moved.Code)
	}
	if removed := npmDistTagRequest(t, handler, http.MethodDelete, "/npm/npm-primary/-/package/widget/dist-tags/beta", "resolver-secret", ""); removed.Code != http.StatusOK {
		t.Fatalf("primary remove=%d %s", removed.Code, removed.Body.String())
	}
	groupTags = decodeNPMDistTags(t, npmDistTagRequest(t, proxyHandler, http.MethodGet, "/npm/npm-tag-group/-/package/widget/dist-tags", "resolver-secret", ""))
	if groupTags["beta"] != "2.0.0" || groupTags["latest"] != "1.0.0" {
		t.Fatalf("group tags after primary removal=%v", groupTags)
	}
	pkg, err := store.GetNPMPackage(ctx, secondary.ID, "widget")
	if err != nil || pkg.DistTags["beta"] != "2.0.0" {
		t.Fatalf("refused writes reached a member: %#v err=%v", pkg.DistTags, err)
	}
}

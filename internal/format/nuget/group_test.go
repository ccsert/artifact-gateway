package nuget

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/secrets"
	"github.com/ccsert/artifact-gateway/internal/upstreamauth"
	"github.com/google/uuid"
)

func hostedPackage(t *testing.T, f fixture, repo repository.HostedRepository, body []byte) {
	t.Helper()
	payload, contentType := multipartPackage(t, body)
	r := httptest.NewRequest("PUT", "https://gateway.test/nuget/"+repo.Name+"/api/v2/package/", bytes.NewReader(payload))
	r.Header.Set("Content-Type", contentType)
	r.Header.Set("X-NuGet-ApiKey", "nuget-test-admin")
	w := httptest.NewRecorder()
	f.module.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("hosted seed: %d %s", w.Code, w.Body.String())
	}
}

func TestNuGetGroupExplicitRevalidationPreservesIdentityAfterCredentialRotation(t *testing.T) {
	t.Setenv(secrets.KeyEnv, strings.Repeat("64", 32))
	body := packageFixture(t, "Acme.Rotate", "1.0.0", "")
	feed := newTestFeed(t, body)
	f, proxy := proxyFixture(t, feed)
	sealed, err := secrets.Seal(upstreamauth.SecretPurpose, "blue-source")
	if err != nil {
		t.Fatal(err)
	}
	proxy.UpstreamAuth = &repository.UpstreamAuth{Scheme: repository.UpstreamAuthSchemeBasic, Username: "upstream", Secret: sealed}
	proxy, err = f.store.UpdateHostedRepository(t.Context(), proxy, proxy.Version)
	if err != nil {
		t.Fatal(err)
	}
	feed.authorization = "Basic " + base64.StdEncoding.EncodeToString([]byte("upstream:blue-source"))
	group := testNuGetGroup(t, f, proxy, f.repo)
	path := group.Name + "/v3/flat/acme.rotate/1.0.0/acme.rotate.1.0.0.nupkg"
	w := nativeGet(t, f.module, path)
	if w.Code != 200 {
		t.Fatalf("cold authority: %d %s", w.Code, w.Body.String())
	}
	priorVersion := proxy.Version
	sealed, err = secrets.Seal(upstreamauth.SecretPurpose, "green-source")
	if err != nil {
		t.Fatal(err)
	}
	proxy.UpstreamAuth = &repository.UpstreamAuth{Scheme: repository.UpstreamAuthSchemeBearer, Secret: sealed}
	proxy, err = f.store.UpdateHostedRepository(t.Context(), proxy, proxy.Version)
	if err != nil {
		t.Fatal(err)
	}
	feed.mu.Lock()
	feed.authorization = "Bearer green-source"
	feed.mu.Unlock()
	w = nativeGet(t, f.module, path)
	if w.Code != 409 {
		t.Fatalf("implicit source rebind: %d %s", w.Code, w.Body.String())
	}
	revalidate := func(prior string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://gateway.test/api/v2/previews/nuget/groups/"+group.ID+"/sources/acme.rotate/1.0.0/revalidate", strings.NewReader(fmt.Sprintf(`{"groupVersion":%q,"repositoryVersion":%q}`, group.Version, prior)))
		r.Header.Set("Authorization", "Bearer nuget-test-admin")
		w := httptest.NewRecorder()
		f.module.Preview().ServeHTTP(w, r)
		return w
	}
	for range 2 {
		w = revalidate(priorVersion)
		if w.Code != 200 {
			t.Fatalf("explicit same-owner revalidation/replay: %d %s", w.Code, w.Body.String())
		}
	}
	w = nativeGet(t, f.module, path)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) {
		t.Fatalf("revalidated bytes: %d %s", w.Code, w.Body.String())
	}
	priorVersion = proxy.Version
	proxy, err = f.store.UpdateHostedRepository(t.Context(), proxy, proxy.Version)
	if err != nil {
		t.Fatal(err)
	}
	feed.mu.Lock()
	feed.packages["acme.rotate@1.0.0"] = packageFixture(t, "Acme.Rotate", "1.0.0", `<dependencies><dependency id="Unexpected" version="1.0.0"/></dependencies>`)
	feed.mu.Unlock()
	w = revalidate(priorVersion)
	if w.Code != 409 {
		t.Fatalf("explicit revalidation replaced immutable bytes: %d %s", w.Code, w.Body.String())
	}
	owner, err := f.store.GetNuGetGroupAuthority(t.Context(), group.ID, "acme.rotate", "1.0.0")
	if err != nil || owner.RepositoryVersion != priorVersion {
		t.Fatalf("failed revalidation altered authority: %+v %v", owner, err)
	}
}
func testNuGetGroup(t *testing.T, f fixture, members ...repository.HostedRepository) repository.HostedGroup {
	t.Helper()
	group := repository.HostedGroup{ID: uuid.NewString(), Name: "nuget-ordered", Format: repository.FormatNuGet}
	for i, member := range members {
		group.Members = append(group.Members, repository.GroupMember{RepositoryID: member.ID, Position: i})
	}
	created, _, err := f.store.CreateHostedGroupIdempotently(t.Context(), group, "operator", uuid.NewString(), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	return created
}
func TestNuGetGroupCanonicalAuthoritySurvivesReorderAndMissingOwner(t *testing.T) {
	body := packageFixture(t, "Acme.Shared", "1.0.0", "")
	public := packageFixture(t, "ACME.Shared", "1.0.0", `<dependencies><dependency id="Public.Unexpected" version="1.0.0"/></dependencies>`)
	next := packageFixture(t, "Acme.Shared", "2.0.0", "")
	feed := newTestFeed(t, public, next)
	f, proxy := proxyFixture(t, feed)
	hostedPackage(t, f, f.repo, body)
	group := testNuGetGroup(t, f, f.repo, proxy)
	path := group.Name + "/v3/flat/acme.shared/1.0.0/acme.shared.1.0.0.nupkg"
	w := nativeGet(t, f.module, path)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) || w.Header().Get("X-NuGet-Source-Repository") != f.repo.ID {
		t.Fatalf("private first authority: %d %s", w.Code, w.Body.String())
	}
	w = nativeGet(t, f.module, group.Name+"/v3/flat/acme.shared/2.0.0/acme.shared.2.0.0.nupkg")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), next) || w.Header().Get("X-NuGet-Source-Repository") != proxy.ID {
		t.Fatalf("different version owner: %d %s", w.Code, w.Body.String())
	}
	group, err := f.store.ReplaceHostedGroupMembers(t.Context(), group.ID, []repository.GroupMember{{RepositoryID: proxy.ID, Position: 0}, {RepositoryID: f.repo.ID, Position: 1}}, group.Version)
	if err != nil {
		t.Fatal(err)
	}
	w = nativeGet(t, f.module, path)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) {
		t.Fatalf("reorder replaced authority: %d %s", w.Code, w.Body.String())
	}
	w = nativeGet(t, f.module, group.Name+"/v3/registration/acme.shared/1.0.0.json")
	if w.Code != 200 {
		t.Fatalf("registration authority: %d %s", w.Code, w.Body.String())
	}
	reader, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	serialized, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	var registration any
	if json.Unmarshal(serialized, &registration) != nil {
		t.Fatal("invalid registration")
	}
	if strings.Contains(string(serialized), "Public.Unexpected") || strings.Contains(string(serialized), feed.server.URL) || !strings.Contains(string(serialized), "/nuget/"+group.Name+"/v3/flat/") {
		t.Fatalf("registration lost source binding: %s", serialized)
	}
	p, err := f.store.GetNuGetPublication(t.Context(), f.repo.ID, "acme.shared", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.objects.Delete(t.Context(), p.ObjectKey); err != nil {
		t.Fatal(err)
	}
	w = nativeGet(t, f.module, path)
	if w.Code == 200 {
		t.Fatal("missing private bytes switched to public same identity")
	}
	if _, err = f.store.ReplaceHostedGroupMembers(t.Context(), group.ID, []repository.GroupMember{{RepositoryID: proxy.ID, Position: 0}}, group.Version); err != nil {
		t.Fatal(err)
	}
	w = nativeGet(t, f.module, path)
	if w.Code == 200 {
		t.Fatal("removed owner switched to another member")
	}
}
func TestNuGetGroupResourceAuthorizationBlocksCachedAndUncachedFallback(t *testing.T) {
	feed := newTestFeed(t, packageFixture(t, "Acme.Shared", "1.0.0", ""))
	f, proxy := proxyFixture(t, feed)
	body := packageFixture(t, "Acme.Shared", "1.0.0", "")
	hostedPackage(t, f, f.repo, body)
	group := testNuGetGroup(t, f, f.repo, proxy)
	path := group.Name + "/v3/flat/acme.shared/1.0.0/acme.shared.1.0.0.nupkg"
	// A valid client has Proxy reads but no authority over the Hosted
	// owner. Empty grant sets are managed explicitly rather than token failures.
	for _, id := range []string{proxy.ID} {
		if _, err := f.store.ReplaceRepositoryGrants(context.Background(), id, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}, ResourcePrefix: "acme."}}, "1"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.store.ReplaceRepositoryGrants(t.Context(), f.repo.ID, []repository.RepositoryGrant{}, "1"); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "https://gateway.test/nuget/"+path, nil)
	r.Header.Set("Authorization", "Bearer nuget-test-reader")
	w := httptest.NewRecorder()
	f.module.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("B-only caller crossed the first member: %d %s", w.Code, w.Body.String())
	}
	if _, err := f.store.GetNuGetGroupAuthority(t.Context(), group.ID, "acme.shared", "1.0.0"); !errors.Is(err, repository.ErrNotFound) || len(feed.requests) != 0 {
		t.Fatalf("denied caller created a global source authority: %v, requests=%d", err, len(feed.requests))
	}
	if w := nativeGet(t, f.module, path); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) || w.Header().Get("X-NuGet-Source-Repository") != f.repo.ID {
		t.Fatalf("B-only request polluted the admin authority: %d %s", w.Code, w.Body.String())
	}
	for _, version := range []string{"1.0.0", "2.0.0"} {
		r := httptest.NewRequest("GET", "https://gateway.test/nuget/"+group.Name+"/v3/flat/acme.shared/"+version+"/acme.shared."+version+".nupkg", nil)
		r.Header.Set("Authorization", "Bearer nuget-test-reader")
		w := httptest.NewRecorder()
		f.module.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("denied owner fallback %s: %d %s", version, w.Code, w.Body.String())
		}
	}
}

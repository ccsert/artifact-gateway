package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestContainsArtifactSearchFindsPathFragment(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "contains-raw", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: repo.ID, Path: "releases/alpha.bin", Digest: "sha256:" + strings.Repeat("a", 64), ObjectKey: "contains/alpha", Size: 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "contains-reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	for name, path := range map[string]string{
		"global":     "/api/v2/artifact-search?q=ALPHA&match=contains",
		"repository": "/api/v2/repositories/" + repo.ID + "/artifact-search?q=ALPHA&match=contains",
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			authorize(request, authenticator.IssueToken("contains-reader"))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			var page struct {
				Items []struct {
					Coordinate string `json:"coordinate"`
				} `json:"items"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].Coordinate != "releases/alpha.bin" {
				t.Fatalf("contains fragment search=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func verifyContainsSearchBoundaries(t *testing.T, store GatewayStore) {
	t.Helper()
	ctx := context.Background()
	actor := "boundary-reader-" + uuid.NewString()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "boundary-" + uuid.NewString(), Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("b", 64)
	paths := []string{"releases/alpha-a.bin", "releases/alpha-b_100%25.bin", "releases/other.bin", "releases/%E4%B8%AD%E6%96%87.txt"}
	for _, path := range paths {
		if _, err = store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: repo.ID, Path: path, Digest: digest, ObjectKey: "boundary/" + repo.ID, Size: 7}); err != nil {
			t.Fatal(err)
		}
	}
	grants, err := store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{
		{Principal: actor, Scopes: []string{"repositories:read"}},
		{Principal: actor + "-scoped", Scopes: []string{"repositories:read"}, ResourcePrefix: "releases/"},
	}, "1")
	if err != nil {
		t.Fatal(err)
	}
	private, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "private-boundary-" + uuid.NewString(), Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: private.ID, Path: "releases/private-alpha-secret.bin", Digest: digest, ObjectKey: "boundary/" + repo.ID, Size: 7}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, private.ID, []repository.RepositoryGrant{{Principal: actor + "-other", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	auth := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, auth)
	call := func(t *testing.T, endpoint, subject string, values url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("GET", endpoint+"?"+values.Encode(), nil)
		if subject != "" {
			if subject == "pending" {
				authorize(req, auth.IssuePrincipalToken(Principal{Actor: subject, Role: RoleNone}))
			} else {
				authorize(req, auth.IssueToken(subject))
			}
		}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if strings.Contains(res.Body.String(), private.Name) || strings.Contains(res.Body.String(), "private-alpha-secret") {
			t.Fatalf("denied repository leaked: %s", res.Body.String())
		}
		return res
	}
	type page struct {
		Items []struct {
			Coordinate string `json:"coordinate"`
			MatchKind  string `json:"matchKind"`
			Match      string `json:"match"`
		} `json:"items"`
		Next string `json:"nextPageToken"`
	}
	read := func(t *testing.T, res *httptest.ResponseRecorder, want int) page {
		t.Helper()
		if res.Code != want {
			t.Fatalf("status=%d want=%d body=%s", res.Code, want, res.Body.String())
		}
		var v page
		if want == 200 && json.Unmarshal(res.Body.Bytes(), &v) != nil {
			t.Fatalf("bad page %s", res.Body.String())
		}
		return v
	}
	for _, endpoint := range []string{"/api/v2/artifact-search", "/api/v2/repositories/" + repo.ID + "/artifact-search"} {
		global := endpoint == "/api/v2/artifact-search"
		t.Run(endpoint, func(t *testing.T) {
			cases := []struct {
				q, wire string
				want    []string
			}{
				{"ALPHA", "ALPHA", paths[:2]},
				{"_", "_", paths[1:2]},
				{"100%", "100%25", paths[1:2]},
				{"ph", "ph", paths[:2]},
				{"x", "x", paths[3:]},
				{"中文", "%E4%B8%AD%E6%96%87", paths[3:]},
				{`\`, "%5C", nil},
			}
			for _, tc := range cases {
				keyword := tc.wire
				if global {
					keyword = tc.q
				}
				result := read(t, call(t, endpoint, actor, url.Values{"q": {keyword}, "match": {"contains"}, "pageSize": {"200"}, "format": {"raw"}}), 200)
				got := make([]string, 0, len(result.Items))
				for _, item := range result.Items {
					got = append(got, item.Coordinate)
					if global && item.Match != "contains" {
						t.Fatalf("wrong match=%s", item.Match)
					}
				}
				if strings.Join(got, "|") != strings.Join(tc.want, "|") {
					t.Fatalf("literal %q got=%v want=%v", tc.q, got, tc.want)
				}
			}
			for _, matching := range []string{"", "prefix"} {
				values := url.Values{"q": {"alpha"}, "format": {"raw"}}
				if matching != "" {
					values.Set("match", matching)
				}
				if result := read(t, call(t, endpoint, actor, values), 200); len(result.Items) != 0 {
					t.Fatalf("legacy prefix changed: %+v", result)
				}
			}
			for _, bad := range []string{"alpha\x00", "\nalpha", strings.Repeat("中", 4097)} {
				read(t, call(t, endpoint, actor, url.Values{"q": {bad}, "match": {"contains"}}), 400)
			}
			read(t, call(t, endpoint, actor, url.Values{"q": {"alpha"}, "match": {"fuzzy"}}), 400)
			// New-mode tokens are bound to their exact query and mode.
			values := url.Values{"q": {"alpha"}, "match": {"contains"}, "pageSize": {"1"}, "format": {"raw"}}
			first := read(t, call(t, endpoint, actor, values), 200)
			if len(first.Items) != 1 || first.Items[0].Coordinate != paths[0] || first.Next == "" {
				t.Fatalf("first=%+v", first)
			}
			values.Set("pageToken", first.Next)
			second := read(t, call(t, endpoint, actor, values), 200)
			if len(second.Items) != 1 || second.Items[0].Coordinate != paths[1] || second.Next != "" {
				t.Fatalf("next=%+v", second)
			}
			values.Set("q", "other")
			read(t, call(t, endpoint, actor, values), 400)
			values.Set("q", "alpha")
			values.Set("match", "prefix")
			read(t, call(t, endpoint, actor, values), 400)
			// Signed cursors created using the old field set remain usable when match is omitted.
			legacy := struct {
				Endpoint, Query, Mode, Format, RepositoryID, Coordinate, Digest string
				BuildNumber                                                     int
				ExpiresAt                                                       int64
			}{
				Endpoint: "artifact-search", Query: "releases/", Mode: "coordinate", Format: "raw", RepositoryID: repo.ID, Coordinate: paths[0], ExpiresAt: time.Now().Add(time.Minute).Unix(),
			}
			if global {
				legacy.Endpoint = "global-artifact-search"
			}
			values = url.Values{"q": {"releases/"}, "format": {"raw"}, "pageSize": {"1"}, "pageToken": {encodeSignedCursor(auth.AdminToken, legacy)}}
			legacyPage := read(t, call(t, endpoint, actor, values), 200)
			if len(legacyPage.Items) != 1 || legacyPage.Items[0].Coordinate != paths[1] {
				t.Fatalf("legacy cursor=%+v", legacyPage)
			}
			values.Set("pageToken", "")
			values.Set("q", "alpha")
			values.Set("match", "contains")
			for _, subject := range []string{actor + "-denied", actor + "-scoped"} {
				if global {
					if result := read(t, call(t, endpoint, subject, values), 200); len(result.Items) != 0 {
						t.Fatalf("unauthorized results=%+v", result)
					}
				} else {
					read(t, call(t, endpoint, subject, values), 403)
				}
			}
			read(t, call(t, endpoint, "", values), 401)
			read(t, call(t, endpoint, "pending", values), 403)
			if global {
				values.Set("q", strings.TrimPrefix(digest, "sha256:"))
				values.Set("pageSize", "200")
				result := read(t, call(t, endpoint, actor, values), 200)
				if len(result.Items) != 4 {
					t.Fatalf("exact digest=%+v", result)
				}
				for _, item := range result.Items {
					if item.MatchKind != "digest" || item.Match != "" {
						t.Fatalf("digest relabeled: %+v", item)
					}
				}
			}
		})
	}
	// Anonymous repository/group reads still obey both policy and member grants.
	public, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "public-boundary-" + uuid.NewString(), Format: repository.FormatRaw, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		public.AnonymousRead = false
		if _, err := store.UpdateHostedRepository(context.Background(), public, public.Version); err != nil {
			t.Errorf("disable fixture anonymous read: %v", err)
		}
	})
	if _, err = store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: public.ID, Path: "public/alpha.bin", Digest: digest, ObjectKey: "boundary/" + repo.ID, Size: 7}); err != nil {
		t.Fatal(err)
	}
	group, _, err := store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{ID: uuid.NewString(), Name: "public-boundary-group-" + uuid.NewString(), Format: repository.FormatRaw, AnonymousRead: true, Members: []repository.GroupMember{{RepositoryID: public.ID, Position: 0}, {RepositoryID: private.ID, Position: 1}}}, "fixture", uuid.NewString(), "boundary")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := store.GetAnonymousAccessPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := store.ReplaceAnonymousAccessPolicy(ctx, repository.AnonymousAccessPolicy{Enabled: true}, policy.Version)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{public.ID, group.ID} {
		v := read(t, call(t, "/api/v2/repositories/"+id+"/artifact-search", "", url.Values{"q": {"ALPHA"}, "match": {"contains"}}), 200)
		if len(v.Items) != 1 || v.Items[0].Coordinate != "public/alpha.bin" {
			t.Fatalf("anonymous contains=%+v", v)
		}
	}
	if _, err = store.ReplaceAnonymousAccessPolicy(ctx, policy, enabled.Version); err != nil {
		t.Fatal(err)
	}
	// Revoking repository authority invalidates a continuation without leaking data.
	values := url.Values{"q": {"alpha"}, "match": {"contains"}, "pageSize": {"1"}}
	first := read(t, call(t, "/api/v2/artifact-search", actor, values), 200)
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, nil, grants.Version); err != nil {
		t.Fatal(err)
	}
	values.Set("pageToken", first.Next)
	read(t, call(t, "/api/v2/artifact-search", actor, values), 400)
	read(t, call(t, "/api/v2/repositories/"+repo.ID+"/artifact-search", actor, values), 403)
}

func TestContainsSearchLiteralPermissionAndLegacyCursorBoundaries(t *testing.T) {
	verifyContainsSearchBoundaries(t, repository.NewMemoryStore())
}

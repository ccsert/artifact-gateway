package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestRepositoryArtifactUsagePaginationAndSearch(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "usage-pages", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "usage-private", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "usage-reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 205; i++ {
		if err := store.RecordArtifactUsage(ctx, repository.ArtifactUsageStat{Repository: repo.Name, Format: "raw", Resource: fmt.Sprintf("a/%03d", i), TotalBytes: 10, LastDownloadedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	for _, stat := range []repository.ArtifactUsageStat{
		{Repository: repo.Name, Format: "raw", Resource: "literal%_\\.txt", TotalBytes: 10, LastDownloadedAt: time.Now()},
		{Repository: other.Name, Format: "raw", Resource: "private-only", TotalBytes: 999, LastDownloadedAt: time.Now()},
	} {
		if err := store.RecordArtifactUsage(ctx, stat); err != nil {
			t.Fatal(err)
		}
	}
	auth := testAuthenticator()
	auth.LegacyReadPermissive = false
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, auth)
	request := func(id, query string, authenticated bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+id+"/artifact-usage"+query, nil)
		if authenticated {
			authorize(r, auth.IssueToken("usage-reader"))
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		query string
		count int64
		paths []string
	}{
		{"?limit=2&offset=200&q=a%2F", 205, []string{"a/200", "a/201"}},
		{"?limit=2&offset=204&q=a%2F", 205, []string{"a/204"}},
		{"?limit=2&offset=999&q=a%2F", 205, nil},
		{"?limit=2&q=" + url.QueryEscape("%_\\"), 1, []string{"literal%_\\.txt"}},
		{"?q=missing", 0, nil},
	} {
		t.Run(tc.query, func(t *testing.T) {
			w := request(repo.ID, tc.query, true)
			var result struct {
				TotalCount int64                          `json:"totalCount"`
				Totals     repository.ArtifactUsageTotals `json:"totals"`
				Items      []struct {
					Resource string `json:"resource"`
				} `json:"items"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if result.TotalCount != tc.count || len(result.Items) != len(tc.paths) {
				t.Fatalf("count=%d items=%v", result.TotalCount, result.Items)
			}
			for i, path := range tc.paths {
				if result.Items[i].Resource != path {
					t.Fatalf("item=%s want=%s", result.Items[i].Resource, path)
				}
			}
			if result.Totals.Resources != 206 || result.Totals.DownloadCount != 206 || result.Totals.TotalBytes != 2060 {
				t.Fatalf("whole-repository totals=%+v", result.Totals)
			}
		})
	}
	for _, query := range []string{"?limit=0", "?limit=501", "?offset=-1", "?offset=1000001", "?offset=1.5", "?q=" + strings.Repeat("a", 513), "?q=%00", "?q=%FF"} {
		if w := request(repo.ID, query, true); w.Code != 400 {
			t.Errorf("%s: status=%d", query, w.Code)
		}
	}
	if w := request(repo.ID, "?q=a", false); w.Code != 401 {
		t.Errorf("anonymous=%d", w.Code)
	}
	if w := request(other.ID, "?q=private", true); w.Code != 403 {
		t.Errorf("other repository=%d", w.Code)
	}
}

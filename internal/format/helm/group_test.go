package helm

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func createHelmGroup(t *testing.T, f fixture, name string, members ...string) repository.HostedGroup {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"name": name, "members": members})
	w := request(t, f.module.Preview(), "POST", "/api/v2/previews/helm/groups", body, admin)
	var created struct{ ID string }
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &created) != nil {
		t.Fatalf("group=%d %s", w.Code, w.Body.String())
	}
	g, e := f.store.GetHostedGroup(t.Context(), created.ID)
	if e != nil {
		t.Fatal(e)
	}
	return g
}
func TestHelmGroupPinsSourceAcrossReorderAndFrozenIndex(t *testing.T) {
	chart := chartFixture(t, "owned", "1.2.3", "proxy first")
	feed := newHelmFeed(t, chart, nil)
	f, proxy := proxyModule(t, feed)
	hosted, e := f.store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "helm-hosted", Format: repository.FormatHelm, Type: repository.RepositoryTypeHosted})
	if e != nil {
		t.Fatal(e)
	}
	hostedFixture := f
	hostedFixture.repo = hosted
	different := chartFixture(t, "owned", "1.2.3", "hosted second")
	if w := upload(t, hostedFixture, "owned", "1.2.3", different, nil); w.Code != 201 {
		t.Fatalf("hosted=%d %s", w.Code, w.Body.String())
	}
	group := createHelmGroup(t, f, "helm-group", proxy.ID, hosted.ID)
	path := indexChartURL(t, request(t, f.module, "GET", "/helm/helm-group/index.yaml", nil, admin), "owned")
	explain := request(t, f.module.Preview(), "GET", "/api/v2/previews/helm/groups/"+group.ID+"/sources/owned/1.2.3", nil, admin)
	if explain.Code != 200 || !strings.Contains(explain.Body.String(), proxy.ID) || !strings.Contains(explain.Body.String(), `"cached":false`) {
		t.Fatalf("explain=%d %s", explain.Code, explain.Body.String())
	}
	if w := request(t, f.module, "GET", path, nil, admin); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), chart) || w.Header().Get("X-Helm-Source-Repository") != proxy.ID {
		t.Fatalf("source=%d %s", w.Code, w.Body.String())
	}
	input, _ := json.Marshal(map[string]any{"name": group.Name, "version": group.Version, "members": []string{hosted.ID, proxy.ID}})
	w := request(t, f.module.Preview(), "PUT", "/api/v2/previews/helm/groups/"+group.ID, input, admin)
	if w.Code != 200 {
		t.Fatalf("reorder=%d %s", w.Code, w.Body.String())
	}
	newPath := indexChartURL(t, request(t, f.module, "GET", "/helm/helm-group/index.yaml", nil, admin), "owned")
	for _, p := range []string{path, newPath} {
		w = request(t, f.module, "GET", p, nil, admin)
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), chart) {
			t.Fatalf("stable source=%d", w.Code)
		}
	}
	group, _ = f.store.GetHostedGroup(t.Context(), group.ID)
	input, _ = json.Marshal(map[string]any{"name": group.Name, "version": group.Version, "members": []string{hosted.ID}})
	if w = request(t, f.module.Preview(), "PUT", "/api/v2/previews/helm/groups/"+group.ID, input, admin); w.Code != 200 {
		t.Fatalf("remove=%d", w.Code)
	}
	for _, p := range []string{path, "/helm/helm-group/index.yaml"} {
		w = request(t, f.module, "GET", p, nil, admin)
		if w.Code != 409 {
			t.Fatalf("removed owner fallback=%d %s", w.Code, w.Body.String())
		}
	}
}
func TestHelmGroupMissingAndCorruptOwnerNeverFallThrough(t *testing.T) {
	for _, kind := range []string{"missing", "digest", "index-denied"} {
		t.Run(kind, func(t *testing.T) {
			chart := chartFixture(t, "owned", "1.2.3", "proxy authoritative")
			feed := newHelmFeed(t, chart, nil)
			f, proxy := proxyModule(t, feed)
			hosted, e := f.store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "hosted", Format: repository.FormatHelm, Type: repository.RepositoryTypeHosted})
			if e != nil {
				t.Fatal(e)
			}
			hf := f
			hf.repo = hosted
			if w := upload(t, hf, "owned", "1.2.3", chartFixture(t, "owned", "1.2.3", "fallback must not happen"), nil); w.Code != 201 {
				t.Fatal(w.Code)
			}
			createHelmGroup(t, f, "group", proxy.ID, hosted.ID)
			path := indexChartURL(t, request(t, f.module, "GET", "/helm/group/index.yaml", nil, admin), "owned")
			feed.mu.Lock()
			switch kind {
			case "missing":
				feed.chart = nil
			case "digest":
				feed.chart = chartFixture(t, "owned", "1.2.3", "mutated upstream")
			case "index-denied":
				feed.status = 403
			}
			feed.mu.Unlock()
			if kind == "index-denied" {
				expireHelmDocument(t, f.store, proxy)
				path = "/helm/group/index.yaml"
			}
			if w := request(t, f.module, "GET", path, nil, admin); w.Code != 502 {
				t.Fatalf("owner failure=%d %s", w.Code, w.Body.String())
			}
		})
	}
}
func TestHelmGroupRejectsNestingCyclesWritesAndMemberPermissionFallback(t *testing.T) {
	feed := newHelmFeed(t, chartFixture(t, "owned", "1.2.3", "fixed"), nil)
	f, proxy := proxyModule(t, feed)
	g := createHelmGroup(t, f, "group", proxy.ID)
	for _, ids := range [][]string{{g.ID}, {proxy.ID, proxy.ID}} {
		body, _ := json.Marshal(map[string]any{"name": "nested", "members": ids})
		w := request(t, f.module.Preview(), "POST", "/api/v2/previews/helm/groups", body, admin)
		if w.Code != 400 {
			t.Fatalf("nested/cycle=%d %s", w.Code, w.Body.String())
		}
	}
	body, _ := json.Marshal(map[string]any{"name": g.Name, "version": g.Version, "members": []string{g.ID}})
	if w := request(t, f.module.Preview(), "PUT", "/api/v2/previews/helm/groups/"+g.ID, body, admin); w.Code != 400 {
		t.Fatalf("self cycle=%d", w.Code)
	}
	if w := request(t, f.module, "PUT", "/helm/group/index.yaml", nil, admin); w.Code != 405 {
		t.Fatalf("group write=%d", w.Code)
	}
	reader := map[string]string{"Authorization": "Bearer helm-owned-reader"}
	if w := request(t, f.module, "GET", "/helm/group/index.yaml", nil, reader); w.Code != 403 {
		t.Fatalf("private group=%d %s", w.Code, w.Body.String())
	}
	if _, e := f.store.UpsertRepositoryGrant(t.Context(), proxy.ID, repository.RepositoryGrant{Principal: "reader", Scopes: []string{"repositories:read"}, ResourcePrefix: "own"}); e != nil {
		t.Fatal(e)
	}
	index := request(t, f.module, "GET", "/helm/group/index.yaml", nil, reader)
	path := indexChartURL(t, index, "owned")
	if w := request(t, f.module, "GET", path, nil, reader); w.Code != 200 {
		t.Fatalf("chart-scoped group=%d %s", w.Code, w.Body.String())
	}
	if _, e := f.store.ReplaceRepositoryGrants(t.Context(), proxy.ID, nil, "2"); e != nil {
		t.Fatal(e)
	}
	if w := request(t, f.module, "GET", path, nil, reader); w.Code != 403 {
		t.Fatalf("revoked authority=%d", w.Code)
	}
}

func TestHelmFrozenSourceAuditMatchesResponseStatusBytesAndAnonymousActor(t *testing.T) {
	chart := chartFixture(t, "owned", "1.2.3", "audited source")
	feed := newHelmFeed(t, chart, nil)
	f, proxy := proxyModule(t, feed)
	g := createHelmGroup(t, f, "audited", proxy.ID)
	path := indexChartURL(t, request(t, f.module, "GET", "/helm/audited/index.yaml", nil, admin), "owned")
	read := func(method, path string, headers map[string]string, status int, outcome repository.AuditOutcome) {
		t.Helper()
		w := request(t, f.module, method, path, nil, headers)
		if w.Code != status {
			t.Fatalf("response=%d %s", w.Code, w.Body.String())
		}
		rows, e := f.store.ListAudits(t.Context(), repository.AuditQuery{Operation: "helm.source.resolve", Repository: g.Name, Limit: 1})
		if e != nil || len(rows) != 1 || rows[0].Status != status || rows[0].Bytes != int64(w.Body.Len()) || rows[0].Outcome != outcome {
			t.Fatalf("audit=%+v %v response=%d/%d", rows, e, status, w.Body.Len())
		}
	}
	read("GET", path, admin, 200, repository.AuditResolved)
	read("GET", path+".prov", admin, 404, repository.AuditNotFound)
	read("GET", path, map[string]string{"Authorization": admin["Authorization"], "Range": "bytes=0-9"}, 206, repository.AuditResolved)
	read("GET", path, map[string]string{"Authorization": admin["Authorization"], "If-None-Match": `"` + digestFor(chart) + `"`}, 304, repository.AuditResolved)
	read("GET", path, map[string]string{"Authorization": admin["Authorization"], "Range": "bytes=999999-"}, 416, repository.AuditProxyDenied)
	read("GET", path, map[string]string{"Authorization": admin["Authorization"], "If-Match": `"other"`}, 412, repository.AuditProxyDenied)
	p, e := f.store.GetHelmPublication(t.Context(), proxy.ID, "owned", "1.2.3")
	if e != nil {
		t.Fatal(e)
	}
	if e = f.objects.Put(t.Context(), p.ObjectKey, []byte("corrupt")); e != nil {
		t.Fatal(e)
	}
	read("GET", path, admin, 503, repository.AuditStorageError)
	if e = f.objects.PutVerifiedReader(t.Context(), p.ObjectKey, bytes.NewReader(chart), int64(len(chart)), p.Digest); e != nil {
		t.Fatal(e)
	}
	proxy.AnonymousRead = true
	proxy, e = f.store.UpdateHostedRepository(t.Context(), proxy, proxy.Version)
	if e != nil {
		t.Fatal(e)
	}
	g.AnonymousRead = true
	g, e = f.store.ReplaceHostedGroup(t.Context(), g, g.Version)
	if e != nil {
		t.Fatal(e)
	}
	// A new view explicitly binds the rotated source version for this anonymous case.
	anonymous := createHelmGroup(t, f, "anonymous", proxy.ID)
	anonymous.AnonymousRead = true
	anonymous, e = f.store.ReplaceHostedGroup(t.Context(), anonymous, anonymous.Version)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.store.ReplaceAnonymousAccessPolicy(t.Context(), repository.AnonymousAccessPolicy{Enabled: true}, "1"); e != nil {
		t.Fatal(e)
	}
	anonymousPath := indexChartURL(t, request(t, f.module, "GET", "/helm/anonymous/index.yaml", nil, nil), "owned")
	w := request(t, f.module, "GET", anonymousPath, nil, nil)
	if w.Code != 200 {
		t.Fatalf("anonymous=%d %s", w.Code, w.Body.String())
	}
	rows, e := f.store.ListAudits(t.Context(), repository.AuditQuery{Operation: "helm.source.resolve", Repository: anonymous.Name, Limit: 1})
	if e != nil || len(rows) != 1 || rows[0].Actor != "anonymous" || rows[0].Status != 200 || rows[0].Bytes != int64(len(chart)) {
		t.Fatalf("anonymous audit=%+v %v", rows, e)
	}
}

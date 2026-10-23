package helm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	helmprotocol "github.com/ccsert/artifact-gateway/internal/protocol/helm"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func governancePath(repo repository.HostedRepository, name, version string) string {
	return "/api/v2/previews/helm/repositories/" + repo.ID + "/charts/" + name + "/" + version + "/governance"
}

func governanceRequest(t *testing.T, f fixture, name, version, action, expectedVersion string, headers map[string]string, want int) previewGovernance {
	t.Helper()
	p, err := f.module.store.GetHelmPublication(t.Context(), f.repo.ID, name, version)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"expectedDigest": p.Digest, "expectedGovernanceVersion": expectedVersion, "action": action, "reason": "Owned public HTTP lifecycle decision"})
	w := request(t, f.module.Preview(), "POST", governancePath(f.repo, name, version), body, headers)
	if w.Code != want {
		t.Fatalf("%s governance=%d want %d %s", action, w.Code, want, w.Body.String())
	}
	var out previewGovernance
	if want == 200 && json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatal("invalid governance response")
	}
	return out
}

func policyRequest(t *testing.T, f fixture, version string, enabled bool, want int) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"expectedVersion": version, "enabled": enabled})
	w := request(t, f.module.Preview(), "PUT", "/api/v2/previews/helm/repositories/"+f.repo.ID+"/quarantine-read-policy", body, admin)
	if w.Code != want {
		t.Fatalf("read policy=%d want %d %s", w.Code, want, w.Body.String())
	}
	var out struct{ Version string }
	if want == 200 && json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatal("invalid read policy response")
	}
	return out.Version
}

func TestHelmGovernanceHTTPHidesIndexAssetsBrowseAndRetainsClosure(t *testing.T) {
	f := newFixture(t)
	chart := chartFixture(t, "owned", "1.2.3", "retained original")
	prov := provenanceFixture(t, chart)
	if w := upload(t, f, "owned", "1.2.3", chart, prov); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	p, err := f.store.GetHelmPublication(t.Context(), f.repo.ID, "owned", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	govPath := governancePath(f.repo, "owned", "1.2.3")
	for _, tc := range []struct {
		headers map[string]string
		want    int
	}{{nil, 401}, {map[string]string{"Authorization": "Bearer helm-owned-reader"}, 403}} {
		if w := request(t, f.module.Preview(), "GET", govPath, nil, tc.headers); w.Code != tc.want {
			t.Fatalf("governance authorization=%d", w.Code)
		}
		governanceRequest(t, f, "owned", "1.2.3", "delete", "1", tc.headers, tc.want)
	}
	bad, _ := json.Marshal(map[string]string{"expectedDigest": "sha256:" + strings.Repeat("0", 64), "expectedGovernanceVersion": "1", "action": "delete", "reason": "wrong digest"})
	if w := request(t, f.module.Preview(), "POST", govPath, bad, admin); w.Code != 409 {
		t.Fatalf("digest CAS=%d", w.Code)
	}
	indexPath := "/helm/helm-test/index.yaml"
	index := request(t, f.module, "GET", indexPath, nil, admin)
	state := governanceRequest(t, f, "owned", "1.2.3", "delete", "1", admin, 200)
	if !state.Tombstoned || state.GovernanceVersion != "2" || state.UpdatedBy != "operator" || state.Reason == "" || state.UpdatedAt == nil {
		t.Fatalf("tombstone=%+v", state)
	}
	governanceRequest(t, f, "owned", "1.2.3", "delete", "1", admin, 409)
	if same := governanceRequest(t, f, "owned", "1.2.3", "delete", "2", admin, 200); same != state {
		// Time pointers compare identity; compare JSON for a true wire replay.
		a, _ := json.Marshal(same)
		b, _ := json.Marshal(state)
		if !bytes.Equal(a, b) {
			t.Fatalf("no-op changed receipt=%s %s", a, b)
		}
	}
	assertHidden := func() {
		t.Helper()
		for _, path := range []string{assetPath(f.repo, p, false), assetPath(f.repo, p, true)} {
			for _, method := range []string{"GET", "HEAD"} {
				for _, headers := range []map[string]string{admin, {"Authorization": admin["Authorization"], "If-None-Match": `"` + p.Digest + `"`, "Range": "bytes=0-3"}} {
					if w := request(t, f.module, method, path, nil, headers); w.Code != 404 {
						t.Fatalf("hidden %s %s=%d", method, path, w.Code)
					}
				}
			}
		}
		w := request(t, f.module, "GET", indexPath, nil, map[string]string{"Authorization": admin["Authorization"], "If-None-Match": index.Header().Get("ETag")})
		if w.Code != 200 || strings.Contains(w.Body.String(), "owned:") || w.Header().Get("ETag") == index.Header().Get("ETag") {
			t.Fatalf("stale conditional index=%d %s", w.Code, w.Body.String())
		}
		browse := "/api/v2/previews/helm/repositories/" + f.repo.ID + "/charts"
		if w = request(t, f.module.Preview(), "GET", browse, nil, admin); w.Code != 200 || strings.Contains(w.Body.String(), `"name":"owned"`) {
			t.Fatalf("hidden browse=%d %s", w.Code, w.Body.String())
		}
		if w = request(t, f.module.Preview(), "GET", browse+"/owned/1.2.3", nil, admin); w.Code != 404 {
			t.Fatalf("hidden detail=%d", w.Code)
		}
	}
	assertHidden()
	corrupt := append([]byte(nil), chart...)
	corrupt[len(corrupt)/2] ^= 1
	if err := f.objects.Put(t.Context(), p.ObjectKey, corrupt); err != nil {
		t.Fatal(err)
	}
	governanceRequest(t, f, "owned", "1.2.3", "restore", "2", admin, 502)
	assertHidden()
	if err := f.objects.Put(t.Context(), p.ObjectKey, chart); err != nil {
		t.Fatal(err)
	}
	state = governanceRequest(t, f, "owned", "1.2.3", "restore", "2", admin, 200)
	if state.Tombstoned || state.GovernanceVersion != "3" {
		t.Fatalf("restore=%+v", state)
	}
	state = governanceRequest(t, f, "owned", "1.2.3", "quarantine", "3", admin, 200)
	if !state.Quarantined || state.GovernanceVersion != "4" {
		t.Fatalf("quarantine=%+v", state)
	}
	if w := request(t, f.module, "GET", assetPath(f.repo, p, false), nil, admin); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), chart) {
		t.Fatalf("default quarantine policy=%d", w.Code)
	}
	if version := policyRequest(t, f, "1", true, 200); version != "2" {
		t.Fatalf("policy version=%s", version)
	}
	policyRequest(t, f, "1", false, 409)
	assertHidden()
	corruptProv := append([]byte(nil), prov...)
	corruptProv[len(corruptProv)/2] ^= 1
	if err := f.objects.Put(t.Context(), p.ProvenanceKey, corruptProv); err != nil {
		t.Fatal(err)
	}
	governanceRequest(t, f, "owned", "1.2.3", "release", "4", admin, 502)
	policyRequest(t, f, "2", false, 502)
	assertHidden()
	if err := f.objects.Put(t.Context(), p.ProvenanceKey, prov); err != nil {
		t.Fatal(err)
	}
	if version := policyRequest(t, f, "2", false, 200); version != "3" {
		t.Fatalf("verified relaxation=%s", version)
	}
	governanceRequest(t, f, "owned", "1.2.3", "release", "4", admin, 200)
	for _, asset := range []struct {
		path string
		body []byte
	}{{assetPath(f.repo, p, false), chart}, {assetPath(f.repo, p, true), prov}} {
		w := request(t, f.module, "GET", asset.path, nil, admin)
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), asset.body) {
			t.Fatalf("restored bytes=%d", w.Code)
		}
	}
	got, err := f.store.GetHelmPublication(t.Context(), f.repo.ID, p.Name, p.Version)
	if err != nil || got.Digest != p.Digest || got.SessionID != p.SessionID || got.ProvenanceDigest != p.ProvenanceDigest || got.ObjectKey != p.ObjectKey || got.ProvenanceKey != p.ProvenanceKey {
		t.Fatalf("replaced closure=%+v %v", got, err)
	}
	capacity, err := f.store.GetRepositoryCapacity(t.Context(), f.repo.ID)
	if err != nil || capacity.UsedBytes != int64(len(chart)+len(prov)) || capacity.ObjectCount != 2 {
		t.Fatalf("retained capacity=%+v %v", capacity, err)
	}
	audits, err := f.store.ListAudits(t.Context(), repository.AuditQuery{Repository: f.repo.Name, Operation: "helm.governance.delete", Limit: 100})
	if err != nil || len(audits) != 1 || audits[0].Evidence["governanceVersion"] != "2" {
		t.Fatalf("atomic no-op audit=%+v %v", audits, err)
	}
}

func TestHelmGovernanceMixedGroupNeverRevivesHiddenAuthority(t *testing.T) {
	for _, ownerType := range []string{"hosted", "proxy"} {
		for _, sourceState := range []string{"fresh", "missing", "offline"} {
			t.Run(ownerType+"/"+sourceState, func(t *testing.T) {
				chart := chartFixture(t, "owned", "1.2.3", "fixed source")
				prov := provenanceFixture(t, chart)
				feed := newHelmFeed(t, chart, prov)
				f, proxy := proxyModule(t, feed)
				hosted, err := f.store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "helm-hosted", Format: repository.FormatHelm, Type: repository.RepositoryTypeHosted})
				if err != nil {
					t.Fatal(err)
				}
				hf := f
				hf.repo = hosted
				hostedChart, hostedProv := chart, prov
				if ownerType == "proxy" {
					hostedChart = chartFixture(t, "owned", "1.2.3", "forbidden fallback")
					hostedProv = nil
				}
				if w := upload(t, hf, "owned", "1.2.3", hostedChart, hostedProv); w.Code != 201 {
					t.Fatal(w.Code, w.Body.String())
				}
				members := []string{proxy.ID, hosted.ID}
				governed := f
				if ownerType == "hosted" {
					members, governed = []string{hosted.ID, proxy.ID}, hf
				}
				g := createHelmGroup(t, f, "governed-group", members...)
				index := "/helm/governed-group/index.yaml"
				oldURL := indexChartURL(t, request(t, f.module, "GET", index, nil, admin), "owned")
				if ownerType == "proxy" {
					if w := request(t, f.module.Preview(), "GET", governancePath(proxy, "owned", "1.2.3"), nil, admin); w.Code != 404 {
						t.Fatalf("uncached governance fabricated=%d", w.Code)
					}
				}
				if w := request(t, f.module, "GET", oldURL, nil, admin); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), chart) {
					t.Fatalf("initial source=%d", w.Code)
				}
				governanceRequest(t, governed, "owned", "1.2.3", "delete", "1", admin, 200)
				body, _ := json.Marshal(map[string]any{"name": g.Name, "version": g.Version, "members": []string{members[1], members[0]}})
				if w := request(t, f.module.Preview(), "PUT", "/api/v2/previews/helm/groups/"+g.ID, body, admin); w.Code != 200 {
					t.Fatalf("reorder=%d %s", w.Code, w.Body.String())
				}
				switch sourceState {
				case "missing":
					feed.mu.Lock()
					feed.index = []byte("apiVersion: v1\nentries: {}\n")
					feed.mu.Unlock()
					expireHelmDocument(t, f.store, proxy)
					// Force a fresh 200 rather than a legitimately unchanged 304.
					doc, err := f.store.GetHelmSourceDocument(t.Context(), proxy.ID, proxy.Version)
					if err != nil {
						t.Fatal(err)
					}
					doc.ETag = ""
					if err := f.store.PutHelmSourceDocument(t.Context(), doc); err != nil {
						t.Fatal(err)
					}
				case "offline":
					feed.server.Close()
					expireHelmDocument(t, f.store, proxy)
				}
				w := request(t, f.module, "GET", index, nil, admin)
				if w.Code != 200 || strings.Contains(w.Body.String(), "owned:") {
					t.Fatalf("hidden authority fallback=%d %s", w.Code, w.Body.String())
				}
				for _, path := range []string{oldURL, oldURL + ".prov"} {
					if w := request(t, f.module, "GET", path, nil, admin); w.Code != 404 {
						t.Fatalf("old source revived=%d %s", w.Code, w.Body.String())
					}
				}
				if w := request(t, f.module.Preview(), "GET", "/api/v2/previews/helm/groups/"+g.ID+"/sources/owned/1.2.3", nil, admin); w.Code != 404 {
					t.Fatalf("hidden source browser=%d", w.Code)
				}
				governanceRequest(t, governed, "owned", "1.2.3", "restore", "2", admin, 200)
				governanceRequest(t, governed, "owned", "1.2.3", "quarantine", "3", admin, 200)
				policyRequest(t, governed, "1", true, 200)
				if w := request(t, f.module, "GET", oldURL, nil, admin); w.Code != 404 {
					t.Fatalf("quarantined snapshot=%d", w.Code)
				}
				governanceRequest(t, governed, "owned", "1.2.3", "release", "4", admin, 200)
				w = request(t, f.module, "GET", oldURL, nil, admin)
				if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), chart) || w.Header().Get("X-Helm-Source-Repository") != governed.repo.ID {
					t.Fatalf("restored fixed closure=%d %s", w.Code, w.Body.String())
				}
			})
		}
	}
}

type failedHelmPolicy struct{ repository.HelmGovernanceStore }

func (failedHelmPolicy) GetRepositoryQuarantineReadPolicy(context.Context, string) (repository.RepositoryQuarantineReadPolicy, error) {
	return repository.RepositoryQuarantineReadPolicy{}, errors.New("owned policy outage")
}

func TestHelmGovernanceRequiresExplicitPortAndPolicyFailureStaysClosed(t *testing.T) {
	f := newFixture(t)
	for _, port := range []repository.HelmGovernanceStore{nil, (*repository.PostgresStore)(nil)} {
		o := moduleOptions(f.store, f.objects)
		o.Governance = port
		if m, err := New(o); err == nil || m != nil {
			t.Fatal("missing governance port accepted")
		}
	}
	chart := chartFixture(t, "owned", "1.2.3", "fail closed")
	if w := upload(t, f, "owned", "1.2.3", chart, nil); w.Code != 201 {
		t.Fatal(w.Code)
	}
	governanceRequest(t, f, "owned", "1.2.3", "quarantine", "1", admin, 200)
	o := moduleOptions(f.store, f.objects)
	o.Governance = failedHelmPolicy{f.store}
	m, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/helm/helm-test/index.yaml", "/helm/helm-test/charts/owned/1.2.3/owned-1.2.3.tgz"} {
		if w := request(t, m, "GET", path, nil, admin); w.Code != 503 {
			t.Fatalf("policy outage bypass=%d", w.Code)
		}
	}
}

func TestHelmGovernanceConcurrentHTTPUsesOneCASAndNoOpAudit(t *testing.T) {
	f := newFixture(t)
	if w := upload(t, f, "owned", "1.2.3", chartFixture(t, "owned", "1.2.3", "CAS"), nil); w.Code != 201 {
		t.Fatal(w.Code)
	}
	p, _ := f.store.GetHelmPublication(t.Context(), f.repo.ID, "owned", "1.2.3")
	body, _ := json.Marshal(map[string]string{"expectedDigest": p.Digest, "expectedGovernanceVersion": "1", "action": "delete", "reason": "Owned concurrent public HTTP decision"})
	var wg sync.WaitGroup
	results := make(chan int, 12)
	for range 12 {
		wg.Go(func() {
			results <- request(t, f.module.Preview(), "POST", governancePath(f.repo, "owned", "1.2.3"), body, admin).Code
		})
	}
	wg.Wait()
	close(results)
	statuses := map[int]int{}
	for status := range results {
		statuses[status]++
	}
	if statuses[200] != 1 || statuses[409] != 11 || len(statuses) != 2 {
		t.Fatal(fmt.Sprint(statuses))
	}
	audits, err := f.store.ListAudits(t.Context(), repository.AuditQuery{Repository: f.repo.Name, Operation: "helm.governance.delete", Limit: 100})
	if err != nil || len(audits) != 1 {
		t.Fatalf("concurrent audit=%+v %v", audits, err)
	}
}

func TestHelmGovernanceRestoreRejectsRetainedIdentityAndMetadataCorruption(t *testing.T) {
	// The persistence boundary intentionally trusts its verified-byte caller.
	// Seed inconsistent retained records through that public boundary to model
	// durable metadata corruption, rather than changing a private module helper.
	for _, defect := range []string{"identity", "metadata", "chart-yaml", "provenance-binding"} {
		t.Run(defect, func(t *testing.T) {
			f := newFixture(t)
			chart := chartFixture(t, "owned", "1.2.3", "original")
			inspection, err := helmprotocol.Inspect(bytes.NewReader(chart))
			if err != nil {
				t.Fatal(err)
			}
			prov := provenanceFixture(t, chart)
			in := repository.HelmSession{ID: uuid.NewString(), RepositoryID: f.repo.ID, Name: "owned", Version: "1.2.3", Digest: digestFor(chart), Size: int64(len(chart)), ProvenanceDigest: digestFor(prov), ProvenanceSize: int64(len(prov)), Actor: "operator", MetadataJSON: inspection.MetadataJSON, ChartYAML: inspection.ChartYAML, ExpiresAt: time.Now().Add(time.Hour)}
			switch defect {
			case "identity":
				in.Name = "other"
				in.MetadataJSON = []byte(`{"name":"other","version":"1.2.3"}`)
			case "metadata":
				in.MetadataJSON = []byte(`{"name":"owned","version":"1.2.3","description":"corrupt metadata"}`)
			case "chart-yaml":
				in.ChartYAML = []byte("name: owned\nversion: 1.2.3\ndescription: corrupt durable metadata\n")
			case "provenance-binding":
				prov = provenanceFixture(t, chartFixture(t, "other", "1.2.3", "different signed closure"))
				in.ProvenanceDigest, in.ProvenanceSize = digestFor(prov), int64(len(prov))
			}
			in, _, err = f.store.ReserveHelmPublication(t.Context(), in)
			if err != nil {
				t.Fatal(err)
			}
			if err = f.objects.Put(t.Context(), in.ObjectKey, chart); err != nil {
				t.Fatal(err)
			}
			if err = f.objects.Put(t.Context(), in.ProvenanceKey, prov); err != nil {
				t.Fatal(err)
			}
			if _, _, err = f.store.CommitHelmPublication(t.Context(), in.ID, in.Actor); err != nil {
				t.Fatal(err)
			}
			governanceRequest(t, f, in.Name, in.Version, "delete", "1", admin, 200)
			governanceRequest(t, f, in.Name, in.Version, "restore", "2", admin, 502)
			p, err := f.store.GetHelmPublication(t.Context(), f.repo.ID, in.Name, in.Version)
			if err != nil || !p.Governance.Tombstoned || p.Governance.Version != "2" {
				t.Fatalf("corrupt identity restored=%+v %v", p.Governance, err)
			}
		})
	}
}

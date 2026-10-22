package nuget

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/repository"
)

func governancePath(repo repository.HostedRepository, id, version string) string {
	return "/api/v2/previews/nuget/repositories/" + repo.ID + "/packages/" + id + "/" + version + "/governance"
}

func getGovernance(t *testing.T, m *Module, repo repository.HostedRepository, id string) previewGovernance {
	t.Helper()
	w := request(t, m.Preview(), "GET", governancePath(repo, id, "1.0.0"), nil, bearer)
	var g previewGovernance
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &g) != nil {
		t.Fatalf("governance=%d %s", w.Code, w.Body.String())
	}
	return g
}

func changeGovernance(t *testing.T, m *Module, repo repository.HostedRepository, id, action string) previewGovernance {
	t.Helper()
	g := getGovernance(t, m, repo, id)
	input, _ := json.Marshal(map[string]string{"expectedDigest": g.Digest, "expectedGovernanceVersion": g.GovernanceVersion, "action": action, "reason": "Owned acceptance " + action})
	w := request(t, m.Preview(), "POST", governancePath(repo, id, "1.0.0"), input, bearer)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &g) != nil {
		t.Fatalf("%s=%d %s", action, w.Code, w.Body.String())
	}
	return g
}

func setReadPolicy(t *testing.T, m *Module, repo repository.HostedRepository, version string, enabled bool) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"expectedVersion": version, "enabled": enabled})
	w := request(t, m.Preview(), "PUT", "/api/v2/previews/nuget/repositories/"+repo.ID+"/quarantine-read-policy", body, bearer)
	if w.Code != 200 {
		t.Fatalf("read-policy=%d %s", w.Code, w.Body.String())
	}
}

func TestNuGetNativeUnlistRelistPreservesExactResolution(t *testing.T) {
	f := newFixture(t)
	body := packageFixture(t, "Acme.Listing", "1.0.0", "")
	if w := push(t, f, body); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	base := "/nuget/nuget-test/"
	path := base + "api/v2/package/ACME.Listing/1.0"
	for range 2 {
		if w := request(t, f.module, "DELETE", path, nil, bearer); w.Code != 204 {
			t.Fatalf("native unlist=%d %s", w.Code, w.Body.String())
		}
	}
	g := getGovernance(t, f.module, f.repo, "acme.listing")
	if g.Listed || g.Tombstoned || g.GovernanceVersion != "2" {
		t.Fatalf("unlisted state=%+v", g)
	}
	audits, err := f.store.ListAudits(t.Context(), repository.AuditQuery{Repository: f.repo.Name, Operation: "nuget.governance.unlist", Limit: 10})
	if err != nil || len(audits) != 1 || audits[0].Status != 204 {
		t.Fatalf("native unlist atomic audit=%+v %v", audits, err)
	}
	for _, endpoint := range []string{"query?q=acme", "autocomplete?q=acme", "autocomplete?id=acme.listing"} {
		w := request(t, f.module, "GET", base+"v3/"+endpoint, nil, bearer)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"totalHits":0`) {
			t.Fatalf("unlisted discovery %s=%d %s", endpoint, w.Code, w.Body.String())
		}
	}
	w := request(t, f.module, "GET", base+"v3/flat/acme.listing/index.json", nil, bearer)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"1.0.0"`) {
		t.Fatalf("unlisted flat directory=%d %s", w.Code, w.Body.String())
	}
	w = request(t, f.module, "GET", base+"v3/registration/acme.listing/1.0.0.json", nil, bearer)
	z, err := gzip.NewReader(w.Body)
	if w.Code != 200 || err != nil {
		t.Fatalf("unlisted registration=%d %v", w.Code, err)
	}
	metadata, _ := io.ReadAll(z)
	_ = z.Close()
	if !bytes.Contains(metadata, []byte(`"listed":false`)) {
		t.Fatalf("unlisted registration=%s", metadata)
	}
	for _, method := range []string{"GET", "HEAD"} {
		for _, file := range []string{"acme.listing.1.0.0.nupkg", "acme.listing.nuspec", "acme.listing.1.0.0.nupkg.sha512"} {
			if w := request(t, f.module, method, base+"v3/flat/acme.listing/1.0.0/"+file, nil, bearer); w.Code != 200 {
				t.Fatalf("unlisted exact %s %s=%d", method, file, w.Code)
			}
		}
	}
	if w := push(t, f, body); w.Code != 201 {
		t.Fatalf("same-byte replay=%d", w.Code)
	}
	if getGovernance(t, f.module, f.repo, "acme.listing").Listed {
		t.Fatal("push replay relisted publication")
	}
	if w := request(t, f.module, "POST", path, nil, bearer); w.Code != 204 {
		t.Fatalf("native relist=%d %s", w.Code, w.Body.String())
	}
	w = request(t, f.module, "GET", base+"v3/query?q=acme", nil, bearer)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"totalHits":1`) {
		t.Fatalf("relisted search=%d %s", w.Code, w.Body.String())
	}
	audits, err = f.store.ListAudits(t.Context(), repository.AuditQuery{Repository: f.repo.Name, Operation: "nuget.governance.relist", Limit: 10})
	if err != nil || len(audits) != 1 || audits[0].Status != 204 {
		t.Fatalf("native relist atomic audit=%+v %v", audits, err)
	}
}

func TestNuGetGovernanceTombstoneQuarantineAndRecovery(t *testing.T) {
	f := newFixture(t)
	body := packageFixture(t, "Acme.Governed", "1.0.0", "")
	if w := push(t, f, body); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	base := "/nuget/nuget-test/v3/"
	file := base + "flat/acme.governed/1.0.0/acme.governed.1.0.0.nupkg"
	etag := request(t, f.module, "GET", file, nil, bearer).Header().Get("ETag")
	capacity, _ := f.store.GetRepositoryCapacity(t.Context(), f.repo.ID)
	changeGovernance(t, f.module, f.repo, "acme.governed", "unlist")
	changeGovernance(t, f.module, f.repo, "acme.governed", "quarantine")
	if w := request(t, f.module, "GET", file, nil, bearer); w.Code != 200 {
		t.Fatalf("quarantine default policy=%d", w.Code)
	}
	setReadPolicy(t, f.module, f.repo, "1", true)
	assertHidden := func() {
		t.Helper()
		for _, endpoint := range []string{"flat/acme.governed/index.json", "registration/acme.governed/index.json", "registration/acme.governed/1.0.0.json", "flat/acme.governed/1.0.0/acme.governed.1.0.0.nupkg", "flat/acme.governed/1.0.0/acme.governed.nuspec", "flat/acme.governed/1.0.0/acme.governed.1.0.0.nupkg.sha512"} {
			for _, method := range []string{"GET", "HEAD"} {
				w := request(t, f.module, method, base+endpoint, nil, map[string]string{"Authorization": "Bearer nuget-test-admin", "If-None-Match": etag, "Range": "bytes=0-9"})
				if w.Code != 404 {
					t.Fatalf("governed %s %s=%d %s", method, endpoint, w.Code, w.Body.String())
				}
			}
		}
	}
	assertHidden()
	changeGovernance(t, f.module, f.repo, "acme.governed", "delete")
	changeGovernance(t, f.module, f.repo, "acme.governed", "release")
	assertHidden() // Releasing quarantine must never restore a tombstone.
	if w := request(t, f.module, "POST", "/nuget/nuget-test/api/v2/package/acme.governed/1.0.0", nil, bearer); w.Code != 404 {
		t.Fatalf("native relist restored tombstone=%d", w.Code)
	}
	if w := push(t, f, body); w.Code != 201 {
		t.Fatalf("retained replay=%d", w.Code)
	}
	assertHidden()
	state := changeGovernance(t, f.module, f.repo, "acme.governed", "restore")
	if state.Tombstoned || state.Quarantined || state.Listed {
		t.Fatalf("restore changed independent governance=%+v", state)
	}
	if w := request(t, f.module, "GET", file, nil, bearer); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) {
		t.Fatalf("restored immutable bytes=%d", w.Code)
	}
	current, _ := f.store.GetRepositoryCapacity(t.Context(), f.repo.ID)
	if current != capacity {
		t.Fatalf("governance changed retained capacity: %+v -> %+v", capacity, current)
	}
	stale, _ := json.Marshal(map[string]string{"expectedDigest": state.Digest, "expectedGovernanceVersion": "1", "action": "delete", "reason": "stale decision"})
	if w := request(t, f.module.Preview(), "POST", governancePath(f.repo, "acme.governed", "1.0.0"), stale, bearer); w.Code != 409 {
		t.Fatalf("stale decision=%d", w.Code)
	}
	for _, method := range []string{"GET", "POST"} {
		w := request(t, f.module.Preview(), method, governancePath(f.repo, "acme.governed", "1.0.0"), stale, map[string]string{"Authorization": "Bearer nuget-test-reader"})
		if w.Code != 403 {
			t.Fatalf("reader governance %s=%d", method, w.Code)
		}
	}
}

func TestNuGetGroupGovernanceCannotSelectFallbackOwner(t *testing.T) {
	body := packageFixture(t, "Acme.Shared", "1.0.0", "")
	feed := newTestFeed(t, body)
	f, proxy := proxyFixture(t, feed)
	hostedPackage(t, f, f.repo, body)
	group := testNuGetGroup(t, f, f.repo, proxy)
	groupBase := "/nuget/" + group.Name + "/v3/"
	file := "flat/acme.shared/1.0.0/acme.shared.1.0.0.nupkg"
	if w := request(t, f.module, "GET", groupBase+file, nil, bearer); w.Code != 200 {
		t.Fatalf("bind initial owner=%d", w.Code)
	}
	changeGovernance(t, f.module, f.repo, "acme.shared", "delete")
	for _, endpoint := range []string{file, "flat/acme.shared/index.json", "registration/acme.shared/index.json"} {
		if w := request(t, f.module, "GET", groupBase+endpoint, nil, bearer); w.Code != 404 {
			t.Fatalf("Group fallback on tombstone %s=%d %s", endpoint, w.Code, w.Body.String())
		}
	}
	for _, endpoint := range []string{"query?q=acme", "autocomplete?id=acme.shared"} {
		w := request(t, f.module, "GET", groupBase+endpoint, nil, bearer)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"totalHits":0`) {
			t.Fatalf("Group governance search=%d %s", w.Code, w.Body.String())
		}
	}
	changeGovernance(t, f.module, f.repo, "acme.shared", "restore")
	if w := request(t, f.module, "GET", groupBase+file, nil, bearer); w.Code != 200 || w.Header().Get("X-NuGet-Source-Repository") != f.repo.ID {
		t.Fatalf("restored canonical owner=%d %s", w.Code, w.Body.String())
	}
	// The same overlay protects verified Proxy bytes and its remote metadata.
	proxyBase := "/nuget/" + proxy.Name + "/v3/"
	if w := request(t, f.module, "GET", proxyBase+file, nil, bearer); w.Code != 200 {
		t.Fatalf("cache proxy=%d", w.Code)
	}
	changeGovernance(t, f.module, proxy, "acme.shared", "quarantine")
	setReadPolicy(t, f.module, proxy, "1", true)
	for _, endpoint := range []string{file, "flat/acme.shared/index.json", "registration/acme.shared/index.json"} {
		if w := request(t, f.module, "GET", proxyBase+endpoint, nil, bearer); w.Code != 404 {
			t.Fatalf("governed proxy %s=%d %s", endpoint, w.Code, w.Body.String())
		}
	}
	changeGovernance(t, f.module, proxy, "acme.shared", "release")
	if w := request(t, f.module, "GET", proxyBase+file, nil, bearer); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) {
		t.Fatalf("released proxy=%d", w.Code)
	}
}

type unavailableNuGetPolicy struct{ repository.NativeNuGetStore }

func (unavailableNuGetPolicy) GetRepositoryQuarantineReadPolicy(context.Context, string) (repository.RepositoryQuarantineReadPolicy, error) {
	return repository.RepositoryQuarantineReadPolicy{}, errors.New("policy unavailable")
}

func TestNuGetQuarantineReadPolicyFailureIsClosed(t *testing.T) {
	f := newFixture(t)
	if w := push(t, f, packageFixture(t, "Acme.FailClosed", "1.0.0", "")); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	changeGovernance(t, f.module, f.repo, "acme.failclosed", "quarantine")
	f.module.store = unavailableNuGetPolicy{f.store}
	for _, endpoint := range []string{"flat/acme.failclosed/index.json", "registration/acme.failclosed/index.json", "query?q=acme", "flat/acme.failclosed/1.0.0/acme.failclosed.1.0.0.nupkg"} {
		if w := request(t, f.module, http.MethodGet, "/nuget/nuget-test/v3/"+endpoint, nil, bearer); w.Code != 503 {
			t.Fatalf("policy failure %s=%d", endpoint, w.Code)
		}
	}
}

func TestNuGetGovernanceRecoveryRejectsCorruptRetainedBytes(t *testing.T) {
	for _, entry := range []struct{ restrict, recover string }{{"delete", "restore"}, {"quarantine", "release"}, {"unlist", "relist"}} {
		t.Run(entry.recover, func(t *testing.T) {
			f := newFixture(t)
			body := packageFixture(t, "Acme.Corrupt", "1.0.0", "")
			if w := push(t, f, body); w.Code != 201 {
				t.Fatal(w.Body.String())
			}
			prior := changeGovernance(t, f.module, f.repo, "acme.corrupt", entry.restrict)
			p, err := f.store.GetNuGetPublication(t.Context(), f.repo.ID, "acme.corrupt", "1.0.0")
			if err != nil {
				t.Fatal(err)
			}
			corrupt := append([]byte(nil), body...)
			corrupt[len(corrupt)/2] ^= 0xff
			if err = f.objects.Put(t.Context(), p.ObjectKey, corrupt); err != nil {
				t.Fatal(err)
			}
			input, _ := json.Marshal(map[string]string{"expectedDigest": prior.Digest, "expectedGovernanceVersion": prior.GovernanceVersion, "action": entry.recover, "reason": "Reject corrupt recovery"})
			if w := request(t, f.module.Preview(), "POST", governancePath(f.repo, "acme.corrupt", "1.0.0"), input, bearer); w.Code != 409 {
				t.Fatalf("corrupt %s=%d %s", entry.recover, w.Code, w.Body.String())
			}
			if current := getGovernance(t, f.module, f.repo, "acme.corrupt"); current.GovernanceVersion != prior.GovernanceVersion || current.Tombstoned != prior.Tombstoned || current.Quarantined != prior.Quarantined || current.Listed != prior.Listed {
				t.Fatalf("corrupt recovery changed state: %+v -> %+v", prior, current)
			}
		})
	}
}

func TestNuGetGovernanceRequiresCompleteDecisionEvidence(t *testing.T) {
	f := newFixture(t)
	if w := push(t, f, packageFixture(t, "Acme.Evidence", "1.0.0", "")); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	g := changeGovernance(t, f.module, f.repo, "acme.evidence", "delete")
	for _, action := range []string{"delete", "restore", "quarantine", "release", "unlist", "relist"} {
		for _, field := range []string{"expectedDigest", "expectedGovernanceVersion", "reason"} {
			input := map[string]string{"expectedDigest": g.Digest, "expectedGovernanceVersion": g.GovernanceVersion, "action": action, "reason": "Owned evidence test"}
			delete(input, field)
			body, _ := json.Marshal(input)
			if w := request(t, f.module.Preview(), "POST", governancePath(f.repo, "acme.evidence", "1.0.0"), body, bearer); w.Code != 400 {
				t.Fatalf("%s missing %s=%d %s", action, field, w.Code, w.Body.String())
			}
		}
	}
	if current := getGovernance(t, f.module, f.repo, "acme.evidence"); current.GovernanceVersion != g.GovernanceVersion || !current.Tombstoned {
		t.Fatalf("invalid decision changed governance=%+v", current)
	}
}

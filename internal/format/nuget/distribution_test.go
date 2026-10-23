package nuget

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	dist "github.com/ccsert/artifact-gateway/internal/nugetdistribution"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type replicationFixture struct {
	fixture
	target repository.HostedRepository
}

func newReplicationFixture(t *testing.T) replicationFixture {
	t.Helper()
	f := newFixture(t)
	f.module.distribution = f.store
	f.module.auth.APIKeys = f.store
	f.module.auth.ServiceAccounts = f.store
	f.module.auth.Users = f.store
	target, err := f.store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "nuget-target", Format: repository.FormatNuGet, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	return replicationFixture{f, target}
}
func replicationPlan(t *testing.T, f replicationFixture, ids ...string) dist.Plan {
	t.Helper()
	p := dist.Plan{SourceRepositoryID: f.repo.ID, TargetRepositoryID: f.target.ID}
	for _, id := range ids {
		pub, err := f.store.GetNuGetPublication(t.Context(), f.repo.ID, strings.ToLower(id), "1.0.0")
		if err != nil {
			t.Fatal(err)
		}
		p.Items = append(p.Items, dist.Identity{PackageID: strings.ToUpper(id), Version: "1.0", Digest: pub.Digest, Size: pub.Size, SourceGovernanceVersion: pub.Governance.Version})
	}
	return p
}
func createReplication(t *testing.T, f replicationFixture, plan dist.Plan, key, token string, want int) dist.Job {
	t.Helper()
	body, _ := json.Marshal(plan)
	w := request(t, f.module.Preview(), "POST", "/api/v2/previews/nuget/replication-jobs", body, map[string]string{"Authorization": "Bearer " + token, "Idempotency-Key": key})
	if w.Code != want {
		t.Fatalf("create=%d want=%d %s", w.Code, want, w.Body.String())
	}
	var job dist.Job
	if want == 202 && json.Unmarshal(w.Body.Bytes(), &job) != nil {
		t.Fatal(w.Body.String())
	}
	return job
}
func runReplication(t *testing.T, f replicationFixture, job dist.Job) dist.Job {
	t.Helper()
	worker, err := f.module.ReplicationWorker(uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if ran, err := worker.RunOnce(t.Context()); !ran || err != nil {
		t.Fatalf("worker=%t %v", ran, err)
	}
	got, err := f.store.GetNuGetReplication(t.Context(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func TestNuGetReplicationPlanPublicIdentityIdempotencyAndBoundaries(t *testing.T) {
	f := newReplicationFixture(t)
	body := packageFixture(t, "Acme.App", "1.0", "<dependencies><dependency id=\"Acme.Core\" version=\"[1.0.0]\"/></dependencies>")
	if w := push(t, f.fixture, body); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	plan := replicationPlan(t, f, "acme.app")
	key := uuid.NewString()
	job := createReplication(t, f, plan, key, "nuget-test-admin", 202)
	replay := createReplication(t, f, plan, key, "nuget-test-admin", 202)
	if job.ID != replay.ID || job.PlanDigest != replay.PlanDigest || job.Items[0].PackageID != "acme.app" || job.Items[0].Version != "1.0.0" || job.State != "queued" {
		t.Fatalf("job=%+v replay=%+v", job, replay)
	}
	changed := plan
	changed.Items = append([]dist.Identity(nil), plan.Items...)
	changed.Items[0].Size++
	createReplication(t, f, changed, key, "nuget-test-admin", 409)
	for name, alter := range map[string]func(*dist.Plan){"same-repository": func(p *dist.Plan) { p.TargetRepositoryID = p.SourceRepositoryID }, "duplicates": func(p *dist.Plan) { p.Items = append(p.Items, p.Items[0]) }, "missing-pin": func(p *dist.Plan) { p.Items[0].SourceGovernanceVersion = "" }, "overflow": func(p *dist.Plan) { p.Items = make([]dist.Identity, 101) }} {
		t.Run(name, func(t *testing.T) {
			p := plan
			p.Items = append([]dist.Identity(nil), plan.Items...)
			alter(&p)
			createReplication(t, f, p, uuid.NewString(), "nuget-test-admin", 400)
		})
	}
	for _, format := range []repository.Format{repository.FormatNuGet, repository.FormatRaw} {
		kind := repository.RepositoryTypeHosted
		if format == repository.FormatNuGet {
			kind = repository.RepositoryTypeProxy
		}
		r, err := f.store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "reject-" + uuid.NewString(), Format: format, Type: kind, Endpoint: "https://example.test/v3/index.json", AllowedHosts: []string{"example.test"}})
		if err != nil {
			t.Fatal(err)
		}
		p := plan
		p.TargetRepositoryID = r.ID
		createReplication(t, f, p, uuid.NewString(), "nuget-test-admin", 404)
	}
	if _, err := f.store.ReplaceRepositoryCapacityQuota(t.Context(), f.target.ID, int64(len(body)-1)); err != nil {
		t.Fatal(err)
	}
	createReplication(t, f, plan, uuid.NewString(), "nuget-test-admin", 409)
}
func TestNuGetReplicationOwnsBytesDependenciesAndUnlistedVisibility(t *testing.T) {
	f := newReplicationFixture(t)
	bodies := map[string][]byte{}
	for _, v := range []struct{ id, deps string }{{"Leaf", ""}, {"Middle", `<dependencies><group targetFramework="net10.0"><dependency id="Leaf" version="[1.0.0]"/></group></dependencies>`}, {"App", `<dependencies><dependency id="Middle" version="[1.0.0]"/></dependencies>`}} {
		bodies[strings.ToLower(v.id)] = packageFixture(t, v.id, "1.0.0", v.deps)
		if w := push(t, f.fixture, bodies[strings.ToLower(v.id)]); w.Code != 201 {
			t.Fatal(w.Body.String())
		}
	}
	w := request(t, f.module, "DELETE", "/nuget/nuget-test/api/v2/package/app/1.0.0", nil, bearer)
	if w.Code != 204 {
		t.Fatal(w.Body.String())
	}
	job := createReplication(t, f, replicationPlan(t, f, "App", "Leaf", "Middle"), uuid.NewString(), "nuget-test-admin", 202)
	job = runReplication(t, f, job)
	if job.State != "succeeded" {
		t.Fatalf("job=%+v", job)
	}
	for _, item := range job.Items {
		source, _ := f.store.GetNuGetPublication(t.Context(), f.repo.ID, item.PackageID, item.Version)
		target, err := f.store.GetNuGetPublication(t.Context(), f.target.ID, item.PackageID, item.Version)
		if err != nil || target.SessionID == source.SessionID || target.ObjectKey == source.ObjectKey || !strings.Contains(target.ObjectKey, f.target.ID) || !bytes.Equal(source.Nuspec, target.Nuspec) || target.Governance.Listed != (item.PackageID != "app") || item.State != "succeeded" || item.Result != "published" {
			t.Fatalf("source=%+v target=%+v item=%+v err=%v", source, target, item, err)
		}
		if err = f.objects.Delete(t.Context(), source.ObjectKey); err != nil {
			t.Fatal(err)
		}
		path := "/nuget/nuget-target/v3/flat/" + item.PackageID + "/1.0.0/" + item.PackageID + ".1.0.0.nupkg"
		w = request(t, f.module, "GET", path, nil, bearer)
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), bodies[item.PackageID]) {
			t.Fatalf("target read after source removal=%d", w.Code)
		}
		// Recovery and repeated native publication use the retained initial listing.
		if _, err = f.module.verifyPublication(t.Context(), target); err != nil {
			t.Fatalf("target recovery=%v", err)
		}
	}
	w = request(t, f.module, "GET", "/nuget/nuget-target/v3/query?q=app", nil, bearer)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"totalHits":0`) {
		t.Fatal(w.Body.String())
	}
	query := request(t, f.module.Preview(), "GET", "/api/v2/previews/nuget/replication-jobs/"+job.ID, nil, bearer)
	if query.Code != 200 || strings.Contains(query.Body.String(), "objectKey") || strings.Contains(query.Body.String(), "credentialId") {
		t.Fatal(query.Body.String())
	}
}
func TestNuGetReplicationCannotBypassSourceOrTargetGovernance(t *testing.T) {
	for _, mode := range []string{"source-unlist", "source-delete", "source-quarantine", "target-conflict", "target-delete", "target-quarantine", "target-unlist", "same-bytes"} {
		t.Run(mode, func(t *testing.T) {
			f := newReplicationFixture(t)
			body := packageFixture(t, "App", "1.0.0", "")
			if w := push(t, f.fixture, body); w.Code != 201 {
				t.Fatal(w.Body.String())
			}
			job := createReplication(t, f, replicationPlan(t, f, "app"), uuid.NewString(), "nuget-test-admin", 202)
			repo := f.repo
			action := strings.TrimPrefix(mode, "source-")
			if strings.HasPrefix(mode, "target-") || mode == "same-bytes" {
				payload := body
				if mode == "target-conflict" {
					payload = packageFixture(t, "App", "1.0.0", "<tags>different</tags>")
				}
				data, ct := multipartPackage(t, payload)
				w := request(t, f.module, "PUT", "/nuget/nuget-target/api/v2/package/", data, map[string]string{"Content-Type": ct, "X-NuGet-ApiKey": "nuget-test-admin"})
				if w.Code != 201 {
					t.Fatal(w.Body.String())
				}
				repo = f.target
				action = strings.TrimPrefix(mode, "target-")
			}
			if mode != "target-conflict" && mode != "same-bytes" {
				p, _ := f.store.GetNuGetPublication(t.Context(), repo.ID, "app", "1.0.0")
				if _, err := f.store.TransitionNuGetGovernance(t.Context(), repository.NuGetGovernanceChange{RepositoryID: repo.ID, PackageID: "app", Version: "1.0.0", ExpectedDigest: p.Digest, ExpectedVersion: p.Governance.Version, Actor: "operator", Action: action, Reason: "test fence"}); err != nil {
					t.Fatal(err)
				}
			}
			result := runReplication(t, f, job)
			if mode == "same-bytes" {
				if result.State != "succeeded" || result.Items[0].Result != "already_present" {
					t.Fatalf("result=%+v", result)
				}
				return
			}
			if result.State != "failed" || result.Items[0].State != "failed" {
				t.Fatalf("governance bypass=%+v", result)
			}
			if strings.HasPrefix(mode, "source-") {
				if _, err := f.store.GetNuGetPublication(t.Context(), f.target.ID, "app", "1.0.0"); !errors.Is(err, repository.ErrNotFound) {
					t.Fatalf("hidden source copied=%v", err)
				}
			}
		})
	}
}

type replicationHookObjects struct {
	objectstore.Store
	once    sync.Once
	hook    func()
	corrupt bool
}

func (o *replicationHookObjects) PutVerifiedReader(ctx context.Context, key string, r io.Reader, n int64, d string) error {
	if o.corrupt {
		body, _ := io.ReadAll(r)
		if len(body) > 0 {
			body[0] ^= 1
		}
		return o.Put(ctx, key, body)
	}
	err := o.Store.PutVerifiedReader(ctx, key, r, n, d)
	if err == nil && o.hook != nil {
		o.once.Do(o.hook)
	}
	return err
}
func TestNuGetReplicationRechecksExecutionAuthorityAndCapacity(t *testing.T) {
	for _, mode := range []string{"revoke-key", "revoke-source-grant", "revoke-target-grant", "quota", "source-delete", "corrupt-target", "corrupt-source"} {
		t.Run(mode, func(t *testing.T) {
			f := newReplicationFixture(t)
			body := packageFixture(t, "App", "1.0.0", "")
			if w := push(t, f.fixture, body); w.Code != 201 {
				t.Fatal(w.Body.String())
			}
			token := "key-" + uuid.NewString()
			key, err := f.store.CreateAPIKey(t.Context(), repository.APIKey{ID: uuid.NewString(), Name: "replicator", SecretHash: authorization.HashAPIKey(token), Roles: []string{"member"}})
			if err != nil {
				t.Fatal(err)
			}
			actor := "api-key:" + key.ID
			for _, r := range []repository.HostedRepository{f.repo, f.target} {
				scopes := []string{"repositories:read"}
				if r.ID == f.target.ID {
					scopes = append(scopes, "repositories:write")
				}
				if _, err = f.store.ReplaceRepositoryGrants(t.Context(), r.ID, []repository.RepositoryGrant{{Principal: actor, Scopes: scopes, ResourcePrefix: "app"}}, "1"); err != nil {
					t.Fatal(err)
				}
			}
			job := createReplication(t, f, replicationPlan(t, f, "app"), uuid.NewString(), token, 202)
			hook := func() {
				switch mode {
				case "revoke-key":
					_, err = f.store.RevokeAPIKey(t.Context(), key.ID)
				case "revoke-source-grant", "revoke-target-grant":
					id := f.repo.ID
					if mode == "revoke-target-grant" {
						id = f.target.ID
					}
					_, err = f.store.ReplaceRepositoryGrants(t.Context(), id, []repository.RepositoryGrant{}, "2")
				case "quota":
					_, err = f.store.ReplaceRepositoryCapacityQuota(t.Context(), f.target.ID, 1)
				case "source-delete":
					p, _ := f.store.GetNuGetPublication(t.Context(), f.repo.ID, "app", "1.0.0")
					_, err = f.store.TransitionNuGetGovernance(t.Context(), repository.NuGetGovernanceChange{RepositoryID: f.repo.ID, PackageID: "app", Version: "1.0.0", ExpectedDigest: p.Digest, ExpectedVersion: p.Governance.Version, Actor: "operator", Action: "delete", Reason: "concurrent delete"})
				}
				if err != nil {
					t.Error(err)
				}
			}
			f.module.objects = &replicationHookObjects{Store: f.objects, hook: hook, corrupt: mode == "corrupt-target"}
			if mode == "corrupt-source" {
				p, _ := f.store.GetNuGetPublication(t.Context(), f.repo.ID, "app", "1.0.0")
				broken := append([]byte(nil), body...)
				broken[0] ^= 1
				if err = f.objects.Put(t.Context(), p.ObjectKey, broken); err != nil {
					t.Fatal(err)
				}
			}
			got := runReplication(t, f, job)
			if got.State != "failed" || got.Items[0].State != "failed" {
				t.Fatalf("boundary bypass mode=%s job=%+v", mode, got)
			}
			if mode == "quota" && got.Items[0].Code != "quota_exceeded" {
				t.Fatalf("quota result=%+v", got.Items[0])
			}
			if strings.HasPrefix(mode, "revoke-") {
				if got.Items[0].Code != "access_denied" {
					t.Fatalf("revocation result=%+v", got.Items[0])
				}
				found := false
				for _, audit := range f.store.Audits {
					if audit.Operation == "nuget.replication.item" && audit.Actor == actor {
						if audit.Status != 403 || audit.Outcome != repository.AuditAccessDenied {
							t.Fatalf("denial audit=%+v", audit)
						}
						found = true
					}
				}
				if !found {
					t.Fatal("denial audit missing")
				}
			}

			if _, err = f.store.GetNuGetPublication(t.Context(), f.target.ID, "app", "1.0.0"); !errors.Is(err, repository.ErrNotFound) {
				t.Fatalf("unverified/unauthorized target visible=%v", err)
			}
		})
	}
}
func TestNuGetReplicationPartialProgressAndExactRetry(t *testing.T) {
	f := newReplicationFixture(t)
	for _, id := range []string{"First", "Second"} {
		if w := push(t, f.fixture, packageFixture(t, id, "1.0.0", "")); w.Code != 201 {
			t.Fatal(w.Body.String())
		}
	}
	second, _ := f.store.GetNuGetPublication(t.Context(), f.repo.ID, "second", "1.0.0")
	original, _ := f.objects.Get(t.Context(), second.ObjectKey)
	job := createReplication(t, f, replicationPlan(t, f, "first", "second"), uuid.NewString(), "nuget-test-admin", 202)
	if err := f.objects.Delete(t.Context(), second.ObjectKey); err != nil {
		t.Fatal(err)
	}
	failed := runReplication(t, f, job)
	if failed.State != "failed" || failed.Items[0].State != "succeeded" || failed.Items[1].State != "failed" {
		t.Fatalf("partial=%+v", failed)
	}
	first, _ := f.store.GetNuGetPublication(t.Context(), f.target.ID, "first", "1.0.0")
	if err := f.objects.Put(t.Context(), second.ObjectKey, original); err != nil {
		t.Fatal(err)
	}
	retry := func(version string, indexes []int, key string, want int) dist.Job {
		body, _ := json.Marshal(map[string]any{"expectedVersion": version, "itemIndexes": indexes})
		w := request(t, f.module.Preview(), "POST", "/api/v2/previews/nuget/replication-jobs/"+job.ID+"/retry", body, map[string]string{"Authorization": "Bearer nuget-test-admin", "Idempotency-Key": key})
		if w.Code != want {
			t.Fatalf("retry=%d %s", w.Code, w.Body.String())
		}
		var out dist.Job
		if want == 202 {
			_ = json.Unmarshal(w.Body.Bytes(), &out)
		}
		return out
	}
	retry(failed.Version, []int{0}, uuid.NewString(), 409)
	retry(job.Version, []int{1}, uuid.NewString(), 409)
	key := uuid.NewString()
	queued := retry(failed.Version, []int{1}, key, 202)
	same := retry(failed.Version, []int{1}, key, 202)
	if queued.Version != same.Version {
		t.Fatal("retry replay changed job")
	}
	retry(failed.Version, []int{0}, key, 409)
	result := runReplication(t, f, job)
	again, _ := f.store.GetNuGetPublication(t.Context(), f.target.ID, "first", "1.0.0")
	if result.State != "succeeded" || result.Items[0].Attempts != 1 || result.Items[1].Attempts != 2 || again.SessionID != first.SessionID || !again.PublishedAt.Equal(first.PublishedAt) {
		t.Fatalf("retry changed completed publication %+v", result)
	}
}
func TestNuGetReplicationExpiredOwnerCannotPublish(t *testing.T) {
	f := newReplicationFixture(t)
	if w := push(t, f.fixture, packageFixture(t, "App", "1.0.0", "")); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	job := createReplication(t, f, replicationPlan(t, f, "app"), uuid.NewString(), "nuget-test-admin", 202)
	claim, err := f.store.ClaimNuGetReplication(t.Context(), "old", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ClaimNuGetReplication(t.Context(), "other", time.Second); !errors.Is(err, dist.ErrNotFound) {
		t.Fatalf("double owner=%v", err)
	}
	time.Sleep(1050 * time.Millisecond)
	replacement, err := f.store.ClaimNuGetReplication(t.Context(), "new", time.Minute)
	if err != nil || replacement.Lease.Fence <= claim.Lease.Fence {
		t.Fatalf("reclaim=%+v %v", replacement, err)
	}
	if err = f.module.ReplicateNuGetItem(t.Context(), claim, claim.Items[0]); !errors.Is(err, dist.ErrLeaseLost) {
		t.Fatalf("old owner publish=%v", err)
	}
	if err = f.module.ReplicateNuGetItem(t.Context(), replacement, replacement.Items[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.FinishNuGetReplication(t.Context(), replacement.Lease); err != nil {
		t.Fatal(err)
	}
	got, _ := f.store.GetNuGetReplication(t.Context(), job.ID)
	if got.State != "succeeded" {
		t.Fatal(got.State)
	}
}

func TestNuGetReplicationReadPrincipalCanFreezeGovernancePinAndSubmit(t *testing.T) {
	f := newReplicationFixture(t)
	body := packageFixture(t, "App", "1.0.0", "")
	if w := push(t, f.fixture, body); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	token := "reader-writer-" + uuid.NewString()
	key, err := f.store.CreateAPIKey(t.Context(), repository.APIKey{ID: uuid.NewString(), Name: "scoped-replicator", SecretHash: authorization.HashAPIKey(token), Roles: []string{"member"}})
	if err != nil {
		t.Fatal(err)
	}
	actor := "api-key:" + key.ID
	if _, err = f.store.ReplaceRepositoryGrants(t.Context(), f.repo.ID, []repository.RepositoryGrant{{Principal: actor, Scopes: []string{"repositories:read"}, ResourcePrefix: "app"}}, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ReplaceRepositoryGrants(t.Context(), f.target.ID, []repository.RepositoryGrant{{Principal: actor, Scopes: []string{"repositories:write"}, ResourcePrefix: "app"}}, "1"); err != nil {
		t.Fatal(err)
	}
	auth := map[string]string{"Authorization": "Bearer " + token}
	path := "/api/v2/previews/nuget/repositories/" + f.repo.ID + "/packages/app/1.0.0"
	w := request(t, f.module.Preview(), "GET", path, nil, auth)
	var pin previewPackage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &pin) != nil || pin.GovernanceVersion != "1" || pin.Digest == "" || pin.Size != int64(len(body)) {
		t.Fatalf("read-only plan pin=%d %s", w.Code, w.Body.String())
	}
	if w = request(t, f.module.Preview(), "GET", path+"/governance", nil, auth); w.Code != 403 {
		t.Fatalf("source read acquired governance admin=%d", w.Code)
	}
	plan := dist.Plan{SourceRepositoryID: f.repo.ID, TargetRepositoryID: f.target.ID, Items: []dist.Identity{{PackageID: pin.PackageID, Version: pin.Version, Digest: pin.Digest, Size: pin.Size, SourceGovernanceVersion: pin.GovernanceVersion}}}
	job := createReplication(t, f, plan, uuid.NewString(), token, 202)
	if w = request(t, f.module.Preview(), "GET", "/api/v2/previews/nuget/replication-jobs/"+job.ID, nil, auth); w.Code != 200 {
		t.Fatalf("existing target write must include read=%d", w.Code)
	}
	if _, err = f.store.ReplaceRepositoryGrants(t.Context(), f.target.ID, []repository.RepositoryGrant{}, "2"); err != nil {
		t.Fatal(err)
	}
	if w = request(t, f.module.Preview(), "GET", "/api/v2/previews/nuget/replication-jobs/"+job.ID, nil, auth); w.Code != 403 {
		t.Fatalf("removed target grant retained query read=%d", w.Code)
	}
	if _, err = f.store.ReplaceRepositoryGrants(t.Context(), f.target.ID, []repository.RepositoryGrant{{Principal: actor, Scopes: []string{"repositories:write", "repositories:read"}, ResourcePrefix: "app"}}, "3"); err != nil {
		t.Fatal(err)
	}
	if w = request(t, f.module.Preview(), "GET", "/api/v2/previews/nuget/replication-jobs/"+job.ID, nil, auth); w.Code != 200 {
		t.Fatalf("two-repository query=%d %s", w.Code, w.Body.String())
	}
}

func TestNuGetReplicationRejectsMutatedWorkerPlan(t *testing.T) {
	f := newReplicationFixture(t)
	if w := push(t, f.fixture, packageFixture(t, "App", "1.0.0", "")); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	job := createReplication(t, f, replicationPlan(t, f, "app"), uuid.NewString(), "nuget-test-admin", 202)
	claim, err := f.store.ClaimNuGetReplication(t.Context(), "worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"empty-plan", "wrong-digest", "different-item"} {
		bad := dist.Clone(claim)
		item := claim.Items[0]
		switch mode {
		case "empty-plan":
			bad.Items = nil
		case "wrong-digest":
			bad.PlanDigest = "sha256:" + strings.Repeat("0", 64)
		case "different-item":
			item.PackageID = "other"
		}
		if err = f.module.ReplicateNuGetItem(t.Context(), bad, item); !errors.Is(err, dist.ErrInvalid) {
			t.Fatalf("mutated %s accepted=%v", mode, err)
		}
	}
	if _, err = f.store.GetNuGetPublication(t.Context(), f.target.ID, "app", "1.0.0"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("forged plan became visible=%v", err)
	}
	unchanged, _ := f.store.GetNuGetReplication(t.Context(), job.ID)
	if unchanged.Items[0].Attempts != 0 || unchanged.Items[0].TargetSessionID != "" {
		t.Fatalf("forged plan changed checkpoint=%+v", unchanged)
	}
}

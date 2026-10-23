package helm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ccsert/artifact-gateway/internal/authorization"
	helmprotocol "github.com/ccsert/artifact-gateway/internal/protocol/helm"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	dist "github.com/ccsert/artifact-gateway/internal/helmdistribution"
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
	target, err := f.store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "helm-target", Format: repository.FormatHelm, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	return replicationFixture{f, target}
}
func replicationPlan(t *testing.T, f replicationFixture, ids ...string) dist.Plan {
	t.Helper()
	p := dist.Plan{SourceRepositoryID: f.repo.ID, TargetRepositoryID: f.target.ID}
	for _, id := range ids {
		pub, err := f.store.GetHelmPublication(t.Context(), f.repo.ID, strings.ToLower(id), "1.0.0")
		if err != nil {
			t.Fatal(err)
		}
		p.Items = append(p.Items, dist.Identity{Name: id, Version: "1.0.0", Digest: pub.Digest, Size: pub.Size, ProvenanceDigest: pub.ProvenanceDigest, ProvenanceSize: pub.ProvenanceSize, SourceGovernanceVersion: pub.Governance.Version})
	}
	return p
}
func createReplication(t *testing.T, f replicationFixture, plan dist.Plan, key, token string, want int) dist.Job {
	t.Helper()
	body, _ := json.Marshal(plan)
	w := request(t, f.module.Preview(), "POST", "/api/v2/previews/helm/replication-jobs", body, map[string]string{"Authorization": "Bearer " + token, "Idempotency-Key": key})
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
	got, err := f.store.GetHelmReplication(t.Context(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
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
func TestHelmReplicationPartialProgressAndExactRetry(t *testing.T) {
	f := newReplicationFixture(t)
	for _, id := range []string{"First", "Second"} {
		if w := replicationUpload(t, f.fixture, chartFixture(t, strings.ToLower(id), "1.0.0", "copy")); w.Code != 201 {
			t.Fatal(w.Body.String())
		}
	}
	second, _ := f.store.GetHelmPublication(t.Context(), f.repo.ID, "second", "1.0.0")
	original, _ := f.objects.Get(t.Context(), second.ObjectKey)
	job := createReplication(t, f, replicationPlan(t, f, "first", "second"), uuid.NewString(), "helm-owned-admin", 202)
	if err := f.objects.Delete(t.Context(), second.ObjectKey); err != nil {
		t.Fatal(err)
	}
	failed := runReplication(t, f, job)
	if failed.State != "failed" || failed.Items[0].State != "succeeded" || failed.Items[1].State != "failed" {
		t.Fatalf("partial=%+v", failed)
	}
	first, _ := f.store.GetHelmPublication(t.Context(), f.target.ID, "first", "1.0.0")
	if err := f.objects.Put(t.Context(), second.ObjectKey, original); err != nil {
		t.Fatal(err)
	}
	retry := func(version string, indexes []int, key string, want int) dist.Job {
		body, _ := json.Marshal(map[string]any{"expectedVersion": version, "itemIndexes": indexes})
		w := request(t, f.module.Preview(), "POST", "/api/v2/previews/helm/replication-jobs/"+job.ID+"/retry", body, map[string]string{"Authorization": "Bearer helm-owned-admin", "Idempotency-Key": key})
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
	again, _ := f.store.GetHelmPublication(t.Context(), f.target.ID, "first", "1.0.0")
	if result.State != "succeeded" || result.Items[0].Attempts != 1 || result.Items[1].Attempts != 2 || again.SessionID != first.SessionID || !again.PublishedAt.Equal(first.PublishedAt) {
		t.Fatalf("retry changed completed publication %+v", result)
	}
}
func TestHelmReplicationExpiredOwnerCannotPublish(t *testing.T) {
	f := newReplicationFixture(t)
	if w := replicationUpload(t, f.fixture, chartFixture(t, "app", "1.0.0", "copy")); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	job := createReplication(t, f, replicationPlan(t, f, "app"), uuid.NewString(), "helm-owned-admin", 202)
	claim, err := f.store.ClaimHelmReplication(t.Context(), "old", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ClaimHelmReplication(t.Context(), "other", time.Second); !errors.Is(err, dist.ErrNotFound) {
		t.Fatalf("double owner=%v", err)
	}
	time.Sleep(1050 * time.Millisecond)
	replacement, err := f.store.ClaimHelmReplication(t.Context(), "new", time.Minute)
	if err != nil || replacement.Lease.Fence <= claim.Lease.Fence {
		t.Fatalf("reclaim=%+v %v", replacement, err)
	}
	if err = f.module.ReplicateHelmItem(t.Context(), claim, claim.Items[0]); !errors.Is(err, dist.ErrLeaseLost) {
		t.Fatalf("old owner publish=%v", err)
	}
	if err = f.module.ReplicateHelmItem(t.Context(), replacement, replacement.Items[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.FinishHelmReplication(t.Context(), replacement.Lease); err != nil {
		t.Fatal(err)
	}
	got, _ := f.store.GetHelmReplication(t.Context(), job.ID)
	if got.State != "succeeded" {
		t.Fatal(got.State)
	}
}

func TestHelmReplicationRejectsMutatedWorkerPlan(t *testing.T) {
	f := newReplicationFixture(t)
	if w := replicationUpload(t, f.fixture, chartFixture(t, "app", "1.0.0", "copy")); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	if w := replicationUpload(t, f.fixture, chartFixture(t, "second", "1.0.0", "copy")); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	job := createReplication(t, f, replicationPlan(t, f, "app", "second"), uuid.NewString(), "helm-owned-admin", 202)
	claim, err := f.store.ClaimHelmReplication(t.Context(), "worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"empty-plan", "wrong-digest", "different-item", "narrowed-plan-and-digest", "wrong-job", "wrong-lease-job", "credential"} {
		bad := dist.Clone(claim)
		item := claim.Items[0]
		switch mode {
		case "narrowed-plan-and-digest":
			bad.Items = bad.Items[:1]
			_, bad.PlanDigest, _ = dist.CanonicalPlan(dist.Plan{SourceRepositoryID: bad.SourceRepositoryID, TargetRepositoryID: bad.TargetRepositoryID, Items: []dist.Identity{bad.Items[0].Identity}})
		case "wrong-job":
			bad.ID = uuid.NewString()
		case "wrong-lease-job":
			bad.Lease.JobID = uuid.NewString()
		case "credential":
			bad.CredentialID = uuid.NewString()
		case "empty-plan":
			bad.Items = nil
		case "wrong-digest":
			bad.PlanDigest = "sha256:" + strings.Repeat("0", 64)
		case "different-item":
			item.Name = "other"
		}
		if err = f.module.ReplicateHelmItem(t.Context(), bad, item); !errors.Is(err, dist.ErrInvalid) {
			t.Fatalf("mutated %s accepted=%v", mode, err)
		}
	}
	if _, err = f.store.GetHelmPublication(t.Context(), f.target.ID, "app", "1.0.0"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("forged plan became visible=%v", err)
	}
	unchanged, _ := f.store.GetHelmReplication(t.Context(), job.ID)
	if unchanged.Items[0].Attempts != 0 || unchanged.Items[0].TargetSessionID != "" {
		t.Fatalf("forged plan changed checkpoint=%+v", unchanged)
	}
}

func replicationUpload(t *testing.T, f fixture, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	in, err := helmprotocol.Inspect(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return upload(t, f, in.Metadata.Name, in.Metadata.Version, body, nil)
}
func TestHelmReplicationPlanPublicIdentityIdempotencyAndBoundaries(t *testing.T) {
	f := newReplicationFixture(t)
	chart := chartFixture(t, "app", "1.0.0", "plan")
	prov := provenanceFixture(t, chart)
	if w := upload(t, f.fixture, "app", "1.0.0", chart, prov); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	plan := replicationPlan(t, f, "app")
	key := uuid.NewString()
	j := createReplication(t, f, plan, key, "helm-owned-admin", 202)
	same := createReplication(t, f, plan, key, "helm-owned-admin", 202)
	if j.ID != same.ID || j.PlanDigest != same.PlanDigest || j.State != "queued" {
		t.Fatal("idempotent job mismatch")
	}
	changed := plan
	changed.Items = append([]dist.Identity(nil), plan.Items...)
	changed.Items[0].Size++
	createReplication(t, f, changed, key, "helm-owned-admin", 409)
	for _, field := range []string{"duplicate", "same-repository", "governance", "size", "provenance", "name", "version", "overflow"} {
		t.Run(field, func(t *testing.T) {
			p := plan
			p.Items = append([]dist.Identity(nil), plan.Items...)
			switch field {
			case "duplicate":
				p.Items = append(p.Items, p.Items[0])
			case "same-repository":
				p.TargetRepositoryID = p.SourceRepositoryID
			case "governance":
				p.Items[0].SourceGovernanceVersion = ""
			case "size":
				p.Items[0].Size = 0
			case "provenance":
				p.Items[0].ProvenanceSize = 0
			case "name":
				p.Items[0].Name = "APP"
			case "version":
				p.Items[0].Version = "1.0"
			case "overflow":
				p.Items = make([]dist.Identity, 101)
			}
			createReplication(t, f, p, uuid.NewString(), "helm-owned-admin", 400)
		})
	}
	if _, err := f.store.ReplaceRepositoryCapacityQuota(t.Context(), f.target.ID, int64(len(chart))); err != nil {
		t.Fatal(err)
	}
	createReplication(t, f, plan, uuid.NewString(), "helm-owned-admin", 409)
	w := request(t, f.module.Preview(), "GET", "/api/v2/previews/helm/replication-jobs/"+j.ID, nil, admin)
	if w.Code != 200 || strings.Contains(w.Body.String(), "objectKey") || strings.Contains(w.Body.String(), "credentialId") {
		t.Fatalf("public job=%d %s", w.Code, w.Body.String())
	}
}
func TestHelmReplicationOwnsCompleteChartAndProvenance(t *testing.T) {
	for _, signed := range []bool{false, true} {
		t.Run(fmt.Sprint(signed), func(t *testing.T) {
			f := newReplicationFixture(t)
			chart := chartFixture(t, "app", "1.0.0", "independent")
			var prov []byte
			if signed {
				prov = provenanceFixture(t, chart)
			}
			if w := upload(t, f.fixture, "app", "1.0.0", chart, prov); w.Code != 201 {
				t.Fatal(w.Body.String())
			}
			job := createReplication(t, f, replicationPlan(t, f, "app"), uuid.NewString(), "helm-owned-admin", 202)
			result := runReplication(t, f, job)
			if result.State != "succeeded" || result.Items[0].Result != "published" {
				t.Fatalf("result=%+v", result)
			}
			src, _ := f.store.GetHelmPublication(t.Context(), f.repo.ID, "app", "1.0.0")
			dst, err := f.store.GetHelmPublication(t.Context(), f.target.ID, "app", "1.0.0")
			if err != nil || dst.SessionID == src.SessionID || dst.ObjectKey == src.ObjectKey || dst.ProvenanceDigest != src.ProvenanceDigest || signed && dst.ProvenanceKey == src.ProvenanceKey {
				t.Fatalf("target=%+v %v", dst, err)
			}
			replay := createReplication(t, f, replicationPlan(t, f, "app"), uuid.NewString(), "helm-owned-admin", 202)
			if got := runReplication(t, f, replay); got.Items[0].Result != "already_present" {
				t.Fatal(got)
			}
			for _, key := range []string{src.ObjectKey, src.ProvenanceKey} {
				if key != "" {
					if err = f.objects.Delete(t.Context(), key); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, asset := range []struct {
				suffix string
				body   []byte
			}{{"", chart}, {".prov", prov}} {
				if asset.body == nil {
					continue
				}
				w := request(t, f.module, "GET", "/helm/"+f.target.Name+"/charts/app/1.0.0/app-1.0.0.tgz"+asset.suffix, nil, admin)
				if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), asset.body) {
					t.Fatalf("cold target=%d", w.Code)
				}
			}
			cap, err := f.store.GetRepositoryCapacity(t.Context(), f.target.ID)
			if err != nil || cap.UsedBytes != int64(len(chart)+len(prov)) {
				t.Fatalf("closure quota=%+v %v", cap, err)
			}
		})
	}
}
func TestHelmReplicationRejectsGovernanceQuotaAndDamagedClosure(t *testing.T) {
	for _, mode := range []string{"source-delete", "source-quarantine", "source-pin", "target-delete", "target-quarantine", "target-different", "missing-provenance", "corrupt-provenance", "corrupt-chart", "corrupt-target", "quota"} {
		t.Run(mode, func(t *testing.T) {
			f := newReplicationFixture(t)
			chart := chartFixture(t, "app", "1.0.0", "governance")
			prov := provenanceFixture(t, chart)
			if w := upload(t, f.fixture, "app", "1.0.0", chart, prov); w.Code != 201 {
				t.Fatal(w.Body.String())
			}
			src, _ := f.store.GetHelmPublication(t.Context(), f.repo.ID, "app", "1.0.0")
			if strings.HasPrefix(mode, "target-") {
				targetFixture := f.fixture
				targetFixture.repo = f.target
				body := chart
				proof := prov
				if mode == "target-different" {
					body = chartFixture(t, "app", "1.0.0", "different")
					proof = nil
				}
				if w := upload(t, targetFixture, "app", "1.0.0", body, proof); w.Code != 201 {
					t.Fatal(w.Body.String())
				}
			}
			job := createReplication(t, f, replicationPlan(t, f, "app"), uuid.NewString(), "helm-owned-admin", 202)
			mutate := func() {
				if strings.HasPrefix(mode, "source-") || mode == "target-delete" || mode == "target-quarantine" {
					repo := f.repo.ID
					action := strings.TrimPrefix(mode, "source-")
					if strings.HasPrefix(mode, "target-") {
						repo = f.target.ID
						action = strings.TrimPrefix(mode, "target-")
					}
					if action == "pin" {
						action = "quarantine"
					}
					p, _ := f.store.GetHelmPublication(t.Context(), repo, "app", "1.0.0")
					_, err := f.store.TransitionHelmGovernance(t.Context(), repository.HelmGovernanceChange{RepositoryID: repo, Name: "app", Version: "1.0.0", ExpectedDigest: p.Digest, ExpectedVersion: p.Governance.Version, Actor: "operator", Action: action, Reason: "replication guard"})
					if err != nil {
						t.Fatal(err)
					}
					if mode == "source-pin" {
						p, _ = f.store.GetHelmPublication(t.Context(), repo, "app", "1.0.0")
						_, err = f.store.TransitionHelmGovernance(t.Context(), repository.HelmGovernanceChange{RepositoryID: repo, Name: "app", Version: "1.0.0", ExpectedDigest: p.Digest, ExpectedVersion: p.Governance.Version, Actor: "operator", Action: "release", Reason: "pin changed"})
						if err != nil {
							t.Fatal(err)
						}
					}
				}
				if mode == "quota" {
					_, err := f.store.ReplaceRepositoryCapacityQuota(t.Context(), f.target.ID, 1)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if strings.HasPrefix(mode, "source-") || mode == "quota" {
				f.module.objects = &replicationHookObjects{Store: f.objects, hook: mutate}
			} else {
				mutate()
			}
			if mode == "missing-provenance" {
				_ = f.objects.Delete(t.Context(), src.ProvenanceKey)
			}
			if mode == "corrupt-provenance" {
				_ = f.objects.Put(t.Context(), src.ProvenanceKey, []byte("broken"))
			}
			if mode == "corrupt-chart" {
				_ = f.objects.Put(t.Context(), src.ObjectKey, []byte("broken"))
			}
			if mode == "corrupt-target" {
				f.module.objects = &replicationHookObjects{Store: f.objects, corrupt: true}
			}
			got := runReplication(t, f, job)
			if got.State != "failed" || got.Items[0].State != "failed" {
				t.Fatalf("guard %s %+v", mode, got)
			}
			if !strings.HasPrefix(mode, "target-") {
				if _, err := f.store.GetHelmPublication(t.Context(), f.target.ID, "app", "1.0.0"); !errors.Is(err, repository.ErrNotFound) {
					t.Fatalf("partial target=%v", err)
				}
			}
		})
	}
}

type replicationLockHook struct {
	repository.NativeHelmStore
	hook func()
}

func (s replicationLockHook) LockHelmPublication(ctx context.Context, repo, name, version string) (context.Context, func(), error) {
	ctx, release, err := s.NativeHelmStore.LockHelmPublication(ctx, repo, name, version)
	if err == nil {
		s.hook()
	}
	return ctx, release, err
}
func TestHelmReplicationRechecksLeaseAndAuthorityAfterPublicationLock(t *testing.T) {
	for _, mode := range []string{"lease", "credential"} {
		t.Run(mode, func(t *testing.T) {
			f := newReplicationFixture(t)
			if w := replicationUpload(t, f.fixture, chartFixture(t, "app", "1.0.0", "lock")); w.Code != 201 {
				t.Fatal(w.Body.String())
			}
			token := "owned-key-" + uuid.NewString()
			k, err := f.store.CreateAPIKey(t.Context(), repository.APIKey{ID: uuid.NewString(), Name: "locked", SecretHash: authorization.HashAPIKey(token), Roles: []string{}})
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range []repository.HostedRepository{f.repo, f.target} {
				scope := "repositories:read"
				if r.ID == f.target.ID {
					scope = "repositories:write"
				}
				if _, err = f.store.UpsertRepositoryGrant(t.Context(), r.ID, repository.RepositoryGrant{Principal: "api-key:" + k.ID, Scopes: []string{scope}, ResourcePrefix: "app"}); err != nil {
					t.Fatal(err)
				}
			}
			job := createReplication(t, f, replicationPlan(t, f, "app"), uuid.NewString(), token, 202)
			claim, err := f.store.ClaimHelmReplication(t.Context(), "old-owner", time.Second)
			if err != nil {
				t.Fatal(err)
			}
			var replacement dist.Job
			f.module.store = replicationLockHook{NativeHelmStore: f.store, hook: func() {
				if mode == "credential" {
					_, err = f.store.RevokeAPIKey(t.Context(), k.ID)
				} else {
					time.Sleep(time.Until(claim.Lease.Until) + time.Millisecond)
					replacement, err = f.store.ClaimHelmReplication(t.Context(), "replacement", time.Minute)
				}
				if err != nil {
					t.Fatal(err)
				}
			}}
			err = f.module.ReplicateHelmItem(t.Context(), claim, claim.Items[0])
			want := dist.ErrAccessDenied
			if mode == "lease" {
				want = dist.ErrLeaseLost
			}
			if !errors.Is(err, want) {
				t.Fatalf("post-lock guard=%v", err)
			}
			saved, _ := f.store.GetHelmReplication(t.Context(), job.ID)
			if saved.Items[0].TargetSessionID != "" || saved.Items[0].Attempts != 0 {
				t.Fatalf("post-lock failure wrote target=%+v", saved)
			}
			if mode == "lease" {
				if err = f.store.FailHelmReplicationItem(t.Context(), claim.Lease, 0, "transfer_failed"); !errors.Is(err, dist.ErrLeaseLost) {
					t.Fatal("old owner changed replacement")
				}
				f.module.store = f.store
				if err = f.module.ReplicateHelmItem(t.Context(), replacement, replacement.Items[0]); err != nil {
					t.Fatal(err)
				}
				if _, err = f.store.FinishHelmReplication(t.Context(), replacement.Lease); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

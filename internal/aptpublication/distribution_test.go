package aptpublication

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type distributionSigner struct {
	target string
	hook   func()
	fail   bool
}

func (s distributionSigner) SignRelease(ctx context.Context, r SignReleaseRequest) (SignReleaseResult, error) {
	if r.RepositoryID != s.target {
		return SignReleaseResult{}, errors.New("wrong target signer scope")
	}
	if s.hook != nil {
		s.hook()
	}
	if s.fail {
		return SignReleaseResult{}, errors.New("signer offline")
	}
	v, e := (deterministicAPTSigner{}).SignRelease(ctx, r)
	v.SignerIdentity = "target-signer"
	return v, e
}

type distributionTestStore interface {
	distributionStore
	repository.ArtifactIntelligenceStore
}

type distributionFixture struct {
	store          distributionStore
	objects        objectstore.Store
	source, target repository.HostedRepository
	session        repository.APTPublicationSession
	asset          repository.APTSnapshotAsset
	distribution   Distribution
}

func newDistributionFixture(t *testing.T, store distributionTestStore, objects objectstore.Store) distributionFixture {
	t.Helper()
	ctx := context.Background()
	create := func() repository.HostedRepository {
		r, e := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "apt-dist-" + uuid.NewString(), Format: repository.FormatAPT, Type: repository.RepositoryTypeHosted})
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	source, target := create(), create()
	t.Cleanup(func() {
		for _, repo := range []string{source.ID, target.ID} {
			for _, suite := range []string{"stable", "testing"} {
				history, e := store.ListAPTRepositorySnapshots(ctx, repo, suite)
				if e != nil {
					t.Error(e)
					continue
				}
				for _, h := range history {
					if h.Snapshot.State == repository.APTRepositorySnapshotBuilding {
						if e = store.FailAPTRepositorySnapshot(ctx, h.Snapshot.ID); e != nil {
							t.Error(e)
						}
					}
				}
			}
		}
		m := Maintenance{Store: store, Objects: objects}
		if e := m.Schedule(ctx); e != nil {
			t.Error(e)
			return
		}
		for i := 0; i < 3; i++ {
			if e := m.RunReclaimJobs(ctx, 100); e != nil {
				t.Error(e)
				return
			}
		}
	})
	session := stageDistributionFixture(t, ctx, store, objects, source.ID, "testing", "widget")
	publishDistributionFixture(t, ctx, store, objects, source.ID, "testing", 1, session.ID)
	asset, e := store.GetAPTScanAsset(ctx, source.ID, "pool/main/w/widget/widget.deb", session.DeclaredDigest)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = store.ReplaceArtifactIntelligence(ctx, repository.ArtifactIntelligence{RepositoryID: source.ID, Format: repository.FormatAPT, Coordinate: asset.Path, Digest: asset.Digest, SBOMs: []repository.ArtifactSBOM{{MediaType: "application/spdx+json", Digest: asset.Digest}}, Licenses: []repository.ArtifactLicense{{SPDXID: "MIT"}}, UpdatedBy: "ci"}, ""); e != nil {
		t.Fatal(e)
	}
	d := Distribution{Store: store, Source: objects, Destination: objects, Intelligence: store, Publisher: NewPublisher(store, objects, distributionSigner{target: target.ID})}
	return distributionFixture{store, objects, source, target, session, asset, d}
}
func stageDistributionFixture(t *testing.T, ctx context.Context, store distributionStore, objects objectstore.Store, repo, suite, name string) repository.APTPublicationSession {
	t.Helper()
	deb := testDebianPackage(t, "Package: "+name+"\nVersion: 1.0-1\nArchitecture: amd64\nDescription: distribution fixture\n")
	m := NewManager(store, objects)
	session, _, err := m.CreateSession(ctx, CreateSessionInput{RepositoryID: repo, Suite: suite, Component: "main", Publisher: "ci", ObjectName: name + ".deb", DeclaredDigest: digestBytes(deb), DeclaredSize: int64(len(deb)), IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.UploadPackage(ctx, session.ID, session.ObjectName, bytes.NewReader(deb), int64(len(deb))); err != nil {
		t.Fatal(err)
	}
	return session
}
func publishDistributionFixture(t *testing.T, ctx context.Context, store distributionStore, objects objectstore.Store, repo, suite string, seq int64, ids ...string) repository.APTRepositorySnapshot {
	t.Helper()
	v, e := NewPublisher(store, objects, deterministicAPTSigner{}).Publish(ctx, PublishSnapshotInput{ID: uuid.NewString(), RepositoryID: repo, Suite: suite, Sequence: seq, SessionIDs: ids, Actor: "ci", CreatedAt: time.Now().UTC()})
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func (f distributionFixture) payload() PromotionPayload {
	return PromotionPayload{Format: repository.FormatAPT, SourceRepositoryID: f.source.ID, Coordinate: f.asset.Path, Digest: f.asset.Digest, TargetSuite: "stable", Actor: "ci"}
}
func (f distributionFixture) enqueue(t *testing.T) repository.LifecycleJob {
	t.Helper()
	j, _, e := f.distribution.EnqueuePromotion(context.Background(), f.target.ID, uuid.NewString(), f.payload())
	if e != nil {
		t.Fatal(e)
	}
	return j
}
func (f distributionFixture) assertInvisible(t *testing.T) {
	t.Helper()
	if _, e := f.store.GetVisibleAPTRepositorySnapshot(context.Background(), f.target.ID, "stable"); !errors.Is(e, repository.ErrNotFound) {
		t.Fatalf("target became visible: %v", e)
	}
}
func TestAPTDistributionMemory(t *testing.T) {
	exerciseAPTDistribution(t, repository.NewMemoryStore(), objectstore.NewMemoryStore())
}
func exerciseAPTDistribution(t *testing.T, store distributionTestStore, objects objectstore.Store) {
	t.Run("promotion appends signs and replays without revival", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		f := newDistributionFixture(t, store, objects)
		safe := stageDistributionFixture(t, ctx, store, objects, f.target.ID, "stable", "safe")
		publishDistributionFixture(t, ctx, store, objects, f.target.ID, "stable", 1, safe.ID)
		job := f.enqueue(t)
		if e := f.distribution.RunPromotionJobs(ctx, 1); e != nil {
			t.Fatal(e)
		}
		current, e := store.GetVisibleAPTRepositorySnapshot(ctx, f.target.ID, "stable")
		if e != nil || current.SignerIdentity != "target-signer" || current.Sequence != 2 {
			t.Fatalf("target signature: %+v %v", current, e)
		}
		_, members, e := store.GetAPTRepositorySnapshot(ctx, current.ID)
		if e != nil || len(members) != 2 {
			t.Fatalf("lost existing membership: %+v %v", members, e)
		}
		evidence, e := store.GetArtifactIntelligence(ctx, f.target.ID, repository.FormatAPT, f.asset.Path, f.asset.Digest)
		if e != nil || len(evidence.SBOMs) != 1 || evidence.SBOMs[0].Digest != f.asset.Digest {
			t.Fatalf("missing package evidence: %+v %v", evidence, e)
		}
		var promoted string
		for _, m := range members {
			if m.PublicationSessionID != safe.ID {
				promoted = m.PublicationSessionID
			}
		}
		if e = f.distribution.promote(ctx, job); e != nil {
			t.Fatal(e)
		}
		result, e := (Lifecycle{Publisher: f.distribution.Publisher}).Apply(ctx, f.target.ID, "ci", uuid.NewString(), LifecycleRequest{Suite: "stable", ExpectedSnapshotID: current.ID, Action: "delete", PublicationSessionIDs: []string{promoted}})
		if e != nil {
			t.Fatal(e)
		}
		if e = f.distribution.promote(ctx, job); e != nil {
			t.Fatal(e)
		}
		after, e := store.GetVisibleAPTRepositorySnapshot(ctx, f.target.ID, "stable")
		if e != nil || after.ID != result.ID {
			t.Fatalf("replay reactivated snapshot: %+v %v", after, e)
		}
	})
	for _, scenario := range []string{"signer unavailable", "source quarantined during signing", "target quarantined during signing", "source deleted during signing", "lease lost during signing", "target changed during signing"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			f := newDistributionFixture(t, store, objects)
			job := f.enqueue(t)
			var changed repository.APTRepositorySnapshot
			signer := distributionSigner{target: f.target.ID, fail: scenario == "signer unavailable"}
			signer.hook = func() {
				switch scenario {
				case "source quarantined during signing", "target quarantined during signing":
					id := f.source.ID
					if strings.HasPrefix(scenario, "target") {
						id = f.target.ID
					}
					_, e := store.ReplaceArtifactQuarantine(ctx, repository.ArtifactQuarantine{RepositoryID: id, Format: repository.FormatAPT, Coordinate: f.asset.Path, Digest: f.asset.Digest, State: repository.ArtifactQuarantineStateQuarantined, Reason: "test", UpdatedBy: "ci"}, "0")
					if e != nil {
						t.Fatal(e)
					}
				case "source deleted during signing":
					base, e := store.GetVisibleAPTRepositorySnapshot(ctx, f.source.ID, "testing")
					if e != nil {
						t.Fatal(e)
					}
					_, e = (Lifecycle{Publisher: NewPublisher(store, objects, deterministicAPTSigner{})}).Apply(ctx, f.source.ID, "ci", uuid.NewString(), LifecycleRequest{Suite: "testing", ExpectedSnapshotID: base.ID, Action: "delete", PublicationSessionIDs: []string{f.session.ID}})
					if e != nil {
						t.Fatal(e)
					}
				case "lease lost during signing":
					if _, e := store.RecoverExpiredLifecycleJobs(ctx, time.Now().Add(time.Hour)); e != nil {
						t.Fatal(e)
					}
				case "target changed during signing":
					safe := stageDistributionFixture(t, ctx, store, objects, f.target.ID, "stable", "other")
					changed = publishDistributionFixture(t, ctx, store, objects, f.target.ID, "stable", 2, safe.ID)
				}
			}
			f.distribution.Publisher = NewPublisher(store, objects, signer)
			if e := f.distribution.RunPromotionJobs(ctx, 1); e == nil {
				t.Fatal("unsafe publication succeeded")
			}
			if changed.ID == "" {
				f.assertInvisible(t)
			} else {
				v, e := store.GetVisibleAPTRepositorySnapshot(ctx, f.target.ID, "stable")
				if e != nil || v.ID != changed.ID {
					t.Fatal("lost concurrent target publication")
				}
			}
			// Finish/cancel this fixture's job so shared integration workers cannot
			// pick it up in subsequent subtests.
			if scenario == "signer unavailable" || scenario == "target changed during signing" || scenario == "lease lost during signing" {
				if _, e := store.RunLifecycleJobNow(ctx, f.target.ID, job.ID); e != nil {
					t.Fatal(e)
				}
				f.distribution.Publisher = NewPublisher(store, objects, distributionSigner{target: f.target.ID})
				if e := f.distribution.RunPromotionJobs(ctx, 1); e != nil {
					t.Fatal(e)
				}
			} else {
				if _, e := store.CancelLifecycleJob(ctx, f.target.ID, job.ID); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
	t.Run("replication verifies resumes and uses target signer", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		f := newDistributionFixture(t, store, objects)
		checks := []repository.ReplicationCheckpoint{{SourceObjectKey: f.asset.ObjectKey, ObjectKey: f.asset.ObjectKey, Digest: f.asset.Digest, Size: f.asset.Size}}
		p, _, e := store.CreateReplicationPlan(ctx, repository.ReplicationPlan{ID: uuid.NewString(), SourceRepositoryID: f.source.ID, TargetRepositoryID: f.target.ID, Format: repository.FormatAPT, Coordinate: f.asset.Path, Digest: f.asset.Digest, APTTargetSuite: "stable", IdempotencyKey: uuid.NewString(), MaxAttempts: 5}, checks)
		if e != nil {
			t.Fatal(e)
		}
		// A separate destination forces the byte-copy path even when source and
		// target share a database in the test.
		dest := &interruptedDistributionObjects{Store: objectstore.NewMemoryStore()}
		f.distribution.Destination = dest
		f.distribution.Publisher = NewPublisher(store, dest, distributionSigner{target: f.target.ID, fail: true})
		if e = f.distribution.RunReplicationJobs(ctx, 1); e != nil {
			t.Fatal(e)
		}
		interrupted, e := store.ListReplicationCheckpoints(ctx, p.ID)
		if e != nil || len(interrupted) != 1 || interrupted[0].State != "failed" || interrupted[0].Attempts != 1 {
			t.Fatalf("copy interruption: %+v %v", interrupted, e)
		}
		if e = f.distribution.RunReplicationJobs(ctx, 1); e != nil {
			t.Fatal(e)
		}
		failed, e := store.GetReplicationPlan(ctx, f.target.ID, p.ID)
		if e != nil || failed.State != "failed" {
			t.Fatalf("offline signer plan: %+v %v", failed, e)
		}
		f.assertInvisible(t)
		durable, e := store.ListReplicationCheckpoints(ctx, p.ID)
		if e != nil || len(durable) != 1 || durable[0].State != "verified" || durable[0].ByteOffset != f.asset.Size {
			t.Fatalf("lost checkpoints: %+v %v", durable, e)
		}
		bodyBefore, e := dest.Get(ctx, f.asset.ObjectKey)
		if e != nil {
			t.Fatal(e)
		}
		corrupt := append([]byte(nil), bodyBefore...)
		corrupt[len(corrupt)-1] ^= 1
		if e = dest.Put(ctx, f.asset.ObjectKey, corrupt); e != nil {
			t.Fatal(e)
		}
		f.distribution.Publisher = NewPublisher(store, dest, distributionSigner{target: f.target.ID})
		if e = f.distribution.RunReplicationJobs(ctx, 1); e != nil {
			t.Fatal(e)
		}
		f.assertInvisible(t)
		if e = dest.Put(ctx, f.asset.ObjectKey, bodyBefore); e != nil {
			t.Fatal(e)
		}
		if e = f.distribution.RunReplicationJobs(ctx, 1); e != nil {
			t.Fatal(e)
		}
		completed, e := store.GetReplicationPlan(ctx, f.target.ID, p.ID)
		if e != nil || completed.State != "completed" {
			t.Fatalf("resume: %+v %v", completed, e)
		}
		v, e := store.GetVisibleAPTRepositorySnapshot(ctx, f.target.ID, "stable")
		if e != nil || v.SignerIdentity != "target-signer" {
			t.Fatalf("target signed view: %+v %v", v, e)
		}
		r, _, e := dest.Open(ctx, f.asset.ObjectKey)
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(r)
		_ = r.Close()
		if e != nil || digestBytes(body) != f.asset.Digest {
			t.Fatal("destination bytes differ")
		}
		// Suite is part of idempotency; it cannot silently redirect an old plan.
		p.APTTargetSuite = "other"
		if _, _, e = store.CreateReplicationPlan(ctx, p, checks); !errors.Is(e, repository.ErrIdempotencyConflict) {
			t.Fatalf("suite replay: %v", e)
		}
	})
}

// Inject a durable partial object, modelling connection loss mid-transfer.
type interruptedDistributionObjects struct {
	objectstore.Store
	interrupted bool
}

func (s *interruptedDistributionObjects) PutReader(ctx context.Context, key string, r io.Reader, size int64) error {
	if !s.interrupted {
		s.interrupted = true
		part, e := io.ReadAll(io.LimitReader(r, size/2))
		if e != nil {
			return e
		}
		if e = s.Put(ctx, key, part); e != nil {
			return e
		}
		return errors.New("connection interrupted")
	}
	return s.Store.PutReader(ctx, key, r, size)
}

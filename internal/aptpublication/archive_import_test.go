package aptpublication

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func rewriteArchiveObjects(t *testing.T, body []byte, change func(*SnapshotArchiveManifest, map[string][]byte)) []byte {
	t.Helper()
	r := tar.NewReader(bytes.NewReader(body))
	var manifest SnapshotArchiveManifest
	objects := make(map[string][]byte)
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		if h.Name == "manifest.json" {
			if err = json.Unmarshal(b, &manifest); err != nil {
				t.Fatal(err)
			}
		} else {
			objects[h.Name] = b
		}
	}
	change(&manifest, objects)
	wanted := make(map[string]bool)
	for _, p := range manifest.Packages {
		wanted[p.Object] = true
	}
	for _, a := range manifest.Assets {
		wanted[a.Object] = true
	}
	var names []string
	for name := range wanted {
		names = append(names, name)
	}
	slices.Sort(names)
	var result bytes.Buffer
	w := tar.NewWriter(&result)
	b, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = writeArchiveBytes(w, "manifest.json", b); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		b, ok := objects[name]
		if !ok {
			t.Fatalf("missing test object %s", name)
		}
		if err = writeArchiveBytes(w, name, b); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	return result.Bytes()
}

func trustedArchiveFixture(t *testing.T) ([]byte, *TrustedSnapshotArchiveVerifier) {
	t.Helper()
	_, _, body := archiveValidationFixture(t)
	var trust *TrustedSnapshotArchiveVerifier
	body = rewriteArchiveObjects(t, body, func(m *SnapshotArchiveManifest, objects map[string][]byte) {
		normalize := func(value string) string {
			parsed, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				t.Fatal(err)
			}
			return parsed.Truncate(time.Microsecond).UTC().Format(time.RFC3339Nano)
		}
		m.Snapshot.CreatedAt = normalize(m.Snapshot.CreatedAt)
		m.Snapshot.PublishedAt = normalize(m.Snapshot.PublishedAt)
		for i := range m.Packages {
			m.Packages[i].CreatedAt = normalize(m.Packages[i].CreatedAt)
		}
		releaseName, _ := portableArchiveObject(m.Snapshot.ReleaseDigest)
		release := objects[releaseName]
		fixture := aptSignerTestFixtureForRelease(t, release)
		signer, err := NewHTTPSigner(HTTPSignerOptions{Endpoint: "http://127.0.0.1:18083/v1/sign-release", Token: strings.Repeat("t", 32), Timeout: time.Second, Client: aptSignerResponseClient(fixture.response), TrustedFingerprints: []string{fixture.fingerprint}, TrustedPublicKeys: fixture.publicKey})
		if err != nil {
			t.Fatal(err)
		}
		result, err := signer.SignRelease(context.Background(), SignReleaseRequest{RepositoryID: m.Snapshot.RepositoryID, SnapshotID: m.Snapshot.ID, ReleaseDigest: m.Snapshot.ReleaseDigest, Release: bytes.NewReader(release)})
		if err != nil {
			t.Fatal(err)
		}
		m.Snapshot.KeyFingerprint = result.KeyFingerprint
		m.Snapshot.SignerIdentity = result.SignerIdentity
		m.Snapshot.SignatureAlgorithm = result.Algorithm
		m.Snapshot.InReleaseDigest = digestBytes(result.InRelease)
		for index := range m.Assets {
			a := &m.Assets[index]
			var replacement []byte
			if strings.HasSuffix(a.Path, "/InRelease") {
				replacement = result.InRelease
			} else if strings.HasSuffix(a.Path, "/Release.gpg") {
				replacement = result.Detached
			} else {
				continue
			}
			a.Digest = digestBytes(replacement)
			a.Size = int64(len(replacement))
			a.Object, _ = portableArchiveObject(a.Digest)
			objects[a.Object] = replacement
		}
		trust, err = NewTrustedSnapshotArchiveVerifier([]string{fixture.fingerprint}, fixture.publicKey)
		if err != nil {
			t.Fatal(err)
		}
	})
	return body, trust
}

func TestTrustedArchivePreparationRejectsForgedMembershipAndUnsignedIdentity(t *testing.T) {
	t.Parallel()
	body, trust := trustedArchiveFixture(t)
	ctx := context.Background()
	good, err := prepareTrustedArchive(ctx, bytes.NewReader(body), MaxSnapshotArchiveImportBytes, digestBytes(body), trust)
	if err != nil {
		t.Fatal(err)
	}
	good.close()
	altered := rewriteArchiveObjects(t, body, func(m *SnapshotArchiveManifest, _ map[string][]byte) { m.Snapshot.RepositoryID = "another-repository" })
	if _, err = prepareTrustedArchive(ctx, bytes.NewReader(altered), MaxSnapshotArchiveImportBytes, digestBytes(body), trust); !errors.Is(err, ErrArchiveReceiptMismatch) {
		t.Fatalf("relabeled backup accepted: %v", err)
	}
	for _, mutation := range []string{"extra package", "sequence", "creation date"} {
		t.Run(mutation, func(t *testing.T) {
			forged := rewriteArchiveObjects(t, body, func(m *SnapshotArchiveManifest, objects map[string][]byte) {
				if mutation == "sequence" {
					m.Snapshot.Sequence++
					return
				}
				if mutation == "creation date" {
					m.Snapshot.CreatedAt = "2020-01-01T00:00:00Z"
					return
				}
				deb := testDebianPackage(t, "Package: intruder\nVersion: 1.0-1\nArchitecture: amd64\nDescription: injected package\n")
				digest := digestBytes(deb)
				name, _ := portableArchiveObject(digest)
				objects[name] = deb
				m.Packages = append(m.Packages, SnapshotArchivePackage{CanonicalIdentity: "intruder@1.0-1#amd64", Package: "intruder", Version: "1.0-1", Architecture: "amd64", Component: "main", ObjectName: "intruder_1.0-1_amd64.deb", Publisher: "forged", CreatedAt: m.Packages[0].CreatedAt, Digest: digest, Size: int64(len(deb)), Object: name})
				m.Assets = append(m.Assets, SnapshotArchiveAsset{Path: repository.APTPoolPath("main", "intruder", "intruder_1.0-1_amd64.deb"), Digest: digest, Size: int64(len(deb)), Object: name, ContentType: "application/vnd.debian.binary-package"})
			})
			if _, verifyErr := VerifySnapshotArchive(ctx, bytes.NewReader(forged)); verifyErr != nil {
				t.Fatalf("fixture must remain digest-consistent: %v", verifyErr)
			}
			if _, err = prepareTrustedArchive(ctx, bytes.NewReader(forged), MaxSnapshotArchiveImportBytes, digestBytes(forged), trust); !errors.Is(err, ErrSnapshotArchiveCorrupt) {
				t.Fatalf("forged signed association accepted: %v", err)
			}
		})
	}
	untrusted := aptSignerTestFixtureForRelease(t, []byte("Suite: stable\n"))
	otherTrust, err := NewTrustedSnapshotArchiveVerifier([]string{untrusted.fingerprint}, untrusted.publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = prepareTrustedArchive(ctx, bytes.NewReader(body), MaxSnapshotArchiveImportBytes, digestBytes(body), otherTrust); !errors.Is(err, ErrSnapshotArchiveUntrusted) {
		t.Fatalf("untrusted signer accepted: %v", err)
	}
	if _, err = NewTrustedSnapshotArchiveVerifier([]string{untrusted.fingerprint}, untrusted.privateKey); err == nil {
		t.Fatal("private key accepted as recovery trust")
	}
}

type restoreRecordingObjects struct {
	objectstore.Store
	writes      int
	failAt      int
	beforeWrite func(string)
}

func (o *restoreRecordingObjects) PutVerifiedReader(ctx context.Context, key string, body io.Reader, size int64, digest string) error {
	o.writes++
	if o.beforeWrite != nil {
		o.beforeWrite(key)
	}
	if o.writes == o.failAt {
		return errors.New("injected recovery write interruption")
	}
	return o.Store.PutVerifiedReader(ctx, key, body, size, digest)
}

func TestArchiveRestoreIsAtomicReplayableAndReclaimable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	body, trust := trustedArchiveFixture(t)
	manifest, err := VerifySnapshotArchive(ctx, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	store := repository.NewMemoryStore()
	repo := createAPTHostedRepository(t, ctx, store)
	objects := &restoreRecordingObjects{Store: objectstore.NewMemoryStore(), failAt: 3}
	objects.beforeWrite = func(key string) {
		referenced, e := store.APTObjectHasDurableReference(ctx, key)
		if e != nil || !referenced {
			t.Fatalf("object intent must precede write: %v %v", referenced, e)
		}
		if _, e = store.GetVisibleAPTRepositorySnapshot(ctx, repo.ID, "stable"); !errors.Is(e, repository.ErrNotFound) {
			t.Fatalf("partial visibility before commit: %v", e)
		}
		capacity, e := store.GetRepositoryCapacity(ctx, repo.ID)
		if e != nil || capacity.UsedBytes != 0 {
			t.Fatalf("partial metadata before commit: %#v %v", capacity, e)
		}
	}
	importer := NewSnapshotArchiveImporter(store, objects, trust)
	if _, err = importer.Import(ctx, repo.ID, digestBytes(body), bytes.NewReader(body), "restore-admin"); err == nil {
		t.Fatal("interrupted restore succeeded")
	}
	abandoned, err := store.ListUnscheduledAPTArchiveObjects(ctx, 100)
	if err != nil || len(abandoned) == 0 {
		t.Fatalf("missing durable cleanup: %v %v", abandoned, err)
	}
	maintenance := Maintenance{Store: store, Objects: objects.Store}
	if err = maintenance.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	objects.failAt = 0
	objects.beforeWrite = nil
	restored, err := importer.Import(ctx, repo.ID, digestBytes(body), bytes.NewReader(body), "restore-admin")
	if err != nil || restored.ID != manifest.Snapshot.ID {
		t.Fatalf("restore=%#v err=%v", restored, err)
	}
	if err = maintenance.RunReclaimJobs(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var exported bytes.Buffer
	if _, err = (SnapshotArchiveExporter{Store: store, Objects: objects.Store}).Export(ctx, restored.ID, &exported); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(exported.Bytes(), body) {
		t.Fatal("restored archive differs from original backup")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			_, e := NewSnapshotArchiveImporter(store, objects.Store, trust).Import(ctx, repo.ID, digestBytes(body), bytes.NewReader(body), "restore-admin")
			errs <- e
		})
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatalf("concurrent exact replay failed: %v", e)
		}
	}
	_, members, err := store.GetAPTRepositorySnapshot(ctx, restored.ID)
	if err != nil || len(members) != len(manifest.Packages) {
		t.Fatalf("replay duplicated members: %v %v", members, err)
	}
}

func TestArchiveRestoreRejectsQuotaAndWrongRepositoryBeforeObjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	body, trust := trustedArchiveFixture(t)
	store := repository.NewMemoryStore()
	repo := createAPTHostedRepository(t, ctx, store)
	objects := &restoreRecordingObjects{Store: objectstore.NewMemoryStore()}
	importer := NewSnapshotArchiveImporter(store, objects, trust)
	if _, err := store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := importer.Import(ctx, repo.ID, digestBytes(body), bytes.NewReader(body), "admin"); !errors.Is(err, repository.ErrQuotaExceeded) {
		t.Fatalf("quota error=%v", err)
	}
	if objects.writes != 0 {
		t.Fatal("quota rejection wrote objects")
	}
	if pending, err := store.ListUnscheduledAPTArchiveObjects(ctx, 100); err != nil || len(pending) != 0 {
		t.Fatalf("quota rejection left intents: %v %v", pending, err)
	}

	other, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "different-repository", Name: "different-repository", Format: repository.FormatAPT, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = importer.Import(ctx, other.ID, digestBytes(body), bytes.NewReader(body), "admin"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("wrong repository error=%v", err)
	}
	if _, err = importer.Import(ctx, repo.ID, "sha256:"+strings.Repeat("0", 64), bytes.NewReader(body), "admin"); !errors.Is(err, ErrArchiveReceiptMismatch) {
		t.Fatalf("wrong receipt error=%v", err)
	}
	if objects.writes != 0 {
		t.Fatal("rejected restore wrote objects")
	}
	importer.MaxArchiveBytes = int64(len(body) - 1)
	if _, err := importer.Import(ctx, repo.ID, digestBytes(body), bytes.NewReader(body), "admin"); !errors.Is(err, ErrSnapshotArchiveTooLarge) {
		t.Fatalf("archive limit error=%v", err)
	}
}

func TestArchiveRestoreRejectsEachInvalidSignatureAndCleansSpools(t *testing.T) {
	// Isolated temp directory lets the test check both successful and rejected spools.
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	body, trust := trustedArchiveFixture(t)
	ctx := context.Background()
	for _, suffix := range []string{"/InRelease", "/Release.gpg"} {
		t.Run(suffix, func(t *testing.T) {
			forged := rewriteArchiveObjects(t, body, func(m *SnapshotArchiveManifest, objects map[string][]byte) {
				for index := range m.Assets {
					a := &m.Assets[index]
					if !strings.HasSuffix(a.Path, suffix) {
						continue
					}
					replacement := []byte("invalid OpenPGP signature\n")
					a.Digest = digestBytes(replacement)
					a.Size = int64(len(replacement))
					a.Object, _ = portableArchiveObject(a.Digest)
					objects[a.Object] = replacement
					if suffix == "/InRelease" {
						m.Snapshot.InReleaseDigest = a.Digest
					}
				}
			})
			if _, err := prepareTrustedArchive(ctx, bytes.NewReader(forged), 0, digestBytes(forged), trust); !errors.Is(err, ErrSnapshotArchiveUntrusted) {
				t.Fatalf("invalid signature error=%v", err)
			}
		})
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := prepareTrustedArchive(canceled, bytes.NewReader(body), 0, digestBytes(body), trust); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled prepare=%v", err)
	}
	archive, err := prepareTrustedArchive(ctx, bytes.NewReader(body), 0, digestBytes(body), trust)
	if err != nil {
		t.Fatal(err)
	}
	archive.close()
	files, err := os.ReadDir(temp)
	if err != nil || len(files) != 0 {
		t.Fatalf("spool leak: %v %v", files, err)
	}
}

func TestArchiveRestoreReservationsAndRetiredReplayPreserveCurrentSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	body, trust := trustedArchiveFixture(t)
	manifest, err := VerifySnapshotArchive(ctx, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	store := repository.NewMemoryStore()
	repo := createAPTHostedRepository(t, ctx, store)
	objects := &restoreRecordingObjects{Store: objectstore.NewMemoryStore()}
	var required int64
	seen := map[string]bool{}
	for _, p := range manifest.Packages {
		if !seen[p.CanonicalIdentity] {
			seen[p.CanonicalIdentity] = true
			required += p.Size
		}
	}
	for _, a := range manifest.Assets {
		if strings.HasPrefix(a.Path, "dists/") && !seen[a.Object] {
			seen[a.Object] = true
			required += a.Size
		}
	}
	if _, err = store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, required); err != nil {
		t.Fatal(err)
	}
	objects.beforeWrite = func(string) {
		p := manifest.Packages[0]
		_, _, e := NewManager(store, objects.Store).CreateSession(ctx, CreateSessionInput{RepositoryID: repo.ID, Suite: "stable", Component: "main", Publisher: "other", ObjectName: p.ObjectName, DeclaredDigest: p.Digest, DeclaredSize: p.Size, IdempotencyKey: "competing-upload"})
		if !errors.Is(e, repository.ErrQuotaExceeded) {
			t.Fatalf("ordinary upload ignored restore reservation: %v", e)
		}
	}
	restored, err := NewSnapshotArchiveImporter(store, objects, trust).Import(ctx, repo.ID, digestBytes(body), bytes.NewReader(body), "admin")
	if err != nil {
		t.Fatal(err)
	}
	capacity, err := store.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil || capacity.UsedBytes != required {
		t.Fatalf("capacity after restore=%#v required=%d err=%v", capacity, required, err)
	}
	if _, err = store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 0); err != nil {
		t.Fatal(err)
	}
	_, members, err := store.GetAPTRepositorySnapshot(ctx, restored.ID)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.PublicationSessionID)
	}
	next, err := NewPublisher(store, objects.Store, deterministicAPTSigner{}).Publish(ctx, PublishSnapshotInput{ID: "00000000-0000-4000-8000-000000000078", RepositoryID: repo.ID, Suite: "stable", Sequence: restored.Sequence + 1, SessionIDs: ids, Actor: "publisher", CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := NewSnapshotArchiveImporter(store, objects.Store, trust).Import(ctx, repo.ID, digestBytes(body), bytes.NewReader(body), "admin")
	if err != nil || replay.State != repository.APTRepositorySnapshotRetired {
		t.Fatalf("retired replay=%#v err=%v", replay, err)
	}
	current, err := store.GetVisibleAPTRepositorySnapshot(ctx, repo.ID, "stable")
	if err != nil || current.ID != next.ID {
		t.Fatalf("retired restore promoted an old snapshot: %#v %v", current, err)
	}
	// Even a trusted backup with an absent ID cannot displace a newer suite sequence.
	older := rewriteArchiveObjects(t, body, func(m *SnapshotArchiveManifest, _ map[string][]byte) {
		m.Snapshot.ID = "00000000-0000-4000-8000-000000000079"
	})
	if _, err = NewSnapshotArchiveImporter(store, objects.Store, trust).Import(ctx, repo.ID, digestBytes(older), bytes.NewReader(older), "admin"); !errors.Is(err, repository.ErrVersionConflict) {
		t.Fatalf("stale restore accepted: %v", err)
	}
}

func TestTrustedArchiveVerifierBindsBothSignaturesToReleaseAndPreservesLegacyIdentity(t *testing.T) {
	t.Parallel()
	config := &packet.Config{RSABits: 2048, DefaultHash: crypto.SHA256}
	entity, err := openpgp.NewEntity("Release Operator", "reference signer", "release@example.test", config)
	if err != nil {
		t.Fatal(err)
	}
	var public bytes.Buffer
	armored, err := armor.Encode(&public, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = entity.Serialize(armored); err != nil {
		t.Fatal(err)
	}
	if err = armored.Close(); err != nil {
		t.Fatal(err)
	}
	fingerprint := fmt.Sprintf("%x", entity.PrimaryKey.Fingerprint)
	trust, err := NewTrustedSnapshotArchiveVerifier([]string{fingerprint}, public.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	sign := func(release []byte) ([]byte, []byte) {
		var clear, detached bytes.Buffer
		writer, e := clearsign.Encode(&clear, entity.PrivateKey, config)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = writer.Write(release); e != nil {
			t.Fatal(e)
		}
		if e = writer.Close(); e != nil {
			t.Fatal(e)
		}
		if e = openpgp.ArmoredDetachSign(&detached, entity, bytes.NewReader(release), config); e != nil {
			t.Fatal(e)
		}
		return clear.Bytes(), detached.Bytes()
	}
	release := []byte("Suite: stable\n")
	inRelease, detached := sign(release)
	manifest := SnapshotArchiveManifest{Snapshot: SnapshotArchiveSnapshot{ReleaseDigest: digestBytes(release), InReleaseDigest: digestBytes(inRelease), KeyFingerprint: fingerprint, SignerIdentity: "Release Operator <release@example.test>", SignatureAlgorithm: "rsa2048-sha256"}}
	if err = trust.Verify(context.Background(), manifest, release, inRelease, detached); err != nil {
		t.Fatalf("legacy identity from verified key rejected: %v", err)
	}
	manifest.Snapshot.SignerIdentity = "arbitrary archive identity"
	if err = trust.Verify(context.Background(), manifest, release, inRelease, detached); !errors.Is(err, ErrSnapshotArchiveUntrusted) {
		t.Fatalf("arbitrary identity accepted: %v", err)
	}
	manifest.Snapshot.SignerIdentity = entity.PrimaryIdentity().Name
	different, _ := sign([]byte("Suite: unrelated\n"))
	manifest.Snapshot.InReleaseDigest = digestBytes(different)
	if err = trust.Verify(context.Background(), manifest, release, different, detached); !errors.Is(err, ErrSnapshotArchiveUntrusted) {
		t.Fatalf("two valid signatures on different Releases accepted: %v", err)
	}
}

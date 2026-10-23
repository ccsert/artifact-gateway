//go:build integration

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	pypi "github.com/ccsert/artifact-gateway/internal/protocol/pypi"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// Real replication owns different object keys from native upload. Reclaiming
// its source must not make a matching, durable destination unimportable.
func seedPyPIMigrationReplica(t *testing.T, f *pypiMigrationPG, partial bool) []repository.PyPIFile {
	t.Helper()
	ctx := t.Context()
	project := "ag461-existing-" + uuid.NewString()[:8]
	f.remote.files, f.remote.bodies = map[string]pypi.File{}, map[string][]byte{}
	bodies := map[string][]byte{
		strings.ReplaceAll(project, "-", "_") + "-1.0.0-py3-none-any.whl": pypiMigrationWheel(t, project, "1.0.0", ">=3.8", ""),
		project + "-1.0.0.tar.gz": pypiMigrationSDist(t, project, "1.0.0"),
	}
	filenames := make([]string, 0, len(bodies))
	for name, body := range bodies {
		path := "packages/" + name
		f.remote.files[path] = pypi.File{Filename: name, SourcePath: path, SHA256: fmt.Sprintf("%x", sha256.Sum256(body)), RequiresPython: ">=3.8"}
		f.remote.bodies[path] = body
		filenames = append(filenames, name)
	}
	sort.Strings(filenames)
	source, err := f.store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "pypi-replica-source-" + uuid.NewString(), Format: repository.FormatPyPI})
	if err != nil {
		t.Fatal(err)
	}
	var digest string
	for i, name := range filenames {
		if partial && i > 0 {
			break
		}
		data, metadata := bodies[name], f.remote.files["packages/"+name]
		identity, err := pypi.FilenameIdentity(name)
		if err != nil {
			t.Fatal(err)
		}
		digest = "sha256:" + metadata.SHA256
		key := "native/pypi/sha256/" + metadata.SHA256
		if err = f.objects.PutVerifiedReader(ctx, key, bytes.NewReader(data), int64(len(data)), digest); err != nil {
			t.Fatal(err)
		}
		_, err = f.store.PublishPyPIFile(ctx, repository.PyPIFile{RepositoryID: source.ID, Project: project, Version: "1.0.0", Filename: name, FileType: identity.FileType, PythonVersion: identity.PythonVersion, RequiresPython: metadata.RequiresPython, Digest: digest, ObjectKey: key, Size: int64(len(data))})
		if err != nil {
			t.Fatal(err)
		}
	}
	handler := newTestGateway(t, GatewayOptions{Store: f.store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatPyPI: f.objects}})
	body, _ := json.Marshal(map[string]string{"targetRepositoryId": f.repo.ID, "coordinate": project + "@1.0.0", "digest": digest})
	request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories/"+source.ID+"/replications", bytes.NewReader(body))
	authorize(request, "admin-secret")
	request.Header.Set("Idempotency-Key", uuid.NewString())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("replication admission=%d %s", response.Code, response.Body.String())
	}
	if err = (PyPIReplication{Store: f.store, Source: f.objects, Destination: f.objects}).RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	files, err := f.store.ListPyPIProjectFiles(ctx, f.repo.ID, project)
	want := 2
	if partial {
		want = 1
	}
	if err != nil || len(files) != want {
		t.Fatalf("replicated files=%+v %v", files, err)
	}
	if _, err = f.store.TombstonePyPIVersion(ctx, source.ID, project, "1.0.0"); err != nil {
		t.Fatal(err)
	}
	maintenance := NativePyPIMaintenance{Store: f.store, Objects: f.objects, Now: func() time.Time { return time.Now().UTC().Add(25 * time.Hour) }}
	if err = maintenance.Collect(ctx); err != nil {
		t.Fatal(err)
	}
	// Lifecycle claims serialize jobs within a repository. Drain the finite
	// per-file jobs enqueued above before asserting the source is reclaimed.
	for range len(files) {
		if err = maintenance.RunReclaimJobs(ctx, 10); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range files {
		if !strings.HasPrefix(file.ObjectKey, "native/pypi/replication/") {
			t.Fatalf("replication did not retain its native object key: %+v", file)
		}
		if _, err = f.objects.Stat(ctx, "native/pypi/sha256/"+strings.TrimPrefix(file.Digest, "sha256:")); !errors.Is(err, objectstore.ErrNotFound) {
			t.Fatalf("source canonical object was not reclaimed: %v", err)
		}
	}
	f.reopen()
	return files
}

func pypiMigrationOneConnectionStore(t *testing.T, databaseURL string) (*repository.PostgresStore, *sql.DB) {
	t.Helper()
	pools := make([]*sql.DB, 3)
	for i := range pools {
		pool, err := sql.Open("pgx", databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		pool.SetMaxOpenConns(1)
		pool.SetMaxIdleConns(1)
		t.Cleanup(func() { _ = pool.Close() })
		pools[i] = pool
	}
	store, err := repository.NewPostgresStoreWithPools(pools[0], pools[1], pools[2])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, pools[2]
}

func missingPyPIMigrationReplicaFile(t *testing.T, f *pypiMigrationPG, existing repository.PyPIFile) repository.PyPIFile {
	t.Helper()
	for path, metadata := range f.remote.files {
		if metadata.Filename == existing.Filename {
			continue
		}
		identity, _ := pypi.FilenameIdentity(metadata.Filename)
		file := repository.PyPIFile{RepositoryID: f.repo.ID, Project: identity.Project, Version: identity.Version, Filename: metadata.Filename, FileType: identity.FileType, PythonVersion: identity.PythonVersion, RequiresPython: metadata.RequiresPython, Digest: "sha256:" + metadata.SHA256, Size: int64(len(f.remote.bodies[path]))}
		file.ObjectKey = pypiReplicationTargetObjectKey(f.repo.ID, file.Filename, file.Digest)
		if err := f.objects.PutVerifiedReader(t.Context(), file.ObjectKey, bytes.NewReader(f.remote.bodies[path]), file.Size, file.Digest); err != nil {
			t.Fatal(err)
		}
		return file
	}
	t.Fatal("missing-file fixture was complete")
	return repository.PyPIFile{}
}

type pypiMigrationBeforeObjectLocksStore struct {
	*repository.PostgresStore
	before func(context.Context) error
}

func (s *pypiMigrationBeforeObjectLocksStore) LockArtifactObjectKeys(ctx context.Context, format repository.Format, keys []string) (context.Context, func(), error) {
	if s.before != nil {
		before := s.before
		s.before = nil
		if err := before(ctx); err != nil {
			return ctx, nil, err
		}
	}
	return s.PostgresStore.LockArtifactObjectKeys(ctx, format, keys)
}

func testPyPIMigrationExistingObjects(t *testing.T) {
	for _, mode := range []string{"complete", "partial", "corrupt", "missing", "metadata", "revoked", "changed-key", "late-row", "reclaim", "publish-race"} {
		t.Run(mode, func(t *testing.T) {
			f := newPyPIMigrationPG(t)
			partial := mode == "partial" || mode == "publish-race" || mode == "late-row"
			files := seedPyPIMigrationReplica(t, f, partial)
			_ = f.store.Close()
			var migrationLocks *sql.DB
			f.store, migrationLocks = pypiMigrationOneConnectionStore(t, f.databaseURL)
			f.reset(nexusmigration.Options{})
			file := files[0]
			acted := false
			var reclaim <-chan error
			var cancelReclaim context.CancelFunc
			collected := false
			if mode == "reclaim" || mode == "publish-race" {
				other, lockPool := pypiMigrationOneConnectionStore(t, f.databaseURL)
				var pid int
				if err := lockPool.QueryRowContext(t.Context(), `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
					t.Fatal(err)
				}
				var migrationPID int
				if err := migrationLocks.QueryRowContext(t.Context(), `SELECT pg_backend_pid()`).Scan(&migrationPID); err != nil || pid == migrationPID {
					t.Fatalf("competing lock sessions are not independent: %d/%d %v", pid, migrationPID, err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				cancelReclaim = cancel
				compete := func() error { return (NativePyPIMaintenance{Store: other, Objects: f.objects}).RunReclaimJobs(ctx, 10) }
				if mode == "reclaim" {
					payload, _ := json.Marshal(pypiReclaimPayload{Format: repository.FormatPyPI, ObjectKey: file.ObjectKey})
					if _, _, err := other.EnqueueLifecycleJob(t.Context(), repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: f.repo.ID, Kind: repository.LifecycleJobReclaim, IdempotencyKey: "pypi:" + file.ObjectKey, Payload: payload}); err != nil {
						t.Fatal(err)
					}
				} else {
					missing := missingPyPIMigrationReplicaFile(t, f, file)
					compete = func() error { _, err := other.PublishPyPIVersion(ctx, []repository.PyPIFile{missing}); return err }
				}
				result := make(chan error, 1)
				reclaim = result
				f.reset(nexusmigration.Options{AfterVerify: func(context.Context, nexusmigration.Entry) error {
					go func() { result <- compete() }()
					deadline := time.NewTimer(3 * time.Second)
					defer deadline.Stop()
					tick := time.NewTicker(5 * time.Millisecond)
					defer tick.Stop()
					for {
						var waiting bool
						if err := f.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid=$1 AND wait_event_type='Lock' AND wait_event='advisory')`, pid).Scan(&waiting); err != nil {
							return err
						}
						if waiting {
							t.Logf("%s: independent PostgreSQL backend %d waits on advisory lock held by migration backend %d; both lock pools have MaxOpenConns=1", mode, pid, migrationPID)
							return nil
						}
						select {
						case err := <-result:
							collected = true
							return fmt.Errorf("competing mutation bypassed actual PyPI locks: %v", err)
						case <-deadline.C:
							return errors.New("independent backend did not wait on the PyPI advisory lock")
						case <-tick.C:
						}
					}
				}})
				t.Cleanup(func() {
					cancel()
					if !collected {
						select {
						case <-result:
						case <-time.After(5 * time.Second):
							t.Error("owned reclaim worker did not exit")
						}
					}
				})
			} else if mode == "late-row" {
				missing := missingPyPIMigrationReplicaFile(t, f, file)
				wrapped := &pypiMigrationBeforeObjectLocksStore{PostgresStore: f.store, before: func(ctx context.Context) error {
					acted = true
					_, err := f.store.PublishPyPIVersion(ctx, []repository.PyPIFile{missing})
					return err
				}}
				f.publication.WithPyPI(NewNexusPyPIMigrationPublication(wrapped, f.objects))
			} else if mode != "complete" && mode != "partial" {
				f.reset(nexusmigration.Options{AfterVerify: func(ctx context.Context, _ nexusmigration.Entry) error {
					acted = true
					var err error
					switch mode {
					case "corrupt":
						return f.objects.Put(ctx, file.ObjectKey, bytes.Repeat([]byte{'x'}, int(file.Size)))
					case "missing":
						return f.objects.Delete(ctx, file.ObjectKey)
					case "metadata":
						_, err = f.db.ExecContext(ctx, `UPDATE native_pypi_files SET requires_python='>=99' WHERE repository_id=$1 AND filename=$2`, f.repo.ID, file.Filename)
					case "revoked":
						_, err = f.store.RevokeAPIKey(ctx, f.keyID)
					case "changed-key":
						key := file.ObjectKey + ".changed"
						if err = f.objects.Put(ctx, key, f.remote.bodies["packages/"+file.Filename]); err != nil {
							return err
						}
						_, err = f.db.ExecContext(ctx, `UPDATE native_pypi_files SET object_key=$3 WHERE repository_id=$1 AND filename=$2`, f.repo.ID, file.Filename, key)
					}
					return err
				}})
			}
			b := f.run(f.start())
			if reclaim != nil && !collected {
				select {
				case err := <-reclaim:
					collected = true
					cancelReclaim()
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("owned reclaim did not finish after migration released its locks")
				}
			}
			entries, _, err := f.service.Entries(t.Context(), b.ID, 10, "")
			if err != nil || len(entries) != 1 {
				t.Fatalf("existing-key entry checkpoint=%+v %v", entries, err)
			}
			success := mode == "complete" || mode == "partial" || mode == "publish-race" || mode == "reclaim"
			if !success {
				codes := map[string]string{"corrupt": "target_bytes_mismatch", "missing": "target_bytes_mismatch", "metadata": "target_conflict", "revoked": "pypi_actor_not_current_admin", "changed-key": "target_coordination_required", "late-row": "target_coordination_required"}
				if !acted || b.State == "completed" || b.VisibleIdentities != 0 || entries[0].LastError != codes[mode] {
					t.Fatalf("changed existing closure was accepted or misclassified: batch=%+v entry=%+v acted=%v", b, entries[0], acted)
				}
				return
			}
			if b.State != "completed" || b.VisibleIdentities != 1 || !partial && b.Counts.Skipped != 1 {
				t.Fatalf("existing replicated closure=%+v entry=%+v", b, entries[0])
			}
			for _, original := range files {
				current, err := f.store.GetPyPIFile(t.Context(), f.repo.ID, original.Filename)
				if err != nil || current.ObjectKey != original.ObjectKey {
					t.Fatalf("existing object reference changed: %+v %v", current, err)
				}
				if _, err = f.objects.Stat(t.Context(), "native/pypi/sha256/"+strings.TrimPrefix(original.Digest, "sha256:")); !errors.Is(err, objectstore.ErrNotFound) {
					t.Fatalf("matching target was unnecessarily restaged: %v", err)
				}
			}
			var audits int
			if err := f.db.QueryRow(`SELECT count(*) FROM resolver_audit_log WHERE repository=$1 AND operation='publish'`, f.repo.Name).Scan(&audits); err != nil || audits != map[bool]int{true: 1, false: 0}[partial] {
				t.Fatalf("publication audit count=%d %v", audits, err)
			}
			f.server.Close()
			handler := newTestGateway(t, GatewayOptions{Store: f.store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: ObjectStores{repository.FormatPyPI: f.objects}})
			for path, metadata := range f.remote.files {
				req := httptest.NewRequest(http.MethodGet, "/pypi/"+f.repo.Name+"/packages/"+metadata.Filename, nil)
				authorize(req, "admin-secret")
				got := httptest.NewRecorder()
				handler.ServeHTTP(got, req)
				if got.Code != http.StatusOK || !bytes.Equal(got.Body.Bytes(), f.remote.bodies[path]) {
					t.Fatalf("source-closed native target read=%d", got.Code)
				}
			}
		})
	}
}

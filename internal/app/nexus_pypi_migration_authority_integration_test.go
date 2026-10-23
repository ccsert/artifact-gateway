//go:build integration

package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/nexusmigration"
	migrationpg "github.com/ccsert/artifact-gateway/internal/nexusmigration/pgstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func (f *pypiMigrationPG) authorityRequest(auth Authenticator, token, method, path, version string, body any, keys ...string) *httptest.ResponseRecorder {
	f.t.Helper()
	handler := newTestGateway(f.t, GatewayOptions{Dependencies: Dependencies{MigrationService: f.service, MigrationSchedules: nexusmigration.NewSchedules(migrationpg.New(f.db), f.service)}, Store: f.store, Adapter: TestAdapter{}, Authenticator: auth})
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(data))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	key := uuid.NewString()
	if len(keys) > 0 {
		key = keys[0]
	}
	request.Header.Set("Idempotency-Key", key)
	if version != "" {
		request.Header.Set("If-Match", version)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
func TestPostgresNexusPyPIMigrationRejectsAuthenticationKindCollisions(t *testing.T) {
	for _, mode := range []string{"static-user-name", "static-key-name", "protocol-v2-static-name", "json-kind-forgery"} {
		t.Run(mode, func(t *testing.T) {
			f := newPyPIMigrationPG(t)
			actor := f.actor
			if mode == "static-user-name" || mode == "protocol-v2-static-name" {
				u, err := f.store.CreateUser(context.Background(), repository.User{ID: uuid.NewString(), Name: "collision-" + uuid.NewString(), Role: "admin"})
				if err != nil {
					t.Fatal(err)
				}
				actor = "user:" + u.Name
			}
			auth := Authenticator{AdminToken: "owned-collision-token", AdminActor: actor, APIKeys: f.store, Users: f.store, ResolverToken: "owned-protocol-signing-secret"}
			token := "owned-collision-token"
			if mode == "protocol-v2-static-name" {
				principal, ok := auth.Authenticate("Bearer " + token)
				if !ok || principal.AuthenticationKind != authorization.AuthenticationStaticAdmin {
					t.Fatal("static fixture did not authenticate")
				}
				token = auth.IssuePrincipalToken(principal)
			}
			var body any = f.input()
			expected := http.StatusUnprocessableEntity
			if mode == "json-kind-forgery" {
				raw, _ := json.Marshal(f.input())
				fields := map[string]any{}
				if err := json.Unmarshal(raw, &fields); err != nil {
					t.Fatal(err)
				}
				fields["actorKind"] = "api_key"
				fields["ownerId"] = f.keyID
				body = fields
				expected = 400
			}
			response := f.authorityRequest(auth, token, "POST", "/api/v2/migrations/nexus/batches", "", body)
			if response.Code != expected {
				t.Fatalf("static authenticated display-name collision admitted: status=%d", response.Code)
			}
			var count int
			if err := f.db.QueryRow(`SELECT count(*) FROM nexus_migration_batches WHERE source_id=$1`, f.created.ID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("rejected collision created batch=%d %v", count, err)
			}
		})
	}
}
func (f *pypiMigrationPG) freshScheduleInput(template nexusmigration.Batch) nexusmigration.ScheduleCreate {
	evidence := pypiMigrationEvidence()
	evidence.ObservedAt = time.Now().UTC().Add(-time.Millisecond)
	return nexusmigration.ScheduleCreate{TemplateBatchID: template.ID, IntervalSeconds: 60, CapacityEvidence: &evidence}
}
func TestPostgresNexusPyPIMigrationScheduledOwnerAuthority(t *testing.T) {
	for _, mode := range []string{"static-key-name", "static-default", "dispatch-revoked", "resume-other-admin", "capacity-other-admin"} {
		t.Run(mode, func(t *testing.T) {
			f := newPyPIMigrationPG(t)
			template := f.run(f.start())
			if template.State != "completed" {
				t.Fatalf("template=%+v", template)
			}
			auth := Authenticator{APIKeys: f.store}
			token := f.keyToken
			if mode == "static-key-name" || mode == "static-default" {
				actor := f.actor
				if mode == "static-default" {
					actor = "owned-bootstrap"
				}
				auth = Authenticator{AdminToken: "owned-collision-token", AdminActor: actor, APIKeys: f.store}
				token = "owned-collision-token"
			}
			response := f.authorityRequest(auth, token, "POST", "/api/v2/migrations/nexus/schedules", "", f.freshScheduleInput(template))
			if mode == "static-key-name" || mode == "static-default" {
				if response.Code != 422 {
					t.Fatalf("static schedule owner admitted: status=%d", response.Code)
				}
				return
			}
			var schedule nexusmigration.Schedule
			if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &schedule) != nil {
				t.Fatalf("managed schedule status=%d", response.Code)
			}
			plans := nexusmigration.NewSchedules(migrationpg.New(f.db), f.service)
			if mode == "resume-other-admin" {
				var err error
				schedule, err = plans.Control(context.Background(), schedule.ID, schedule.Version, f.actor, uuid.NewString(), "pause", nil)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.store.RevokeAPIKey(context.Background(), f.keyID); err != nil {
				t.Fatal(err)
			}
			if mode == "dispatch-revoked" {
				if err := plans.RunDue(context.Background(), 10); err != nil {
					t.Fatal(err)
				}
				var count int
				if err := f.db.QueryRow(`SELECT count(*) FROM nexus_migration_batches WHERE schedule_id=$1`, schedule.ID).Scan(&count); err != nil || count != 0 {
					t.Fatalf("revoked owner dispatched %d batches: %v", count, err)
				}
				return
			}
			action := "resume"
			if mode == "capacity-other-admin" {
				action = "capacity"
			}
			evidence := pypiMigrationEvidence()
			evidence.ObservedAt = time.Now().UTC()
			response = f.authorityRequest(Authenticator{AdminToken: "other-admin-token", AdminActor: "other-admin"}, "other-admin-token", "POST", "/api/v2/migrations/nexus/schedules/"+schedule.ID+"/"+action, schedule.Version, map[string]any{"capacityEvidence": evidence})
			if response.Code != 422 {
				t.Fatalf("current operator replaced revoked schedule owner: status=%d", response.Code)
			}
		})
	}
}

func (f *pypiMigrationPG) createManagedSchedule(t *testing.T, template nexusmigration.Batch, auth Authenticator, token, key string) (nexusmigration.Schedule, nexusmigration.ScheduleCreate) {
	t.Helper()
	input := f.freshScheduleInput(template)
	response := f.authorityRequest(auth, token, "POST", "/api/v2/migrations/nexus/schedules", "", input, key)
	var plan nexusmigration.Schedule
	if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &plan) != nil {
		t.Fatalf("create managed schedule status=%d body=%s", response.Code, response.Body.String())
	}
	if bytes.Contains(response.Body.Bytes(), []byte(`"actorKind"`)) || bytes.Contains(response.Body.Bytes(), []byte(`"ownerId"`)) {
		t.Fatal("private owner binding leaked")
	}
	return plan, input
}
func (f *pypiMigrationPG) managedUser(t *testing.T) (repository.User, Authenticator, string) {
	t.Helper()
	user, err := f.store.CreateUser(context.Background(), repository.User{ID: uuid.NewString(), Name: "durable-pypi-owner-" + uuid.NewString(), Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	auth := Authenticator{AdminToken: "owned-user-session-signing-secret", AdminActor: "owned-bootstrap", Users: f.store, APIKeys: f.store}
	return user, auth, auth.IssueUserSession(user.ID, user.SessionVersion)
}
func (f *pypiMigrationPG) recreateUser(t *testing.T, old repository.User) repository.User {
	t.Helper()
	if _, err := f.store.CreateUser(context.Background(), repository.User{ID: uuid.NewString(), Name: "owned-admin-guard-" + uuid.NewString(), Role: "admin"}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DeleteUser(context.Background(), old.ID); err != nil {
		t.Fatal(err)
	}
	user, err := f.store.CreateUser(context.Background(), repository.User{ID: uuid.NewString(), Name: old.Name, Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	return user
}
func TestPostgresNexusPyPIMigrationDurableOwnerBindingAndPoolOne(t *testing.T) {
	t.Run("same-name-batch-and-replay", func(t *testing.T) {
		f := newPyPIMigrationPG(t)
		user, auth, token := f.managedUser(t)
		input := f.input()
		key := uuid.NewString()
		response := f.authorityRequest(auth, token, "POST", "/api/v2/migrations/nexus/batches", "", input, key)
		var original nexusmigration.Batch
		if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &original) != nil {
			t.Fatalf("user batch status=%d", response.Code)
		}
		original, err := f.service.Get(context.Background(), original.ID)
		if err != nil || original.OwnerID != user.ID || original.ActorKind != "local_session" {
			t.Fatalf("unbound batch: %+v %v", original, err)
		}
		// The same stable user through another real credential kind cannot silently
		// reinterpret a previously bound idempotency key.
		oidcToken := auth.IssueWebSession(authorization.Principal{Actor: "user:" + user.Name, AuthenticationKind: authorization.AuthenticationOIDC, Role: authorization.RoleAdmin, UserID: user.ID}, user.SessionVersion)
		different := f.authorityRequest(auth, oidcToken, "POST", "/api/v2/migrations/nexus/batches", "", input, key)
		if different.Code != 409 {
			t.Fatalf("different kind replay=%d", different.Code)
		}
		recreated := f.recreateUser(t, user)
		newToken := auth.IssueUserSession(recreated.ID, recreated.SessionVersion)
		replay := f.authorityRequest(auth, newToken, "POST", "/api/v2/migrations/nexus/batches", "", input, key)
		if replay.Code != 409 {
			t.Fatalf("new user inherited old batch replay=%d", replay.Code)
		}
		var hash string
		if err = f.db.QueryRow(`SELECT request_hash FROM nexus_migration_batches WHERE id=$1`, original.ID).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		incoming := original
		incoming.ID = uuid.NewString()
		incoming.OwnerID = recreated.ID
		if _, err = migrationpg.New(f.db).Create(context.Background(), incoming, key, hash); !errors.Is(err, nexusmigration.ErrIdempotencyConflict) {
			t.Fatalf("transaction replay changed owner: %v", err)
		}
		f.reopen()
		blocked := f.run(original)
		if blocked.State != "blocked" || blocked.VisibleIdentities != 0 {
			t.Fatalf("recreated user ran old job=%+v", blocked)
		}
		f.noPublication()
		evidence := pypiMigrationEvidence()
		evidence.ObservedAt = time.Now().UTC()
		resume := f.authorityRequest(auth, newToken, "POST", "/api/v2/migrations/nexus/batches/"+blocked.ID+"/resume", blocked.Version, map[string]any{"capacityEvidence": evidence})
		if resume.Code != 422 {
			t.Fatalf("new user resumed old batch=%d", resume.Code)
		}
	})
	t.Run("same-name-plan-and-replay", func(t *testing.T) {
		f := newPyPIMigrationPG(t)
		template := f.run(f.start())
		user, auth, token := f.managedUser(t)
		key := uuid.NewString()
		plan, input := f.createManagedSchedule(t, template, auth, token, key)
		ledger := migrationpg.New(f.db)
		persisted, err := ledger.GetSchedule(context.Background(), plan.ID)
		if err != nil || persisted.OwnerID != user.ID || persisted.ActorKind != "local_session" {
			t.Fatalf("plan binding=%+v %v", persisted, err)
		}
		recreated := f.recreateUser(t, user)
		newToken := auth.IssueUserSession(recreated.ID, recreated.SessionVersion)
		replay := f.authorityRequest(auth, newToken, "POST", "/api/v2/migrations/nexus/schedules", "", input, key)
		if replay.Code != 409 {
			t.Fatalf("new user inherited plan replay=%d", replay.Code)
		}
		var hash string
		if err = f.db.QueryRow(`SELECT request_hash FROM nexus_migration_schedules WHERE id=$1`, plan.ID).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		incoming := persisted
		incoming.ID = uuid.NewString()
		incoming.OwnerID = recreated.ID
		if _, err = ledger.CreateSchedule(context.Background(), incoming, key, hash); !errors.Is(err, nexusmigration.ErrIdempotencyConflict) {
			t.Fatalf("transaction plan replay changed owner: %v", err)
		}
		f.reopen()
		plans := nexusmigration.NewSchedules(migrationpg.New(f.db), f.service)
		if err = plans.RunDue(context.Background(), 10); err != nil {
			t.Fatal(err)
		}
		actual, err := plans.Get(context.Background(), plan.ID)
		if err != nil || actual.State != "blocked" || actual.ActorKind != "local_session" || actual.OwnerID != user.ID || actual.ActiveBatchID != "" {
			t.Fatalf("recreated user inherited plan=%+v %v", actual, err)
		}
		evidence := pypiMigrationEvidence()
		evidence.ObservedAt = time.Now().UTC()
		resume := f.authorityRequest(auth, newToken, "POST", "/api/v2/migrations/nexus/schedules/"+actual.ID+"/resume", actual.Version, map[string]any{"capacityEvidence": evidence})
		if resume.Code != 422 {
			t.Fatalf("new user resumed old plan=%d", resume.Code)
		}
	})
	t.Run("one-connection-restart-dispatch-and-publication", func(t *testing.T) {
		f := newPyPIMigrationPG(t)
		_ = f.store.Close()
		f.db.SetMaxOpenConns(1)
		f.db.SetMaxIdleConns(1)
		listener, err := sql.Open("pgx", f.databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = listener.Close() })
		locks, err := sql.Open("pgx", f.databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		locks.SetMaxOpenConns(1)
		locks.SetMaxIdleConns(1)
		t.Cleanup(func() { _ = locks.Close() })
		f.store, err = repository.NewPostgresStoreWithPools(f.db, listener, locks)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.store.Close() })
		f.reset(nexusmigration.Options{})
		template := f.run(f.start())
		if template.State != "completed" || template.ActorKind != "api_key" || template.OwnerID != f.keyID {
			t.Fatalf("one-connection template=%+v", template)
		}
		plan, _ := f.createManagedSchedule(t, template, Authenticator{APIKeys: f.store}, f.keyToken, uuid.NewString())
		f.reset(nexusmigration.Options{})
		plans := nexusmigration.NewSchedules(migrationpg.New(f.db), f.service)
		stored, err := plans.Get(context.Background(), plan.ID)
		if err != nil || stored.ActorKind != "api_key" || stored.OwnerID != f.keyID {
			t.Fatalf("restart lost bound owner=%+v %v", stored, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		stored, err = plans.Control(ctx, stored.ID, stored.Version, "other-current-admin", uuid.NewString(), "pause", nil)
		if err != nil {
			t.Fatal(err)
		}
		evidence := pypiMigrationEvidence()
		evidence.ObservedAt = time.Now().UTC()
		stored, err = plans.Control(ctx, stored.ID, stored.Version, "other-current-admin", uuid.NewString(), "resume", &evidence)
		if err != nil {
			t.Fatal(err)
		}
		if err = plans.RunDue(ctx, 10); err != nil {
			t.Fatal(err)
		}
		stored, err = plans.Get(ctx, plan.ID)
		if err != nil || stored.ActiveBatchID == "" {
			t.Fatalf("one-connection dispatch=%+v %v", stored, err)
		}
		batch, err := f.service.Get(ctx, stored.ActiveBatchID)
		if err != nil || batch.ActorKind != stored.ActorKind || batch.OwnerID != f.keyID || batch.Actor != f.actor {
			t.Fatalf("dispatch changed owner=%+v %v", batch, err)
		}
		batch = f.run(batch)
		if batch.State != "completed" || batch.Counts.Skipped != 4 {
			t.Fatalf("one-connection native checkpoint=%+v", batch)
		}
	})
	t.Run("transaction-rejects-revoked-dispatch", func(t *testing.T) {
		f := newPyPIMigrationPG(t)
		template := f.run(f.start())
		plan, _ := f.createManagedSchedule(t, template, Authenticator{APIKeys: f.store}, f.keyToken, uuid.NewString())
		ledger := migrationpg.New(f.db)
		claim, err := ledger.ClaimDueSchedule(context.Background(), time.Now().UTC())
		if err != nil || claim.ID != plan.ID {
			t.Fatalf("claim=%+v %v", claim, err)
		}
		if _, err = f.store.RevokeAPIKey(context.Background(), f.keyID); err != nil {
			t.Fatal(err)
		}
		batch := nexusmigration.Batch{ID: uuid.NewString(), Actor: claim.Actor, ActorKind: claim.ActorKind, OwnerID: claim.OwnerID, StartInput: nexusmigration.StartInput{SourceID: claim.SourceID, SourceVersion: claim.SourceVersion, Mappings: claim.Mappings, PolicyVersion: claim.PolicyVersion, CapacityEvidence: *claim.CapacityEvidence}}
		if err = ledger.DispatchSchedule(context.Background(), claim, batch, time.Now().UTC()); err == nil || err.Error() != "pypi_actor_not_current_admin" {
			t.Fatalf("transaction admitted revoked plan: %v", err)
		}
		var count int
		if err = f.db.QueryRow(`SELECT count(*) FROM nexus_migration_batches WHERE schedule_id=$1`, plan.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("rejected dispatch inserted batch=%d %v", count, err)
		}
	})
	t.Run("persisted-owner-binding-changed", func(t *testing.T) {
		f := newPyPIMigrationPG(t)
		batch := f.start()
		intercept := &interceptedNPMMigration{Publication: f.publication}
		intercept.beforeFinal = func(ctx context.Context) error {
			_, err := f.db.ExecContext(ctx, `UPDATE nexus_migration_batches SET owner_id=$2 WHERE id=$1`, batch.ID, uuid.NewString())
			return err
		}
		f.service = nexusmigration.New(migrationpg.New(f.db), f.source, intercept, nexusmigration.Options{TargetID: "pypi-fixture-target", PoolID: "pypi-fixture-pool", HeartbeatInterval: time.Hour})
		finished := f.run(batch)
		if finished.VisibleIdentities != 0 || finished.State == "completed" {
			t.Fatalf("native transaction accepted changed persisted owner=%+v", finished)
		}
		f.noPublication()
	})
	t.Run("unbound-persisted-batch", func(t *testing.T) {
		f := newPyPIMigrationPG(t)
		batch := f.start()
		if _, err := f.db.Exec(`UPDATE nexus_migration_batches SET actor_kind='',owner_id=NULL WHERE id=$1`, batch.ID); err != nil {
			t.Fatal(err)
		}
		f.reopen()
		blocked := f.run(batch)
		if blocked.State != "blocked" || !strings.Contains(blocked.LastError, "actor_not_current_admin") {
			t.Fatalf("unbound job gained fallback authority=%+v", blocked)
		}
		f.noPublication()
	})
}

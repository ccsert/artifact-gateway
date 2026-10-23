package app

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

type migrationActorRepositoryLookup struct {
	NexusMigrationPublicationStore
	lookup func(string) (repository.HostedRepository, error)
}

func (s migrationActorRepositoryLookup) GetHostedRepository(_ context.Context, id string) (repository.HostedRepository, error) {
	return s.lookup(id)
}

type migrationActorCheck struct {
	NexusFormatMigrationPublication
	check func(string, string, string) error
}

func (p migrationActorCheck) CheckActor(_ context.Context, actor, kind, ownerID string) error {
	return p.check(actor, kind, ownerID)
}

func TestNexusMigrationActorLeavesMissingTargetsToBindingAndTargetValidation(t *testing.T) {
	missing := fmt.Errorf("target lookup: %w", repository.ErrNotFound)
	store := migrationActorRepositoryLookup{lookup: func(string) (repository.HostedRepository, error) {
		return repository.HostedRepository{}, missing
	}}
	p := NewNexusMigrationPublication(store, nil, "owned-target", "owned-pool")
	mappings := []nexusmigration.Mapping{{SourceRepository: "source", TargetRepositoryID: "missing"}}
	required, err := p.ValidateMigrationActor(t.Context(), "synthetic-admin", "", "", mappings)
	if err != nil || required {
		t.Fatalf("actor lookup masked missing-target binding validation: required=%v err=%v", required, err)
	}
	var failure *nexusmigration.Failure
	if err = p.ValidateTargets(t.Context(), mappings); !errors.As(err, &failure) || failure.Code != "target_not_active_hosted" {
		t.Fatalf("missing target passed target admission: %v", err)
	}
}

func TestNexusMigrationActorPreservesRepositoryBackendErrors(t *testing.T) {
	backend := errors.New("owned repository backend unavailable")
	store := migrationActorRepositoryLookup{lookup: func(string) (repository.HostedRepository, error) {
		return repository.HostedRepository{}, backend
	}}
	p := NewNexusMigrationPublication(store, nil, "owned-target", "owned-pool")
	required, err := p.ValidateMigrationActor(t.Context(), "synthetic-admin", "", "", []nexusmigration.Mapping{{TargetRepositoryID: "unavailable"}})
	if required || !errors.Is(err, backend) {
		t.Fatalf("backend failure was hidden: required=%v err=%v", required, err)
	}
}

func TestNexusMigrationActorChecksPyPIAfterMissingTarget(t *testing.T) {
	store := migrationActorRepositoryLookup{lookup: func(id string) (repository.HostedRepository, error) {
		if id == "missing" {
			return repository.HostedRepository{}, repository.ErrNotFound
		}
		return repository.HostedRepository{ID: id, Format: repository.FormatPyPI}, nil
	}}
	calls := 0
	denied := nexusmigration.Failed("pypi_actor_not_current_admin")
	checker := migrationActorCheck{check: func(actor, kind, ownerID string) error {
		calls++
		if actor != "api-key:owned-key" || kind != "api_key" || ownerID != "owned-key" {
			t.Fatalf("owner binding changed: %q %q %q", actor, kind, ownerID)
		}
		return denied
	}}
	p := NewNexusMigrationPublication(store, nil, "owned-target", "owned-pool").WithPyPI(checker)
	mappings := []nexusmigration.Mapping{{TargetRepositoryID: "missing"}, {TargetRepositoryID: "pypi"}}
	required, err := p.ValidateMigrationActor(t.Context(), "api-key:owned-key", "api_key", "owned-key", mappings)
	if !required || !errors.Is(err, denied) || calls != 1 {
		t.Fatalf("missing mapping bypassed PyPI authority: required=%v calls=%d err=%v", required, calls, err)
	}
}

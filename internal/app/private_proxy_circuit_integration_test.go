//go:build integration

package app

import (
	"os"
	"testing"
)

func TestPrivateProxyCircuitDoesNotFallThroughPostgres(t *testing.T) {
	privateProxyCircuitContract(t, func(t *testing.T) privateProxyCircuitBackend {
		base := newPrivateProxyPostgresBackend(t)
		newCoordinator := func() *PostgresCacheCoordinator {
			coordinator, err := NewPostgresCacheCoordinator(os.Getenv("TEST_DATABASE_URL"))
			if err != nil {
				t.Fatal(err)
			}
			return coordinator
		}
		coordinator := newCoordinator()
		t.Cleanup(func() { _ = coordinator.Close() })
		if err := coordinator.CloseCircuit(t.Context(), "https://"+redirectOrigin); err != nil {
			t.Fatal(err)
		}
		backend := privateProxyCircuitBackend{privateProxyBackend: base, coordinator: coordinator}
		backend.reopen = func() privateProxyCircuitBackend {
			backend.store, backend.cache = base.reload()
			if err := coordinator.Close(); err != nil {
				t.Fatal(err)
			}
			coordinator = newCoordinator()
			backend.coordinator = coordinator
			return backend
		}
		return backend
	})
}

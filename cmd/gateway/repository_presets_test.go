package main

import (
	"testing"

	"github.com/ccsert/artifact-gateway/internal/config"
)

func TestRepositoryPresetsOnlyRunOnEnabledAPINodes(t *testing.T) {
	for _, test := range []struct {
		mode string
		role config.NodeRole
	}{
		{"none", config.NodeRoleStandalone},
		{"standard", config.NodeRoleWorker},
		{"standard", config.NodeRoleScheduler},
	} {
		cfg := config.Config{RepositoryPresetsMode: test.mode, NodeRoles: []config.NodeRole{test.role}}
		if err := initializeRepositoryPresets(cfg, nil); err != nil {
			t.Fatalf("inactive initialization attempted database access: %v", err)
		}
	}
	for _, role := range []config.NodeRole{config.NodeRoleAPI, config.NodeRoleStandalone} {
		cfg := config.Config{RepositoryPresetsMode: "standard", NodeRoles: []config.NodeRole{role}}
		if err := initializeRepositoryPresets(cfg, nil); err == nil {
			t.Fatalf("enabled %s node ignored unavailable initialization database", role)
		}
	}
}

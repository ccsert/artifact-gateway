package backupops

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/backupmanifest"
)

func TestCleanupRequiresCreatedContainersExactConfiguredNetwork(t *testing.T) {
	for _, changed := range []bool{false, true} {
		name := "exact configured network"
		if changed {
			name = "foreign configured network"
		}
		t.Run(name, func(t *testing.T) {
			project, owner := "ag-restore-synthetic", "synthetic-owner"
			networkID, containerID := strings.Repeat("b", 64), strings.Repeat("c", 64)
			networkName := project + "-network"
			key := networkName
			if changed {
				key = "foreign-synthetic-network"
			}
			item := map[string]any{"Id": containerID, "Name": "/" + project + "-gateway", "Config": map[string]any{"Labels": map[string]string{"artifact-gateway.restore-owner": owner}}, "State": map[string]string{"Status": "created"}, "HostConfig": map[string]string{"NetworkMode": networkName}, "NetworkSettings": map[string]any{"Networks": map[string]any{key: map[string]string{"NetworkID": ""}}}, "Mounts": []any{}}
			container, _ := json.Marshal([]any{item})
			network, _ := json.Marshal([]any{map[string]any{"Id": networkID, "Name": networkName, "Labels": map[string]string{"artifact-gateway.restore-owner": owner}, "Driver": "bridge", "Scope": "local", "Internal": false}})
			dir := t.TempDir()
			log := filepath.Join(dir, "removed")
			script := "#!/bin/sh\nif [ \"$3\" = \"container\" ] && [ \"$4\" = \"inspect\" ]; then\ncat <<'FIXTURE'\n" + string(container) + "\nFIXTURE\nexit 0\nfi\nif [ \"$3\" = \"network\" ] && [ \"$4\" = \"inspect\" ]; then\ncat <<'FIXTURE'\n" + string(network) + "\nFIXTURE\nexit 0\nfi\nprintf '%s\\n' \"$*\" >> '" + strings.ReplaceAll(log, "'", "'\"'\"'") + "'\n"
			if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			target := &OwnedTarget{owner: owner, spec: TargetSpec{Project: project, Docker: Docker{Context: "synthetic"}, Release: Release{Identity: backupmanifest.GatewayIdentity{Artifact: &backupmanifest.SoftwareArtifact{Kind: "oci-image"}}}}, resources: []ownedResource{{"network", networkID, networkName}, {"container", containerID, project + "-gateway"}}}
			err := target.Cleanup(context.Background())
			data, e := os.ReadFile(log)
			if e != nil {
				t.Fatal(e)
			}
			removedContainer := strings.Contains(string(data), containerID)
			if changed && (err == nil || removedContainer) {
				t.Fatal("foreign configured network allowed container cleanup")
			}
			if !changed && (err != nil || !removedContainer) {
				t.Fatal("exact owned unstarted container was not cleaned")
			}
		})
	}
}

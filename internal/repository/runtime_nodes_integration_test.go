//go:build integration

package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresRuntimeNodeHeartbeatUpsertsCapabilities(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	started := time.Date(2026, 8, 8, 8, 0, 0, 0, time.UTC)
	instanceID := "integration-" + uuid.NewString()
	node := RuntimeNode{InstanceID: instanceID, SessionID: "session-" + uuid.NewString(), Roles: []string{"worker"}, WorkerFormats: []string{"oci"}, WorkerKinds: []string{"reclaim"}, StartedAt: started, LastSeenAt: started}
	if err := store.UpsertRuntimeNodeHeartbeat(ctx, node); err != nil {
		t.Fatal(err)
	}
	node.LastSeenAt = started.Add(time.Minute)
	node.WorkerKinds = []string{"reclaim", "replication"}
	if err := store.UpsertRuntimeNodeHeartbeat(ctx, node); err != nil {
		t.Fatal(err)
	}
	nodes, err := store.ListRuntimeNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got RuntimeNode
	for _, candidate := range nodes {
		if candidate.InstanceID == instanceID {
			got = candidate
			break
		}
	}
	if got.InstanceID == "" || !got.StartedAt.Equal(started) || !got.LastSeenAt.Equal(started.Add(time.Minute)) || len(got.WorkerKinds) != 2 || got.WorkerKinds[1] != "replication" {
		t.Fatalf("runtime node=%#v", got)
	}
	second := RuntimeNode{InstanceID: instanceID, SessionID: "integration-second-" + uuid.NewString(), Roles: []string{"worker"}, StartedAt: started.Add(time.Hour), LastSeenAt: started.Add(time.Hour)}
	if err := store.UpsertRuntimeNodeHeartbeat(ctx, second); err != nil {
		t.Fatal(err)
	}
	nodes, err = store.ListRuntimeNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var sessions int
	for _, candidate := range nodes {
		if candidate.InstanceID == instanceID {
			sessions++
		}
	}
	if sessions != 2 {
		t.Fatalf("runtime node sessions=%d nodes=%#v", sessions, nodes)
	}
	if err := store.MarkRuntimeNodeStopped(ctx, instanceID, node.SessionID, started.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
}

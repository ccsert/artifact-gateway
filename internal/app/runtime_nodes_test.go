package app

import (
	"context"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestRuntimeNodeHeartbeatRecordsStableStartAndFreshLastSeen(t *testing.T) {
	store := repository.NewMemoryStore()
	clock := time.Date(2026, 8, 8, 8, 0, 0, 0, time.UTC)
	heartbeat := &RuntimeNodeHeartbeat{
		Store: store,
		Node: repository.RuntimeNode{
			InstanceID:    "worker-01",
			SessionID:     "session-01",
			Roles:         []string{"worker"},
			WorkerFormats: []string{"oci"},
			WorkerKinds:   []string{"reclaim"},
		},
		Now: func() time.Time { return clock },
	}
	if err := heartbeat.Record(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(10 * time.Second)
	if err := heartbeat.Record(context.Background()); err != nil {
		t.Fatal(err)
	}
	nodes, err := store.ListRuntimeNodes(context.Background())
	if err != nil || len(nodes) != 1 {
		t.Fatalf("nodes=%#v err=%v", nodes, err)
	}
	if !nodes[0].StartedAt.Equal(clock.Add(-10*time.Second)) || !nodes[0].LastSeenAt.Equal(clock) {
		t.Fatalf("node timestamps=%#v", nodes[0])
	}
}

func TestRuntimeNodeHeartbeatStartStopsOnContextCancellation(t *testing.T) {
	store := repository.NewMemoryStore()
	clock := time.Date(2026, 8, 8, 8, 0, 0, 0, time.UTC)
	heartbeat := &RuntimeNodeHeartbeat{Store: store, Node: repository.RuntimeNode{InstanceID: "scheduler-01", SessionID: "session-01"}, Now: func() time.Time { return clock }}
	ctx, cancel := context.WithCancel(context.Background())
	done := heartbeat.Start(ctx, 100*time.Millisecond)
	deadline := time.Now().Add(time.Second)
	for {
		nodes, err := store.ListRuntimeNodes(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(nodes) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("heartbeat did not publish initial node")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not stop after cancellation")
	}
	clock = clock.Add(time.Hour)
	nodes, err := store.ListRuntimeNodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || !nodes[0].LastSeenAt.Equal(time.Date(2026, 8, 8, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("heartbeat continued after cancellation: %#v", nodes)
	}
}

func TestRuntimeNodeHeartbeatMarksOnlyItsSessionStopped(t *testing.T) {
	store := repository.NewMemoryStore()
	clock := time.Date(2026, 8, 8, 8, 0, 0, 0, time.UTC)
	first := &RuntimeNodeHeartbeat{Store: store, Node: repository.RuntimeNode{InstanceID: "worker-01", SessionID: "session-01"}, Now: func() time.Time { return clock }}
	second := &RuntimeNodeHeartbeat{Store: store, Node: repository.RuntimeNode{InstanceID: "worker-01", SessionID: "session-02"}, Now: func() time.Time { return clock }}
	if err := first.Record(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := second.Record(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Minute)
	if err := first.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	nodes, err := store.ListRuntimeNodes(context.Background())
	if err != nil || len(nodes) != 2 {
		t.Fatalf("nodes=%#v err=%v", nodes, err)
	}
	stopped := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		stopped[node.SessionID] = !node.StoppedAt.IsZero()
	}
	if !stopped["session-01"] || stopped["session-02"] {
		t.Fatalf("stopped sessions=%#v", stopped)
	}
}

func TestRuntimeNodeStorePrunesExpiredSessions(t *testing.T) {
	store := repository.NewMemoryStore()
	now := time.Date(2026, 8, 8, 8, 0, 0, 0, time.UTC)
	for _, node := range []repository.RuntimeNode{
		{InstanceID: "old", SessionID: "old-session", StartedAt: now.Add(-48 * time.Hour), LastSeenAt: now.Add(-48 * time.Hour)},
		{InstanceID: "fresh", SessionID: "fresh-session", StartedAt: now, LastSeenAt: now},
	} {
		if err := store.UpsertRuntimeNodeHeartbeat(context.Background(), node); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := store.PruneRuntimeNodes(context.Background(), now.Add(-24*time.Hour))
	if err != nil || deleted != 1 {
		t.Fatalf("pruned=%d err=%v", deleted, err)
	}
	nodes, err := store.ListRuntimeNodes(context.Background())
	if err != nil || len(nodes) != 1 || nodes[0].SessionID != "fresh-session" {
		t.Fatalf("remaining nodes=%#v err=%v", nodes, err)
	}
}

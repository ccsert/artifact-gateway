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
	heartbeat := &RuntimeNodeHeartbeat{Store: store, Node: repository.RuntimeNode{InstanceID: "scheduler-01"}, Now: func() time.Time { return clock }}
	ctx, cancel := context.WithCancel(context.Background())
	heartbeat.Start(ctx, 100*time.Millisecond)
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
	clock = clock.Add(time.Hour)
	time.Sleep(150 * time.Millisecond)
	nodes, err := store.ListRuntimeNodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || !nodes[0].LastSeenAt.Equal(time.Date(2026, 8, 8, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("heartbeat continued after cancellation: %#v", nodes)
	}
}

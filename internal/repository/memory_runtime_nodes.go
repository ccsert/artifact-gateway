package repository

import (
	"context"
	"sort"
	"time"
)

func cloneRuntimeNode(node RuntimeNode) RuntimeNode {
	node.Roles = append([]string{}, node.Roles...)
	node.WorkerFormats = append([]string{}, node.WorkerFormats...)
	node.WorkerKinds = append([]string{}, node.WorkerKinds...)
	return node
}

func (s *MemoryStore) UpsertRuntimeNodeHeartbeat(_ context.Context, node RuntimeNode) error {
	if err := node.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	node.StoppedAt = time.Time{}
	s.runtimeNodes[node.SessionID] = cloneRuntimeNode(node)
	s.mu.Unlock()
	return nil
}

func (s *MemoryStore) MarkRuntimeNodeStopped(_ context.Context, instanceID, sessionID string, stoppedAt time.Time) error {
	if instanceID == "" || sessionID == "" || stoppedAt.IsZero() {
		return ErrInvalidRuntimeNode
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	node, ok := s.runtimeNodes[sessionID]
	if !ok || node.InstanceID != instanceID {
		return nil
	}
	node.StoppedAt = stoppedAt
	if node.LastSeenAt.Before(stoppedAt) {
		node.LastSeenAt = stoppedAt
	}
	s.runtimeNodes[sessionID] = node
	return nil
}

func (s *MemoryStore) ListRuntimeNodes(_ context.Context) ([]RuntimeNode, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	nodes := make([]RuntimeNode, 0, len(s.runtimeNodes))
	for _, node := range s.runtimeNodes {
		nodes = append(nodes, cloneRuntimeNode(node))
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].LastSeenAt.Equal(nodes[j].LastSeenAt) {
			return nodes[i].InstanceID < nodes[j].InstanceID
		}
		return nodes[i].LastSeenAt.After(nodes[j].LastSeenAt)
	})
	return nodes, nil
}

func (s *MemoryStore) PruneRuntimeNodes(_ context.Context, before time.Time) (int64, error) {
	if before.IsZero() {
		return 0, ErrInvalidRuntimeNode
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var deleted int64
	for sessionID, node := range s.runtimeNodes {
		if node.LastSeenAt.Before(before) {
			delete(s.runtimeNodes, sessionID)
			deleted++
		}
	}
	return deleted, nil
}

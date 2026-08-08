package repository

import (
	"context"
	"sort"
)

func cloneRuntimeNode(node RuntimeNode) RuntimeNode {
	node.Roles = append([]string(nil), node.Roles...)
	node.WorkerFormats = append([]string(nil), node.WorkerFormats...)
	node.WorkerKinds = append([]string(nil), node.WorkerKinds...)
	return node
}

func (s *MemoryStore) UpsertRuntimeNodeHeartbeat(_ context.Context, node RuntimeNode) error {
	if err := node.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	s.runtimeNodes[node.InstanceID] = cloneRuntimeNode(node)
	s.mu.Unlock()
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

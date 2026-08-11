package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
)

func artifactScanLockKey(repositoryID string, format Format, coordinate, digest string) string {
	identity := strings.Join([]string{repositoryID, string(format), coordinate, digest}, "\x00")
	sum := sha256.Sum256([]byte(identity))
	return "artifact-scan:" + hex.EncodeToString(sum[:])
}

func (s *MemoryStore) LockArtifactScanIdentity(_ context.Context, repositoryID string, format Format, coordinate, digest string) (func(), error) {
	s.mu.Lock()
	if s.artifactScanLocks == nil {
		s.artifactScanLocks = make(map[string]*sync.Mutex)
	}
	s.mu.Unlock()
	return s.lockMemoryObject(s.artifactScanLocks, artifactScanLockKey(repositoryID, format, coordinate, digest))
}

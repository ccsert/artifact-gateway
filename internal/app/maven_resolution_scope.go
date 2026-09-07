package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// mavenResolutionScope describes the already-authorized candidate sequence,
// including upstream/egress settings and the current global allowlist decision.
// Only its digest enters the cache; credentials are never stored in this field.
func (h MavenHandler) mavenResolutionScope(members []repository.Member) string {
	type candidate struct {
		Member       repository.Member
		ProxyAllowed bool
	}
	candidates := make([]candidate, 0, len(members))
	for _, member := range members {
		candidates = append(candidates, candidate{Member: member, ProxyAllowed: member.Type != repository.MemberProxy || h.Cache.ProxyAllowed(member.Endpoint)})
	}
	encoded, err := json.Marshal(candidates)
	if err != nil {
		// An unrepresentable scope must never authorize an aggregate cache hit.
		return ""
	}
	sum := sha256.Sum256(encoded)
	return "v1:" + hex.EncodeToString(sum[:])
}

func (h MavenHandler) storeMavenResolution(ctx context.Context, key, path string, content CachedMavenContent, complete bool) error {
	if !complete {
		return nil
	}
	return h.Cache.Store(ctx, key, path, content)
}

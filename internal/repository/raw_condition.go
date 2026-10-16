package repository

import (
	"errors"
	"slices"
)

// ErrPreconditionFailed reports that a conditional write found a current
// reference that does not satisfy the caller's precondition.
var ErrPreconditionFailed = errors.New("write precondition failed")

// RawWriteCondition is the precondition a Raw path reference must satisfy at
// the moment it is replaced. Stores evaluate it under the same lock or
// transaction that replaces the reference, so two writers holding the same
// old validator cannot both succeed. The zero value is an unconditional write.
type RawWriteCondition struct {
	// RequireAbsent fails the write when the path has a current asset
	// (If-None-Match: *).
	RequireAbsent bool
	// RequirePresent fails the write when the path has no current asset
	// (If-Match: *).
	RequirePresent bool
	// MatchDigests, when non-empty, requires the current digest to be one of
	// them (If-Match with entity tags).
	MatchDigests []string
	// NoneMatchDigests fails the write when the current digest is one of them
	// (If-None-Match with entity tags).
	NoneMatchDigests []string
}

// Unconditional reports whether the condition accepts every current state.
func (c RawWriteCondition) Unconditional() bool {
	return !c.RequireAbsent && !c.RequirePresent && len(c.MatchDigests) == 0 && len(c.NoneMatchDigests) == 0
}

// Allows evaluates the condition against the path's current reference.
func (c RawWriteCondition) Allows(current RawAsset, exists bool) bool {
	if !exists {
		return !c.RequirePresent && len(c.MatchDigests) == 0
	}
	if c.RequireAbsent {
		return false
	}
	if len(c.MatchDigests) > 0 && !slices.Contains(c.MatchDigests, current.Digest) {
		return false
	}
	return !slices.Contains(c.NoneMatchDigests, current.Digest)
}

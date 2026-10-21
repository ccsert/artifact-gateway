package repository

import (
	"encoding/json"
	"sort"
	"time"

	discovery "github.com/ccsert/artifact-gateway/internal/ocidiscovery"
)

func ociBindingKey(scope, owner, image, subject string) string {
	return scope + "\x00" + owner + "\x00" + image + "\x00" + subject
}

func sortedOCIGuards(guards []discovery.Guard) []discovery.Guard {
	copy := append([]discovery.Guard(nil), guards...)
	// Group edits lock the Group before changing member references. Keep this
	// lock order, then repository/grant IDs, across every discovery transaction.
	sort.Slice(copy, func(i, j int) bool {
		rank := map[string]int{"group": 0, "repository": 1, "grants": 2}
		if rank[copy[i].Kind] != rank[copy[j].Kind] {
			return rank[copy[i].Kind] < rank[copy[j].Kind]
		}
		return copy[i].ID < copy[j].ID
	})
	return copy
}

func mergeOCIBinding(old, next discovery.Binding) (discovery.Binding, error) {
	if old.Owner != next.Owner || old.Actor != next.Actor || old.Anonymous != next.Anonymous || old.SourceID != next.SourceID || old.SourceVersion != next.SourceVersion {
		return discovery.Binding{}, discovery.ErrChanged
	}
	byDigest := map[string]discovery.Object{}
	for _, object := range old.Objects {
		byDigest[object.Digest] = object
	}
	for _, object := range next.Objects {
		if prior, found := byDigest[object.Digest]; found && prior != object {
			return discovery.Binding{}, discovery.ErrChanged
		}
		byDigest[object.Digest] = object
	}
	next.Objects = make([]discovery.Object, 0, len(byDigest))
	for _, object := range byDigest {
		next.Objects = append(next.Objects, object)
	}
	sort.Slice(next.Objects, func(i, j int) bool { return next.Objects[i].Digest < next.Objects[j].Digest })
	if err := discovery.ValidateBinding(next); err != nil {
		return discovery.Binding{}, err
	}
	return next, nil
}

func sameOCISnapshot(a, b discovery.Snapshot) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func ociDiscoveryAudit(actor, scope, image, subject, operation string) AuditRecord {
	return AuditRecord{Actor: actor, Repository: scope, GroupName: scope, Format: "oci", Resource: image, Representation: subject, Operation: operation, Status: 200, Outcome: AuditResolved, OccurredAt: time.Now().UTC(), CacheDisposition: "discovery"}
}

func ociSnapshotAudit(s discovery.Snapshot) AuditRecord {
	if s.Kind == "referrers" {
		return ociDiscoveryAudit(s.Actor, s.Scope, s.Image, s.Subject, "oci.referrers.snapshot")
	}
	return ociDiscoveryAudit(s.Actor, s.Scope, "_catalog", "", "oci.catalog.snapshot")
}

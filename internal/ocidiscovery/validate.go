package ocidiscovery

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/ccsert/artifact-gateway/internal/ocigraph"
	"github.com/google/uuid"
)

func ValidateGuards(guards []Guard) error {
	if len(guards) > MaxSources*3 {
		return ErrLimit
	}
	seen := map[string]bool{}
	for _, guard := range guards {
		_, idErr := uuid.Parse(guard.ID)
		version, versionErr := strconv.ParseUint(guard.Version, 10, 63)
		key := guard.Kind + ":" + guard.ID
		if idErr != nil || versionErr != nil || version == 0 || seen[key] || guard.Kind != "group" && guard.Kind != "repository" && guard.Kind != "grants" {
			return ErrInvalid
		}
		seen[key] = true
	}
	return nil
}

func ValidateSnapshot(s Snapshot, now time.Time) error {
	if _, err := uuid.Parse(s.ID); err != nil || s.Owner == "" || len(s.Owner) > 128 || s.Actor == "" || s.Kind != "catalog" && s.Kind != "referrers" || !s.ExpiresAt.After(now) || s.ExpiresAt.After(now.Add(SnapshotTTL+time.Second)) {
		return ErrInvalid
	}
	if len(s.Entries) > MaxNames || len(s.Referrers) > MaxReferrers || len(s.Coverage) > MaxSources+1 {
		return ErrLimit
	}
	if err := ValidateGuards(s.Guards); err != nil {
		return err
	}
	if s.Kind == "referrers" {
		kind, id, ok := strings.Cut(s.Scope, ":")
		_, idErr := uuid.Parse(id)
		if !ok || idErr != nil || kind != "repository" && kind != "group" || !ocigraph.ValidName(s.Image) || !ocigraph.ValidDigest(s.Subject) || len(s.Entries) != 0 || len(s.ArtifactType) > 512 {
			return ErrInvalid
		}
		last := ""
		for _, ref := range s.Referrers {
			if !ocigraph.ValidDigest(ref.Digest) || ref.Digest <= last || ref.Size < 0 || !ocigraph.IsManifest(ref.MediaType) && !ocigraph.IsIndex(ref.MediaType) {
				return ErrInvalid
			}
			last = ref.Digest
		}
	}
	last := ""
	for _, entry := range s.Entries {
		if !ocigraph.ValidName(entry.Name) || entry.Name <= last || len(entry.Sources) == 0 || len(entry.Sources) > MaxSources {
			return ErrInvalid
		}
		last = entry.Name
	}
	for _, coverage := range s.Coverage {
		if coverage.State != "complete" && coverage.State != "partial" && coverage.State != "unsupported" {
			return ErrInvalid
		}
	}
	_, err := Payload(s)
	return err
}

func ValidateBinding(b Binding) error {
	kind, id, found := strings.Cut(b.Scope, ":")
	_, idErr := uuid.Parse(id)
	_, sourceErr := uuid.Parse(b.SourceID)
	if !found || idErr != nil || sourceErr != nil || b.Actor == "" || b.Owner != OwnerForActor(b.Actor) || b.FetchedAt.IsZero() || kind != "repository" && kind != "group" || !ocigraph.ValidName(b.Image) || !ocigraph.ValidDigest(b.Subject) || len(b.Objects) == 0 || len(b.Objects) > ocigraph.MaxObjects || len(b.Referrers) > MaxReferrers {
		return ErrInvalid
	}
	if err := ValidateGuards(b.Guards); err != nil {
		return err
	}
	sourceGuard := false
	scopeGuard := kind == "repository" && id == b.SourceID
	for _, guard := range b.Guards {
		if guard.Kind == "repository" && guard.ID == b.SourceID && guard.Version == b.SourceVersion {
			sourceGuard = true
		}
		if kind == "group" && guard.Kind == "group" && guard.ID == id {
			scopeGuard = true
		}
	}
	if !sourceGuard || !scopeGuard {
		return ErrInvalid
	}
	objects := map[string]Object{}
	for _, object := range b.Objects {
		cacheDigest := strings.TrimSuffix(strings.TrimPrefix(object.CacheKey, "oci/index/"), ".json")
		if !ocigraph.ValidDigest(object.Digest) || object.Size < 0 || object.MediaType == "" || len(object.MediaType) > 512 || object.Resource != "manifests" && object.Resource != "blobs" || !strings.HasPrefix(object.CacheKey, "oci/index/") || !strings.HasSuffix(object.CacheKey, ".json") || !ocigraph.ValidDigest("sha256:"+cacheDigest) || objects[object.Digest].Digest != "" {
			return ErrInvalid
		}
		objects[object.Digest] = object
	}
	if objects[b.Subject].Resource != "manifests" {
		return ErrInvalid
	}
	for _, referrer := range b.Referrers {
		object, found := objects[referrer.Digest]
		if !found || object.Resource != "manifests" || object.Size != referrer.Size || object.MediaType != referrer.MediaType || referrer.Digest == b.Subject {
			return ErrInvalid
		}
	}
	_, err := Payload(b)
	return err
}

// DecodeIndex is intentionally separate from migration's image-only graph
// decoder: an OCI referrer descriptor may have an arbitrary artifactType.
func DecodeIndex(body []byte) ([]Descriptor, error) {
	var index struct {
		SchemaVersion int          `json:"schemaVersion"`
		MediaType     string       `json:"mediaType"`
		Manifests     []Descriptor `json:"manifests"`
	}
	if len(body) > ocigraph.MaxManifestBytes || json.Unmarshal(body, &index) != nil || index.SchemaVersion != 2 || index.MediaType != "" && index.MediaType != ocigraph.OCIIndex || index.Manifests == nil {
		return nil, ErrInvalid
	}
	if len(index.Manifests) > MaxReferrers {
		return nil, ErrLimit
	}
	var raw struct {
		Manifests []wireDescriptor `json:"manifests"`
	}
	if json.Unmarshal(body, &raw) != nil {
		return nil, ErrInvalid
	}
	for i, descriptor := range index.Manifests {
		if _, err := descriptorRequirement(raw.Manifests[i], "manifests"); err != nil {
			return nil, err
		}
		if !ocigraph.ValidDigest(descriptor.Digest) || descriptor.Size < 0 || !ocigraph.IsManifest(descriptor.MediaType) && !ocigraph.IsIndex(descriptor.MediaType) {
			return nil, ErrInvalid
		}
	}
	return index.Manifests, nil
}

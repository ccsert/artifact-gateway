package nexusmigration

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"sort"
	"strings"

	"github.com/ccsert/artifact-gateway/internal/nexussource"
	"github.com/ccsert/artifact-gateway/internal/ocigraph"
	"github.com/google/uuid"
)

type ociInventory struct {
	asset nexussource.Asset
	path  nexussource.OCIAssetPath
}

type ociReadManifest struct {
	asset    Asset
	document ocigraph.Document
}

// planOCIEntries seals each public source tag/digest with its complete immutable
// graph. Manifest reads count as source verification, never transferred bytes.
func (s *Service) planOCIEntries(ctx context.Context, b Batch, sourceRepo, targetRepo string, inventory []nexussource.Asset, failure *ScanFailure) ([]Entry, error) {
	items := make([]ociInventory, 0, len(inventory))
	manifests, blobs := map[string][]ociInventory{}, map[string][]ociInventory{}
	entries := []Entry{}
	unsupported := func(sourcePath, identity, reason string) {
		entries = append(entries, Entry{ID: uuid.NewString(), BatchID: b.ID, SourceRepository: sourceRepo, TargetRepositoryID: targetRepo, Format: "oci", Identity: identity, State: "unsupported", Assets: []Asset{}, LastError: reason})
	}
	sort.Slice(inventory, func(i, j int) bool { return inventory[i].Path < inventory[j].Path })
	for _, asset := range inventory {
		canonical, _, reason := nexussource.OCISourcePath(asset.Path)
		if reason != "" || asset.Repository != sourceRepo {
			unsupported(asset.Path, "/unsupported/"+asset.Path, "oci_source_path_unsupported")
			continue
		}
		asset.Path = canonical
		parsed, _ := nexussource.ParseOCIAssetPath(canonical)
		digest := "sha256:" + asset.Checksum["sha256"]
		if !ocigraph.ValidDigest(digest) || asset.Size < 0 || asset.Size > 1<<30 || (ocigraph.ValidDigest(parsed.Reference) && parsed.Reference != digest) {
			unsupported(asset.Path, "/unsupported/"+asset.Path, "oci_inventory_digest_required")
			continue
		}
		item := ociInventory{asset: asset, path: parsed}
		items = append(items, item)
		if parsed.Kind == "manifests" {
			manifests[parsed.Name+"\x00"+digest] = append(manifests[parsed.Name+"\x00"+digest], item)
		} else {
			blobs[digest] = append(blobs[digest], item)
		}
	}
	cache := map[string]ociReadManifest{}
	var manifestBytes int64
	used := map[string]bool{}
	for _, root := range items {
		if root.path.Kind != "manifests" {
			continue
		}
		identity := root.path.Name + ":" + root.path.Reference
		if ocigraph.ValidDigest(root.path.Reference) {
			identity = root.path.Name + "@" + root.path.Reference
		}
		failure.SourceRepository, failure.SourcePath, failure.Identity = sourceRepo, root.asset.Path, identity
		rootDigest := "sha256:" + root.asset.Checksum["sha256"]
		assets := map[string]Asset{}
		objects := []ocigraph.Object{}
		active := map[string]bool{}
		var visit func(ociInventory, string, int) error
		visit = func(item ociInventory, wantType string, depth int) error {
			if depth > ocigraph.MaxDepth {
				return ocigraph.ErrLimit
			}
			digest := "sha256:" + item.asset.Checksum["sha256"]
			if active[digest] {
				return ocigraph.ErrInvalid
			}
			if existing, ok := assets[digest]; ok {
				if wantType != "" && existing.MediaType != wantType {
					return ocigraph.ErrInvalid
				}
				return nil
			}
			if len(assets) >= ocigraph.MaxObjects {
				return ocigraph.ErrLimit
			}
			active[digest] = true
			defer delete(active, digest)
			if err := s.ociScanAdmission(ctx, b); err != nil {
				return err
			}
			read, ok := cache[item.asset.Path]
			if !ok {
				if item.asset.Size > ocigraph.MaxManifestBytes || item.asset.Size > ocigraph.MaxInventoryManifestBytes-manifestBytes {
					return ocigraph.ErrLimit
				}
				failure.Stage = "source_verification_open"
				content, err := s.source.OpenAsset(ctx, b.SourceID, b.SourceVersion, sourceRepo, AssetRequest{Format: "oci", Path: item.asset.Path, Size: item.asset.Size, Validator: digest})
				if err != nil {
					return err
				}
				failure.Stage = "source_verification_read"
				var body bytes.Buffer
				written, readErr := io.Copy(io.MultiWriter(&body, &scanByteWriter{ctx: ctx, store: s.store, batch: b.ID, token: b.LeaseToken}), io.LimitReader(content.Body, min(item.asset.Size, ocigraph.MaxManifestBytes)+1))
				closeErr := content.Body.Close()
				if item.asset.Size > ocigraph.MaxManifestBytes {
					return ocigraph.ErrLimit
				}
				if readErr != nil {
					return readErr
				}
				if closeErr != nil || written != item.asset.Size || ocigraph.Digest(body.Bytes()) != digest {
					return Failed("source_changed")
				}
				manifestBytes += written
				failure.Stage = "oci_graph_validation"
				document, err := ocigraph.Decode(body.Bytes(), content.MediaType)
				if err != nil {
					return err
				}
				validator := content.Validator
				if validator == "" {
					validator = digest
				}
				read = ociReadManifest{asset: Asset{SourcePath: item.asset.Path, TargetPath: root.path.Name + "/manifests/" + digest, Size: item.asset.Size, SHA256: item.asset.Checksum["sha256"], Validator: validator, MediaType: document.MediaType}, document: document}
				cache[item.asset.Path] = read
			}
			if wantType != "" && read.document.MediaType != wantType {
				return ocigraph.ErrInvalid
			}
			assets[digest] = read.asset
			objects = append(objects, ocigraph.Object{Digest: digest, Size: read.asset.Size, MediaType: read.asset.MediaType, Document: &read.document})
			used[item.asset.Path] = true
			for _, descriptor := range read.document.Dependencies() {
				var candidates []ociInventory
				if ocigraph.IsIndex(read.document.MediaType) {
					candidates = manifests[root.path.Name+"\x00"+descriptor.Digest]
				} else {
					candidates = blobs[descriptor.Digest]
				}
				if len(candidates) == 0 {
					return ocigraph.ErrIncomplete
				}
				candidate := candidates[0]
				for _, alias := range candidates {
					if alias.asset.Size != candidate.asset.Size {
						return ocigraph.ErrInvalid
					}
					used[alias.asset.Path] = true
				}
				if descriptor.Size == nil || candidate.asset.Size != *descriptor.Size {
					return ocigraph.ErrInvalid
				}
				if ocigraph.IsIndex(read.document.MediaType) {
					if err := visit(candidate, descriptor.MediaType, depth+1); err != nil {
						return err
					}
					continue
				}
				if old, exists := assets[descriptor.Digest]; exists {
					if old.Size != *descriptor.Size || old.MediaType != descriptor.MediaType {
						return ocigraph.ErrInvalid
					}
					continue
				}
				if len(assets) >= ocigraph.MaxObjects {
					return ocigraph.ErrLimit
				}
				assets[descriptor.Digest] = Asset{SourcePath: candidate.asset.Path, TargetPath: "blobs/" + descriptor.Digest, Size: candidate.asset.Size, SHA256: strings.TrimPrefix(descriptor.Digest, "sha256:"), Validator: descriptor.Digest, MediaType: descriptor.MediaType}
				objects = append(objects, ocigraph.Object{Digest: descriptor.Digest, Size: candidate.asset.Size, MediaType: descriptor.MediaType})
				used[candidate.asset.Path] = true
			}
			return nil
		}
		err := visit(root, "", 0)
		if err == nil {
			err = ocigraph.Validate(rootDigest, objects)
		}
		if err != nil {
			reason := "oci_graph_invalid"
			switch {
			case errors.Is(err, ocigraph.ErrUnsupported):
				reason = "oci_media_type_unsupported"
			case errors.Is(err, ocigraph.ErrIncomplete):
				reason = "oci_dependency_missing"
			case errors.Is(err, ocigraph.ErrLimit):
				reason = "oci_graph_limit_exceeded"
			case !errors.Is(err, ocigraph.ErrInvalid):
				return nil, err
			}
			unsupported(root.asset.Path, identity, reason)
			continue
		}
		entry := Entry{ID: uuid.NewString(), BatchID: b.ID, SourceRepository: sourceRepo, TargetRepositoryID: targetRepo, Format: "oci", Identity: identity, State: "pending", Assets: make([]Asset, 0, len(assets))}
		for _, asset := range assets {
			entry.Assets = append(entry.Assets, asset)
		}
		sort.Slice(entry.Assets, func(i, j int) bool { return entry.Assets[i].TargetPath < entry.Assets[j].TargetPath })
		entries = append(entries, entry)
	}
	for _, item := range items {
		if item.path.Kind == "blobs" && !used[item.asset.Path] {
			unsupported(item.asset.Path, "/unsupported/"+item.asset.Path, "oci_unreferenced_inventory_object")
		}
	}
	return entries, nil
}

func (s *Service) ociScanAdmission(ctx context.Context, b Batch) error {
	current, err := s.store.Get(ctx, b.ID)
	if err != nil {
		return err
	}
	if current.CancelRequested || current.PauseRequested {
		return ErrStopped
	}
	return s.ValidateEvidence(b.CapacityEvidence)
}

// OCIEntryRoot derives only a reference explicitly present in the fixed source
// inventory. It never substitutes a single-platform child for the original root.
func OCIEntryRoot(entry Entry) (name, reference, digest string, err error) {
	name, reference, ok := nexussource.ParseOCIIdentity(entry.Identity)
	if !ok || entry.Format != "oci" {
		return "", "", "", Failed("invalid_oci_identity")
	}
	rootPath := "v2/" + name + "/manifests/" + reference
	for _, asset := range entry.Assets {
		if asset.SourcePath == rootPath {
			digest = "sha256:" + asset.SHA256
			if !ocigraph.ValidDigest(digest) || asset.TargetPath != name+"/manifests/"+digest {
				return "", "", "", Failed("invalid_oci_identity")
			}
			if _, decodeErr := hex.DecodeString(asset.SHA256); decodeErr != nil {
				return "", "", "", Failed("invalid_oci_identity")
			}
			return name, reference, digest, nil
		}
	}
	return "", "", "", Failed("oci_root_missing")
}

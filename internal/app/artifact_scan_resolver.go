package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/scanning"
)

const maxOCIManifestBytes = 8 << 20

// NativeArtifactScanResolver maps protocol-native metadata to the common
// scanner input. It is deliberately read-only; publication and retention stay
// behind their existing repository interfaces.
type NativeArtifactScanResolver struct {
	Maven   repository.NativeMavenStore
	OCI     repository.NativeOCIStore
	Raw     repository.NativeRawStore
	NPM     repository.NativeNPMStore
	PyPI    repository.NativePyPIStore
	Go      repository.NativeGoStore
	Conan   repository.NativeConanStore
	Objects OCIObjectStore
}

// NewNativeArtifactScanResolver adapts a store implementing the native format
// interfaces. Missing interfaces simply make that format unavailable rather
// than weakening validation for formats that are present.
func NewNativeArtifactScanResolver(store any, bytes OCIObjectStore) *NativeArtifactScanResolver {
	resolver := &NativeArtifactScanResolver{Objects: bytes}
	resolver.Maven, _ = store.(repository.NativeMavenStore)
	resolver.OCI, _ = store.(repository.NativeOCIStore)
	resolver.Raw, _ = store.(repository.NativeRawStore)
	resolver.NPM, _ = store.(repository.NativeNPMStore)
	resolver.PyPI, _ = store.(repository.NativePyPIStore)
	resolver.Go, _ = store.(repository.NativeGoStore)
	resolver.Conan, _ = store.(repository.NativeConanStore)
	return resolver
}

func (r *NativeArtifactScanResolver) ResolveArtifactScan(ctx context.Context, repositoryID string, payload repository.ArtifactScanPayload) (scanning.Artifact, error) {
	if r == nil || r.Objects == nil {
		return scanning.Artifact{}, errors.New("artifact object store is unavailable")
	}
	switch payload.Format {
	case repository.FormatRaw:
		return r.resolveRaw(ctx, repositoryID, payload)
	case repository.FormatOCI:
		return r.resolveOCI(ctx, repositoryID, payload)
	case repository.FormatMaven:
		return r.resolveMaven(ctx, repositoryID, payload)
	case repository.FormatNPM:
		return r.resolveNPM(ctx, repositoryID, payload)
	case repository.FormatPyPI:
		return r.resolvePyPI(ctx, repositoryID, payload)
	case repository.FormatGo:
		return r.resolveGo(ctx, repositoryID, payload)
	case repository.FormatConan:
		return r.resolveConan(ctx, repositoryID, payload)
	default:
		return scanning.Artifact{}, fmt.Errorf("format %q cannot be scanned", payload.Format)
	}
}

func (r *NativeArtifactScanResolver) resolveRaw(ctx context.Context, repositoryID string, payload repository.ArtifactScanPayload) (scanning.Artifact, error) {
	if r.Raw == nil {
		return scanning.Artifact{}, errors.New("raw repository store is unavailable")
	}
	asset, err := r.Raw.GetRawAsset(ctx, repositoryID, payload.Coordinate)
	if err != nil {
		return scanning.Artifact{}, err
	}
	if asset.Digest != payload.Digest {
		return scanning.Artifact{}, errors.New("raw asset digest changed")
	}
	return scanning.Artifact{RepositoryID: repositoryID, Format: payload.Format, Coordinate: payload.Coordinate, Digest: payload.Digest, Assets: []scanning.Asset{r.asset(asset.Path, asset.ObjectKey, asset.Digest, asset.Size, asset.ContentType)}}, nil
}

func (r *NativeArtifactScanResolver) resolveMaven(ctx context.Context, repositoryID string, payload repository.ArtifactScanPayload) (scanning.Artifact, error) {
	if r.Maven == nil {
		return scanning.Artifact{}, errors.New("maven repository store is unavailable")
	}
	artifacts, err := r.Maven.ListMavenArtifacts(ctx, repositoryID)
	if err != nil {
		return scanning.Artifact{}, err
	}
	var selected repository.MavenArtifact
	for _, candidate := range artifacts {
		if candidate.Coordinate == payload.Coordinate && candidate.Digest == payload.Digest {
			selected = candidate
			break
		}
	}
	if selected.ID == "" {
		return scanning.Artifact{}, repository.ErrNotFound
	}
	assets, err := r.Maven.ListMavenAssets(ctx, repositoryID, payload.Coordinate)
	if err != nil {
		return scanning.Artifact{}, err
	}
	resolved := make([]scanning.Asset, 0, len(assets))
	for _, asset := range assets {
		if !mavenAssetBelongsToBuild(asset, selected) {
			continue
		}
		resolved = append(resolved, r.asset(asset.Path, asset.ObjectKey, asset.Digest, asset.Size, "application/octet-stream"))
	}
	if len(resolved) == 0 {
		return scanning.Artifact{}, repository.ErrNotFound
	}
	return scanning.Artifact{RepositoryID: repositoryID, Format: payload.Format, Coordinate: payload.Coordinate, Digest: payload.Digest, Assets: resolved}, nil
}

func (r *NativeArtifactScanResolver) resolveOCI(ctx context.Context, repositoryID string, payload repository.ArtifactScanPayload) (scanning.Artifact, error) {
	if r.OCI == nil {
		return scanning.Artifact{}, errors.New("oci repository store is unavailable")
	}
	manifest, err := r.OCI.GetOCIManifest(ctx, repositoryID, payload.Coordinate, payload.Digest)
	if err != nil {
		return scanning.Artifact{}, err
	}
	if manifest.Digest != payload.Digest {
		return scanning.Artifact{}, errors.New("oci manifest digest changed")
	}
	assets := make([]scanning.Asset, 0, 8)
	seen := make(map[string]bool)
	if err := r.appendOCIManifest(ctx, repositoryID, manifest.Name, manifest, &assets, seen); err != nil {
		return scanning.Artifact{}, err
	}
	return scanning.Artifact{RepositoryID: repositoryID, Format: payload.Format, Coordinate: payload.Coordinate, Digest: payload.Digest, Assets: assets}, nil
}

type scanOCIDescriptor struct {
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	MediaType string `json:"mediaType"`
}

type scanOCIDocument struct {
	Config    *scanOCIDescriptor  `json:"config,omitempty"`
	Layers    []scanOCIDescriptor `json:"layers,omitempty"`
	Manifests []scanOCIDescriptor `json:"manifests,omitempty"`
}

func (r *NativeArtifactScanResolver) appendOCIManifest(ctx context.Context, repositoryID, name string, manifest repository.OCIManifest, assets *[]scanning.Asset, seen map[string]bool) error {
	if seen[manifest.Digest] {
		return nil
	}
	seen[manifest.Digest] = true
	*assets = append(*assets, r.asset(name+"/manifest/"+manifest.Digest, manifest.ObjectKey, manifest.Digest, manifest.Size, manifest.MediaType))
	body, err := r.Objects.Get(ctx, manifest.ObjectKey)
	if err != nil {
		return err
	}
	if len(body) > maxOCIManifestBytes {
		return errors.New("oci manifest exceeds resolver limit")
	}
	var document scanOCIDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return fmt.Errorf("decode oci manifest: %w", err)
	}
	descriptors := append([]scanOCIDescriptor(nil), document.Layers...)
	if document.Config != nil {
		descriptors = append([]scanOCIDescriptor{*document.Config}, descriptors...)
	}
	for _, descriptor := range descriptors {
		if descriptor.Digest == "" {
			continue
		}
		if seen[descriptor.Digest] {
			continue
		}
		seen[descriptor.Digest] = true
		blob, err := r.OCI.GetOCIBlob(ctx, repositoryID, descriptor.Digest)
		if err != nil {
			return err
		}
		if blob.Digest != descriptor.Digest {
			return errors.New("oci blob digest changed")
		}
		*assets = append(*assets, r.asset(name+"/blob/"+descriptor.Digest, blob.ObjectKey, blob.Digest, blob.Size, descriptor.MediaType))
	}
	for _, descriptor := range document.Manifests {
		if descriptor.Digest == "" {
			continue
		}
		child, err := r.OCI.GetOCIManifest(ctx, repositoryID, name, descriptor.Digest)
		if err != nil {
			return err
		}
		if child.Digest != descriptor.Digest {
			return errors.New("oci child manifest digest changed")
		}
		if err := r.appendOCIManifest(ctx, repositoryID, name, child, assets, seen); err != nil {
			return err
		}
	}
	return nil
}

func (r *NativeArtifactScanResolver) resolveNPM(ctx context.Context, repositoryID string, payload repository.ArtifactScanPayload) (scanning.Artifact, error) {
	if r.NPM == nil {
		return scanning.Artifact{}, errors.New("npm repository store is unavailable")
	}
	name, version, ok := splitVersionCoordinate(payload.Coordinate)
	if !ok {
		return scanning.Artifact{}, errors.New("npm coordinate must be package@version")
	}
	item, err := r.NPM.GetNPMVersion(ctx, repositoryID, name, version)
	if err != nil {
		return scanning.Artifact{}, err
	}
	if item.Digest != payload.Digest || item.ObjectKey == "" {
		return scanning.Artifact{}, errors.New("npm tarball is unavailable or digest changed")
	}
	path := item.TarballName
	if path == "" {
		path = name + "-" + version + ".tgz"
	}
	return singleAssetArtifact(repositoryID, payload, r.asset(path, item.ObjectKey, item.Digest, item.Size, "application/gzip")), nil
}

func (r *NativeArtifactScanResolver) resolvePyPI(ctx context.Context, repositoryID string, payload repository.ArtifactScanPayload) (scanning.Artifact, error) {
	if r.PyPI == nil {
		return scanning.Artifact{}, errors.New("pypi repository store is unavailable")
	}
	project, version, ok := splitVersionCoordinate(payload.Coordinate)
	if !ok {
		return scanning.Artifact{}, errors.New("pypi coordinate must be project@version")
	}
	files, err := r.PyPI.ListPyPIProjectFiles(ctx, repositoryID, project)
	if err != nil {
		return scanning.Artifact{}, err
	}
	for _, file := range files {
		if file.Version == version && file.Digest == payload.Digest && file.ObjectKey != "" {
			return singleAssetArtifact(repositoryID, payload, r.asset(file.Filename, file.ObjectKey, file.Digest, file.Size, "application/octet-stream")), nil
		}
	}
	return scanning.Artifact{}, repository.ErrNotFound
}

func (r *NativeArtifactScanResolver) resolveGo(ctx context.Context, repositoryID string, payload repository.ArtifactScanPayload) (scanning.Artifact, error) {
	if r.Go == nil {
		return scanning.Artifact{}, errors.New("go repository store is unavailable")
	}
	module, version, ok := splitVersionCoordinate(payload.Coordinate)
	if !ok {
		return scanning.Artifact{}, errors.New("go coordinate must be module@version")
	}
	if _, err := r.Go.GetGoModuleVersion(ctx, repositoryID, module, version); err != nil {
		return scanning.Artifact{}, err
	}
	assets := make([]scanning.Asset, 0, 3)
	matched := false
	for _, kind := range []string{"info", "mod", "zip"} {
		item, err := r.Go.GetGoModuleAsset(ctx, repositoryID, module, version, kind)
		if errors.Is(err, repository.ErrNotFound) {
			continue
		}
		if err != nil {
			return scanning.Artifact{}, err
		}
		if item.Digest == payload.Digest {
			matched = true
		}
		if item.ObjectKey != "" {
			assets = append(assets, r.asset(kind, item.ObjectKey, item.Digest, item.Size, "application/octet-stream"))
		}
	}
	if !matched || len(assets) == 0 {
		return scanning.Artifact{}, repository.ErrNotFound
	}
	return scanning.Artifact{RepositoryID: repositoryID, Format: payload.Format, Coordinate: payload.Coordinate, Digest: payload.Digest, Assets: assets}, nil
}

func (r *NativeArtifactScanResolver) resolveConan(ctx context.Context, repositoryID string, payload repository.ArtifactScanPayload) (scanning.Artifact, error) {
	if r.Conan == nil {
		return scanning.Artifact{}, errors.New("conan repository store is unavailable")
	}
	parts := strings.Split(payload.Coordinate, "#")
	if len(parts) == 2 {
		revision, err := r.Conan.GetConanRecipeRevision(ctx, repositoryID, parts[0], parts[1])
		if err != nil || revision.State != "visible" || revision.Digest != payload.Digest {
			if err != nil {
				return scanning.Artifact{}, err
			}
			return scanning.Artifact{}, errors.New("conan recipe is unavailable or digest changed")
		}
		assets, err := r.Conan.ListConanRecipeAssets(ctx, repositoryID, parts[0], parts[1])
		return r.conanAssets(repositoryID, payload, assets, err)
	}
	if len(parts) == 3 {
		packageParts := strings.SplitN(parts[1], "/", 2)
		if len(packageParts) != 2 {
			return scanning.Artifact{}, errors.New("invalid conan package coordinate")
		}
		revision, err := r.Conan.GetConanPackageRevision(ctx, repositoryID, parts[0], packageParts[0], packageParts[1], parts[2])
		if err != nil || revision.State != "visible" || revision.Digest != payload.Digest {
			if err != nil {
				return scanning.Artifact{}, err
			}
			return scanning.Artifact{}, errors.New("conan package is unavailable or digest changed")
		}
		assets, err := r.Conan.ListConanPackageAssets(ctx, repositoryID, parts[0], packageParts[0], packageParts[1], parts[2])
		return r.conanAssets(repositoryID, payload, assets, err)
	}
	return scanning.Artifact{}, errors.New("invalid conan coordinate")
}

func (r *NativeArtifactScanResolver) conanAssets(repositoryID string, payload repository.ArtifactScanPayload, assets []repository.ConanAsset, err error) (scanning.Artifact, error) {
	if err != nil {
		return scanning.Artifact{}, err
	}
	resolved := make([]scanning.Asset, 0, len(assets))
	for _, asset := range assets {
		resolved = append(resolved, r.asset(asset.Path, asset.ObjectKey, asset.Digest, asset.Size, "application/octet-stream"))
	}
	if len(resolved) == 0 {
		return scanning.Artifact{}, repository.ErrNotFound
	}
	return scanning.Artifact{RepositoryID: repositoryID, Format: payload.Format, Coordinate: payload.Coordinate, Digest: payload.Digest, Assets: resolved}, nil
}

func (r *NativeArtifactScanResolver) asset(path, key, digest string, size int64, mediaType string) scanning.Asset {
	return scanning.Asset{Path: path, Digest: digest, Size: size, MediaType: mediaType, Open: func(ctx context.Context) (io.ReadCloser, error) {
		reader, _, err := r.Objects.Open(ctx, key)
		return reader, err
	}}
}

func singleAssetArtifact(repositoryID string, payload repository.ArtifactScanPayload, asset scanning.Asset) scanning.Artifact {
	return scanning.Artifact{RepositoryID: repositoryID, Format: payload.Format, Coordinate: payload.Coordinate, Digest: payload.Digest, Assets: []scanning.Asset{asset}}
}

func splitVersionCoordinate(coordinate string) (string, string, bool) {
	index := strings.LastIndex(coordinate, "@")
	if index <= 0 || index >= len(coordinate)-1 {
		return "", "", false
	}
	return coordinate[:index], coordinate[index+1:], true
}

func mavenAssetBelongsToBuild(asset repository.MavenAsset, artifact repository.MavenArtifact) bool {
	parts := strings.Split(artifact.Coordinate, ":")
	if len(parts) != 3 || !strings.HasPrefix(asset.Path, strings.ReplaceAll(parts[0], ".", "/")+"/"+parts[1]+"/"+parts[2]+"/") {
		return false
	}
	if artifact.BuildNumber == 0 {
		return true
	}
	base := strings.TrimSuffix(parts[2], "-SNAPSHOT")
	prefix := parts[1] + "-" + base + "-" + artifact.CreatedAt.UTC().Format("20060102.150405") + "-" + strconv.Itoa(artifact.BuildNumber)
	return strings.Contains(asset.Path, "/"+prefix)
}

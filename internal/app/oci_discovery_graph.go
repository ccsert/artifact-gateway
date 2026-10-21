package app

import (
	"context"
	"errors"
	"io"
	"maps"
	"mime"
	"net/http"
	"sort"

	discovery "github.com/ccsert/artifact-gateway/internal/ocidiscovery"
	"github.com/ccsert/artifact-gateway/internal/ocigraph"
	ociprotocol "github.com/ccsert/artifact-gateway/internal/protocol/oci"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/upstreamauth"
)

const discoveryMaxBlobBytes int64 = 64 << 20
const discoveryMaxClosureBytes int64 = 256 << 20

type limitedOCIResponse struct {
	io.Reader
	io.Closer
}

func (h nativeOCIHandler) discoveryObject(ctx context.Context, repo repository.HostedRepository, image, resource string, expected discovery.Descriptor) (CachedOCIContent, error) {
	if h.proxy == nil || h.proxy.Cache == nil {
		return CachedOCIContent{}, errors.New("OCI discovery cache is not configured")
	}
	if !validOCIDigest(expected.Digest) {
		return CachedOCIContent{}, discovery.ErrInvalid
	}
	member := ociDiscoveryMember(repo)
	if repo.Type == repository.RepositoryTypeProxy {
		if upstreamauth.Validate(repo.UpstreamAuth) != nil {
			return CachedOCIContent{}, &ociFetchError{status: 503, code: "UNAVAILABLE", message: "source credential unavailable"}
		}
		if !h.proxy.cacheSourceAllowed(CachedOCIContent{Member: repo.Name, Endpoint: repo.Endpoint}, []repository.Member{member}) {
			return CachedOCIContent{}, &ociFetchError{status: 403, code: "DENIED", message: "source egress denied"}
		}
	} else {
		blocked, err := h.ociDigestReadBlocked(ctx, repo, image, expected.Digest)
		if err != nil {
			return CachedOCIContent{}, err
		}
		if blocked {
			return CachedOCIContent{}, &ociFetchError{status: 403, code: "DENIED", message: repository.ArtifactQuarantinedReason}
		}
	}
	key := h.proxy.Cache.Key(upstreamCacheNamespace("discovery:"+repo.ID, []repository.Member{member}), image, resource, expected.Digest)
	if content, err := h.proxy.Cache.Load(ctx, key); err == nil {
		if content.Digest != expected.Digest || expected.Size >= 0 && content.Size != expected.Size || expected.MediaType != "" && resource == "manifests" && content.ContentType != expected.MediaType {
			return CachedOCIContent{}, discovery.ErrInvalid
		}
		return content, nil
	}
	var content CachedOCIContent
	var err error
	if repo.Type != repository.RepositoryTypeProxy {
		if resource == "manifests" {
			manifest, readErr := h.store.GetOCIManifest(ctx, repo.ID, image, expected.Digest)
			if readErr != nil {
				return CachedOCIContent{}, readErr
			}
			content = ociprotocol.NewStoredContent(manifest.Digest, manifest.MediaType, manifest.ObjectKey, manifest.Size, h.objects)
		} else {
			blob, readErr := h.store.GetOCIBlob(ctx, repo.ID, expected.Digest)
			if readErr != nil {
				return CachedOCIContent{}, readErr
			}
			content = ociprotocol.NewStoredContent(blob.Digest, expected.MediaType, blob.ObjectKey, blob.Size, h.objects)
		}
		reader, size, readErr := content.Open(ctx)
		if readErr != nil {
			return CachedOCIContent{}, readErr
		}
		response := &http.Response{StatusCode: 200, Header: http.Header{"Docker-Content-Digest": {content.Digest}}, Body: reader, ContentLength: size, Request: &http.Request{Method: http.MethodGet}}
		response.Body = limitedOCIResponse{Reader: io.LimitReader(reader, discoveryMaxBlobBytes+1), Closer: reader}
		mediaType := content.ContentType
		content, err = verifyOCIResponse(response, expected.Digest)
		content.ContentType = mediaType
	} else {
		response, fetchErr := h.proxy.Client.Fetch(ctx, http.MethodGet, member, image, resource, expected.Digest, http.Header{"Accept": {ocigraph.OCIIndex + ", " + ocigraph.OCIManifest + ", " + ocigraph.DockerList + ", " + ocigraph.DockerManifest}})
		if fetchErr != nil {
			return CachedOCIContent{}, fetchErr
		}
		if response.StatusCode != 200 {
			_ = response.Body.Close()
			if response.StatusCode == 404 {
				return CachedOCIContent{}, repository.ErrNotFound
			}
			if response.StatusCode == 401 || response.StatusCode == 403 {
				return CachedOCIContent{}, &ociFetchError{status: 502, code: "UNAUTHORIZED", message: "source authorization failed"}
			}
			return CachedOCIContent{}, &ociFetchError{status: 502, code: "UNAVAILABLE", message: "source content unavailable"}
		}
		maximum := discoveryMaxBlobBytes
		if resource == "manifests" {
			maximum = ocigraph.MaxManifestBytes
		}
		if response.ContentLength > maximum {
			_ = response.Body.Close()
			return CachedOCIContent{}, discovery.ErrLimit
		}
		if response.Header.Get("Docker-Content-Digest") == "" {
			response.Header.Set("Docker-Content-Digest", expected.Digest)
		}
		reader := response.Body
		response.Body = limitedOCIResponse{Reader: io.LimitReader(reader, maximum+1), Closer: reader}
		mediaType, _, parseErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if resource == "manifests" && parseErr != nil {
			_ = response.Body.Close()
			return CachedOCIContent{}, discovery.ErrInvalid
		}
		content, err = verifyOCIResponse(response, expected.Digest)
		content.ContentType = mediaType
		if resource == "blobs" {
			content.ContentType = expected.MediaType
		}
	}
	if err != nil {
		return CachedOCIContent{}, err
	}
	defer content.Cleanup()
	if ctx.Err() != nil {
		return CachedOCIContent{}, ctx.Err()
	}
	if resource == "blobs" {
		content.ContentType = "application/octet-stream"
	}
	if content.Size > discoveryMaxBlobBytes || expected.Size >= 0 && content.Size != expected.Size || resource == "manifests" && expected.MediaType != "" && content.ContentType != expected.MediaType {
		return CachedOCIContent{}, discovery.ErrInvalid
	}
	content.Member, content.Endpoint, content.Repository = repo.Name, repo.Endpoint, repo.Name+"/"+image
	content.SetCacheable(true)
	if err := h.proxy.Cache.Store(ctx, key, content); err != nil {
		return CachedOCIContent{}, err
	}
	return h.proxy.Cache.Load(ctx, key)
}

func (h nativeOCIHandler) discoveryObjectKey(repo repository.HostedRepository, image, resource, digest string) string {
	return h.proxy.Cache.Key(upstreamCacheNamespace("discovery:"+repo.ID, []repository.Member{ociDiscoveryMember(repo)}), image, resource, digest)
}

type discoveryGraphNode struct {
	object   discovery.Object
	body     []byte
	height   int
	metadata discovery.ArtifactMetadata
}
type discoveryGraph struct {
	h                    nativeOCIHandler
	repo                 repository.HostedRepository
	image                string
	nodes                map[string]discoveryGraphNode
	active               map[string]bool
	bytes, manifestBytes int64
	rootFound            bool
}

func (g *discoveryGraph) visit(ctx context.Context, requirement discovery.Requirement, subject *discovery.Descriptor, depth int) (discovery.Object, discovery.ArtifactMetadata, error) {
	if depth > ocigraph.MaxDepth {
		return discovery.Object{}, discovery.ArtifactMetadata{}, discovery.ErrLimit
	}
	if g.active[requirement.Digest] {
		return discovery.Object{}, discovery.ArtifactMetadata{}, discovery.ErrInvalid
	}
	if prior, found := g.nodes[requirement.Digest]; found {
		if depth+prior.height > ocigraph.MaxDepth {
			return discovery.Object{}, discovery.ArtifactMetadata{}, discovery.ErrLimit
		}
		if prior.object.Resource != requirement.Resource || requirement.Size >= 0 && prior.object.Size != requirement.Size || requirement.Resource == "manifests" && requirement.MediaType != "" && prior.object.MediaType != requirement.MediaType {
			return discovery.Object{}, discovery.ArtifactMetadata{}, discovery.ErrInvalid
		}
		if subject != nil {
			if _, _, err := discovery.DecodeArtifact(prior.body, requirement.Descriptor, subject); err != nil {
				return discovery.Object{}, discovery.ArtifactMetadata{}, err
			}
		}
		return prior.object, prior.metadata, nil
	}
	if len(g.nodes)+len(g.active) >= ocigraph.MaxObjects || requirement.Size > discoveryMaxClosureBytes-g.bytes {
		return discovery.Object{}, discovery.ArtifactMetadata{}, discovery.ErrLimit
	}
	content, err := g.h.discoveryObject(ctx, g.repo, g.image, requirement.Resource, requirement.Descriptor)
	if err != nil {
		return discovery.Object{}, discovery.ArtifactMetadata{}, err
	}
	if depth == 0 {
		g.rootFound = true
	}
	object := discovery.Object{Digest: content.Digest, Size: content.Size, MediaType: content.ContentType, Resource: requirement.Resource, CacheKey: g.h.discoveryObjectKey(g.repo, g.image, requirement.Resource, content.Digest)}
	g.bytes += object.Size
	if g.bytes > discoveryMaxClosureBytes {
		return discovery.Object{}, discovery.ArtifactMetadata{}, discovery.ErrLimit
	}
	node := discoveryGraphNode{object: object}
	if requirement.Resource == "manifests" {
		g.manifestBytes += object.Size
		if g.manifestBytes > ocigraph.MaxInventoryManifestBytes {
			return discovery.Object{}, discovery.ArtifactMetadata{}, discovery.ErrLimit
		}
		reader, _, err := content.Open(ctx)
		if err != nil {
			return discovery.Object{}, discovery.ArtifactMetadata{}, err
		}
		node.body, err = io.ReadAll(io.LimitReader(reader, ocigraph.MaxManifestBytes+1))
		closeErr := reader.Close()
		if err != nil || closeErr != nil {
			return discovery.Object{}, discovery.ArtifactMetadata{}, errors.Join(err, closeErr)
		}
		descriptor := discovery.Descriptor{Digest: object.Digest, Size: object.Size, MediaType: object.MediaType, ArtifactType: requirement.ArtifactType}
		children, metadata, err := discovery.DecodeArtifact(node.body, descriptor, subject)
		if err != nil {
			return discovery.Object{}, discovery.ArtifactMetadata{}, err
		}
		node.metadata = metadata
		g.active[object.Digest] = true
		defer delete(g.active, object.Digest)
		for _, child := range children {
			if _, _, err := g.visit(ctx, child, nil, depth+1); err != nil {
				return discovery.Object{}, discovery.ArtifactMetadata{}, err
			}
			node.height = max(node.height, g.nodes[child.Digest].height+1)
		}
	}
	g.nodes[object.Digest] = node
	return object, node.metadata, nil
}

func (g *discoveryGraph) objects() []discovery.Object {
	objects := make([]discovery.Object, 0, len(g.nodes))
	for _, node := range g.nodes {
		objects = append(objects, node.object)
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].Digest < objects[j].Digest })
	return objects
}

func (h nativeOCIHandler) collectReferrers(ctx context.Context, repo repository.HostedRepository, image, subject string) ([]discovery.Descriptor, error) {
	if repo.Type != repository.RepositoryTypeProxy {
		blocked, err := h.ociQuarantinedDigestClosure(ctx, repo, image)
		if err != nil {
			return nil, err
		}
		items, err := h.visibleOCIReferrers(ctx, repo, image, subject, discovery.MaxReferrers+1, "", blocked)
		if err != nil {
			return nil, err
		}
		if len(items) > discovery.MaxReferrers {
			return nil, discovery.ErrLimit
		}
		refs := make([]discovery.Descriptor, 0, len(items))
		for _, m := range items {
			refs = append(refs, discovery.Descriptor{Digest: m.Digest, Size: m.Size, MediaType: m.MediaType, ArtifactType: m.ArtifactType})
		}
		return refs, nil
	}
	client, ok := h.proxy.Client.(OCIDiscoveryClient)
	if !ok {
		return nil, &ociFetchError{status: 501, code: "UNSUPPORTED", message: "source Referrers API unsupported"}
	}
	refs := []discovery.Descriptor{}
	seen := map[string]discovery.Descriptor{}
	pages := map[string]bool{}
	next := ""
	for page := 0; page < discovery.MaxPages; page++ {
		response, err := client.FetchOCIDiscovery(ctx, ociDiscoveryMember(repo), OCIDiscoveryRequest{Kind: "referrers", Image: image, Subject: subject, Next: next})
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, ocigraph.MaxManifestBytes+1))
		closeErr := response.Body.Close()
		if response.StatusCode == 404 || response.StatusCode == 405 || response.StatusCode == 501 {
			return nil, &ociFetchError{status: 501, code: "UNSUPPORTED", message: "source Referrers API unsupported; tag fallback is not implemented"}
		}
		if response.StatusCode == 401 || response.StatusCode == 403 {
			return nil, &ociFetchError{status: 502, code: "UNAUTHORIZED", message: "source referrers authorization failed"}
		}
		mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if response.StatusCode != 200 || readErr != nil || closeErr != nil || mediaErr != nil || mediaType != ocigraph.OCIIndex {
			return nil, discovery.ErrInvalid
		}
		batch, err := discovery.DecodeIndex(body)
		if err != nil {
			return nil, err
		}
		for _, descriptor := range batch {
			if old, found := seen[descriptor.Digest]; found {
				if old.Size != descriptor.Size || old.MediaType != descriptor.MediaType || old.ArtifactType != descriptor.ArtifactType || !maps.Equal(old.Annotations, descriptor.Annotations) {
					return nil, discovery.ErrInvalid
				}
				continue
			}
			if len(refs) >= discovery.MaxReferrers {
				return nil, discovery.ErrLimit
			}
			seen[descriptor.Digest] = descriptor
			refs = append(refs, descriptor)
		}
		next, err = ociDiscoveryNext(response.Header.Values("Link"))
		if err != nil {
			return nil, err
		}
		if next == "" {
			sort.Slice(refs, func(i, j int) bool { return refs[i].Digest < refs[j].Digest })
			return refs, nil
		}
		if pages[next] {
			return nil, discovery.ErrInvalid
		}
		pages[next] = true
	}
	return nil, discovery.ErrLimit
}

func writeReferrerFailure(w http.ResponseWriter, err error) {
	var upstream *ociFetchError
	if errors.As(err, &upstream) {
		writeOCIError(w, upstream.status, upstream.code, upstream.message)
		return
	}
	if errors.Is(err, repository.ErrNotFound) {
		writeOCIError(w, 404, "MANIFEST_UNKNOWN", "subject manifest not found")
		return
	}
	if errors.Is(err, discovery.ErrChanged) || errors.Is(err, discovery.ErrLimit) || errors.Is(err, discovery.ErrNotFound) {
		writeDiscoveryFailure(w, err)
		return
	}
	if errors.Is(err, discovery.ErrInvalid) {
		writeOCIError(w, 502, "MANIFEST_INVALID", "source referrer graph is invalid or incomplete")
		return
	}
	writeOCIError(w, 503, "UNAVAILABLE", "referrer discovery unavailable")
}

package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	discovery "github.com/ccsert/artifact-gateway/internal/ocidiscovery"
	"github.com/ccsert/artifact-gateway/internal/ocigraph"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// Authentication is a request fact, independent of a principal's display or
// stable actor name. In particular, a configured actor may be named anonymous.
type ociDiscoveryPrincipal struct {
	Principal
	authenticated bool
}

func (p ociDiscoveryPrincipal) bindingOwner() string {
	if !p.authenticated {
		return discovery.OwnerForAnonymous()
	}
	return discovery.OwnerForActor(p.Actor)
}

type ociDiscoveryScope struct {
	id, name     string
	group        *repository.HostedGroup
	repositories []repository.HostedRepository
}

func repositoryDiscoveryScope(repo repository.HostedRepository) ociDiscoveryScope {
	return ociDiscoveryScope{id: "repository:" + repo.ID, name: repo.Name, repositories: []repository.HostedRepository{repo}}
}

func (h nativeOCIHandler) discoveryGuards(ctx context.Context, scope ociDiscoveryScope, p ociDiscoveryPrincipal, repo repository.HostedRepository, image string) ([]discovery.Guard, error) {
	if repo.Format != repository.FormatOCI || repo.State != repository.RepositoryActive || p.AccountStateReason() != "" {
		return nil, discovery.ErrChanged
	}
	if !p.authenticated {
		if !repo.AnonymousRead || !anonymousAccessAllowed(ctx, h.store) {
			return nil, &ociFetchError{status: 403, code: "DENIED", message: "requested source access is denied"}
		}
	} else {
		decision, managed := h.authorizer.ManagedResourceDecision(ctx, p.Principal, repo, RepositoryRead, image)
		if !managed || !decision.Allowed {
			return nil, &ociFetchError{status: 403, code: "DENIED", message: "requested source access is denied"}
		}
	}
	guards := []discovery.Guard{{Kind: "repository", ID: repo.ID, Version: repo.Version}}
	if h.authorizer.Grants != nil {
		set, err := h.authorizer.Grants.GetRepositoryGrants(ctx, repo.ID)
		if err != nil {
			return nil, err
		}
		guards = append(guards, discovery.Guard{Kind: "grants", ID: repo.ID, Version: set.Version})
	}
	if scope.group != nil {
		guards = append(guards, discovery.Guard{Kind: "group", ID: scope.group.ID, Version: scope.group.Version})
	}
	return guards, nil
}

func (h nativeOCIHandler) boundDiscoverySource(ctx context.Context, scope ociDiscoveryScope, p ociDiscoveryPrincipal, image string, binding discovery.Binding) (repository.HostedRepository, []discovery.Guard, error) {
	if binding.Owner != p.bindingOwner() || binding.Actor != p.Actor || binding.Anonymous == p.authenticated {
		return repository.HostedRepository{}, nil, discovery.ErrChanged
	}
	present := false
	if scope.group != nil {
		for _, member := range scope.group.Members {
			if member.RepositoryID == binding.SourceID {
				present = true
				break
			}
		}
	} else {
		present = len(scope.repositories) == 1 && scope.repositories[0].ID == binding.SourceID
	}
	if !present {
		return repository.HostedRepository{}, nil, discovery.ErrChanged
	}
	repo, err := h.repos.GetHostedRepository(ctx, binding.SourceID)
	if err != nil || repo.Version != binding.SourceVersion {
		return repository.HostedRepository{}, nil, discovery.ErrChanged
	}
	guards, err := h.discoveryGuards(ctx, scope, p, repo, image)
	return repo, guards, err
}

func newDiscoveryGraph(h nativeOCIHandler, repo repository.HostedRepository, image string) *discoveryGraph {
	return &discoveryGraph{h: h, repo: repo, image: image, nodes: map[string]discoveryGraphNode{}, active: map[string]bool{}}
}

func uniqueDiscoveryGuards(items []discovery.Guard) []discovery.Guard {
	byID := map[string]discovery.Guard{}
	for _, item := range items {
		byID[item.Kind+":"+item.ID] = item
	}
	items = make([]discovery.Guard, 0, len(byID))
	for _, item := range byID {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Kind+":"+items[i].ID < items[j].Kind+":"+items[j].ID })
	return items
}

func (h nativeOCIHandler) discoverSubject(ctx context.Context, scope ociDiscoveryScope, p ociDiscoveryPrincipal, image, subject string) (discovery.Binding, []discovery.Guard, error) {
	binding, err := h.discovery.GetOCISubjectBinding(ctx, scope.id, p.bindingOwner(), image, subject)
	var source repository.HostedRepository
	var guards []discovery.Guard
	var graph *discoveryGraph
	if err == nil {
		source, guards, err = h.boundDiscoverySource(ctx, scope, p, image, binding)
		if err != nil {
			return discovery.Binding{}, nil, err
		}
		if time.Since(binding.FetchedAt) < discovery.SnapshotTTL {
			// Validate availability through the fixed source. A cache miss may only
			// refetch that source; it cannot select a different Group member.
			for _, object := range binding.Objects {
				if _, err = h.discoveryObject(ctx, source, image, object.Resource, discovery.Descriptor{Digest: object.Digest, Size: object.Size, MediaType: object.MediaType}); err != nil {
					return discovery.Binding{}, nil, err
				}
			}
			return binding, guards, nil
		}
		graph = newDiscoveryGraph(h, source, image)
		if _, _, err = graph.visit(ctx, discovery.Requirement{Descriptor: discovery.Descriptor{Digest: subject, Size: -1}, Resource: "manifests"}, nil, 0); err != nil {
			return discovery.Binding{}, nil, err
		}
	} else if errors.Is(err, discovery.ErrNotFound) {
		if len(scope.repositories) > discovery.MaxSources {
			return discovery.Binding{}, nil, discovery.ErrLimit
		}
		for _, repo := range scope.repositories {
			memberGuards, guardErr := h.discoveryGuards(ctx, scope, p, repo, image)
			if guardErr != nil {
				return discovery.Binding{}, nil, guardErr
			}
			guards = append(guards, memberGuards...)
			candidate := newDiscoveryGraph(h, repo, image)
			_, _, readErr := candidate.visit(ctx, discovery.Requirement{Descriptor: discovery.Descriptor{Digest: subject, Size: -1}, Resource: "manifests"}, nil, 0)
			if errors.Is(readErr, repository.ErrNotFound) {
				// Only an absent root permits fall-through. A present root with a
				// missing child is an invalid authoritative graph.
				if len(candidate.nodes) == 0 && !candidate.rootFound {
					continue
				}
				return discovery.Binding{}, nil, discovery.ErrInvalid
			}
			if readErr != nil {
				return discovery.Binding{}, nil, readErr
			}
			source, graph = repo, candidate
			break
		}
		if graph == nil {
			return discovery.Binding{}, nil, repository.ErrNotFound
		}
	} else {
		return discovery.Binding{}, nil, err
	}
	refs, err := h.collectReferrers(ctx, source, image, subject)
	if err != nil {
		return discovery.Binding{}, nil, err
	}
	root := graph.nodes[subject].object
	rootDescriptor := discovery.Descriptor{Digest: root.Digest, MediaType: root.MediaType, Size: root.Size}
	for i, ref := range refs {
		_, metadata, err := graph.visit(ctx, discovery.Requirement{Descriptor: ref, Resource: "manifests"}, &rootDescriptor, 0)
		if errors.Is(err, repository.ErrNotFound) {
			err = discovery.ErrInvalid
		}
		if err != nil {
			return discovery.Binding{}, nil, err
		}
		for key, value := range ref.Annotations {
			verified, found := metadata.Annotations[key]
			if !found || verified != value {
				return discovery.Binding{}, nil, discovery.ErrInvalid
			}
		}
		refs[i].ArtifactType = metadata.ArtifactType
		refs[i].Annotations = metadata.Annotations
	}
	guards = uniqueDiscoveryGuards(guards)
	binding = discovery.Binding{Scope: scope.id, Owner: p.bindingOwner(), Image: image, Subject: subject, SourceID: source.ID, SourceVersion: source.Version, Actor: p.Actor, Anonymous: !p.authenticated, Guards: guards, Objects: graph.objects(), Referrers: refs, FetchedAt: time.Now().UTC()}
	if err = h.discovery.PublishOCISubjectBinding(ctx, binding); err != nil {
		return discovery.Binding{}, nil, err
	}
	return binding, guards, nil
}

func (h nativeOCIHandler) discoveryReferrers(w http.ResponseWriter, r *http.Request, scope ociDiscoveryScope, image, subject string, p ociDiscoveryPrincipal) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeOCIError(w, 405, "UNSUPPORTED", "method not allowed")
		return
	}
	if rejectFailedOCIDiscoveryAuthentication(w, r, p) {
		return
	}
	if h.discovery == nil || h.proxy == nil || h.proxy.Cache == nil {
		writeDiscoveryFailure(w, errors.New("discovery unavailable"))
		return
	}
	limit, err := discoveryPageSize(r)
	filter := r.URL.Query().Get("artifactType")
	if err != nil || !ocigraph.ValidName(image) || !ocigraph.ValidDigest(subject) || len(filter) > 512 {
		writeDiscoveryFailure(w, discovery.ErrInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), ociSharedWorkTimeout)
	defer cancel()
	var snapshot discovery.Snapshot
	if id := r.URL.Query().Get("snapshot"); id != "" {
		snapshot, err = h.discovery.GetOCIDiscoverySnapshot(ctx, id, discoveryOwner(p.Principal, p.authenticated))
		if err == nil && (snapshot.Kind != "referrers" || snapshot.Scope != scope.id || snapshot.Image != image || snapshot.Subject != subject || snapshot.ArtifactType != filter) {
			writeDiscoveryFailure(w, discovery.ErrInvalid)
			return
		}
	} else {
		var binding discovery.Binding
		var guards []discovery.Guard
		binding, guards, err = h.discoverSubject(ctx, scope, p, image, subject)
		if err == nil {
			snapshot = discovery.Snapshot{ID: uuid.NewString(), Owner: discoveryOwner(p.Principal, p.authenticated), Actor: p.Actor, Kind: "referrers", Scope: scope.id, Image: image, Subject: subject, ArtifactType: filter, Guards: guards, Coverage: []discovery.Coverage{{Namespace: scope.name, State: "complete"}}, Referrers: []discovery.Descriptor{}, ExpiresAt: time.Now().UTC().Add(discovery.SnapshotTTL)}
			for _, ref := range binding.Referrers {
				if filter == "" || ref.ArtifactType == filter {
					snapshot.Referrers = append(snapshot.Referrers, ref)
				}
			}
			err = h.discovery.SaveOCIDiscoverySnapshot(ctx, snapshot)
		}
	}
	if err != nil {
		writeReferrerFailure(w, err)
		return
	}
	refs := []discovery.Descriptor{}
	for _, ref := range snapshot.Referrers {
		if ref.Digest > r.URL.Query().Get("last") {
			refs = append(refs, ref)
			if len(refs) > limit {
				break
			}
		}
	}
	if len(refs) > limit {
		refs = refs[:limit]
		query := url.Values{"n": {strconv.Itoa(limit)}, "last": {refs[len(refs)-1].Digest}, "snapshot": {snapshot.ID}}
		if filter != "" {
			query.Set("artifactType", filter)
		}
		w.Header().Set("Link", "</v2/"+scope.name+"/"+image+"/referrers/"+subject+"?"+query.Encode()+">; rel=\"next\"")
	}
	if h.audit != nil {
		if err = h.audit.RecordAudit(ctx, repository.AuditRecord{Actor: p.Actor, Repository: scope.name, Format: "oci", Resource: image + "@" + subject, Operation: "oci.referrers.read", Status: 200, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC()}); err != nil {
			writeReferrerFailure(w, err)
			return
		}
	}
	w.Header().Set("Content-Type", ocigraph.OCIIndex)
	w.Header().Set("OCI-Gateway-Discovery-Coverage", "complete")
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	if filter != "" {
		w.Header().Set("OCI-Filters-Applied", "artifactType")
	}
	w.WriteHeader(200)
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"schemaVersion": 2, "mediaType": ocigraph.OCIIndex, "manifests": refs})
}

func (h nativeOCIHandler) serveOCIBoundObject(w http.ResponseWriter, r *http.Request, scope ociDiscoveryScope, image, resource, digest string, p ociDiscoveryPrincipal) bool {
	if h.discovery == nil || !ocigraph.ValidDigest(digest) {
		return false
	}
	if rejectFailedOCIDiscoveryAuthentication(w, r, p) {
		return true
	}
	binding, object, err := h.discovery.GetOCIBoundObject(r.Context(), scope.id, p.bindingOwner(), image, digest)
	if errors.Is(err, discovery.ErrNotFound) {
		return false
	}
	if err != nil {
		writeReferrerFailure(w, err)
		return true
	}
	source, _, err := h.boundDiscoverySource(r.Context(), scope, p, image, binding)
	if err == nil && object.Resource != resource {
		err = discovery.ErrInvalid
	}
	var content CachedOCIContent
	if err == nil {
		content, err = h.discoveryObject(r.Context(), source, image, resource, discovery.Descriptor{Digest: object.Digest, MediaType: object.MediaType, Size: object.Size})
	}
	if err != nil {
		writeReferrerFailure(w, err)
		return true
	}
	if h.audit != nil {
		if err = h.audit.RecordAudit(r.Context(), repository.AuditRecord{Actor: p.Actor, Repository: scope.name, MemberName: source.Name, Format: "oci", Resource: image + "@" + digest, Operation: "oci.referrers.object.read", Status: 200, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC()}); err != nil {
			writeReferrerFailure(w, err)
			return true
		}
	}
	serveCachedOCIContent(w, r, digest, content)
	return true
}

// The surrounding protocol can admit unauthenticated public reads. A supplied
// credential that failed authentication must never inherit that anonymous view.
func rejectFailedOCIDiscoveryAuthentication(w http.ResponseWriter, r *http.Request, p ociDiscoveryPrincipal) bool {
	if p.authenticated || r.Header.Get("Authorization") == "" {
		return false
	}
	writeOCIChallenge(w, r)
	return true
}

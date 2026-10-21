package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	discovery "github.com/ccsert/artifact-gateway/internal/ocidiscovery"
	"github.com/ccsert/artifact-gateway/internal/ocigraph"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func discoveryOwner(p Principal) string {
	body, _ := json.Marshal([]any{p.Actor, p.Role, p.Admin, p.RepositoryPatterns})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func discoveryPageSize(r *http.Request) (int, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return 0, discovery.ErrInvalid
	}
	for _, values := range query {
		if len(values) != 1 {
			return 0, discovery.ErrInvalid
		}
	}
	limit := 100
	if value := query.Get("n"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 1000 {
			return 0, discovery.ErrInvalid
		}
	}
	return limit, nil
}

func writeDiscoveryFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, discovery.ErrChanged):
		writeOCIError(w, 409, "DISCOVERY_CHANGED", "discovery source or authorization changed; start a new snapshot")
	case errors.Is(err, discovery.ErrNotFound):
		writeOCIError(w, 404, "DISCOVERY_UNKNOWN", "discovery snapshot is missing or expired")
	case errors.Is(err, discovery.ErrInvalid):
		writeOCIError(w, 400, "NAME_INVALID", "invalid discovery request")
	case errors.Is(err, discovery.ErrLimit):
		writeOCIError(w, 429, "TOOMANYREQUESTS", "discovery safety budget exceeded")
	default:
		writeOCIError(w, 503, "UNAVAILABLE", "discovery unavailable")
	}
}

func (h nativeOCIHandler) catalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeOCIError(w, 405, "UNSUPPORTED", "method not allowed")
		return
	}
	p, authenticated := h.auth.Authenticate(r.Header.Get("Authorization"))
	if !authenticated {
		writeOCIChallenge(w, r)
		return
	}
	if h.discovery == nil {
		writeDiscoveryFailure(w, errors.New("missing discovery store"))
		return
	}
	limit, err := discoveryPageSize(r)
	if err != nil {
		writeDiscoveryFailure(w, err)
		return
	}
	var snapshot discovery.Snapshot
	if id := r.URL.Query().Get("snapshot"); id != "" {
		snapshot, err = h.discovery.GetOCIDiscoverySnapshot(r.Context(), id, discoveryOwner(p))
		if err == nil && snapshot.Kind != "catalog" {
			err = discovery.ErrInvalid
		}
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), ociSharedWorkTimeout)
		defer cancel()
		snapshot, err = h.collectCatalog(ctx, p)
		if err == nil {
			err = h.discovery.SaveOCIDiscoverySnapshot(ctx, snapshot)
		}
	}
	if err != nil {
		writeDiscoveryFailure(w, err)
		return
	}
	state := discoveryCoverage(snapshot.Coverage)
	w.Header().Set("OCI-Gateway-Discovery-Coverage", state)
	if state == "unsupported" {
		writeOCIError(w, 501, "UNSUPPORTED", "authorized upstreams do not provide Catalog")
		return
	}
	if state == "partial" && len(snapshot.Entries) == 0 {
		writeOCIError(w, 502, "DISCOVERY_INCOMPLETE", "authorized catalog enumeration is incomplete")
		return
	}
	entries := make([]discovery.CatalogEntry, 0, limit+1)
	for _, entry := range snapshot.Entries {
		visible, err := h.catalogEntryVisible(r.Context(), p, entry)
		if err != nil {
			writeDiscoveryFailure(w, err)
			return
		}
		if visible && entry.Name > r.URL.Query().Get("last") {
			entries = append(entries, entry)
			if len(entries) > limit {
				break
			}
		}
	}
	if len(entries) > limit {
		entries = entries[:limit]
		query := url.Values{"n": {strconv.Itoa(limit)}, "last": {entries[len(entries)-1].Name}, "snapshot": {snapshot.ID}}
		w.Header().Set("Link", "</v2/_catalog?"+query.Encode()+">; rel=\"next\"")
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	if h.audit != nil {
		if err := h.audit.RecordAudit(r.Context(), repository.AuditRecord{Actor: p.Actor, Format: "oci", Resource: "_catalog", Operation: "oci.catalog.read", Status: 200, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC()}); err != nil {
			writeDiscoveryFailure(w, err)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"repositories": names, "gateway": map[string]any{"coverage": state, "sources": snapshot.Coverage, "entries": entries, "snapshot": snapshot.ID}})
}

func (h nativeOCIHandler) catalogEntryVisible(ctx context.Context, p Principal, entry discovery.CatalogEntry) (bool, error) {
	_, image, found := strings.Cut(entry.Name, "/")
	if !found {
		return false, discovery.ErrInvalid
	}
	for _, name := range entry.Sources {
		repo, err := h.repos.GetHostedRepositoryByName(ctx, name)
		if err != nil {
			return false, err
		}
		decision, managed := h.authorizer.ManagedResourceDecision(ctx, p, repo, RepositoryRead, image)
		if !managed || !decision.Allowed {
			continue
		}
		if repo.Type == repository.RepositoryTypeProxy {
			return true, nil
		}
		visible, err := h.ociImageHasVisibleManifest(ctx, repo, image)
		if err != nil {
			return false, err
		}
		if visible {
			return true, nil
		}
	}
	return false, nil
}

func discoveryCoverage(coverage []discovery.Coverage) string {
	unsupported, incomplete := 0, false
	for _, source := range coverage {
		if source.State == "unsupported" {
			unsupported++
		}
		if source.State != "complete" {
			incomplete = true
		}
	}
	if len(coverage) > 0 && unsupported == len(coverage) {
		return "unsupported"
	}
	if incomplete {
		return "partial"
	}
	return "complete"
}

type ociCatalogView struct {
	name         string
	group        *repository.HostedGroup
	repositories []repository.HostedRepository
}

func (h nativeOCIHandler) catalogEligible(ctx context.Context, p Principal, repo repository.HostedRepository) (bool, []discovery.Guard) {
	guards := []discovery.Guard{{Kind: "repository", ID: repo.ID, Version: repo.Version}}
	if p.AccountStateReason() != "" {
		return false, nil
	}
	if p.Admin || p.Role == "admin" {
		return true, guards
	}
	if h.authorizer.Grants == nil {
		return false, nil
	}
	set, err := h.authorizer.Grants.GetRepositoryGrants(ctx, repo.ID)
	if err != nil {
		return false, nil
	}
	guards = append(guards, discovery.Guard{Kind: "grants", ID: repo.ID, Version: set.Version})
	for _, grant := range set.Grants {
		if grant.Principal != p.Actor {
			continue
		}
		for _, scope := range grant.Scopes {
			if scope == "repositories:read" || scope == "repositories:write" || scope == "repositories:admin" {
				return true, guards
			}
		}
	}
	return false, nil
}

func (h nativeOCIHandler) catalogViews(ctx context.Context) ([]ociCatalogView, bool, error) {
	views := map[string]ociCatalogView{}
	truncated := false
	after := ""
	for page := 0; page < discovery.MaxPages; page++ {
		repos, next, err := h.repos.ListHostedRepositories(ctx, 200, after)
		if err != nil {
			return nil, false, err
		}
		for _, repo := range repos {
			if repo.Format == repository.FormatOCI && repo.State == repository.RepositoryActive {
				if len(views) >= discovery.MaxSources {
					truncated = true
					break
				}
				views[repo.Name] = ociCatalogView{name: repo.Name, repositories: []repository.HostedRepository{repo}}
			}
		}
		if truncated || next == "" {
			break
		}
		if next == after {
			return nil, false, discovery.ErrInvalid
		}
		after = next
		if page+1 == discovery.MaxPages {
			truncated = true
		}
	}
	if h.groups != nil {
		after = ""
		for page := 0; page < discovery.MaxPages; page++ {
			groups, next, err := h.groups.ListHostedGroups(ctx, 200, after)
			if err != nil {
				return nil, false, err
			}
			for _, group := range groups {
				if group.Format != repository.FormatOCI {
					continue
				}
				if _, exists := views[group.Name]; !exists && len(views) >= discovery.MaxSources {
					truncated = true
					continue
				}
				resolver := v2GroupResolver{groups: h.groups, repos: h.repos}
				members, err := resolver.resolveMembers(ctx, group)
				if err != nil {
					return nil, false, err
				}
				view := ociCatalogView{name: group.Name, group: &group}
				for _, member := range members {
					repo, err := h.repos.GetHostedRepository(ctx, member.RepositoryID)
					if err != nil {
						return nil, false, err
					}
					view.repositories = append(view.repositories, repo)
				}
				views[group.Name] = view
			}
			if next == "" {
				break
			}
			if next == after {
				return nil, false, discovery.ErrInvalid
			}
			after = next
			if page+1 == discovery.MaxPages {
				truncated = true
			}
		}
	}
	ordered := make([]ociCatalogView, 0, len(views))
	for _, view := range views {
		// The router gives a Group namespace precedence over a repository of
		// the same name, including Groups outside the enumeration budget.
		if view.group == nil && h.groups != nil {
			group, err := h.groups.GetHostedGroupByName(ctx, view.name)
			if err != nil && !errors.Is(err, repository.ErrNotFound) {
				return nil, false, err
			}
			if err == nil && group.Format == repository.FormatOCI {
				resolver := v2GroupResolver{groups: h.groups, repos: h.repos}
				members, err := resolver.resolveMembers(ctx, group)
				if err != nil {
					return nil, false, err
				}
				view = ociCatalogView{name: group.Name, group: &group}
				for _, member := range members {
					repo, err := h.repos.GetHostedRepository(ctx, member.RepositoryID)
					if err != nil {
						return nil, false, err
					}
					view.repositories = append(view.repositories, repo)
				}
			}
		}
		ordered = append(ordered, view)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].name < ordered[j].name })
	return ordered, truncated, nil
}

func (h nativeOCIHandler) collectCatalog(ctx context.Context, principal Principal) (discovery.Snapshot, error) {
	snapshot := discovery.Snapshot{ID: uuid.NewString(), Owner: discoveryOwner(principal), Actor: principal.Actor, Kind: "catalog", Entries: []discovery.CatalogEntry{}, Coverage: []discovery.Coverage{}, ExpiresAt: time.Now().UTC().Add(discovery.SnapshotTTL)}
	views, truncated, err := h.catalogViews(ctx)
	if err != nil {
		return snapshot, err
	}
	guardMap := map[string]discovery.Guard{}
	type inventory struct {
		names         []string
		state, reason string
	}
	inventories := map[string]inventory{}
	sources := map[string]bool{}
	names := map[string][]string{}
	for _, view := range views {
		coverage := discovery.Coverage{Namespace: view.name, State: "complete"}
		eligible := 0
		unsupported, failed := 0, false
		for _, repo := range view.repositories {
			allowed, guards := h.catalogEligible(ctx, principal, repo)
			if !allowed {
				continue
			}
			eligible++
			if !sources[repo.ID] && len(sources) >= discovery.MaxSources {
				truncated, failed = true, true
				coverage.Reason = "safety_budget"
				break
			}
			sources[repo.ID] = true
			for _, guard := range guards {
				key := guard.Kind + ":" + guard.ID
				if prior, found := guardMap[key]; found && prior.Version != guard.Version {
					return snapshot, discovery.ErrChanged
				}
				guardMap[key] = guard
			}
			observed, found := inventories[repo.ID]
			if !found {
				observed.names, observed.state, observed.reason = h.catalogRepository(ctx, repo)
				inventories[repo.ID] = observed
			}
			items, state, reason := observed.names, observed.state, observed.reason
			if state == "unsupported" {
				unsupported++
			}
			if state != "complete" {
				failed, coverage.Reason = true, reason
			}
			for _, image := range items {
				decision, managed := h.authorizer.ManagedResourceDecision(ctx, principal, repo, RepositoryRead, image)
				if !managed || !decision.Allowed {
					continue
				}
				if repo.Type != repository.RepositoryTypeProxy {
					visible, err := h.ociImageHasVisibleManifest(ctx, repo, image)
					if err != nil {
						return snapshot, err
					}
					if !visible {
						continue
					}
				}
				name := view.name + "/" + image
				if len(names) >= discovery.MaxNames && names[name] == nil {
					truncated = true
					break
				}
				duplicate := false
				for _, source := range names[name] {
					if source == repo.Name {
						duplicate = true
					}
				}
				if !duplicate {
					names[name] = append(names[name], repo.Name)
				}
			}
		}
		if eligible == 0 {
			continue
		}
		if unsupported == eligible {
			coverage.State = "unsupported"
		} else if failed {
			coverage.State = "partial"
		}
		if view.group != nil {
			guard := discovery.Guard{Kind: "group", ID: view.group.ID, Version: view.group.Version}
			guardMap[guard.Kind+":"+guard.ID] = guard
		}
		snapshot.Coverage = append(snapshot.Coverage, coverage)
	}
	if truncated {
		snapshot.Coverage = append(snapshot.Coverage, discovery.Coverage{Namespace: "_catalog", State: "partial", Reason: "safety_budget"})
	}
	for name, sources := range names {
		snapshot.Entries = append(snapshot.Entries, discovery.CatalogEntry{Name: name, Sources: sources})
	}
	sort.Slice(snapshot.Entries, func(i, j int) bool { return snapshot.Entries[i].Name < snapshot.Entries[j].Name })
	for _, guard := range guardMap {
		snapshot.Guards = append(snapshot.Guards, guard)
	}
	sort.Slice(snapshot.Guards, func(i, j int) bool {
		a, b := snapshot.Guards[i], snapshot.Guards[j]
		return a.Kind+":"+a.ID < b.Kind+":"+b.ID
	})
	return snapshot, nil
}

func ociDiscoveryMember(repo repository.HostedRepository) repository.Member {
	return repository.Member{Name: repo.Name, Type: repository.MemberProxy, RepositoryID: repo.ID, RepositoryVersion: repo.Version, Endpoint: repo.Endpoint, AllowedHosts: repo.AllowedHosts, UpstreamAuth: repo.UpstreamAuth, OCIBearer: repo.OCIBearer, EgressProxy: repo.EgressProxy}
}

func (h nativeOCIHandler) catalogRepository(ctx context.Context, repo repository.HostedRepository) ([]string, string, string) {
	seen, pages := map[string]bool{}, map[string]bool{}
	items := []string{}
	next := ""
	for page := 0; page < discovery.MaxPages; page++ {
		var names []string
		if repo.Type != repository.RepositoryTypeProxy {
			batch, err := h.store.ListOCIManifestNames(ctx, repo.ID, 200, next)
			if err != nil {
				return items, "partial", "unavailable"
			}
			names = batch
			next = ""
			if len(batch) == 200 {
				next = batch[len(batch)-1]
			}
		} else {
			if h.proxy == nil || h.proxy.Cache == nil || !h.proxy.cacheSourceAllowed(CachedOCIContent{Member: repo.Name, Endpoint: repo.Endpoint}, []repository.Member{ociDiscoveryMember(repo)}) {
				return items, "partial", "unavailable"
			}
			client, ok := h.proxy.Client.(OCIDiscoveryClient)
			if !ok {
				return items, "unsupported", "client_unavailable"
			}
			response, err := client.FetchOCIDiscovery(ctx, ociDiscoveryMember(repo), OCIDiscoveryRequest{Kind: "catalog", Next: next})
			if err != nil {
				return items, "partial", "unavailable"
			}
			body, readErr := io.ReadAll(io.LimitReader(response.Body, ocigraph.MaxManifestBytes+1))
			closeErr := response.Body.Close()
			if response.StatusCode == 404 || response.StatusCode == 405 || response.StatusCode == 501 {
				return items, "unsupported", "catalog_unsupported"
			}
			if response.StatusCode == 401 || response.StatusCode == 403 {
				return items, "partial", "upstream_authorization"
			}
			if response.StatusCode != 200 || readErr != nil || closeErr != nil || len(body) > ocigraph.MaxManifestBytes {
				return items, "partial", "invalid_response"
			}
			var document struct {
				Repositories []string `json:"repositories"`
			}
			if json.Unmarshal(body, &document) != nil || document.Repositories == nil {
				return items, "partial", "invalid_response"
			}
			names = document.Repositories
			next, err = ociDiscoveryNext(response.Header.Values("Link"))
			if err != nil {
				return items, "partial", "invalid_pagination"
			}
		}
		for _, name := range names {
			if !ocigraph.ValidName(name) {
				return items, "partial", "invalid_name"
			}
			if !seen[name] {
				if len(items) >= discovery.MaxNames {
					return items, "partial", "name_budget"
				}
				seen[name] = true
				items = append(items, name)
			}
		}
		if next == "" {
			return items, "complete", ""
		}
		if pages[next] {
			return items, "partial", "pagination_loop"
		}
		pages[next] = true
	}
	return items, "partial", "page_budget"
}

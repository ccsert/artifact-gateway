package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/felixge/httpsnoop"
	"golang.org/x/mod/semver"
)

type v2GroupCargoHandler struct {
	native *nativeCargoHandler
	owners repository.CargoGroupStore
}

func (h v2GroupCargoHandler) serve(w http.ResponseWriter, r *http.Request, resolver v2GroupResolver, group repository.HostedGroup) {
	route, ok := parseCargoRoute(r.URL.EscapedPath())
	if !ok || route.repository != group.Name {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	principal, authenticated := h.native.principal(r)
	if !authenticated {
		if !group.AnonymousRead || !anonymousAccessAllowed(r.Context(), resolver.groups) {
			h.native.challenge(w, http.StatusUnauthorized)
			return
		}
		principal = anonymousPrincipal()
	}
	members, err := resolver.resolveMembers(r.Context(), group)
	if err != nil {
		h.native.writeError(w, http.StatusServiceUnavailable, "Cargo group members unavailable")
		return
	}
	allMembers := members
	if isAnonymous(principal) {
		members = anonymousHostedGroupMembers(group, members)
	}
	members, denied := resolver.authorizeRepositoryMembers(r.Context(), principal, group.Name, repository.FormatCargo, route.name, members)
	if denied && len(members) == 0 {
		h.native.challenge(w, http.StatusForbidden)
		return
	}
	if len(members) == 0 {
		h.native.challenge(w, http.StatusUnauthorized)
		return
	}
	switch route.kind {
	case "config":
		public := group.AnonymousRead
		for _, member := range allMembers {
			public = public && member.Anonymous
		}
		h.native.config(w, r, repository.HostedRepository{Name: group.Name, Format: repository.FormatCargo,
			State: repository.RepositoryActive, AnonymousRead: public})
	case "index":
		versions, err := h.reconcileIndex(r, resolver, group, members, route.name)
		if denied && errors.Is(err, repository.ErrUpstreamChanged) {
			h.native.challenge(w, http.StatusForbidden)
			return
		}
		if err != nil {
			h.writeResolutionError(w, r, err)
			return
		}
		var body bytes.Buffer
		for _, version := range versions {
			body.Write(bytes.TrimSuffix(version.IndexRow, []byte("\n")))
			body.WriteByte('\n')
		}
		writeCargoGroupIndex(w, r, route.name, body.Bytes())
		resolver.auditResolution(r.Context(), group, repository.FormatCargo, route.name, strings.ToLower(r.Method), principal.Actor, repository.AuditResolved, http.StatusOK)
	case "download":
		h.download(w, r, resolver, group, members, allMembers, route, principal.Actor)
	case "search":
		h.search(w, r, resolver, group, members, principal.Actor)
	default:
		http.NotFound(w, r)
	}
}

func (h v2GroupCargoHandler) reconcileIndex(r *http.Request, resolver v2GroupResolver, group repository.HostedGroup, members []repository.Member, name string) ([]repository.CargoGroupVersion, error) {
	candidates := make([]repository.CargoGroupVersion, 0)
	for _, member := range members {
		repo, err := resolver.repos.GetHostedRepository(r.Context(), member.RepositoryID)
		if err != nil {
			return nil, err
		}
		var body []byte
		if repo.Type == repository.RepositoryTypeProxy {
			index, err := h.native.proxyIndex(r, repo, name)
			if err != nil {
				return nil, err
			}
			if index.Status == http.StatusNotFound {
				continue
			}
			if index.Status != http.StatusOK {
				return nil, repository.ErrUpstreamChanged
			}
			body = index.Body
		} else {
			items, err := h.native.store.ListCargoPublications(r.Context(), repo.ID, name)
			if errors.Is(err, repository.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			for _, item := range items {
				var fields map[string]json.RawMessage
				if json.Unmarshal(item.IndexRow, &fields) != nil {
					return nil, repository.ErrInvalidCargoIdentity
				}
				fields["yanked"], _ = json.Marshal(item.Yanked)
				row, err := json.Marshal(fields)
				if err != nil {
					return nil, err
				}
				body = append(body, row...)
				body = append(body, '\n')
			}
		}
		for _, row := range bytes.Split(bytes.TrimSuffix(body, []byte("\n")), []byte("\n")) {
			if len(row) == 0 {
				continue
			}
			var identity struct {
				Name     string `json:"name"`
				Version  string `json:"vers"`
				Checksum string `json:"cksum"`
			}
			if json.Unmarshal(row, &identity) != nil {
				return nil, repository.ErrInvalidCargoIdentity
			}
			candidates = append(candidates, repository.CargoGroupVersion{GroupID: group.ID,
				SourceRepositoryID: repo.ID, Name: identity.Name, Version: identity.Version,
				Checksum: identity.Checksum, IndexRow: bytes.Clone(row)})
		}
	}
	return h.owners.ReconcileCargoGroupIndex(r.Context(), group.ID, name, candidates)
}

func (h v2GroupCargoHandler) writeResolutionError(w http.ResponseWriter, _ *http.Request, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		h.native.writeError(w, http.StatusNotFound, "Cargo crate not found")
	case errors.Is(err, repository.ErrCargoGroupConflict):
		h.native.writeError(w, http.StatusConflict, "Cargo group version conflicts across members")
	default:
		h.native.writeError(w, http.StatusServiceUnavailable, "Cargo group version owner unavailable or changed")
	}
}

func writeCargoGroupIndex(w http.ResponseWriter, r *http.Request, name string, body []byte) {
	sum := sha256.Sum256(body)
	w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
}

func (h v2GroupCargoHandler) download(w http.ResponseWriter, r *http.Request, resolver v2GroupResolver, group repository.HostedGroup, members, allMembers []repository.Member, route cargoRoute, actor string) {
	owner, err := h.owners.GetCargoGroupVersion(r.Context(), group.ID, route.name, route.version)
	if errors.Is(err, repository.ErrNotFound) {
		if _, err := h.reconcileIndex(r, resolver, group, members, route.name); err != nil {
			h.writeResolutionError(w, r, err)
			return
		}
		owner, err = h.owners.GetCargoGroupVersion(r.Context(), group.ID, route.name, route.version)
	}
	if err != nil {
		h.writeResolutionError(w, r, err)
		return
	}
	var source *repository.HostedRepository
	for _, member := range members {
		if member.RepositoryID != owner.SourceRepositoryID {
			continue
		}
		repo, err := resolver.repos.GetHostedRepository(r.Context(), member.RepositoryID)
		if err == nil && repo.Format == repository.FormatCargo && repo.State == repository.RepositoryActive {
			source = &repo
		}
		break
	}
	if source == nil {
		for _, member := range allMembers {
			if member.RepositoryID == owner.SourceRepositoryID {
				h.native.challenge(w, http.StatusForbidden)
				return
			}
		}
		h.native.writeError(w, http.StatusServiceUnavailable, "Cargo group version owner unavailable")
		return
	}
	if source.Type == repository.RepositoryTypeProxy {
		index, err := h.native.proxyIndex(r, *source, route.name)
		if err != nil {
			h.writeResolutionError(w, r, err)
			return
		}
		checksum, err := repository.CargoProxyIndexChecksum(index, route.version)
		if err != nil || checksum != owner.Checksum {
			h.native.writeError(w, http.StatusServiceUnavailable, "Cargo group version checksum changed")
			return
		}
	} else {
		publication, err := h.native.store.GetCargoPublication(r.Context(), source.ID, route.name, route.version)
		if err != nil || publication.Digest != "sha256:"+owner.Checksum {
			h.native.writeError(w, http.StatusServiceUnavailable, "Cargo group version checksum changed")
			return
		}
	}
	captured := httpsnoop.CaptureMetricsFn(w, func(out http.ResponseWriter) {
		if source.Type == repository.RepositoryTypeProxy {
			h.native.proxyDownload(out, r, *source, route.name, route.version, actor)
		} else {
			h.native.download(out, r, *source, route.name, route.version, actor)
		}
	})
	if captured.Code == http.StatusOK || captured.Code == http.StatusPartialContent {
		resolver.auditResolution(r.Context(), group, repository.FormatCargo, route.name+"@"+route.version,
			strings.ToLower(r.Method), actor, repository.AuditResolved, captured.Code)
	}
}

func (h v2GroupCargoHandler) search(w http.ResponseWriter, r *http.Request, resolver v2GroupResolver, group repository.HostedGroup, members []repository.Member, actor string) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" || len(query) > 255 || strings.ContainsRune(query, '\x00') {
		h.native.writeError(w, http.StatusBadRequest, "q must contain between 1 and 255 characters")
		return
	}
	limit := 10
	if raw := r.URL.Query().Get("per_page"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			h.native.writeError(w, http.StatusBadRequest, "per_page must be between 1 and 100")
			return
		}
		limit = parsed
	}
	type result struct {
		Name        string `json:"name"`
		MaxVersion  string `json:"max_version"`
		Description string `json:"description"`
	}
	seen := make(map[string]result)
	for _, member := range members {
		repo, err := resolver.repos.GetHostedRepository(r.Context(), member.RepositoryID)
		if err != nil {
			h.native.writeError(w, http.StatusServiceUnavailable, "Cargo group member unavailable")
			return
		}
		if repo.Type == repository.RepositoryTypeProxy {
			body, err := h.native.proxySearchBody(r, repo)
			if err != nil {
				h.native.writeError(w, http.StatusServiceUnavailable, "Cargo group member search unavailable")
				return
			}
			var remote struct {
				Crates []result `json:"crates"`
			}
			if json.Unmarshal(body, &remote) != nil {
				h.native.writeError(w, http.StatusServiceUnavailable, "Cargo group member search invalid")
				return
			}
			for _, item := range remote.Crates {
				if item.Name != "" {
					if _, exists := seen[item.Name]; !exists {
						seen[item.Name] = item
					}
				}
			}
			continue
		}
		items, _, err := h.native.store.SearchCargoCrates(r.Context(), repo.ID, query, 100, "", false)
		if err != nil {
			h.native.writeError(w, http.StatusServiceUnavailable, "Cargo group member search unavailable")
			return
		}
		for _, item := range items {
			if _, exists := seen[item.Name]; !exists {
				seen[item.Name] = result{Name: item.Name, MaxVersion: item.MaxVersion, Description: item.Description}
			}
		}
	}
	all := make([]result, 0, len(seen))
	for _, item := range seen {
		all = append(all, item)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	total := len(all)
	if len(all) > limit {
		all = all[:limit]
	}
	for i := range all {
		versions, err := h.reconcileIndex(r, resolver, group, members, all[i].Name)
		if err != nil {
			h.writeResolutionError(w, r, err)
			return
		}
		latest := ""
		for _, version := range versions {
			var row struct {
				Yanked bool `json:"yanked"`
			}
			if json.Unmarshal(version.IndexRow, &row) != nil || row.Yanked {
				continue
			}
			if latest == "" || semver.Compare("v"+version.Version, "v"+latest) > 0 {
				latest = version.Version
			}
		}
		if latest != "" {
			all[i].MaxVersion = latest
		}
	}
	body, err := json.Marshal(map[string]any{"crates": all, "meta": map[string]int{"total": total}})
	if err != nil {
		h.native.writeError(w, http.StatusServiceUnavailable, "Cargo group search unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		_, _ = w.Write(body)
	}
	resolver.auditResolution(r.Context(), group, repository.FormatCargo, query, strings.ToLower(r.Method), actor, repository.AuditResolved, http.StatusOK)
}

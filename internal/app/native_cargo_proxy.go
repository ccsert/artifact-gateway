package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/felixge/httpsnoop"
)

const (
	cargoIndexTTL        = 5 * time.Minute
	cargoNegativeTTL     = 30 * time.Second
	cargoConfigTTL       = time.Hour
	cargoProxyCrateLimit = 128 << 20
)

func (h nativeCargoHandler) serveProxy(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, route cargoRoute, actor string) {
	switch route.kind {
	case "config":
		if _, err := h.proxyConfig(r, repo); err != nil {
			h.writeError(w, http.StatusBadGateway, "Cargo upstream configuration unavailable")
			return
		}
		h.config(w, r, repo)
	case "index":
		h.proxyIndexResponse(w, r, repo, route.name, actor)
	case "download":
		h.proxyDownload(w, r, repo, route.name, route.version, actor)
	case "search":
		h.proxySearch(w, r, repo, actor)
	default:
		http.NotFound(w, r)
	}
}

func cargoUpstreamURL(repo repository.HostedRepository, path string) string {
	return strings.TrimRight(repo.Endpoint, "/") + "/" + strings.TrimLeft(path, "/")
}

func (h nativeCargoHandler) proxyConfig(r *http.Request, repo repository.HostedRepository) (repository.CargoProxyConfig, error) {
	current, err := h.proxy.GetCargoProxyConfig(r.Context(), repo.ID)
	have := err == nil
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return current, err
	}
	now := time.Now().UTC()
	if have && now.Before(current.ExpiresAt) {
		return current, nil
	}
	response, err := h.upstream.FetchCargo(r.Context(), repo, cargoUpstreamURL(repo, "config.json"), nil)
	if err != nil {
		if have {
			return current, nil
		}
		return current, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		if have {
			return current, nil
		}
		return current, errors.New("upstream config status")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
	if err != nil || len(body) > 1<<20 {
		return current, errors.New("invalid upstream config size")
	}
	var remote struct {
		DL  string `json:"dl"`
		API string `json:"api"`
	}
	if json.Unmarshal(body, &remote) != nil || remote.DL == "" || !cargoTemplateAllowed(repo, remote.DL) || (remote.API != "" && !cargoTemplateAllowed(repo, remote.API)) {
		return current, errors.New("invalid upstream config")
	}
	next := repository.CargoProxyConfig{RepositoryID: repo.ID, DownloadTemplate: remote.DL, SearchAPI: remote.API,
		FetchedAt: now, ExpiresAt: now.Add(cargoConfigTTL)}
	if err := h.proxy.PutCargoProxyConfig(r.Context(), next); err != nil {
		return current, err
	}
	return next, nil
}

func cargoTemplateAllowed(repo repository.HostedRepository, template string) bool {
	for _, marker := range []string{"{crate}", "{version}", "{prefix}", "{lowerprefix}", "{sha256-checksum}"} {
		template = strings.ReplaceAll(template, marker, "x")
	}
	u, err := url.Parse(template)
	return err == nil && !strings.ContainsAny(template, "{}") && proxyUpstreamURLAllowed(repo, u)
}

func (h nativeCargoHandler) proxyIndex(r *http.Request, repo repository.HostedRepository, name string) (repository.CargoProxyIndex, error) {
	current, err := h.proxy.GetCargoProxyIndex(r.Context(), repo.ID, name)
	have := err == nil
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return current, err
	}
	now := time.Now().UTC()
	if have && now.Before(current.ExpiresAt) {
		return current, nil
	}
	path, err := cargo.SparseIndexPath(name)
	if err != nil {
		return current, err
	}
	headers := make(http.Header)
	if have {
		if current.ETag != "" {
			headers.Set("If-None-Match", current.ETag)
		}
		if current.Modified != "" {
			headers.Set("If-Modified-Since", current.Modified)
		}
	}
	response, err := h.upstream.FetchCargo(r.Context(), repo, cargoUpstreamURL(repo, path), headers)
	if err != nil {
		if have && current.Status == http.StatusOK {
			return current, nil
		}
		return current, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified && have {
		current.FetchedAt, current.ExpiresAt = now, now.Add(cargoIndexTTL)
		if err := h.proxy.PutCargoProxyIndex(r.Context(), current); err != nil {
			return current, err
		}
		return current, nil
	}
	status := response.StatusCode
	if status != http.StatusOK && status != http.StatusNotFound && status != http.StatusGone && status != http.StatusUnavailableForLegalReasons {
		if have && current.Status == http.StatusOK {
			return current, nil
		}
		return current, errors.New("upstream index status")
	}
	var body []byte
	if status == http.StatusOK {
		body, err = io.ReadAll(io.LimitReader(response.Body, 16<<20+1))
		if err != nil || len(body) > 16<<20 {
			return current, errors.New("invalid upstream index size")
		}
	}
	ttl := cargoIndexTTL
	if status != http.StatusOK {
		ttl = cargoNegativeTTL
	}
	next := repository.CargoProxyIndex{RepositoryID: repo.ID, Name: name, Body: body, Status: status,
		ETag: response.Header.Get("ETag"), Modified: response.Header.Get("Last-Modified"), FetchedAt: now, ExpiresAt: now.Add(ttl)}
	if err := h.proxy.PutCargoProxyIndex(r.Context(), next); err != nil {
		return current, err
	}
	return next, nil
}

func (h nativeCargoHandler) proxyIndexResponse(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, name, actor string) {
	index, err := h.proxyIndex(r, repo, name)
	if err != nil {
		h.writeError(w, http.StatusBadGateway, "Cargo upstream index changed or is unavailable")
		return
	}
	if index.Status != http.StatusOK {
		w.WriteHeader(index.Status)
		return
	}
	sum := sha256.Sum256(index.Body)
	w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	captured := httpsnoop.CaptureMetricsFn(w, func(out http.ResponseWriter) {
		http.ServeContent(out, r, name, time.Time{}, bytes.NewReader(index.Body))
	})
	h.recordAudit(r, repo, name, "index", actor, repository.AuditResolved, captured.Code, captured.Written)
}

func cargoDownloadURL(config repository.CargoProxyConfig, name, version, checksum string) string {
	template := config.DownloadTemplate
	if !strings.Contains(template, "{") {
		template = strings.TrimRight(template, "/") + "/{crate}/{version}/download"
	}
	path, _ := cargo.SparseIndexPath(name)
	prefix := strings.TrimSuffix(path, "/"+name)
	return strings.NewReplacer("{crate}", url.PathEscape(name), "{version}", url.PathEscape(version),
		"{prefix}", prefix, "{lowerprefix}", strings.ToLower(prefix), "{sha256-checksum}", checksum).Replace(template)
}

func (h nativeCargoHandler) proxyDownload(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, name, version, actor string) {
	index, err := h.proxyIndex(r, repo, name)
	if err != nil {
		h.writeError(w, http.StatusBadGateway, "Cargo index unavailable")
		return
	}
	if index.Status != http.StatusOK {
		http.NotFound(w, r)
		return
	}
	checksum, err := repository.CargoProxyIndexChecksum(index, version)
	if errors.Is(err, repository.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.writeError(w, http.StatusBadGateway, "Cargo index invalid")
		return
	}
	crate, err := h.proxy.GetCargoProxyCrate(r.Context(), repo.ID, name, version)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		h.writeError(w, http.StatusServiceUnavailable, "Cargo cache unavailable")
		return
	}
	if errors.Is(err, repository.ErrNotFound) {
		config, err := h.proxyConfig(r, repo)
		if err != nil {
			h.writeError(w, http.StatusBadGateway, "Cargo upstream configuration unavailable")
			return
		}
		response, err := h.upstream.FetchCargo(r.Context(), repo, cargoDownloadURL(config, name, version, checksum), nil)
		if err != nil {
			h.writeError(w, http.StatusBadGateway, "Cargo upstream archive unavailable")
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			h.writeError(w, http.StatusBadGateway, "Cargo upstream archive unavailable")
			return
		}
		spool, err := spoolUpload(response.Body, cargoProxyCrateLimit)
		if err != nil || spool.Size() == 0 {
			if spool != nil {
				_ = spool.Close()
			}
			h.writeError(w, http.StatusBadGateway, "Cargo upstream archive invalid")
			return
		}
		defer spool.Close()
		if spool.Digest() != "sha256:"+checksum {
			h.writeError(w, http.StatusBadGateway, "Cargo upstream archive checksum mismatch")
			return
		}
		crate = repository.CargoProxyCrate{RepositoryID: repo.ID, Name: name, Version: version, Checksum: checksum,
			ObjectKey: "native/cargo-proxy/sha256/" + checksum, Size: spool.Size(), CachedAt: time.Now().UTC()}
		if err := h.objects.PutVerifiedReader(r.Context(), crate.ObjectKey, spool.Reader(), crate.Size, "sha256:"+checksum); err != nil {
			h.writeError(w, http.StatusServiceUnavailable, "Cargo archive cache unavailable")
			return
		}
		if err := h.proxy.PutCargoProxyCrate(r.Context(), crate); err != nil {
			h.writeError(w, http.StatusServiceUnavailable, "Cargo archive cache unavailable")
			return
		}
	}
	if crate.Checksum != checksum {
		h.writeError(w, http.StatusBadGateway, "Cargo archive checksum changed")
		return
	}
	reader, size, err := h.objects.Open(r.Context(), crate.ObjectKey)
	if err != nil {
		h.writeError(w, http.StatusServiceUnavailable, "Cargo archive object unavailable")
		return
	}
	defer reader.Close()
	if size != crate.Size {
		h.writeError(w, http.StatusServiceUnavailable, "Cargo archive object size changed")
		return
	}
	spool, err := spoolUpload(reader, crate.Size)
	if err != nil || spool.Size() != crate.Size || spool.Digest() != "sha256:"+checksum {
		if spool != nil {
			_ = spool.Close()
		}
		h.writeError(w, http.StatusServiceUnavailable, "Cargo archive object checksum changed")
		return
	}
	defer spool.Close()
	w.Header().Set("ETag", `"`+checksum+`"`)
	w.Header().Set("X-Checksum-Sha256", checksum)
	w.Header().Set("Content-Type", "application/octet-stream")
	captured := httpsnoop.CaptureMetricsFn(w, func(out http.ResponseWriter) {
		http.ServeContent(out, r, name+"-"+version+".crate", crate.CachedAt, spool.file)
	})
	h.recordAudit(r, repo, name+"@"+version, "crate", actor, repository.AuditResolved, captured.Code, captured.Written)
}

func (h nativeCargoHandler) proxySearch(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, actor string) {
	config, err := h.proxyConfig(r, repo)
	if err != nil || config.SearchAPI == "" {
		h.writeError(w, http.StatusBadGateway, "Cargo upstream search unavailable")
		return
	}
	u, err := url.Parse(strings.TrimRight(config.SearchAPI, "/") + "/api/v1/crates")
	if err != nil {
		h.writeError(w, http.StatusBadGateway, "Cargo upstream search unavailable")
		return
	}
	u.RawQuery = r.URL.RawQuery
	response, err := h.upstream.FetchCargo(r.Context(), repo, u.String(), nil)
	if err != nil {
		h.writeError(w, http.StatusBadGateway, "Cargo upstream search unavailable")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		h.writeError(w, http.StatusBadGateway, "Cargo upstream search unavailable")
		return
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
	if err != nil || len(body) > 1<<20 || !json.Valid(body) {
		h.writeError(w, http.StatusBadGateway, "Cargo upstream search invalid")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
	h.recordAudit(r, repo, r.URL.Query().Get("q"), "search", actor, repository.AuditResolved, http.StatusOK, int64(len(body)))
}

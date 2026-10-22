package helm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/ccsert/artifact-gateway/internal/authorization"
	helmprotocol "github.com/ccsert/artifact-gateway/internal/protocol/helm"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"go.yaml.in/yaml/v3"
)

var errInventoryLimit = errors.New("helm preview inventory exceeds its bound")

func assetRelative(name, version string, provenance bool) string {
	filename := helmprotocol.Filename(name, version)
	if provenance {
		filename += ".prov"
	}
	return "charts/" + url.PathEscape(name) + "/" + url.PathEscape(version) + "/" + url.PathEscape(filename)
}
func assetPath(repo repository.HostedRepository, p repository.HelmPublication, prov bool) string {
	return "/helm/" + url.PathEscape(repo.Name) + "/" + assetRelative(p.Name, p.Version, prov)
}
func (m *Module) snapshot(r *http.Request, repo repository.HostedRepository, p authorization.Principal) ([]repository.HelmPublication, error) {
	all, err := m.store.ListHelmPublications(r.Context(), repo.ID, helmprotocol.MaxCharts+1)
	if err != nil {
		return nil, err
	}
	if len(all) > helmprotocol.MaxCharts {
		return nil, errInventoryLimit
	}
	out := []repository.HelmPublication{}
	for _, in := range all {
		if m.allowed(r.Context(), p, repo, in.Name) {
			out = append(out, in)
		}
	}
	return out, nil
}

type indexEntry struct {
	helmprotocol.Metadata `yaml:",inline"`
	Created               time.Time `yaml:"created"`
	Digest                string    `yaml:"digest"`
	URLs                  []string  `yaml:"urls"`
}

func (m *Module) index(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository) {
	p, ok := m.authorize(w, r, repo, authorization.RepositoryRead, "", true)
	if !ok {
		return
	}
	publications, err := m.snapshot(r, repo, p)
	if err != nil {
		m.failure(w, err)
		return
	}
	entries := map[string][]indexEntry{}
	generated := repo.CreatedAt.UTC()
	if generated.IsZero() {
		generated = time.Unix(0, 0).UTC()
	}
	for _, in := range publications {
		var meta helmprotocol.Metadata
		if json.Unmarshal(in.MetadataJSON, &meta) != nil || meta.Name != in.Name || meta.Version != in.Version || !helmprotocol.ValidIdentity(meta.Name, meta.Version) {
			m.failure(w, errIntegrity)
			return
		}
		entries[in.Name] = append(entries[in.Name], indexEntry{Metadata: meta, Created: in.PublishedAt.UTC(), Digest: strings.TrimPrefix(in.Digest, "sha256:"), URLs: []string{assetRelative(in.Name, in.Version, false)}})
		if in.PublishedAt.After(generated) {
			generated = in.PublishedAt.UTC()
		}
	}
	for name := range entries {
		sort.Slice(entries[name], func(i, j int) bool {
			a, _ := semver.StrictNewVersion(entries[name][i].Version)
			b, _ := semver.StrictNewVersion(entries[name][j].Version)
			if a.Equal(b) {
				return entries[name][i].Version > entries[name][j].Version
			}
			return a.GreaterThan(b)
		})
	}
	body, err := yaml.Marshal(struct {
		APIVersion string                  `yaml:"apiVersion"`
		Generated  time.Time               `yaml:"generated"`
		Entries    map[string][]indexEntry `yaml:"entries"`
	}{"v1", generated, entries})
	if err != nil {
		m.failure(w, err)
		return
	}
	sum := sha256.Sum256(body)
	etag := `"sha256:` + hex.EncodeToString(sum[:]) + `"`
	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("Vary", "Authorization")
	w.Header().Set("ETag", etag)
	for _, candidate := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		if strings.TrimSpace(candidate) == etag || strings.TrimSpace(candidate) == "*" || strings.TrimSpace(candidate) == "W/"+etag {
			w.WriteHeader(304)
			return
		}
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}
func (m *Module) asset(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, name, version, filename string) {
	if !helmprotocol.ValidIdentity(name, version) {
		m.problem(w, 404, "not_found")
		return
	}
	if _, ok := m.authorize(w, r, repo, authorization.RepositoryRead, name, false); !ok {
		return
	}
	expected := helmprotocol.Filename(name, version)
	provAsset := filename == expected+".prov"
	if filename != expected && !provAsset {
		m.problem(w, 404, "not_found")
		return
	}
	p, err := m.store.GetHelmPublication(r.Context(), repo.ID, name, version)
	if err != nil {
		m.failure(w, err)
		return
	}
	if provAsset && p.ProvenanceSize == 0 {
		m.problem(w, 404, "not_found")
		return
	}
	in := repository.HelmSession{RepositoryID: p.RepositoryID, Name: p.Name, Version: p.Version, Digest: p.Digest, Size: p.Size, ObjectKey: p.ObjectKey, ProvenanceDigest: p.ProvenanceDigest, ProvenanceSize: p.ProvenanceSize, ProvenanceKey: p.ProvenanceKey, MetadataJSON: p.MetadataJSON, ChartYAML: p.ChartYAML}
	chart, prov, err := m.verify(r.Context(), in)
	if err != nil {
		m.problem(w, 503, "helm_object_integrity_failure")
		return
	}
	defer func() {
		_ = chart.Close()
		if prov != nil {
			_ = prov.Close()
		}
	}()
	content, digest := chart, p.Digest
	w.Header().Set("Content-Type", "application/gzip")
	if provAsset {
		content, digest = prov, p.ProvenanceDigest
		w.Header().Set("Content-Type", "application/pgp-signature")
	}
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("Vary", "Authorization")
	w.Header().Set("ETag", `"`+digest+`"`)
	http.ServeContent(w, r, filename, p.PublishedAt, content.File())
}

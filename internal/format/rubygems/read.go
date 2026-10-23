package rubygems

import (
	"bytes"
	"crypto/md5" // Compact-index ETag is a transport checksum, not package identity.
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	protocol "github.com/ccsert/artifact-gateway/internal/protocol/rubygems"
	"github.com/ccsert/artifact-gateway/internal/repository"
	domain "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
)

func metadata(p domain.Publication) (protocol.Metadata, error) {
	m, e := protocol.ParseMetadata(p.MetadataYAML)
	if e != nil || m.Name != p.Name || m.Version != p.Version || m.Platform != p.Platform {
		return m, errIntegrity
	}
	encoded, _ := json.Marshal(m)
	if !bytes.Equal(encoded, p.MetadataJSON) {
		return m, errIntegrity
	}
	return m, nil
}
func (m *Module) read(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, relative string) {
	if strings.HasPrefix(relative, "gems/") && strings.HasSuffix(relative, ".gem") {
		m.asset(w, r, repo, strings.TrimSuffix(strings.TrimPrefix(relative, "gems/"), ".gem"), false)
		return
	}
	if strings.HasPrefix(relative, "quick/Marshal.4.8/") && strings.HasSuffix(relative, ".gemspec.rz") {
		m.asset(w, r, repo, strings.TrimSuffix(strings.TrimPrefix(relative, "quick/Marshal.4.8/"), ".gemspec.rz"), true)
		return
	}
	name := ""
	if strings.HasPrefix(relative, "info/") {
		name = strings.TrimPrefix(relative, "info/")
		if !protocol.ValidName(name) {
			m.problem(w, 404, "not_found")
			return
		}
	}
	if name == "" && relative != "names" && relative != "versions" && relative != "specs.4.8.gz" && relative != "latest_specs.4.8.gz" && relative != "prerelease_specs.4.8.gz" {
		m.problem(w, 404, "not_found")
		return
	}
	p, ok := m.authorize(w, r, repo, authorization.RepositoryRead, name, name == "")
	if !ok {
		return
	}
	records, e := m.store.List(r.Context(), repo.ID, protocol.MaxIndexEntries+1)
	if e != nil {
		m.failure(w, e)
		return
	}
	if len(records) > protocol.MaxIndexEntries {
		m.failure(w, errIntegrity)
		return
	}
	entries := []protocol.IndexEntry{}
	for _, record := range records {
		if name != "" && record.Name != name || !m.allowed(r.Context(), p, repo, record.Name) {
			continue
		}
		meta, e := metadata(record)
		if e != nil {
			m.failure(w, e)
			return
		}
		entries = append(entries, protocol.IndexEntry{Metadata: meta, Digest: record.Digest})
	}
	var body []byte
	contentType := "text/plain; charset=utf-8"
	switch relative {
	case "names":
		body = protocol.Names(entries)
	case "versions":
		body = protocol.Versions(entries)
	case "specs.4.8.gz":
		body = protocol.LegacySpecs(entries, "all")
		contentType = "application/octet-stream"
	case "latest_specs.4.8.gz":
		body = protocol.LegacySpecs(entries, "latest")
		contentType = "application/octet-stream"
	case "prerelease_specs.4.8.gz":
		body = protocol.LegacySpecs(entries, "prerelease")
		contentType = "application/octet-stream"
	default:
		body = protocol.Info(entries)
	}
	sum := md5.Sum(body)
	m.respond(w, r, repo, p, name, body, contentType, `"`+hex.EncodeToString(sum[:])+`"`, "rubygems.index", "")
}
func (m *Module) asset(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, fullName string, quick bool) {
	if !protocol.SafePath(fullName) || strings.Contains(fullName, "/") {
		m.problem(w, 404, "not_found")
		return
	}
	record, e := m.store.Get(r.Context(), repo.ID, fullName)
	if e != nil {
		m.failure(w, e)
		return
	}
	p, ok := m.authorize(w, r, repo, authorization.RepositoryRead, record.Name, false)
	if !ok {
		return
	}
	meta, e := metadata(record)
	if e != nil {
		m.failure(w, e)
		return
	}
	if quick {
		body := protocol.QuickSpecification(meta)
		sum := sha256.Sum256(body)
		m.respond(w, r, repo, p, fullName, body, "application/octet-stream", `"sha256:`+hex.EncodeToString(sum[:])+`"`, "rubygems.gemspec", record.Digest)
		return
	}
	spool, e := m.openVerified(r.Context(), record.ObjectKey, record.Digest, record.Size)
	if e != nil {
		m.failure(w, e)
		return
	}
	defer func() { _ = spool.Close() }()
	body, e := io.ReadAll(spool.Reader())
	if e != nil {
		m.failure(w, e)
		return
	}
	m.respond(w, r, repo, p, fullName, body, "application/octet-stream", `"`+record.Digest+`"`, "download", record.Digest)
}
func (m *Module) respond(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, p authorization.Principal, resource string, body []byte, contentType, etag, operation, digest string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
	w.Header().Set("ETag", etag)
	w.Header().Set("Vary", "Authorization")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	status := 200
	written := 0
	if r.Header.Get("If-None-Match") == etag {
		status = 304
		w.Header().Del("Content-Length")
		w.WriteHeader(304)
	} else {
		w.WriteHeader(200)
		if r.Method != http.MethodHead {
			written, _ = w.Write(body)
		}
	}
	_ = m.audit.RecordAudit(r.Context(), repository.AuditRecord{Actor: p.Actor, Repository: repo.Name, GroupName: repo.Name, Format: "rubygems", Resource: resource, Operation: operation, Outcome: repository.AuditResolved, Status: status, Bytes: int64(written), OccurredAt: time.Now().UTC(), Evidence: map[string]string{"digest": digest}})
}

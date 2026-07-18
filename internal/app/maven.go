package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type MavenClient interface {
	FetchMaven(context.Context, string, repository.Member, string, http.Header) (*http.Response, error)
}

func (c GiteaClient) FetchMaven(ctx context.Context, method string, member repository.Member, artifactPath string, headers http.Header) (*http.Response, error) {
	endpoint, err := url.Parse(member.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse Maven endpoint: %w", err)
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/" + artifactPath
	endpoint.RawPath = ""
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create Maven request: %w", err)
	}
	for _, name := range []string{"Accept", "If-Modified-Since", "If-None-Match", "Range"} {
		if value := headers.Get(name); value != "" {
			request.Header.Set(name, value)
		}
	}
	if member.Type == repository.MemberHosted {
		request.SetBasicAuth(c.Username, c.Token)
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch Maven content: %w", err)
	}
	return response, nil
}

type MavenHandler struct {
	Store         repository.MavenStore
	Authenticator Authenticator
	Client        MavenClient
	Metrics       *Metrics
}

func (h MavenHandler) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	actor, ok := h.authenticate(request)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Maven"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	groupName, artifactPath, ok := parseMavenPath(request.URL.Path)
	if !ok {
		http.NotFound(w, request)
		return
	}
	group, err := h.Store.GetMavenGroup(request.Context(), groupName)
	if err != nil {
		if auditErr := h.audit(request.Context(), groupName, artifactPath, "", actor, auditOutcome(err)); auditErr != nil {
			h.Metrics.failed.Add(1)
			http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
			return
		}
		h.Metrics.failed.Add(1)
		if !errors.Is(err, repository.ErrNotFound) {
			http.Error(w, "unable to resolve Maven group", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, request)
		return
	}
	if !group.Enabled {
		if err := h.audit(request.Context(), groupName, artifactPath, "", actor, repository.AuditGroupDisabled); err != nil {
			h.Metrics.failed.Add(1)
			http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
			return
		}
		h.Metrics.failed.Add(1)
		http.Error(w, "Maven group is disabled", http.StatusForbidden)
		return
	}
	members := prioritizeHosted(group.Members)
	if len(members) == 0 {
		if err := h.audit(request.Context(), groupName, artifactPath, "", actor, repository.AuditNotFound); err != nil {
			h.Metrics.failed.Add(1)
			http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
			return
		}
		h.Metrics.failed.Add(1)
		http.NotFound(w, request)
		return
	}
	hadFailure := false
	for _, member := range members {
		response, fetchErr := h.Client.FetchMaven(request.Context(), request.Method, member, artifactPath, request.Header)
		if fetchErr != nil {
			if err := h.audit(request.Context(), groupName, artifactPath, member.Name, actor, repository.AuditUpstreamError); err != nil {
				h.Metrics.failed.Add(1)
				http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
				return
			}
			hadFailure = true
			continue
		}
		if response.StatusCode == http.StatusNotFound {
			_ = response.Body.Close()
			if err := h.audit(request.Context(), groupName, artifactPath, member.Name, actor, repository.AuditNotFound); err != nil {
				h.Metrics.failed.Add(1)
				http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
				return
			}
			continue
		}
		if response.StatusCode == http.StatusNotModified {
			defer func() { _ = response.Body.Close() }()
			if err := h.audit(request.Context(), groupName, artifactPath, member.Name, actor, repository.AuditResolved); err != nil {
				h.Metrics.failed.Add(1)
				http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
				return
			}
			h.Metrics.resolved.Add(1)
			copyMavenHeaders(w.Header(), response.Header)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			_ = response.Body.Close()
			if err := h.audit(request.Context(), groupName, artifactPath, member.Name, actor, repository.AuditUpstreamError); err != nil {
				h.Metrics.failed.Add(1)
				http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
				return
			}
			hadFailure = true
			continue
		}
		defer func() { _ = response.Body.Close() }()
		if err := h.audit(request.Context(), groupName, artifactPath, member.Name, actor, repository.AuditResolved); err != nil {
			h.Metrics.failed.Add(1)
			http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
			return
		}
		h.Metrics.resolved.Add(1)
		copyMavenHeaders(w.Header(), response.Header)
		w.WriteHeader(response.StatusCode)
		if request.Method != http.MethodHead {
			_, _ = io.Copy(w, response.Body)
		}
		return
	}
	h.Metrics.failed.Add(1)
	if hadFailure {
		http.Error(w, "upstream repository unavailable", http.StatusBadGateway)
		return
	}
	http.NotFound(w, request)
}

func (h MavenHandler) authenticate(request *http.Request) (string, bool) {
	if principal, ok := h.Authenticator.Authenticate(request.Header.Get("Authorization")); ok {
		return principal.Actor, true
	}
	username, password, ok := request.BasicAuth()
	return username, ok && username != "" && tokenMatches(password, h.Authenticator.ResolverToken)
}

func (h MavenHandler) audit(ctx context.Context, groupName, artifactPath, memberName, actor string, outcome repository.AuditOutcome) error {
	return h.Store.RecordAudit(ctx, repository.AuditRecord{GroupName: groupName, Repository: artifactPath, MemberName: memberName, Actor: actor, Outcome: outcome, OccurredAt: time.Now().UTC()})
}

func parseMavenPath(path string) (groupName, artifactPath string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/maven/"), "/")
	if len(parts) < 2 || parts[0] == "" {
		return "", "", false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", "", false
		}
	}
	return parts[0], strings.Join(parts[1:], "/"), true
}

func prioritizeHosted(members []repository.Member) []repository.Member {
	result := make([]repository.Member, 0, len(members))
	for _, member := range members {
		if member.Type == repository.MemberHosted {
			result = append(result, member)
		}
	}
	for _, member := range members {
		if member.Type == repository.MemberProxy {
			result = append(result, member)
		}
	}
	return result
}

func auditOutcome(err error) repository.AuditOutcome {
	if errors.Is(err, repository.ErrNotFound) {
		return repository.AuditNotFound
	}
	return repository.AuditStorageError
}

func copyMavenHeaders(destination, source http.Header) {
	for _, name := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified", "Cache-Control", "Content-Disposition"} {
		if value := source.Get(name); value != "" {
			destination.Set(name, value)
		}
	}
}

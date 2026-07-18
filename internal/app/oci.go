package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const ociManifest = "manifests"
const ociBlob = "blobs"

type OCIClient interface {
	Fetch(context.Context, string, repository.Member, string, string, string, http.Header) (*http.Response, error)
}

type GiteaClient struct {
	HTTPClient *http.Client
	Username   string
	Token      string
}

func (c GiteaClient) Fetch(ctx context.Context, method string, member repository.Member, repositoryName, resource, reference string, headers http.Header) (*http.Response, error) {
	endpoint, err := url.Parse(strings.TrimRight(member.Endpoint, "/") + "/v2/" + repositoryName + "/" + resource + "/" + reference)
	if err != nil {
		return nil, fmt.Errorf("parse Gitea OCI endpoint: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create Gitea OCI request: %w", err)
	}
	if accept := headers.Get("Accept"); accept != "" {
		request.Header.Set("Accept", accept)
	}
	if rangeHeader := headers.Get("Range"); rangeHeader != "" {
		request.Header.Set("Range", rangeHeader)
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
		return nil, fmt.Errorf("fetch Gitea OCI content: %w", err)
	}
	return response, nil
}

type OCIHandler struct {
	Resolver      Resolver
	Client        OCIClient
	Authenticator Authenticator
	Cache         *OCICache
}

func (h OCIHandler) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/v2/" {
		h.authenticateProbe(w, request)
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writeOCIError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed")
		return
	}
	principal, ok := h.Authenticator.Authenticate(request.Header.Get("Authorization"))
	if !ok {
		writeOCIChallenge(w, request)
		return
	}
	repositoryName, resource, reference, ok := parseOCIPath(request.URL.Path)
	if !ok {
		writeOCIError(w, http.StatusNotFound, "NAME_UNKNOWN", "repository name not known to registry")
		return
	}
	groupName := strings.SplitN(repositoryName, "/", 2)[0]
	members, err := h.Resolver.ResolveOCIMembers(request.Context(), groupName, repositoryName, principal.Actor)
	if errors.Is(err, repository.ErrDisabled) {
		writeOCIError(w, http.StatusForbidden, "DENIED", "requested access to the resource is denied")
		return
	}
	if errors.Is(err, repository.ErrNotFound) {
		writeOCIError(w, http.StatusNotFound, "NAME_UNKNOWN", "repository name not known to registry")
		return
	}
	if err != nil {
		writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to resolve repository")
		return
	}
	cacheKey := ""
	hasHostedMember := false
	for _, member := range members {
		if member.Type == repository.MemberHosted {
			hasHostedMember = true
			break
		}
	}
	if h.Cache != nil {
		cacheKey = h.Cache.key(groupName, repositoryName, resource, reference)
	}
	if h.Cache != nil && !hasHostedMember {
		content, cacheErr := h.Cache.Load(request.Context(), cacheKey)
		if cacheErr == nil {
			h.Resolver.Metrics.ociCacheHit.Add(1)
			if err := h.Resolver.RecordOCIResolution(request.Context(), groupName, repositoryName, content.Member, principal.Actor); err != nil {
				writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to record repository audit")
				return
			}
			serveCachedOCIContent(w, request, reference, content)
			return
		}
		if errors.Is(cacheErr, errOCICacheNegative) {
			h.Resolver.Metrics.ociCacheHit.Add(1)
			h.Resolver.RecordOCIRequestFailure()
			writeOCIError(w, http.StatusNotFound, map[string]string{ociManifest: "MANIFEST_UNKNOWN", ociBlob: "BLOB_UNKNOWN"}[resource], "resource unknown to registry")
			return
		}
		h.Resolver.Metrics.ociCacheMiss.Add(1)
	}
	var content CachedOCIContent
	if request.Method == http.MethodGet && h.Cache != nil {
		// The complete Hosted-first resolution, Proxy fallback, verification, and
		// publication is one operation, including Hosted-first Groups.
		content, err = h.Cache.Do(cacheKey, func() (CachedOCIContent, error) {
			if !hasHostedMember {
				if cached, loadErr := h.Cache.Load(request.Context(), cacheKey); loadErr == nil {
					return cached, nil
				}
			}
			fetched, fetchErr := h.fetchOCIContent(request.Context(), request.Method, members, repositoryName, resource, reference, request.Header, groupName, principal.Actor, cacheKey)
			if fetchErr != nil {
				return CachedOCIContent{}, fetchErr
			}
			if fetched.cacheable {
				if cacheErr := h.Cache.Store(request.Context(), cacheKey, fetched); cacheErr != nil {
					return CachedOCIContent{}, cacheErr
				}
			}
			return fetched, nil
		})
	} else {
		content, err = h.fetchOCIContent(request.Context(), request.Method, members, repositoryName, resource, reference, request.Header, groupName, principal.Actor, cacheKey)
	}
	if err != nil {
		h.Resolver.RecordOCIRequestFailure()
		var fetchErr *ociFetchError
		if errors.As(err, &fetchErr) {
			if request.Method == http.MethodGet && h.Cache != nil && fetchErr.status == http.StatusNotFound {
				_ = h.Cache.StoreNegative(request.Context(), cacheKey)
			}
			writeOCIError(w, fetchErr.status, fetchErr.code, fetchErr.message)
			return
		}
		writeOCIError(w, http.StatusBadGateway, "UNKNOWN", "upstream registry unavailable")
		return
	}
	serveCachedOCIContent(w, request, reference, content)
}

type ociFetchError struct {
	status  int
	code    string
	message string
}

func (e *ociFetchError) Error() string { return e.message }

func (h OCIHandler) fetchOCIContent(ctx context.Context, method string, members []repository.Member, repositoryName, resource, reference string, requestHeaders http.Header, groupName, actor, cacheKey string) (CachedOCIContent, error) {
	headers := requestHeaders.Clone()
	headers.Del("Range")
	hadUpstreamFailure := false
	lastUpstreamStatus := 0
	digestInvalid := false
	hostedAttempted := false
	for _, member := range members {
		if member.Type == repository.MemberProxy && hostedAttempted && h.Cache != nil {
			cached, cacheErr := h.Cache.Load(ctx, cacheKey)
			if cacheErr == nil {
				h.Resolver.Metrics.ociCacheHit.Add(1)
				if err := h.Resolver.RecordOCIResolution(ctx, groupName, repositoryName, cached.Member, actor); err != nil {
					return CachedOCIContent{}, err
				}
				return cached, nil
			}
			if errors.Is(cacheErr, errOCICacheNegative) {
				h.Resolver.Metrics.ociCacheHit.Add(1)
				return CachedOCIContent{}, &ociFetchError{http.StatusNotFound, map[string]string{ociManifest: "MANIFEST_UNKNOWN", ociBlob: "BLOB_UNKNOWN"}[resource], "resource unknown to registry"}
			}
			h.Resolver.Metrics.ociCacheMiss.Add(1)
		}
		if member.Type == repository.MemberHosted {
			hostedAttempted = true
		}
		if member.Type == repository.MemberProxy && h.Cache != nil && !h.Cache.ProxyAllowed(member.Endpoint) {
			continue
		}
		if member.Type == repository.MemberProxy && h.Cache != nil && !h.Cache.UpstreamAllowed(member.Endpoint) {
			h.Resolver.Metrics.ociCircuitOpen.Add(1)
			hadUpstreamFailure = true
			continue
		}
		response, fetchErr := h.Client.Fetch(ctx, method, member, repositoryName, resource, reference, headers)
		if fetchErr != nil {
			if err := h.Resolver.RecordOCIFailure(ctx, groupName, repositoryName, member.Name, actor, repository.AuditUpstreamError); err != nil {
				return CachedOCIContent{}, err
			}
			if member.Type == repository.MemberProxy && h.Cache != nil {
				h.Cache.RecordUpstreamFailure(member.Endpoint)
			}
			hadUpstreamFailure = true
			continue
		}
		if response.StatusCode == http.StatusNotFound {
			_ = response.Body.Close()
			if err := h.Resolver.RecordOCIFailure(ctx, groupName, repositoryName, member.Name, actor, repository.AuditNotFound); err != nil {
				return CachedOCIContent{}, err
			}
			continue
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			_ = response.Body.Close()
			if err := h.Resolver.RecordOCIFailure(ctx, groupName, repositoryName, member.Name, actor, repository.AuditUpstreamError); err != nil {
				return CachedOCIContent{}, err
			}
			if member.Type == repository.MemberProxy && h.Cache != nil {
				h.Cache.RecordUpstreamFailure(member.Endpoint)
			}
			hadUpstreamFailure = true
			lastUpstreamStatus = response.StatusCode
			continue
		}
		content, err := verifyOCIResponse(response, reference)
		if err != nil {
			_ = response.Body.Close()
			if auditErr := h.Resolver.RecordOCIFailure(ctx, groupName, repositoryName, member.Name, actor, repository.AuditUpstreamError); auditErr != nil {
				return CachedOCIContent{}, auditErr
			}
			if member.Type == repository.MemberProxy && h.Cache != nil {
				h.Cache.RecordUpstreamFailure(member.Endpoint)
			}
			hadUpstreamFailure = true
			digestInvalid = true
			continue
		}
		if err := h.Resolver.RecordOCIResolution(ctx, groupName, repositoryName, member.Name, actor); err != nil {
			return CachedOCIContent{}, err
		}
		if h.Cache != nil {
			h.Cache.RecordUpstreamSuccess(member.Endpoint)
		}
		return CachedOCIContent{Body: content, Digest: response.Header.Get("Docker-Content-Digest"), ContentType: response.Header.Get("Content-Type"), Member: member.Name, cacheable: member.Type == repository.MemberProxy}, nil
	}
	if hadUpstreamFailure {
		if digestInvalid {
			return CachedOCIContent{}, &ociFetchError{http.StatusBadGateway, "DIGEST_INVALID", "upstream content failed digest verification"}
		}
		if lastUpstreamStatus != 0 {
			return CachedOCIContent{}, &ociFetchError{http.StatusBadGateway, "UNKNOWN", "upstream registry returned an error"}
		}
		return CachedOCIContent{}, errOCIUpstreamOpen
	}
	return CachedOCIContent{}, &ociFetchError{http.StatusNotFound, map[string]string{ociManifest: "MANIFEST_UNKNOWN", ociBlob: "BLOB_UNKNOWN"}[resource], "resource unknown to registry"}
}

func (h OCIHandler) authenticateProbe(w http.ResponseWriter, request *http.Request) {
	if _, ok := h.Authenticator.Authenticate(request.Header.Get("Authorization")); !ok {
		writeOCIChallenge(w, request)
		return
	}
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	w.WriteHeader(http.StatusOK)
}

func parseOCIPath(path string) (repositoryName, resource, reference string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/v2/"), "/")
	for i, part := range parts {
		if (part == ociManifest || part == ociBlob) && i > 0 && i+1 < len(parts) && i+2 == len(parts) {
			return strings.Join(parts[:i], "/"), part, parts[i+1], true
		}
	}
	return "", "", "", false
}

func verifyOCIResponse(response *http.Response, reference string) ([]byte, error) {
	expectedDigest := response.Header.Get("Docker-Content-Digest")
	if strings.HasPrefix(reference, "sha256:") && expectedDigest != reference {
		return nil, errors.New("upstream digest header does not match requested digest")
	}
	if !strings.HasPrefix(expectedDigest, "sha256:") {
		return nil, errors.New("upstream response does not include a sha256 digest")
	}
	if response.Request.Method == http.MethodHead {
		return nil, nil
	}
	hash := sha256.New()
	content, err := io.ReadAll(io.TeeReader(response.Body, hash))
	if err != nil {
		return nil, err
	}
	if err := response.Body.Close(); err != nil {
		return nil, err
	}
	if "sha256:"+hex.EncodeToString(hash.Sum(nil)) != expectedDigest {
		return nil, errors.New("upstream body digest does not match requested digest")
	}
	return content, nil
}

func serveCachedOCIContent(w http.ResponseWriter, request *http.Request, reference string, content CachedOCIContent) {
	if content.ContentType != "" {
		w.Header().Set("Content-Type", content.ContentType)
	}
	w.Header().Set("Docker-Content-Digest", content.Digest)
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	if request.Header.Get("Range") != "" && request.Method == http.MethodGet {
		http.ServeContent(w, request, reference, time.Time{}, bytes.NewReader(content.Body))
		return
	}
	w.Header().Set("Content-Length", utoa(uint64(len(content.Body))))
	w.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = w.Write(content.Body)
	}
}

func writeOCIChallenge(w http.ResponseWriter, request *http.Request) {
	realm := "http://" + request.Host + "/auth/token"
	if request.TLS != nil {
		realm = "https://" + request.Host + "/auth/token"
	}
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	w.Header().Set("WWW-Authenticate", `Bearer realm="`+realm+`",service="artifact-gateway"`)
	writeOCIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
}

func writeOCIError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]string{{"code": code, "message": message}}})
}

func (h OCIHandler) Token(w http.ResponseWriter, request *http.Request) {
	username, password, ok := request.BasicAuth()
	if !ok || username == "" || !tokenMatches(password, h.Authenticator.ResolverToken) {
		writeOCIChallenge(w, request)
		return
	}
	token := h.Authenticator.IssueToken(username)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"token": token, "access_token": token, "expires_in": 300, "issued_at": time.Now().UTC().Format(time.RFC3339)})
}

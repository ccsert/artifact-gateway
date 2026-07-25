package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	ociprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/oci"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const ociManifest = "manifests"
const ociBlob = "blobs"
const ociSharedWorkTimeout = 30 * time.Second

var errOCIUpstreamOpen = errors.New("OCI upstream circuit is open")

type OCIClient interface {
	Fetch(context.Context, string, repository.Member, string, string, string, http.Header) (*http.Response, error)
}

// UpstreamClient is used only by legacy Group reads. Native Hosted repositories
// are served from PostgreSQL metadata and the object store.
type UpstreamClient struct{ HTTPClient *http.Client }

func (c UpstreamClient) Fetch(ctx context.Context, method string, member repository.Member, repositoryName, resource, reference string, headers http.Header) (*http.Response, error) {
	endpoint, err := url.Parse(strings.TrimRight(member.Endpoint, "/") + "/v2/" + repositoryName + "/" + resource + "/" + reference)
	if err != nil {
		return nil, fmt.Errorf("parse OCI upstream endpoint: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create OCI upstream request: %w", err)
	}
	if accept := headers.Get("Accept"); accept != "" {
		request.Header.Set("Accept", accept)
	}
	if rangeHeader := headers.Get("Range"); rangeHeader != "" {
		request.Header.Set("Range", rangeHeader)
	}
	response, err := tracedHTTPClient(c.HTTPClient).Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch OCI upstream content: %w", err)
	}
	return response, nil
}

type OCIHandler struct {
	Resolver      Resolver
	Repositories  repository.HostedRepositoryStore
	Authorizer    RepositoryAuthorizer
	Client        OCIClient
	Authenticator Authenticator
	Cache         *OCICache
}

func (h OCIHandler) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/v2/" {
		h.authenticateProbe(w, request)
		return
	}
	repositoryName, resource, reference, ok := parseOCIPath(request.URL.Path)
	if !ok {
		writeOCIError(w, http.StatusNotFound, "NAME_UNKNOWN", "repository name not known to registry")
		return
	}
	groupName := strings.SplitN(repositoryName, "/", 2)[0]
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		if _, authenticated := h.Authenticator.Authenticate(request.Header.Get("Authorization")); !authenticated {
			if err := h.Resolver.RecordOCIAnonymousDenied(request.Context(), groupName, repositoryName, resource, request.Method, http.StatusMethodNotAllowed); err != nil {
				writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to record repository audit")
				return
			}
		}
		writeOCIError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed")
		return
	}
	h.Resolver.Metrics.recordRequest(repositoryName)
	principal, ok := h.Authenticator.Authenticate(request.Header.Get("Authorization"))
	if !ok && h.anonymousOCIAllowed(request.Context(), groupName) {
		principal = Principal{Actor: "anonymous"}
		ok = true
	}
	if !ok {
		if err := h.Resolver.RecordOCIAnonymousDenied(request.Context(), groupName, repositoryName, resource, request.Method, http.StatusUnauthorized); err != nil {
			writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to record repository audit")
			return
		}
		writeOCIChallenge(w, request)
		return
	}
	if principal.Actor == "anonymous" {
		h.Resolver.Metrics.recordAnonymousRead()
	}
	if principal.Actor != "anonymous" && !h.Authenticator.CanReadRepository(principal, repositoryName) {
		if err := h.Resolver.RecordOCIFailure(request.Context(), groupName, repositoryName, "", principal.Actor, repository.AuditAccessDenied); err != nil {
			writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to record repository audit")
			return
		}
		h.Resolver.RecordOCIRequestFailure()
		writeOCIError(w, http.StatusForbidden, "DENIED", "requested access to the resource is denied")
		return
	}
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
	if principal.Actor != "anonymous" {
		var hadAuthorizationDenied bool
		members, hadAuthorizationDenied, err = h.authorizedOCIMembers(request.Context(), groupName, repositoryName, resource, request.Method, principal, members)
		if err != nil {
			writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to record repository audit")
			return
		}
		if len(members) == 0 && hadAuthorizationDenied {
			h.Resolver.RecordOCIRequestFailure()
			writeOCIError(w, http.StatusForbidden, "DENIED", "requested access to the resource is denied")
			return
		}
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
		cacheKey = h.Cache.Key(groupName, repositoryName, resource, reference)
	}
	if h.Cache != nil && !hasHostedMember {
		content, cacheErr := h.Cache.Load(request.Context(), cacheKey)
		if cacheErr == nil {
			if content.Endpoint == "" {
				h.Cache.Invalidate(request.Context(), cacheKey)
				cacheErr = errOCICacheMiss
			}
			if cacheErr == nil && !h.cacheSourcePresent(content, members) {
				h.Cache.Invalidate(request.Context(), cacheKey)
				cacheErr = errOCICacheMiss
			}
			if cacheErr == nil && !h.cacheSourceAllowed(content, members) {
				h.Resolver.Metrics.ociProxyDenied.Add(1)
				if err := h.Resolver.RecordOCIFailure(request.Context(), groupName, repositoryName, content.Member, principal.Actor, repository.AuditProxyDenied); err != nil {
					writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to record repository audit")
					return
				}
				writeOCIError(w, http.StatusForbidden, "DENIED", "upstream repository is not allowed")
				return
			}
			if cacheErr == nil {
				h.Resolver.Metrics.ociCacheHit.Add(1)
				h.Resolver.Metrics.recordCache(repositoryName, true)
				if err := h.Resolver.RecordOCIResolution(request.Context(), groupName, repositoryName, content.Member, principal.Actor); err != nil {
					writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to record repository audit")
					return
				}
				serveCachedOCIContent(w, request, reference, content)
				return
			}
		}
		if errors.Is(cacheErr, errOCICacheNegative) {
			if !h.cacheSourcePresent(content, members) {
				h.Cache.Invalidate(request.Context(), cacheKey)
				cacheErr = errOCICacheMiss
			} else if !h.cacheSourceAllowed(content, members) {
				h.Resolver.Metrics.ociProxyDenied.Add(1)
				if err := h.Resolver.RecordOCIFailure(request.Context(), groupName, repositoryName, content.Member, principal.Actor, repository.AuditProxyDenied); err != nil {
					writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to record repository audit")
					return
				}
				writeOCIError(w, http.StatusForbidden, "DENIED", "upstream repository is not allowed")
				return
			}
			if errors.Is(cacheErr, errOCICacheMiss) {
				h.Resolver.Metrics.ociCacheMiss.Add(1)
				h.Resolver.Metrics.recordCache(repositoryName, false)
			} else {
				h.Resolver.Metrics.ociCacheHit.Add(1)
				h.Resolver.Metrics.recordCache(repositoryName, true)
				h.Resolver.Metrics.ociNegativeHit.Add(1)
				h.Resolver.RecordOCIRequestFailure()
				writeOCIError(w, http.StatusNotFound, map[string]string{ociManifest: "MANIFEST_UNKNOWN", ociBlob: "BLOB_UNKNOWN"}[resource], "resource unknown to registry")
				return
			}
		}
		h.Resolver.Metrics.ociCacheMiss.Add(1)
		h.Resolver.Metrics.recordCache(repositoryName, false)
	}
	var content CachedOCIContent
	if request.Method == http.MethodGet && h.Cache != nil {
		workCtx, cancel := context.WithTimeout(context.WithoutCancel(request.Context()), ociSharedWorkTimeout)
		defer cancel()
		// The complete Hosted-first resolution, Proxy fallback, verification, and
		// publication is one operation, including Hosted-first Groups.
		content, err = h.Cache.Do(workCtx, cacheKey, func(fetchCtx context.Context) (CachedOCIContent, error) {
			if !hasHostedMember {
				if cached, loadErr := h.Cache.Load(fetchCtx, cacheKey); loadErr == nil {
					if cached.Endpoint == "" {
						h.Cache.Invalidate(fetchCtx, cacheKey)
					} else if !h.cacheSourcePresent(cached, members) {
						h.Cache.Invalidate(fetchCtx, cacheKey)
					} else if !h.cacheSourceAllowed(cached, members) {
						h.Resolver.Metrics.ociProxyDenied.Add(1)
						if auditErr := h.Resolver.RecordOCIFailure(fetchCtx, groupName, repositoryName, cached.Member, principal.Actor, repository.AuditProxyDenied); auditErr != nil {
							return CachedOCIContent{}, auditErr
						}
						return CachedOCIContent{}, &ociFetchError{status: http.StatusForbidden, code: "DENIED", message: "upstream repository is not allowed"}
					} else {
						return cached, nil
					}
				}
			}
			fetched, fetchErr := h.fetchOCIContent(fetchCtx, request.Method, members, repositoryName, resource, reference, request.Header, groupName, principal.Actor, cacheKey)
			if fetchErr != nil {
				var notFound *ociFetchError
				if errors.As(fetchErr, &notFound) && notFound.status == http.StatusNotFound {
					if cacheErr := h.Cache.StoreNegative(fetchCtx, cacheKey, notFound.member); cacheErr != nil {
						return CachedOCIContent{}, cacheErr
					}
				}
				return CachedOCIContent{}, fetchErr
			}
			if fetched.Cacheable() {
				fetched.Repository = repositoryName
				if cacheErr := h.Cache.Store(fetchCtx, cacheKey, fetched); cacheErr != nil {
					if errors.Is(cacheErr, ErrCacheQuotaExceeded) {
						h.Resolver.Metrics.cacheQuotaDenied.Add(1)
						return fetched, nil
					}
					fetched.Cleanup()
					return CachedOCIContent{}, cacheErr
				}
				fetched.Cleanup()
				return h.Cache.Load(fetchCtx, cacheKey)
			}
			if hasHostedMember && fetched.HasTemporaryReader() {
				staged, stageErr := h.Cache.Stage(fetchCtx, fetched)
				fetched.Cleanup()
				if stageErr != nil {
					return CachedOCIContent{}, stageErr
				}
				return staged, nil
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
			writeOCIError(w, fetchErr.status, fetchErr.code, fetchErr.message)
			return
		}
		writeOCIError(w, http.StatusBadGateway, "UNKNOWN", "upstream registry unavailable")
		return
	}
	defer content.Cleanup()
	serveCachedOCIContent(w, request, reference, content)
}

func (h OCIHandler) authorizedOCIMembers(ctx context.Context, groupName, repositoryName, resource, method string, principal Principal, members []repository.Member) ([]repository.Member, bool, error) {
	access := groupMemberAccess{Repositories: h.Repositories, Authorizer: h.Authorizer, Format: repository.FormatOCI}
	return access.filterManaged(ctx, principal, members, func(member repository.Member, decision AuthorizationDecision) error {
		return h.Resolver.RecordOCIGrantDenied(ctx, groupName, repositoryName, resource, method, member.Name, principal.Actor, decision)
	})
}

type ociFetchError struct {
	status  int
	code    string
	message string
	member  repository.Member
}

func (e *ociFetchError) Error() string { return e.message }

func (h OCIHandler) fetchOCIContent(ctx context.Context, method string, members []repository.Member, repositoryName, resource, reference string, requestHeaders http.Header, groupName, actor, cacheKey string) (CachedOCIContent, error) {
	headers := requestHeaders.Clone()
	headers.Del("Range")
	hadUpstreamFailure := false
	lastUpstreamStatus := 0
	digestInvalid := false
	hadProxyDenied := false
	hostedAttempted := false
	var lastNotFoundMember repository.Member
	for _, member := range members {
		if member.Type == repository.MemberProxy && hostedAttempted && h.Cache != nil {
			cached, cacheErr := h.Cache.Load(ctx, cacheKey)
			if cacheErr == nil {
				if cached.Endpoint == "" {
					h.Cache.Invalidate(ctx, cacheKey)
				} else if !h.cacheSourcePresent(cached, members) {
					h.Cache.Invalidate(ctx, cacheKey)
				} else if !h.cacheSourceAllowed(cached, members) {
					h.Resolver.Metrics.ociProxyDenied.Add(1)
					if auditErr := h.Resolver.RecordOCIFailure(ctx, groupName, repositoryName, cached.Member, actor, repository.AuditProxyDenied); auditErr != nil {
						return CachedOCIContent{}, auditErr
					}
					return CachedOCIContent{}, &ociFetchError{status: http.StatusForbidden, code: "DENIED", message: "upstream repository is not allowed"}
				} else {
					h.Resolver.Metrics.ociCacheHit.Add(1)
					h.Resolver.Metrics.recordCache(repositoryName, true)
					if err := h.Resolver.RecordOCIResolution(ctx, groupName, repositoryName, cached.Member, actor); err != nil {
						return CachedOCIContent{}, err
					}
					return cached, nil
				}
			}
			if errors.Is(cacheErr, errOCICacheNegative) {
				if !h.cacheSourcePresent(cached, members) {
					h.Cache.Invalidate(ctx, cacheKey)
				} else if !h.cacheSourceAllowed(cached, members) {
					h.Resolver.Metrics.ociProxyDenied.Add(1)
					if auditErr := h.Resolver.RecordOCIFailure(ctx, groupName, repositoryName, cached.Member, actor, repository.AuditProxyDenied); auditErr != nil {
						return CachedOCIContent{}, auditErr
					}
					return CachedOCIContent{}, &ociFetchError{status: http.StatusForbidden, code: "DENIED", message: "upstream repository is not allowed"}
				} else {
					h.Resolver.Metrics.ociCacheHit.Add(1)
					h.Resolver.Metrics.recordCache(repositoryName, true)
					h.Resolver.Metrics.ociNegativeHit.Add(1)
					return CachedOCIContent{}, &ociFetchError{status: http.StatusNotFound, code: map[string]string{ociManifest: "MANIFEST_UNKNOWN", ociBlob: "BLOB_UNKNOWN"}[resource], message: "resource unknown to registry"}
				}
			}
			h.Resolver.Metrics.ociCacheMiss.Add(1)
			h.Resolver.Metrics.recordCache(repositoryName, false)
		}
		if member.Type == repository.MemberHosted {
			hostedAttempted = true
		}
		if member.Type == repository.MemberProxy && h.Cache != nil && !h.Cache.ProxyAllowed(member.Endpoint) {
			h.Resolver.Metrics.ociProxyDenied.Add(1)
			hadProxyDenied = true
			if err := h.Resolver.RecordOCIFailure(ctx, groupName, repositoryName, member.Name, actor, repository.AuditProxyDenied); err != nil {
				return CachedOCIContent{}, err
			}
			continue
		}
		if member.Type == repository.MemberProxy && h.Cache != nil && !h.Cache.UpstreamAllowed(ctx, member.Endpoint) {
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
				h.Cache.RecordUpstreamFailure(ctx, member.Endpoint)
			}
			hadUpstreamFailure = true
			continue
		}
		if response.StatusCode == http.StatusNotFound {
			_ = response.Body.Close()
			if err := h.Resolver.RecordOCIFailure(ctx, groupName, repositoryName, member.Name, actor, repository.AuditNotFound); err != nil {
				return CachedOCIContent{}, err
			}
			lastNotFoundMember = member
			continue
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			_ = response.Body.Close()
			if err := h.Resolver.RecordOCIFailure(ctx, groupName, repositoryName, member.Name, actor, repository.AuditUpstreamError); err != nil {
				return CachedOCIContent{}, err
			}
			if member.Type == repository.MemberProxy && h.Cache != nil {
				h.Cache.RecordUpstreamFailure(ctx, member.Endpoint)
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
				h.Cache.RecordUpstreamFailure(ctx, member.Endpoint)
			}
			hadUpstreamFailure = true
			digestInvalid = true
			continue
		}
		if err := h.Resolver.RecordOCIResolution(ctx, groupName, repositoryName, member.Name, actor); err != nil {
			content.Cleanup()
			return CachedOCIContent{}, err
		}
		if h.Cache != nil {
			h.Cache.RecordUpstreamSuccess(ctx, member.Endpoint)
		}
		content.ContentType = response.Header.Get("Content-Type")
		content.Member = member.Name
		content.Endpoint = member.Endpoint
		content.SetCacheable(member.Type == repository.MemberProxy)
		return content, nil
	}
	if hadProxyDenied {
		return CachedOCIContent{}, &ociFetchError{status: http.StatusForbidden, code: "DENIED", message: "upstream repository is not allowed"}
	}
	if hadUpstreamFailure {
		if digestInvalid {
			return CachedOCIContent{}, &ociFetchError{status: http.StatusBadGateway, code: "DIGEST_INVALID", message: "upstream content failed digest verification"}
		}
		if lastUpstreamStatus != 0 {
			return CachedOCIContent{}, &ociFetchError{status: http.StatusBadGateway, code: "UNKNOWN", message: "upstream registry returned an error"}
		}
		return CachedOCIContent{}, errOCIUpstreamOpen
	}
	return CachedOCIContent{}, &ociFetchError{status: http.StatusNotFound, code: map[string]string{ociManifest: "MANIFEST_UNKNOWN", ociBlob: "BLOB_UNKNOWN"}[resource], message: "resource unknown to registry", member: lastNotFoundMember}
}

func (h OCIHandler) cacheSourceAllowed(content CachedOCIContent, members []repository.Member) bool {
	return cacheSourceAllowed(content.Member, content.Endpoint, members, h.Cache.ProxyAllowed)
}

func (h OCIHandler) cacheSourcePresent(content CachedOCIContent, members []repository.Member) bool {
	return cacheSourcePresent(content.Member, content.Endpoint, members)
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

func verifyOCIResponse(response *http.Response, reference string) (CachedOCIContent, error) {
	expectedDigest := response.Header.Get("Docker-Content-Digest")
	if strings.HasPrefix(reference, "sha256:") && expectedDigest != reference {
		return CachedOCIContent{}, errors.New("upstream digest header does not match requested digest")
	}
	if !strings.HasPrefix(expectedDigest, "sha256:") {
		return CachedOCIContent{}, errors.New("upstream response does not include a sha256 digest")
	}
	if response.Request.Method == http.MethodHead {
		if err := response.Body.Close(); err != nil {
			return CachedOCIContent{}, err
		}
		return CachedOCIContent{Digest: expectedDigest}, nil
	}
	file, err := os.CreateTemp("", "artifact-gateway-oci-*")
	if err != nil {
		return CachedOCIContent{}, err
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), response.Body)
	closeResponseErr := response.Body.Close()
	closeFileErr := file.Close()
	if err != nil {
		_ = os.Remove(file.Name())
		return CachedOCIContent{}, err
	}
	if closeResponseErr != nil || closeFileErr != nil {
		_ = os.Remove(file.Name())
		return CachedOCIContent{}, errors.Join(closeResponseErr, closeFileErr)
	}
	if "sha256:"+hex.EncodeToString(hash.Sum(nil)) != expectedDigest {
		_ = os.Remove(file.Name())
		return CachedOCIContent{}, errors.New("upstream body digest does not match requested digest")
	}
	return ociprotocol.NewVerifiedContent(expectedDigest, size, file.Name()), nil
}

func serveCachedOCIContent(w http.ResponseWriter, request *http.Request, reference string, content CachedOCIContent) {
	if content.ContentType != "" {
		w.Header().Set("Content-Type", content.ContentType)
	}
	w.Header().Set("Docker-Content-Digest", content.Digest)
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	if request.Header.Get("Range") != "" && request.Method == http.MethodGet {
		size := content.Size
		if size == 0 {
			reader, actualSize, err := content.Open(request.Context())
			if err != nil {
				writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to read cached content")
				return
			}
			_ = reader.Close()
			size = actualSize
		}
		start, end, ok := parseOCIRange(w, request, size)
		if !ok {
			return
		}
		reader, _, err := content.OpenRange(request.Context(), start, end-start+1)
		if err != nil {
			writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to read cached content")
			return
		}
		defer func() { _ = reader.Close() }()
		length := end - start + 1
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", "bytes "+utoa(uint64(start))+"-"+utoa(uint64(end))+"/"+utoa(uint64(size)))
		w.Header().Set("Content-Length", utoa(uint64(length)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.CopyN(w, reader, length)
		return
	}
	reader, size, err := content.Open(request.Context())
	if err != nil {
		writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to read cached content")
		return
	}
	if content.Size > 0 {
		size = content.Size
	}
	defer func() { _ = reader.Close() }()
	w.Header().Set("Content-Length", utoa(uint64(size)))
	w.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = io.Copy(w, reader)
	}
}

func parseOCIRange(w http.ResponseWriter, request *http.Request, size int64) (int64, int64, bool) {
	rangeHeader := strings.TrimPrefix(request.Header.Get("Range"), "bytes=")
	parts := strings.SplitN(rangeHeader, "-", 2)
	if len(parts) != 2 || strings.Contains(rangeHeader, ",") {
		w.Header().Set("Content-Range", "bytes */"+utoa(uint64(size)))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return 0, 0, false
	}
	start, end := int64(0), size-1
	if parts[0] == "" {
		suffix, err := parseOCIByte(parts[1])
		if err != nil || suffix <= 0 {
			w.Header().Set("Content-Range", "bytes */"+utoa(uint64(size)))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return 0, 0, false
		}
		if suffix < size {
			start = size - suffix
		}
	} else {
		value, err := parseOCIByte(parts[0])
		if err != nil || value >= size {
			w.Header().Set("Content-Range", "bytes */"+utoa(uint64(size)))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return 0, 0, false
		}
		start = value
		if parts[1] != "" {
			end, err = parseOCIByte(parts[1])
			if err != nil || end < start {
				w.Header().Set("Content-Range", "bytes */"+utoa(uint64(size)))
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return 0, 0, false
			}
			if end >= size {
				end = size - 1
			}
		}
	}
	return start, end, true
}

func parseOCIByte(value string) (int64, error) {
	if value == "" {
		return 0, errors.New("empty range")
	}
	var result int64
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, errors.New("invalid range")
		}
		if result > (1<<63-1)/10 {
			return 0, errors.New("range overflow")
		}
		result = result*10 + int64(char-'0')
	}
	return result, nil
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
	if !ok || username == "" || !h.Authenticator.ResolverPasswordMatches(password) {
		writeOCIChallenge(w, request)
		return
	}
	token := h.Authenticator.IssueToken(username)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"token": token, "access_token": token, "expires_in": 300, "issued_at": time.Now().UTC().Format(time.RFC3339)})
}

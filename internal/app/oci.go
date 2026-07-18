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
	headers := request.Header.Clone()
	headers.Del("Range")
	hadUpstreamFailure := false
	lastUpstreamStatus := 0
	digestInvalid := false
	for _, member := range members {
		response, fetchErr := h.Client.Fetch(request.Context(), request.Method, member, repositoryName, resource, reference, headers)
		if fetchErr != nil {
			if err := h.Resolver.RecordOCIFailure(request.Context(), groupName, repositoryName, member.Name, principal.Actor, repository.AuditUpstreamError); err != nil {
				h.Resolver.RecordOCIRequestFailure()
				writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to record repository audit")
				return
			}
			hadUpstreamFailure = true
			continue
		}
		if response.StatusCode == http.StatusNotFound {
			_ = response.Body.Close()
			if err := h.Resolver.RecordOCIFailure(request.Context(), groupName, repositoryName, member.Name, principal.Actor, repository.AuditNotFound); err != nil {
				h.Resolver.RecordOCIRequestFailure()
				writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to record repository audit")
				return
			}
			continue
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			_ = response.Body.Close()
			if err := h.Resolver.RecordOCIFailure(request.Context(), groupName, repositoryName, member.Name, principal.Actor, repository.AuditUpstreamError); err != nil {
				h.Resolver.RecordOCIRequestFailure()
				writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to record repository audit")
				return
			}
			hadUpstreamFailure = true
			lastUpstreamStatus = response.StatusCode
			continue
		}
		if err := verifyOCIResponse(response, reference); err != nil {
			_ = response.Body.Close()
			if auditErr := h.Resolver.RecordOCIFailure(request.Context(), groupName, repositoryName, member.Name, principal.Actor, repository.AuditUpstreamError); auditErr != nil {
				h.Resolver.RecordOCIRequestFailure()
				writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to record repository audit")
				return
			}
			hadUpstreamFailure = true
			digestInvalid = true
			continue
		}
		defer func() { _ = response.Body.Close() }()
		if err := h.Resolver.RecordOCIResolution(request.Context(), groupName, repositoryName, member.Name, principal.Actor); err != nil {
			writeOCIError(w, http.StatusInternalServerError, "UNKNOWN", "unable to record repository audit")
			return
		}
		copyOCIHeaders(w.Header(), response.Header)
		if request.Header.Get("Range") != "" && request.Method == http.MethodGet {
			content, ok := response.Body.(io.ReadSeeker)
			if !ok {
				writeOCIError(w, http.StatusBadGateway, "UNKNOWN", "verified content is not seekable")
				return
			}
			http.ServeContent(w, request, reference, time.Time{}, content)
			return
		}
		w.WriteHeader(response.StatusCode)
		if request.Method != http.MethodHead {
			_, _ = io.Copy(w, response.Body)
		}
		return
	}
	if hadUpstreamFailure {
		h.Resolver.RecordOCIRequestFailure()
		if digestInvalid {
			writeOCIError(w, http.StatusBadGateway, "DIGEST_INVALID", "upstream content failed digest verification")
			return
		}
		if lastUpstreamStatus != 0 {
			writeUpstreamOCIError(w, lastUpstreamStatus, resource)
			return
		}
		writeOCIError(w, http.StatusBadGateway, "UNKNOWN", "upstream registry unavailable")
		return
	}
	h.Resolver.RecordOCIRequestFailure()
	writeOCIError(w, http.StatusNotFound, map[string]string{ociManifest: "MANIFEST_UNKNOWN", ociBlob: "BLOB_UNKNOWN"}[resource], "resource unknown to registry")
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

func verifyOCIResponse(response *http.Response, reference string) error {
	expectedDigest := response.Header.Get("Docker-Content-Digest")
	if strings.HasPrefix(reference, "sha256:") && expectedDigest != reference {
		return errors.New("upstream digest header does not match requested digest")
	}
	if !strings.HasPrefix(expectedDigest, "sha256:") {
		return errors.New("upstream response does not include a sha256 digest")
	}
	if response.Request.Method == http.MethodHead {
		return nil
	}
	content, err := os.CreateTemp("", "artifact-gateway-oci-*")
	if err != nil {
		return err
	}
	removeContent := true
	defer func() {
		if removeContent {
			_ = content.Close()
			_ = os.Remove(content.Name())
		}
	}()
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(content, hash), response.Body); err != nil {
		return err
	}
	if err := response.Body.Close(); err != nil {
		return err
	}
	if "sha256:"+hex.EncodeToString(hash.Sum(nil)) != expectedDigest {
		return errors.New("upstream body digest does not match requested digest")
	}
	if _, err := content.Seek(0, io.SeekStart); err != nil {
		return err
	}
	response.Body = temporaryOCIContent{File: content, name: content.Name()}
	removeContent = false
	return nil
}

// temporaryOCIContent releases the on-disk verification buffer after it has
// been copied to the downstream client.
type temporaryOCIContent struct {
	*os.File
	name string
}

func (content temporaryOCIContent) Close() error {
	err := content.File.Close()
	removeErr := os.Remove(content.name)
	if err != nil {
		return err
	}
	return removeErr
}

func copyOCIHeaders(destination, source http.Header) {
	for _, key := range []string{"Content-Type", "Content-Length", "Content-Range", "Docker-Content-Digest", "Docker-Distribution-API-Version", "Etag", "Accept-Ranges"} {
		if value := source.Get(key); value != "" {
			destination.Set(key, value)
		}
	}
}

func writeUpstreamOCIError(w http.ResponseWriter, status int, resource string) {
	if status == http.StatusNotFound {
		code, message := "MANIFEST_UNKNOWN", "manifest unknown"
		if resource == ociBlob {
			code, message = "BLOB_UNKNOWN", "blob unknown to registry"
		}
		writeOCIError(w, status, code, message)
		return
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		writeOCIError(w, http.StatusBadGateway, "DENIED", "upstream registry denied access")
		return
	}
	writeOCIError(w, http.StatusBadGateway, "UNKNOWN", "upstream registry returned an error")
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

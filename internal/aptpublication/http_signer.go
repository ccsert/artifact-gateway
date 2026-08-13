package aptpublication

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const (
	maxHTTPSignerReleaseBytes  = int64(16 << 20)
	maxHTTPSignerResponseBytes = int64(24 << 20)
)

type HTTPSignerOptions struct {
	Endpoint string
	Token    string
	Timeout  time.Duration
	Client   *http.Client
}

// HTTPSigner keeps private-key operations outside Gateway processes. The wire
// contract contains only immutable Release bytes, their digest, and public
// signature evidence.
type HTTPSigner struct {
	endpoint string
	token    string
	timeout  time.Duration
	client   *http.Client
}

func NewHTTPSigner(options HTTPSignerOptions) (*HTTPSigner, error) {
	endpoint, err := validHTTPSignerEndpoint(options.Endpoint)
	if err != nil || len(options.Token) < 32 || len(options.Token) > 256 || strings.ContainsAny(options.Token, "\x00\r\n") ||
		options.Timeout < time.Second || options.Timeout > time.Minute {
		return nil, errors.New("APT signer HTTP configuration is invalid")
	}
	client := options.Client
	if client == nil {
		client = &http.Client{}
	}
	return &HTTPSigner{endpoint: endpoint, token: options.Token, timeout: options.Timeout, client: client}, nil
}

func validHTTPSignerEndpoint(raw string) (string, error) {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.Path == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", errors.New("invalid signer endpoint")
	}
	if parsed.Scheme == "http" && !loopbackSignerHost(parsed.Hostname()) {
		return "", errors.New("insecure signer endpoint")
	}
	return parsed.String(), nil
}

func loopbackSignerHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *HTTPSigner) SignRelease(ctx context.Context, input SignReleaseRequest) (SignReleaseResult, error) {
	if s == nil || input.Release == nil || input.RepositoryID == "" || input.SnapshotID == "" ||
		strings.ContainsAny(input.RepositoryID+input.SnapshotID, "\x00\r\n") || !repository.ValidAPTSHA256Digest(input.ReleaseDigest) {
		return SignReleaseResult{}, ErrInvalidSnapshotInput
	}
	release, err := io.ReadAll(io.LimitReader(input.Release, maxHTTPSignerReleaseBytes+1))
	if err != nil {
		return SignReleaseResult{}, fmt.Errorf("read APT Release: %w", err)
	}
	if len(release) == 0 || int64(len(release)) > maxHTTPSignerReleaseBytes || digestBytes(release) != input.ReleaseDigest {
		return SignReleaseResult{}, ErrInvalidSnapshotInput
	}
	requestContext, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, s.endpoint, bytes.NewReader(release))
	if err != nil {
		return SignReleaseResult{}, err
	}
	request.Header.Set("Authorization", "Bearer "+s.token)
	request.Header.Set("Content-Type", "text/plain; charset=utf-8")
	request.Header.Set("X-Artifact-Signer-Schema", "v1")
	request.Header.Set("X-Artifact-Repository-Id", input.RepositoryID)
	request.Header.Set("X-Artifact-Snapshot-Id", input.SnapshotID)
	request.Header.Set("X-Artifact-Release-Digest", input.ReleaseDigest)
	response, err := s.client.Do(request)
	if err != nil {
		return SignReleaseResult{}, fmt.Errorf("call APT signer: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return SignReleaseResult{}, fmt.Errorf("APT signer returned status %d", response.StatusCode)
	}
	mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" || response.Header.Get("X-Artifact-Signer-Schema") != "v1" {
		return SignReleaseResult{}, errors.New("APT signer response contract is invalid")
	}
	limited := io.LimitReader(response.Body, maxHTTPSignerResponseBytes+1)
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	var result SignReleaseResult
	if err = decoder.Decode(&result); err != nil || decoder.Decode(&struct{}{}) != io.EOF || !validSignatureResult(result) ||
		!signatureEnvelopeMatchesRelease(release, result) {
		return SignReleaseResult{}, errors.New("APT signer result is invalid")
	}
	return result, nil
}

func signatureEnvelopeMatchesRelease(release []byte, result SignReleaseResult) bool {
	cleartext, rest := clearsign.Decode(result.InRelease)
	if cleartext == nil || cleartext.ArmoredSignature == nil || len(rest) != 0 || !bytes.Equal(cleartext.Plaintext, release) {
		return false
	}
	if _, err := io.Copy(io.Discard, cleartext.ArmoredSignature.Body); err != nil {
		return false
	}
	detached, err := armor.Decode(bytes.NewReader(result.Detached))
	if err != nil || detached.Type != openpgp.SignatureType {
		return false
	}
	_, err = io.Copy(io.Discard, detached.Body)
	return err == nil
}

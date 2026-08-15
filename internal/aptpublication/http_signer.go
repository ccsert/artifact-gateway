package aptpublication

import (
	"bytes"
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const (
	maxHTTPSignerReleaseBytes   = int64(16 << 20)
	maxHTTPSignerResponseBytes  = int64(24 << 20)
	maxTrustedPublicKeysBytes   = int64(1 << 20)
	maxTLSRootCertificatesBytes = int64(1 << 20)
	maxTLSRootCertificates      = 8
)

var (
	ErrUntrustedSigner        = errors.New("APT signer fingerprint is not trusted")
	ErrSignerInvalidSignature = errors.New("APT signer signature is invalid")
)

type HTTPSignerOptions struct {
	Endpoint            string
	Token               string
	Timeout             time.Duration
	Client              *http.Client
	TrustedFingerprints []string
	TrustedPublicKeys   []byte
	TLSRootCertificates []byte
}

// HTTPSigner keeps private-key operations outside Gateway processes. The wire
// contract contains only immutable Release bytes, their digest, and public
// signature evidence.
type HTTPSigner struct {
	endpoint string
	token    string
	timeout  time.Duration
	client   *http.Client
	trusted  map[string]struct{}
	keyring  openpgp.EntityList
}

func NewHTTPSigner(options HTTPSignerOptions) (*HTTPSigner, error) {
	endpoint, err := validHTTPSignerEndpoint(options.Endpoint)
	if err != nil || len(options.Token) < 32 || len(options.Token) > 256 || strings.ContainsAny(options.Token, "\x00\r\n") ||
		options.Timeout < time.Second || options.Timeout > time.Minute {
		return nil, errors.New("APT signer HTTP configuration is invalid")
	}
	canonicalFingerprints, err := NormalizeTrustedSignerFingerprints(options.TrustedFingerprints)
	keyring, keyFingerprints, keyErr := parseTrustedSignerPublicKeys(options.TrustedPublicKeys)
	parsed, parseErr := url.Parse(endpoint)
	if err != nil || keyErr != nil || parseErr != nil || !sameFingerprints(canonicalFingerprints, keyFingerprints) ||
		(!IsLoopbackSignerHost(parsed.Hostname()) && (len(canonicalFingerprints) == 0 || len(keyring) == 0)) {
		return nil, errors.New("APT signer HTTP configuration is invalid")
	}
	trusted := make(map[string]struct{}, len(canonicalFingerprints))
	for _, fingerprint := range canonicalFingerprints {
		trusted[fingerprint] = struct{}{}
	}
	client := options.Client
	if len(options.TLSRootCertificates) > 0 {
		if parsed.Scheme != "https" || client != nil {
			return nil, errors.New("APT signer HTTP configuration is invalid")
		}
		rootCertificates, rootErr := parseTLSRootCertificates(options.TLSRootCertificates)
		if rootErr != nil {
			return nil, errors.New("APT signer HTTP configuration is invalid")
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: rootCertificates}
		client = &http.Client{Transport: transport}
	} else if client == nil {
		client = &http.Client{}
	}
	return &HTTPSigner{endpoint: endpoint, token: options.Token, timeout: options.Timeout, client: client, trusted: trusted, keyring: keyring}, nil
}

// LoadHTTPSignerTLSRootCertificates loads a bounded public certificate bundle.
// The caller retains the bytes so the signer client can build a dedicated
// trust pool without changing process-wide TLS roots.
func LoadHTTPSignerTLSRootCertificates(path string) ([]byte, error) {
	file, err := os.Open(strings.TrimSpace(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, maxTLSRootCertificatesBytes+1))
	if err != nil || len(body) == 0 || int64(len(body)) > maxTLSRootCertificatesBytes {
		return nil, errors.New("APT signer TLS root certificate file is invalid")
	}
	if _, err = parseTLSRootCertificates(body); err != nil {
		return nil, err
	}
	return body, nil
}

func parseTLSRootCertificates(body []byte) (*x509.CertPool, error) {
	remaining := bytes.TrimSpace(body)
	pool := x509.NewCertPool()
	count := 0
	for len(remaining) > 0 {
		block, rest := pem.Decode(remaining)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, errors.New("APT signer TLS roots must contain only certificates")
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, errors.New("APT signer TLS root certificate is invalid")
		}
		count++
		if count > maxTLSRootCertificates {
			return nil, errors.New("APT signer TLS root certificate bundle is too large")
		}
		pool.AddCert(certificate)
		remaining = bytes.TrimSpace(rest)
	}
	if count == 0 {
		return nil, errors.New("APT signer TLS root certificate bundle is empty")
	}
	return pool, nil
}

func LoadTrustedSignerPublicKeys(path string) ([]byte, error) {
	file, err := os.Open(strings.TrimSpace(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, maxTrustedPublicKeysBytes+1))
	if err != nil || len(body) == 0 || int64(len(body)) > maxTrustedPublicKeysBytes {
		return nil, errors.New("APT signer trusted public-key file is invalid")
	}
	return body, nil
}

func parseTrustedSignerPublicKeys(body []byte) (openpgp.EntityList, []string, error) {
	if len(body) == 0 {
		return nil, nil, nil
	}
	block, err := decodeSingleArmoredBlock(body, openpgp.PublicKeyType)
	if err != nil {
		return nil, nil, errors.New("APT signer trusted public-key ring is invalid")
	}
	decoded, err := io.ReadAll(block.Body)
	if err != nil {
		return nil, nil, errors.New("APT signer trusted public-key ring is invalid")
	}
	keyring, err := openpgp.ReadKeyRing(bytes.NewReader(decoded))
	if err != nil || len(keyring) == 0 || len(keyring) > 2 {
		return nil, nil, errors.New("APT signer trusted public-key ring is invalid")
	}
	fingerprints := make([]string, 0, len(keyring))
	for _, entity := range keyring {
		if entity == nil || entity.PrimaryKey == nil || entity.PrivateKey != nil {
			return nil, nil, errors.New("APT signer trusted key ring must contain public keys only")
		}
		for _, subkey := range entity.Subkeys {
			if subkey.PrivateKey != nil {
				return nil, nil, errors.New("APT signer trusted key ring must contain public keys only")
			}
		}
		if !validTrustedSignerEntity(entity) {
			return nil, nil, errors.New("APT signer trusted signing keys must use RSA 2048-4096")
		}
		fingerprints = append(fingerprints, strings.ToLower(hex.EncodeToString(entity.PrimaryKey.Fingerprint)))
	}
	canonical, err := NormalizeTrustedSignerFingerprints(fingerprints)
	if err != nil {
		return nil, nil, err
	}
	return keyring, canonical, nil
}

func validTrustedSignerEntity(entity *openpgp.Entity) bool {
	if entity == nil || entity.PrimaryKey == nil {
		return false
	}
	hasSigningKey := false
	primaryCanSign := entity.PrimaryKey.PubKeyAlgo.CanSign()
	if identity := entity.PrimaryIdentity(); identity != nil && identity.SelfSignature != nil && identity.SelfSignature.FlagsValid {
		primaryCanSign = primaryCanSign && identity.SelfSignature.FlagSign
	}
	if primaryCanSign {
		if !validTrustedRSASigningKey(entity.PrimaryKey) {
			return false
		}
		hasSigningKey = true
	}
	for _, subkey := range entity.Subkeys {
		if subkey.PublicKey == nil || subkey.Sig == nil || !subkey.PublicKey.PubKeyAlgo.CanSign() ||
			(subkey.Sig.FlagsValid && !subkey.Sig.FlagSign) {
			continue
		}
		if !validTrustedRSASigningKey(subkey.PublicKey) {
			return false
		}
		hasSigningKey = true
	}
	return hasSigningKey
}

func validTrustedRSASigningKey(key *packet.PublicKey) bool {
	if key == nil || (key.PubKeyAlgo != packet.PubKeyAlgoRSA && key.PubKeyAlgo != packet.PubKeyAlgoRSASignOnly) {
		return false
	}
	bits, err := key.BitLength()
	return err == nil && bits >= 2048 && bits <= 4096
}

func decodeSingleArmoredBlock(body []byte, blockType string) (*armor.Block, error) {
	trimmed := bytes.TrimSpace(body)
	begin := []byte("-----BEGIN " + blockType + "-----")
	end := []byte("-----END " + blockType + "-----")
	if !bytes.HasPrefix(trimmed, begin) || !bytes.HasSuffix(trimmed, end) ||
		bytes.Count(trimmed, []byte("-----BEGIN ")) != 1 || bytes.Count(trimmed, []byte("-----END ")) != 1 {
		return nil, errors.New("armored data must contain exactly one block")
	}
	block, err := armor.Decode(bytes.NewReader(trimmed))
	if err != nil || block.Type != blockType {
		return nil, errors.New("armored block type is invalid")
	}
	return block, nil
}

// ValidateTrustedSignerPublicKeys verifies that the bounded public-only keyring
// exactly matches the configured rotation fingerprints.
func ValidateTrustedSignerPublicKeys(fingerprints []string, body []byte) error {
	canonical, err := NormalizeTrustedSignerFingerprints(fingerprints)
	if err != nil {
		return err
	}
	_, keyFingerprints, err := parseTrustedSignerPublicKeys(body)
	if err != nil {
		return err
	}
	if len(canonical) == 0 || !sameFingerprints(canonical, keyFingerprints) {
		return errors.New("APT signer trusted fingerprints do not match the public-key ring")
	}
	return nil
}

// MergeTrustedSignerPublicKeys builds the single public-only armor block used
// during a one- or two-key rotation window. Each input must independently
// satisfy the bounded trusted-key parser.
func MergeTrustedSignerPublicKeys(bodies ...[]byte) ([]byte, []string, error) {
	if len(bodies) == 0 || len(bodies) > 2 {
		return nil, nil, errors.New("one or two APT signer public-key files are required")
	}
	entities := make(openpgp.EntityList, 0, len(bodies))
	fingerprints := make([]string, 0, len(bodies))
	for _, body := range bodies {
		keyring, parsedFingerprints, err := parseTrustedSignerPublicKeys(body)
		if err != nil {
			return nil, nil, err
		}
		entities = append(entities, keyring...)
		fingerprints = append(fingerprints, parsedFingerprints...)
	}
	canonical, err := NormalizeTrustedSignerFingerprints(fingerprints)
	if err != nil || len(entities) != len(canonical) {
		return nil, nil, errors.New("APT signer public-key rotation set is invalid")
	}
	var output bytes.Buffer
	armored, err := armor.Encode(&output, openpgp.PublicKeyType, nil)
	if err != nil {
		return nil, nil, err
	}
	for _, entity := range entities {
		if err = entity.Serialize(armored); err != nil {
			_ = armored.Close()
			return nil, nil, err
		}
	}
	if err = armored.Close(); err != nil {
		return nil, nil, err
	}
	return output.Bytes(), canonical, nil
}

func sameFingerprints(configured, keys []string) bool {
	if len(configured) != len(keys) {
		return false
	}
	configuredSet := make(map[string]struct{}, len(configured))
	for _, fingerprint := range configured {
		configuredSet[fingerprint] = struct{}{}
	}
	for _, fingerprint := range keys {
		if _, ok := configuredSet[fingerprint]; !ok {
			return false
		}
	}
	return true
}

// NormalizeTrustedSignerFingerprints is the shared H3 trust-policy boundary
// used by runtime configuration and the signer client.
func NormalizeTrustedSignerFingerprints(values []string) ([]string, error) {
	if len(values) > 2 {
		return nil, errors.New("at most two APT signer fingerprints may be trusted")
	}
	trusted := make(map[string]struct{}, len(values))
	canonical := make([]string, 0, len(values))
	for _, value := range values {
		fingerprint := strings.ToLower(strings.TrimSpace(value))
		if (len(fingerprint) != 40 && len(fingerprint) != 64) || strings.IndexFunc(fingerprint, func(r rune) bool {
			return (r < '0' || r > '9') && (r < 'a' || r > 'f')
		}) >= 0 {
			return nil, errors.New("APT signer fingerprint is invalid")
		}
		if _, exists := trusted[fingerprint]; exists {
			return nil, errors.New("APT signer fingerprint is duplicated")
		}
		trusted[fingerprint] = struct{}{}
		canonical = append(canonical, fingerprint)
	}
	return canonical, nil
}

func validHTTPSignerEndpoint(raw string) (string, error) {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.Path == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", errors.New("invalid signer endpoint")
	}
	if parsed.Scheme == "http" && !IsLoopbackSignerHost(parsed.Hostname()) {
		return "", errors.New("insecure signer endpoint")
	}
	return parsed.String(), nil
}

func IsLoopbackSignerHost(host string) bool {
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
		return SignReleaseResult{}, ErrSignerInvalidSignature
	}
	if len(s.trusted) > 0 {
		if _, trusted := s.trusted[strings.ToLower(result.KeyFingerprint)]; !trusted {
			return SignReleaseResult{}, ErrUntrustedSigner
		}
		evidence, verified := signatureEnvelopeVerifiedByTrustedKey(s.keyring, release, result)
		if !verified {
			return SignReleaseResult{}, ErrSignerInvalidSignature
		}
		result.KeyFingerprint = evidence.fingerprint
		result.SignerIdentity = evidence.identity
		result.Algorithm = evidence.algorithm
	}
	return result, nil
}

type verifiedSignerEvidence struct {
	fingerprint           string
	signingKeyFingerprint string
	identity              string
	algorithm             string
}

func signatureEnvelopeVerifiedByTrustedKey(keyring openpgp.EntityList, release []byte, result SignReleaseResult) (verifiedSignerEvidence, bool) {
	cleartext, rest := clearsign.Decode(result.InRelease)
	if cleartext == nil || len(rest) != 0 {
		return verifiedSignerEvidence{}, false
	}
	clearSignature, clearSigner, err := openpgp.VerifyDetachedSignature(keyring, bytes.NewReader(cleartext.Bytes), cleartext.ArmoredSignature.Body, nil)
	if err != nil || clearSignature == nil || clearSigner == nil || clearSigner.PrimaryKey == nil {
		return verifiedSignerEvidence{}, false
	}
	detachedBlock, err := decodeSingleArmoredBlock(result.Detached, openpgp.SignatureType)
	if err != nil {
		return verifiedSignerEvidence{}, false
	}
	detachedSignature, detachedSigner, err := openpgp.VerifyDetachedSignature(keyring, bytes.NewReader(release), detachedBlock.Body, nil)
	if err != nil || detachedSignature == nil || detachedSigner == nil || detachedSigner.PrimaryKey == nil {
		return verifiedSignerEvidence{}, false
	}
	clearEvidence, ok := verifiedEvidenceForSignature(clearSigner, clearSignature)
	if !ok {
		return verifiedSignerEvidence{}, false
	}
	detachedEvidence, ok := verifiedEvidenceForSignature(detachedSigner, detachedSignature)
	if !ok || clearEvidence != detachedEvidence {
		return verifiedSignerEvidence{}, false
	}
	claimed := strings.ToLower(result.KeyFingerprint)
	return clearEvidence, claimed == clearEvidence.fingerprint
}

func verifiedEvidenceForSignature(entity *openpgp.Entity, signature *packet.Signature) (verifiedSignerEvidence, bool) {
	if entity == nil || entity.PrimaryKey == nil || signature == nil || signature.Hash != crypto.SHA256 {
		return verifiedSignerEvidence{}, false
	}
	identity := entity.PrimaryIdentity()
	if identity == nil || identity.Name == "" || len(identity.Name) > 512 || strings.ContainsAny(identity.Name, "\x00\r\n") {
		return verifiedSignerEvidence{}, false
	}
	key := signingPublicKey(entity, signature)
	if key == nil || (signature.PubKeyAlgo != packet.PubKeyAlgoRSA && signature.PubKeyAlgo != packet.PubKeyAlgoRSASignOnly) {
		return verifiedSignerEvidence{}, false
	}
	bits, err := key.BitLength()
	if err != nil || bits < 2048 || bits > 4096 {
		return verifiedSignerEvidence{}, false
	}
	return verifiedSignerEvidence{
		fingerprint:           strings.ToLower(hex.EncodeToString(entity.PrimaryKey.Fingerprint)),
		signingKeyFingerprint: strings.ToLower(hex.EncodeToString(key.Fingerprint)),
		identity:              identity.Name,
		algorithm:             fmt.Sprintf("rsa%d-sha256", bits),
	}, true
}

func signingPublicKey(entity *openpgp.Entity, signature *packet.Signature) *packet.PublicKey {
	if signature.CheckKeyIdOrFingerprint(entity.PrimaryKey) {
		return entity.PrimaryKey
	}
	for _, subkey := range entity.Subkeys {
		if subkey.PublicKey != nil && signature.CheckKeyIdOrFingerprint(subkey.PublicKey) {
			return subkey.PublicKey
		}
	}
	return nil
}

func signatureEnvelopeMatchesRelease(release []byte, result SignReleaseResult) bool {
	cleartext, rest := clearsign.Decode(result.InRelease)
	if cleartext == nil || cleartext.ArmoredSignature == nil || len(rest) != 0 || !bytes.Equal(cleartext.Plaintext, release) {
		return false
	}
	if _, err := io.Copy(io.Discard, cleartext.ArmoredSignature.Body); err != nil {
		return false
	}
	detached, err := decodeSingleArmoredBlock(result.Detached, openpgp.SignatureType)
	if err != nil {
		return false
	}
	_, err = io.Copy(io.Discard, detached.Body)
	return err == nil
}

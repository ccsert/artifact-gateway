package refaptsigner

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type Options struct {
	Token           string
	Service         *Service
	MaxReleaseBytes int64
}

type Handler struct {
	tokenDigest     [32]byte
	service         *Service
	maxReleaseBytes int64
}

func NewHandler(options Options) (*Handler, error) {
	if len(options.Token) < 32 || len(options.Token) > 256 || strings.ContainsAny(options.Token, "\x00\r\n") ||
		options.Service == nil || options.MaxReleaseBytes < 1024 || options.MaxReleaseBytes > 16<<20 {
		return nil, errors.New("reference APT signer handler options are invalid")
	}
	return &Handler{tokenDigest: sha256.Sum256([]byte("Bearer " + options.Token)), service: options.Service, maxReleaseBytes: options.MaxReleaseBytes}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/healthz":
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/public-key":
		w.Header().Set("Content-Type", "application/pgp-keys")
		w.Header().Set("X-Artifact-Signer-Key-Fingerprint", h.service.Fingerprint())
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(h.service.PublicKey())
	case r.Method == http.MethodPost && r.URL.Path == "/v1/sign-release":
		h.signRelease(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) signRelease(w http.ResponseWriter, r *http.Request) {
	presented := sha256.Sum256([]byte(r.Header.Get("Authorization")))
	if subtle.ConstantTimeCompare(presented[:], h.tokenDigest[:]) != 1 {
		writeProblem(w, http.StatusUnauthorized, "access_denied", "signer authentication failed")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	repositoryID := r.Header.Get("X-Artifact-Repository-Id")
	snapshotID := r.Header.Get("X-Artifact-Snapshot-Id")
	releaseDigest := r.Header.Get("X-Artifact-Release-Digest")
	if err != nil || mediaType != "text/plain" || r.Header.Get("X-Artifact-Signer-Schema") != "v1" ||
		uuid.Validate(repositoryID) != nil || uuid.Validate(snapshotID) != nil || !repository.ValidAPTSHA256Digest(releaseDigest) {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "signer request is invalid")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.maxReleaseBytes+1))
	if err != nil || len(body) == 0 || int64(len(body)) > h.maxReleaseBytes {
		writeProblem(w, http.StatusRequestEntityTooLarge, "invalid_request", "APT Release exceeds the configured limit")
		return
	}
	sum := sha256.Sum256(body)
	if "sha256:"+hex.EncodeToString(sum[:]) != releaseDigest {
		writeProblem(w, http.StatusUnprocessableEntity, "digest_mismatch", "APT Release digest does not match")
		return
	}
	result, err := h.service.SignRelease(r.Context(), body)
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "signer_unavailable", "APT Release signing failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Artifact-Signer-Schema", "v1")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(struct {
		InRelease      []byte `json:"inRelease"`
		Detached       []byte `json:"detached"`
		SignerIdentity string `json:"signerIdentity"`
		KeyFingerprint string `json:"keyFingerprint"`
		Algorithm      string `json:"algorithm"`
	}{result.InRelease, result.Detached, result.SignerIdentity, result.KeyFingerprint, result.Algorithm})
}

func writeProblem(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "code": code, "message": message})
}

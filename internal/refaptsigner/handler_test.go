package refaptsigner

import (
	"bytes"
	"context"
	"crypto"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/artifact-gateway/artifact-gateway/internal/aptpublication"
	"github.com/google/uuid"
)

func TestHandlerProducesVerifiableAPTReleaseSignatures(t *testing.T) {
	t.Parallel()
	entity, err := openpgp.NewEntity("Artifact Gateway Test", "reference signer", "apt@example.test", &packet.Config{RSABits: 1024, DefaultHash: crypto.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(entity, "Artifact Gateway Test <apt@example.test>")
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("t", 32)
	handler, err := NewHandler(Options{Token: token, Service: service, MaxReleaseBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	release := []byte("Suite: stable\nAcquire-By-Hash: yes\nSHA256:\n")
	request := httptest.NewRequest(http.MethodPost, "/v1/sign-release", bytes.NewReader(release))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "text/plain; charset=utf-8")
	request.Header.Set("X-Artifact-Signer-Schema", "v1")
	request.Header.Set("X-Artifact-Repository-Id", uuid.NewString())
	request.Header.Set("X-Artifact-Snapshot-Id", uuid.NewString())
	request.Header.Set("X-Artifact-Release-Digest", digest(release))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("X-Artifact-Signer-Schema") != "v1" {
		t.Fatalf("sign=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	var signed aptpublication.SignReleaseResult
	if err = json.Unmarshal(response.Body.Bytes(), &signed); err != nil {
		t.Fatal(err)
	}
	block, rest := clearsign.Decode(signed.InRelease)
	if block == nil || len(rest) != 0 || !bytes.Equal(block.Plaintext, release) {
		t.Fatalf("invalid InRelease cleartext: block=%#v rest=%q", block, rest)
	}
	if _, err = openpgp.CheckDetachedSignature(openpgp.EntityList{entity}, bytes.NewReader(block.Bytes), block.ArmoredSignature.Body, nil); err != nil {
		t.Fatalf("verify InRelease: %v", err)
	}
	if _, err = openpgp.CheckArmoredDetachedSignature(openpgp.EntityList{entity}, bytes.NewReader(release), bytes.NewReader(signed.Detached), nil); err != nil {
		t.Fatalf("verify Release.gpg: %v", err)
	}
	if signed.SignerIdentity != "Artifact Gateway Test <apt@example.test>" || signed.KeyFingerprint == "" || signed.Algorithm != "rsa1024-sha256" {
		t.Fatalf("signature evidence=%#v", signed)
	}

	publicKey := httptest.NewRecorder()
	handler.ServeHTTP(publicKey, httptest.NewRequest(http.MethodGet, "/v1/public-key", nil))
	if publicKey.Code != http.StatusOK || !strings.Contains(publicKey.Body.String(), "BEGIN PGP PUBLIC KEY BLOCK") || strings.Contains(publicKey.Body.String(), "PRIVATE") {
		t.Fatalf("public key=%d body=%s", publicKey.Code, publicKey.Body.String())
	}
}

func TestHandlerRejectsUnauthenticatedAndMismatchedRelease(t *testing.T) {
	t.Parallel()
	entity, err := openpgp.NewEntity("Artifact Gateway Test", "", "apt@example.test", &packet.Config{RSABits: 1024})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(entity, "test")
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("s", 32)
	handler, err := NewHandler(Options{Token: token, Service: service, MaxReleaseBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	request := func(authorization, releaseDigest string) *http.Request {
		value := httptest.NewRequest(http.MethodPost, "/v1/sign-release", strings.NewReader("release"))
		value.Header.Set("Authorization", authorization)
		value.Header.Set("Content-Type", "text/plain")
		value.Header.Set("X-Artifact-Signer-Schema", "v1")
		value.Header.Set("X-Artifact-Repository-Id", uuid.NewString())
		value.Header.Set("X-Artifact-Snapshot-Id", uuid.NewString())
		value.Header.Set("X-Artifact-Release-Digest", releaseDigest)
		return value
	}
	for _, testCase := range []struct {
		name, authorization, releaseDigest string
		status                             int
	}{
		{name: "missing token", releaseDigest: digest([]byte("release")), status: http.StatusUnauthorized},
		{name: "wrong token", authorization: "Bearer " + strings.Repeat("x", 32), releaseDigest: digest([]byte("release")), status: http.StatusUnauthorized},
		{name: "wrong digest", authorization: "Bearer " + token, releaseDigest: digest([]byte("other")), status: http.StatusUnprocessableEntity},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request(testCase.authorization, testCase.releaseDigest))
			if response.Code != testCase.status || strings.Contains(response.Body.String(), token) {
				t.Fatalf("response=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestLoadOrCreateEntityPersistsOnePrivateKeyWithRestrictedMode(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "keys", "apt-release.gpg")
	first, err := LoadOrCreateEntity(context.Background(), KeyOptions{
		Path: path, Name: "Artifact Gateway Local", Comment: "reference signer", Email: "apt@localhost", RSABits: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateEntity(context.Background(), KeyOptions{
		Path: path, Name: "ignored on reload", Email: "other@localhost", RSABits: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.PrimaryKey.KeyId != second.PrimaryKey.KeyId {
		t.Fatalf("key changed across reload: %x != %x", first.PrimaryKey.KeyId, second.PrimaryKey.KeyId)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("private key mode=%o", info.Mode().Perm())
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	entities, err := openpgp.ReadKeyRing(file)
	if err != nil || len(entities) != 1 || entities[0].PrivateKey == nil {
		t.Fatalf("persisted key entities=%d err=%v", len(entities), err)
	}
}

func TestLoadEntityAcceptsReadOnlyPrivateKeyWithoutMutatingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "read-only-private.gpg")
	if _, err := LoadOrCreateEntity(context.Background(), KeyOptions{
		Path: path, Name: "Read Only", Email: "read-only@example.test", RSABits: 1024,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEntity(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o400 {
		t.Fatalf("private key mode=%#o", info.Mode().Perm())
	}
}

func digest(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

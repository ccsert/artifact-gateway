package aptpublication

import (
	"bytes"
	"context"
	"crypto"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

type aptSignerRoundTripper func(*http.Request) (*http.Response, error)

func (f aptSignerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestHTTPSignerSendsOnlyImmutableReleaseEvidence(t *testing.T) {
	t.Parallel()
	release := []byte("Suite: stable\nSHA256:\n")
	digest := digestBytes(release)
	signedResponse := aptSignerTestResponse(t, release)
	client := &http.Client{Transport: aptSignerRoundTripper(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if request.Method != http.MethodPost || request.URL.String() != "https://signer.example.test/v1/sign-release" ||
			request.Header.Get("Authorization") != "Bearer "+strings.Repeat("t", 32) ||
			request.Header.Get("X-Artifact-Repository-Id") != "repository-one" ||
			request.Header.Get("X-Artifact-Snapshot-Id") != "snapshot-one" ||
			request.Header.Get("X-Artifact-Release-Digest") != digest ||
			request.Header.Get("X-Artifact-Signer-Schema") != "v1" || !bytes.Equal(body, release) {
			t.Fatalf("unexpected signer request: method=%s url=%s headers=%v body=%q", request.Method, request.URL, request.Header, body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Artifact-Signer-Schema": []string{"v1"}},
			Body:       io.NopCloser(bytes.NewReader(signedResponse)),
		}, nil
	})}
	signer, err := NewHTTPSigner(HTTPSignerOptions{
		Endpoint: "https://signer.example.test/v1/sign-release", Token: strings.Repeat("t", 32), Timeout: 5 * time.Second, Client: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := signer.SignRelease(context.Background(), SignReleaseRequest{
		RepositoryID: "repository-one", SnapshotID: "snapshot-one", ReleaseDigest: digest, Release: bytes.NewReader(release),
	})
	if err != nil || len(result.InRelease) == 0 || len(result.Detached) == 0 ||
		result.SignerIdentity != "release@example.test" || result.KeyFingerprint != strings.Repeat("a", 40) || result.Algorithm != "rsa4096-sha256" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestHTTPSignerRejectsSignatureEnvelopeForDifferentRelease(t *testing.T) {
	t.Parallel()
	release := []byte("Suite: stable\n")
	staleResponse := aptSignerTestResponse(t, []byte("Suite: oldstable\n"))
	client := &http.Client{Transport: aptSignerRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Artifact-Signer-Schema": []string{"v1"}},
			Body:       io.NopCloser(bytes.NewReader(staleResponse)),
		}, nil
	})}
	signer, err := NewHTTPSigner(HTTPSignerOptions{
		Endpoint: "https://signer.example.test/v1/sign-release", Token: strings.Repeat("t", 32), Timeout: time.Second, Client: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = signer.SignRelease(context.Background(), SignReleaseRequest{
		RepositoryID: "repository", SnapshotID: "snapshot", ReleaseDigest: digestBytes(release), Release: bytes.NewReader(release),
	}); err == nil {
		t.Fatal("signature envelope for a different Release was accepted")
	}
}

func TestHTTPSignerRejectsUnsafeConfigurationAndInvalidResponses(t *testing.T) {
	t.Parallel()
	for _, options := range []HTTPSignerOptions{
		{Endpoint: "http://signer.example.test/v1/sign-release", Token: strings.Repeat("t", 32), Timeout: time.Second},
		{Endpoint: "https://user@signer.example.test/v1/sign-release", Token: strings.Repeat("t", 32), Timeout: time.Second},
		{Endpoint: "https://signer.example.test/v1/sign-release?key=secret", Token: strings.Repeat("t", 32), Timeout: time.Second},
		{Endpoint: "https://signer.example.test/v1/sign-release", Token: "short", Timeout: time.Second},
		{Endpoint: "https://signer.example.test/v1/sign-release", Token: strings.Repeat("t", 32), Timeout: 100 * time.Millisecond},
	} {
		if _, err := NewHTTPSigner(options); err == nil {
			t.Fatalf("unsafe options accepted: %#v", options)
		}
	}

	client := &http.Client{Transport: aptSignerRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"inRelease":"","detached":"","signerIdentity":"","keyFingerprint":"","algorithm":""}`))}, nil
	})}
	signer, err := NewHTTPSigner(HTTPSignerOptions{
		Endpoint: "http://127.0.0.1:18083/v1/sign-release", Token: strings.Repeat("t", 32), Timeout: time.Second, Client: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	release := []byte("release")
	if _, err = signer.SignRelease(context.Background(), SignReleaseRequest{RepositoryID: "repository", SnapshotID: "snapshot", ReleaseDigest: digestBytes([]byte("other")), Release: bytes.NewReader(release)}); err == nil {
		t.Fatal("mismatched Release digest was accepted")
	}
	if _, err = signer.SignRelease(context.Background(), SignReleaseRequest{RepositoryID: "repository", SnapshotID: "snapshot", ReleaseDigest: digestBytes(release), Release: bytes.NewReader(release)}); err == nil {
		t.Fatal("invalid signer result was accepted")
	}
}

func aptSignerTestResponse(t *testing.T, release []byte) []byte {
	t.Helper()
	config := &packet.Config{RSABits: 1024, DefaultHash: crypto.SHA256}
	entity, err := openpgp.NewEntity("APT Test", "", "apt@example.test", config)
	if err != nil {
		t.Fatal(err)
	}
	var inRelease bytes.Buffer
	cleartext, err := clearsign.Encode(&inRelease, entity.PrivateKey, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cleartext.Write(release); err != nil {
		t.Fatal(err)
	}
	if err = cleartext.Close(); err != nil {
		t.Fatal(err)
	}
	var detached bytes.Buffer
	if err = openpgp.ArmoredDetachSign(&detached, entity, bytes.NewReader(release), config); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"inRelease": inRelease.Bytes(), "detached": detached.Bytes(), "signerIdentity": "release@example.test",
		"keyFingerprint": strings.Repeat("a", 40), "algorithm": "rsa4096-sha256",
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

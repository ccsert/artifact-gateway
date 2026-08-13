package aptpublication

import (
	"bytes"
	"context"
	"crypto"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

func TestHTTPSignerRequiresCompletePinnedRemoteTrustPolicy(t *testing.T) {
	t.Parallel()
	release := []byte("Suite: stable\n")
	active := aptSignerTestFixtureForRelease(t, release)
	if _, err := NewHTTPSigner(HTTPSignerOptions{
		Endpoint: "https://signer.example.test/v1/sign-release", Token: strings.Repeat("t", 32), Timeout: time.Second,
	}); err == nil {
		t.Fatal("remote signer without pinned public-key evidence was accepted")
	}
	if _, err := NewHTTPSigner(HTTPSignerOptions{
		Endpoint: "https://signer.example.test/v1/sign-release", Token: strings.Repeat("t", 32), Timeout: time.Second,
		TrustedFingerprints: []string{active.fingerprint},
	}); err == nil {
		t.Fatal("remote signer accepted a self-reported fingerprint without its public key")
	}
}

func TestHTTPSignerRejectsTrailingPrivateKeyBlock(t *testing.T) {
	t.Parallel()
	active := aptSignerTestFixtureForRelease(t, []byte("Suite: stable\n"))
	publicThenPrivate := append(append([]byte(nil), active.publicKey...), active.privateKey...)
	if _, err := NewHTTPSigner(HTTPSignerOptions{
		Endpoint: "https://signer.example.test/v1/sign-release", Token: strings.Repeat("t", 32), Timeout: time.Second,
		TrustedFingerprints: []string{active.fingerprint}, TrustedPublicKeys: publicThenPrivate,
	}); err == nil {
		t.Fatal("trusted public-key file with a trailing private-key block was accepted")
	}
}

func TestHTTPSignerSupportsRotationOverlapAndDerivesEvidence(t *testing.T) {
	t.Parallel()
	release := []byte("Suite: stable\n")
	active := aptSignerTestFixtureForRelease(t, release)
	next := aptSignerTestFixtureForRelease(t, release)
	signer, err := NewHTTPSigner(HTTPSignerOptions{
		Endpoint: "https://signer.example.test/v1/sign-release", Token: strings.Repeat("t", 32), Timeout: time.Second,
		Client: aptSignerResponseClient(active.response), TrustedFingerprints: []string{strings.ToUpper(next.fingerprint), strings.ToUpper(active.fingerprint)},
		TrustedPublicKeys: aptSignerPublicKeyRing(t, active, next),
	})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := signer.SignRelease(context.Background(), SignReleaseRequest{
		RepositoryID: "repository", SnapshotID: "snapshot", ReleaseDigest: digestBytes(release), Release: bytes.NewReader(release),
	})
	if err != nil {
		t.Fatalf("rotation overlap rejected the active trusted key: %v", err)
	}
	if verified.SignerIdentity != "APT Test <apt@example.test>" || verified.Algorithm != "rsa2048-sha256" || verified.KeyFingerprint != active.fingerprint {
		t.Fatalf("signature evidence was not derived from the verified key: %#v", verified)
	}
}

func TestHTTPSignerRejectsForgedFingerprint(t *testing.T) {
	t.Parallel()
	release := []byte("Suite: stable\n")
	active := aptSignerTestFixtureForRelease(t, release)
	next := aptSignerTestFixtureForRelease(t, release)
	var forged map[string]any
	if err := json.Unmarshal(active.response, &forged); err != nil {
		t.Fatal(err)
	}
	forged["keyFingerprint"] = next.fingerprint
	forgedResponse, err := json.Marshal(forged)
	if err != nil {
		t.Fatal(err)
	}
	forgedSigner, err := NewHTTPSigner(HTTPSignerOptions{
		Endpoint: "https://signer.example.test/v1/sign-release", Token: strings.Repeat("t", 32), Timeout: time.Second,
		Client: aptSignerResponseClient(forgedResponse), TrustedFingerprints: []string{active.fingerprint, next.fingerprint},
		TrustedPublicKeys: aptSignerPublicKeyRing(t, active, next),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = forgedSigner.SignRelease(context.Background(), SignReleaseRequest{
		RepositoryID: "repository", SnapshotID: "snapshot", ReleaseDigest: digestBytes(release), Release: bytes.NewReader(release),
	}); !errors.Is(err, ErrUntrustedSigner) {
		t.Fatalf("forged signer fingerprint err=%v", err)
	}
}

func TestHTTPSignerRejectsUntrustedSigningKey(t *testing.T) {
	t.Parallel()
	release := []byte("Suite: stable\n")
	active := aptSignerTestFixtureForRelease(t, release)
	untrustedFixture := aptSignerTestFixtureForRelease(t, release)
	untrusted, err := NewHTTPSigner(HTTPSignerOptions{
		Endpoint: "https://signer.example.test/v1/sign-release", Token: strings.Repeat("t", 32), Timeout: time.Second,
		Client: aptSignerResponseClient(untrustedFixture.response), TrustedFingerprints: []string{active.fingerprint}, TrustedPublicKeys: active.publicKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = untrusted.SignRelease(context.Background(), SignReleaseRequest{
		RepositoryID: "repository", SnapshotID: "snapshot", ReleaseDigest: digestBytes(release), Release: bytes.NewReader(release),
	}); !errors.Is(err, ErrUntrustedSigner) {
		t.Fatalf("untrusted signer err=%v", err)
	}
}

func TestHTTPSignerRejectsWeakTrustedSigningKey(t *testing.T) {
	t.Parallel()
	release := []byte("Suite: stable\n")
	weak := aptSignerTestFixtureForReleaseWithBits(t, release, 1024)
	signer, err := NewHTTPSigner(HTTPSignerOptions{
		Endpoint: "https://signer.example.test/v1/sign-release", Token: strings.Repeat("t", 32), Timeout: time.Second,
		Client: aptSignerResponseClient(weak.response), TrustedFingerprints: []string{weak.fingerprint}, TrustedPublicKeys: weak.publicKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = signer.SignRelease(context.Background(), SignReleaseRequest{
		RepositoryID: "repository", SnapshotID: "snapshot", ReleaseDigest: digestBytes(release), Release: bytes.NewReader(release),
	}); !errors.Is(err, ErrUntrustedSigner) {
		t.Fatalf("weak signing key err=%v", err)
	}
}

func aptSignerResponseClient(response []byte) *http.Client {
	return &http.Client{Transport: aptSignerRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Artifact-Signer-Schema": []string{"v1"}},
			Body:       io.NopCloser(bytes.NewReader(response)),
		}, nil
	})}
}

func aptSignerPublicKeyRing(t *testing.T, fixtures ...aptSignerTestFixture) []byte {
	t.Helper()
	var result bytes.Buffer
	armored, err := armor.Encode(&result, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		entities, readErr := openpgp.ReadArmoredKeyRing(bytes.NewReader(fixture.publicKey))
		if readErr != nil || len(entities) != 1 {
			t.Fatalf("read fixture public key: entities=%d err=%v", len(entities), readErr)
		}
		if err = entities[0].Serialize(armored); err != nil {
			t.Fatal(err)
		}
	}
	if err = armored.Close(); err != nil {
		t.Fatal(err)
	}
	return result.Bytes()
}

type aptSignerRoundTripper func(*http.Request) (*http.Response, error)

func (f aptSignerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestHTTPSignerSendsOnlyImmutableReleaseEvidence(t *testing.T) {
	t.Parallel()
	release := []byte("Suite: stable\nSHA256:\n")
	digest := digestBytes(release)
	fixture := aptSignerTestFixtureForRelease(t, release)
	client := &http.Client{Transport: aptSignerRoundTripper(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if request.Method != http.MethodPost || request.URL.String() != "https://signer.example.test/v1/sign-release" ||
			request.Header.Get("Authorization") != "Bearer "+strings.Repeat("t", 32) ||
			request.Header.Get("X-Artifact-Snapshot-Id") != "snapshot-one" ||
			request.Header.Get("X-Artifact-Release-Digest") != digest ||
			request.Header.Get("X-Artifact-Signer-Schema") != "v1" || !bytes.Equal(body, release) {
			t.Fatalf("unexpected signer request: method=%s url=%s headers=%v body=%q", request.Method, request.URL, request.Header, body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Artifact-Signer-Schema": []string{"v1"}},
			Body:       io.NopCloser(bytes.NewReader(fixture.response)),
		}, nil
	})}
	signer, err := NewHTTPSigner(HTTPSignerOptions{
		Endpoint: "https://signer.example.test/v1/sign-release", Token: strings.Repeat("t", 32), Timeout: 5 * time.Second, Client: client,
		TrustedFingerprints: []string{fixture.fingerprint}, TrustedPublicKeys: fixture.publicKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := signer.SignRelease(context.Background(), SignReleaseRequest{
		RepositoryID: "repository-one", SnapshotID: "snapshot-one", ReleaseDigest: digest, Release: bytes.NewReader(release),
	})
	if err != nil || len(result.InRelease) == 0 || len(result.Detached) == 0 ||
		result.SignerIdentity != "APT Test <apt@example.test>" || result.KeyFingerprint != fixture.fingerprint || result.Algorithm != "rsa2048-sha256" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestHTTPSignerRejectsSignatureEnvelopeForDifferentRelease(t *testing.T) {
	t.Parallel()
	release := []byte("Suite: stable\n")
	stale := aptSignerTestFixtureForRelease(t, []byte("Suite: oldstable\n"))
	client := &http.Client{Transport: aptSignerRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Artifact-Signer-Schema": []string{"v1"}},
			Body:       io.NopCloser(bytes.NewReader(stale.response)),
		}, nil
	})}
	signer, err := NewHTTPSigner(HTTPSignerOptions{
		Endpoint: "https://signer.example.test/v1/sign-release", Token: strings.Repeat("t", 32), Timeout: time.Second, Client: client,
		TrustedFingerprints: []string{stale.fingerprint}, TrustedPublicKeys: stale.publicKey,
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

type aptSignerTestFixture struct {
	response    []byte
	publicKey   []byte
	privateKey  []byte
	fingerprint string
}

func aptSignerTestFixtureForRelease(t *testing.T, release []byte) aptSignerTestFixture {
	return aptSignerTestFixtureForReleaseWithBits(t, release, 2048)
}

func aptSignerTestFixtureForReleaseWithBits(t *testing.T, release []byte, bits int) aptSignerTestFixture {
	t.Helper()
	config := &packet.Config{RSABits: bits, DefaultHash: crypto.SHA256}
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
	var publicKey bytes.Buffer
	armored, err := armor.Encode(&publicKey, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = entity.Serialize(armored); err != nil {
		t.Fatal(err)
	}
	if err = armored.Close(); err != nil {
		t.Fatal(err)
	}
	var privateKey bytes.Buffer
	privateArmored, err := armor.Encode(&privateKey, openpgp.PrivateKeyType, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = entity.SerializePrivate(privateArmored, config); err != nil {
		t.Fatal(err)
	}
	if err = privateArmored.Close(); err != nil {
		t.Fatal(err)
	}
	fingerprint := hex.EncodeToString(entity.PrimaryKey.Fingerprint)
	body, err := json.Marshal(map[string]any{
		"inRelease": inRelease.Bytes(), "detached": detached.Bytes(), "signerIdentity": "release@example.test",
		"keyFingerprint": fingerprint, "algorithm": "rsa4096-sha256",
	})
	if err != nil {
		t.Fatal(err)
	}
	return aptSignerTestFixture{response: body, publicKey: publicKey.Bytes(), privateKey: privateKey.Bytes(), fingerprint: fingerprint}
}
